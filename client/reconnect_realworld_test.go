package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
	httptransport "github.com/finemcp/finemcp/client/transport/http"
	stdiotransport "github.com/finemcp/finemcp/client/transport/stdio"
)

// ── HTTP Transport Real-World Tests ──────────────────────────────────

// mcpHandler is a minimal MCP server handler for testing
type mcpHandler struct {
	requestCounter atomic.Int32
}

func (h *mcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.requestCounter.Add(1)

	var msg finemcp.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch msg.Method {
	case "initialize":
		resp := finemcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: finemcp.InitializeResult{
				ProtocolVersion: finemcp.ProtocolVersion,
				Capabilities:    finemcp.ServerCapabilities{},
				ServerInfo:      finemcp.ProcessInfo{Name: "test-http-server", Version: "1.0"},
			},
		}
		json.NewEncoder(w).Encode(resp)

	case "ping":
		resp := finemcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  struct{}{},
		}
		json.NewEncoder(w).Encode(resp)

	default:
		resp := finemcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &finemcp.JSONRPCError{
				Code:    -32601,
				Message: "Method not found",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestReconnect_HTTPTransport_ServerRestart(t *testing.T) {
	handler := &mcpHandler{}
	server := httptest.NewServer(handler)
	defer server.Close()

	var reconnectedCalled atomic.Bool

	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 5,
			Strategy:   client.LinearBackoff(50 * time.Millisecond),
			OnReconnected: func() {
				reconnectedCalled.Store(true)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Verify initial ping works
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Initial ping failed: %v", err)
	}

	initialRequests := handler.requestCounter.Load()
	t.Logf("Initial requests: %d", initialRequests)

	// Simulate server restart by closing and recreating
	server.Close()
	time.Sleep(100 * time.Millisecond)

	// The client's next request will fail, triggering reconnection
	// Since HTTP is request-response, the transport won't detect the failure
	// until a request is made

	// Start new server on same URL pattern (httptest will use different port though)
	// For true reconnection test, we'd need a real server on a fixed port
	// For now, this tests that transport failures are handled

	// Make a request that should fail (server is down)
	ctx2, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = c.Ping(ctx2)
	if err == nil {
		t.Error("Expected ping to fail after server shutdown")
	}
	t.Logf("Got expected error: %v", err)
}

func TestReconnect_HTTPTransport_NetworkError(t *testing.T) {
	handler := &mcpHandler{}
	server := httptest.NewServer(handler)

	var reconnectingCount atomic.Int32
	var failedCalled atomic.Bool

	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 3,
			Strategy:   client.NoBackoff(),
			OnReconnecting: func(attempt int, err error) {
				reconnectingCount.Add(1)
				t.Logf("Reconnecting attempt %d: %v", attempt, err)
			},
			OnFailed: func(err error) {
				failedCalled.Store(true)
				t.Logf("Reconnection failed: %v", err)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Close server to simulate network error
	server.Close()

	// Try to ping - should fail and trigger reconnection attempts
	ctx2, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = c.Ping(ctx2)
	if err == nil {
		t.Error("Expected ping to fail")
	}

	// Note: HTTP transport doesn't have a persistent connection,
	// so reconnection happens on a per-request basis
	t.Logf("Final error: %v", err)
}

// ── Stdio Transport Real-World Tests ─────────────────────────────────

// createMockMCPServer creates a simple MCP server executable for testing
func createMockMCPServer(t *testing.T, shouldCrash bool) string {
	t.Helper()

	// Create a Go program that acts as an MCP server
	goCode := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type Request struct {
	JSONRPC string          ` + "`" + `json:"jsonrpc"` + "`" + `
	ID      json.RawMessage ` + "`" + `json:"id"` + "`" + `
	Method  string          ` + "`" + `json:"method"` + "`" + `
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	requestCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		
		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		
		// Only count actual requests (with IDs), not notifications.
		// Notifications have no ID or ID is null.
		isRequest := len(req.ID) > 0 && string(req.ID) != "null"
		if isRequest {
			requestCount++
		}
		
		// Crash after handling 2 requests if shouldCrash (ignoring notifications)
		if %t && isRequest && requestCount > 2 {
			os.Exit(1)
		}
		
		var resp map[string]interface{}
		switch req.Method {
		case "initialize":
			resp = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "mock-server",
						"version": "1.0",
					},
				},
			}
		case "ping":
			resp = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]interface{}{},
			}
		default:
			continue
		}
		
		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
	}
}
`

	goCode = fmt.Sprintf(goCode, shouldCrash)

	// Create temp directory for the Go program
	tmpDir, err := os.MkdirTemp("", "mcp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	goFile := tmpDir + "/server.go"
	if err := os.WriteFile(goFile, []byte(goCode), 0644); err != nil {
		t.Fatalf("Failed to write Go file: %v", err)
	}

	// Build the Go program
	exeFile := tmpDir + "/server"
	if runtime.GOOS == "windows" {
		exeFile += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exeFile, goFile)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build server: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	return exeFile
}

func TestReconnect_StdioTransport_ProcessCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stdio test in short mode")
	}

	scriptPath := createMockMCPServer(t, true)

	var reconnectingCount atomic.Int32
	var reconnectedCount atomic.Int32

	tr := stdiotransport.New(stdiotransport.Config{
		Command: scriptPath,
		Args:    []string{},
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 3,
			Strategy:   client.LinearBackoff(100 * time.Millisecond),
			OnReconnecting: func(attempt int, err error) {
				reconnectingCount.Add(1)
				t.Logf("Reconnecting attempt %d: %v", attempt, err)
			},
			OnReconnected: func() {
				reconnectedCount.Add(1)
				t.Log("Reconnected!")
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// First ping should work
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("First ping failed: %v", err)
	}

	// Second ping will cause crash (server crashes after 2 requests)
	ctx2, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = c.Ping(ctx2) // Expected to fail/timeout as server crashes

	// Wait for reconnection attempt to be initiated (100ms backoff + processing time)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if reconnectingCount.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Logf("After crash - reconnecting: %d, reconnected: %d",
		reconnectingCount.Load(), reconnectedCount.Load())

	// The process crashed, so we should see reconnection attempts
	if reconnectingCount.Load() == 0 {
		t.Error("Expected reconnection attempts after process crash")
	}
}

func TestReconnect_StdioTransport_NormalOperation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stdio test in short mode")
	}

	scriptPath := createMockMCPServer(t, false)

	tr := stdiotransport.New(stdiotransport.Config{
		Command: scriptPath,
		Args:    []string{},
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Multiple pings should all work
	for i := 0; i < 5; i++ {
		if err := c.Ping(ctx); err != nil {
			t.Fatalf("Ping %d failed: %v", i+1, err)
		}
	}
}

// ── HTTP Transport Timeout and Error Tests ───────────────────────────

func TestReconnect_HTTPTransport_Timeout(t *testing.T) {
	// Create a server that delays responses
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		mcpHandler := &mcpHandler{}
		mcpHandler.ServeHTTP(w, r)
	})

	server := httptest.NewServer(slowHandler)
	defer server.Close()

	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
		Timeout: 100 * time.Millisecond, // Short timeout
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Initialize should timeout
	_, err = c.Initialize(ctx)
	if err == nil {
		t.Error("Expected initialize to timeout")
	}

	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("Expected timeout error, got: %v", err)
	}
}

func TestReconnect_HTTPTransport_InvalidResponse(t *testing.T) {
	// Server returns invalid JSON
	badHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	})

	server := httptest.NewServer(badHandler)
	defer server.Close()

	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	_, err = c.Initialize(ctx)
	if err == nil {
		t.Error("Expected error from invalid JSON response")
	}
}

// ── Stdio Transport Error Tests ──────────────────────────────────────

func TestReconnect_StdioTransport_CommandNotFound(t *testing.T) {
	tr := stdiotransport.New(stdiotransport.Config{
		Command: "/nonexistent/command",
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	_, err = c.Initialize(ctx)
	if err == nil {
		t.Error("Expected error when command not found")
	}

	t.Logf("Got expected error: %v", err)
}

func TestReconnect_StdioTransport_InvalidOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stdio test in short mode")
	}

	// Create server that outputs invalid  JSON
	script := `#!/bin/bash
