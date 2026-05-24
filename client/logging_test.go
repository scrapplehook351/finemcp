package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
	httptransport "github.com/finemcp/finemcp/client/transport/http"
)

// TestLogging_RequestResponse verifies that Logger logs client-initiated requests and responses.
func TestLogging_RequestResponse(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: finemcp.InitializeResult{
					ProtocolVersion: finemcp.ProtocolVersion,
					Capabilities:    finemcp.ServerCapabilities{},
					ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
				},
			})
		case "ping":
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  struct{}{},
			})
		default:
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &finemcp.JSONRPCError{
					Code:    finemcp.ErrCodeMethodNotFound,
					Message: "method not found",
				},
			})
		}
	}))
	defer server.Close()

	// Create a buffer to capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create client with logger
	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Initialize
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Clear log buffer after initialize
	logBuf.Reset()

	// Send a ping request
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	logOutput := logBuf.String()

	// Verify log contains request
	if !strings.Contains(logOutput, "mcp request") {
		t.Error("Log output should contain 'mcp request'")
	}
	if !strings.Contains(logOutput, "\"method\":\"ping\"") {
		t.Error("Log output should contain method name 'ping'")
	}

	// Verify log contains successful response
	if !strings.Contains(logOutput, "mcp response success") {
		t.Error("Log output should contain 'mcp response success'")
	}

	// Verify log contains elapsed time
	if !strings.Contains(logOutput, "elapsed") {
		t.Error("Log output should contain 'elapsed' duration")
	}
}

// TestLogging_ErrorResponse verifies that Logger logs error responses.
func TestLogging_ErrorResponse(t *testing.T) {
	// Create a mock HTTP server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		if req.Method == "initialize" {
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: finemcp.InitializeResult{
					ProtocolVersion: finemcp.ProtocolVersion,
					Capabilities:    finemcp.ServerCapabilities{},
					ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
				},
			})
		} else {
			// Return error for all other methods
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &finemcp.JSONRPCError{
					Code:    finemcp.ErrCodeMethodNotFound,
					Message: "method not found",
				},
			})
		}
	}))
	defer server.Close()

	// Create a buffer to capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create client with logger
	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Initialize
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Clear log buffer after initialize
	logBuf.Reset()

	// Send a ping request (should fail)
	_ = c.Ping(ctx) // Ignore error, we expect it to fail

	logOutput := logBuf.String()

	// Verify log contains request
	if !strings.Contains(logOutput, "mcp request") {
		t.Error("Log output should contain 'mcp request'")
	}

	// Verify log contains error response
	if !strings.Contains(logOutput, "mcp response error") {
		t.Error("Log output should contain 'mcp response error'")
	}

	// Verify log contains error code and message
	if !strings.Contains(logOutput, "code") {
		t.Error("Log output should contain error 'code'")
	}
	if !strings.Contains(logOutput, "message") {
		t.Error("Log output should contain error 'message'")
	}
}

// TestLogging_ContextTimeout verifies that Logger logs context timeouts.
func TestLogging_ContextTimeout(t *testing.T) {
	// Create a mock HTTP server that delays responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		if req.Method == "initialize" {
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: finemcp.InitializeResult{
					ProtocolVersion: finemcp.ProtocolVersion,
					Capabilities:    finemcp.ServerCapabilities{},
					ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
				},
			})
		} else {
			// Delay response to trigger timeout
			time.Sleep(200 * time.Millisecond)
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  struct{}{},
			})
		}
	}))
	defer server.Close()

	// Create a buffer to capture log output
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Create client with logger
	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Initialize
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Clear log buffer after initialize
	logBuf.Reset()

	// Send a ping request with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_ = c.Ping(ctx) // Ignore error, we expect it to time out

	logOutput := logBuf.String()

	// Debug: print log output if test fails
	if testing.Verbose() {
		t.Logf("Log output: %s", logOutput)
	}

	// Verify log contains request
	if !strings.Contains(logOutput, "mcp request") {
		t.Error("Log output should contain 'mcp request'")
	}

	// Verify log contains timeout or error
	// Note: The timeout might occur before the response is received,
	// so we check for either "timeout" or "failed" (send error)
	hasTimeout := strings.Contains(logOutput, "mcp request timeout")
	hasFailed := strings.Contains(logOutput, "mcp request failed")

	if !hasTimeout && !hasFailed {
		t.Errorf("Log output should contain 'mcp request timeout' or 'mcp request failed', got: %s", logOutput)
	}
}

// TestLogging_Disabled verifies that no logging occurs when Logger is nil.
func TestLogging_Disabled(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: finemcp.InitializeResult{
					ProtocolVersion: finemcp.ProtocolVersion,
					Capabilities:    finemcp.ServerCapabilities{},
					ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
				},
			})
		case "ping":
			json.NewEncoder(w).Encode(finemcp.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  struct{}{},
			})
		}
	}))
	defer server.Close()

	// Create client WITHOUT logger
	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		// Logger: nil (not set)
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Initialize and ping should work without logging
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// Test passes if no panic occurs
}