echo "This is not valid JSON"
`
	tmpfile, err := os.CreateTemp("", "bad-server-*.sh")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	if err := os.WriteFile(tmpfile.Name(), []byte(script), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	tr := stdiotransport.New(stdiotransport.Config{
		Command: "/bin/bash",
		Args:    []string{tmpfile.Name()},
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err = c.Initialize(ctx)
	if err == nil {
		t.Error("Expected error from invalid server output")
	}
	t.Logf("Got expected error: %v", err)
}

// ── Cross-Transport Integration Tests ────────────────────────────────

func TestReconnect_HTTPTransport_MultipleSequentialRequests(t *testing.T) {
	handler := &mcpHandler{}
	server := httptest.NewServer(handler)
	defer server.Close()

	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Make many sequential requests
	for i := 0; i < 20; i++ {
		if err := c.Ping(ctx); err != nil {
			t.Fatalf("Ping %d failed: %v", i+1, err)
		}
	}

	requests := handler.requestCounter.Load()
	if requests < 21 { // initialize + 20 pings
		t.Errorf("Expected at least 21 requests, got %d", requests)
	}
}

func TestReconnect_StdioTransport_LongRunningSession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping long-running test in short mode")
	}

	scriptPath := createMockMCPServer(t, false)

	tr := stdiotransport.New(stdiotransport.Config{
		Command: scriptPath,
		Args:    []string{},
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Run for a few seconds with regular pings
	deadline := time.Now().Add(2 * time.Second)
	pings := 0
	for time.Now().Before(deadline) {
		if err := c.Ping(ctx); err != nil {
			t.Fatalf("Ping failed: %v", err)
		}
		pings++
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Completed %d pings over 2 seconds", pings)
	if pings < 10 {
		t.Errorf("Expected at least 10 pings, got %d", pings)
	}
}
