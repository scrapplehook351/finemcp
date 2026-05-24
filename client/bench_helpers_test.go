package client_test

// bench_helpers_test.go contains shared benchmark helpers for the L2
// performance benchmark suite.  All types and functions here are in the
// client_test package so they integrate naturally with the existing test
// helpers (mockTransport, etc.) defined in client_test.go.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// ── autoBenchTransport ────────────────────────────────────────────────
//
// autoBenchTransport is a lock-free, always-responding transport for
// benchmarks.  Unlike MockServer (which requires pre-queuing one response
// per request), autoBenchTransport generates a response inline inside Send
// so benchmarks can call b.N iterations without pre-population.

// autoBenchTransport responds to every JSON-RPC request with a configurable
// preset result. It never blocks: the response is written to the incoming
// channel synchronously inside Send.
type autoBenchTransport struct {
	mu       sync.Mutex
	closed   bool
	incoming chan []byte

	// responses holds method→result overrides.  Methods not found here
	// receive a generic empty-object result.
	responses map[string]any
}

func newAutoBenchTransport() *autoBenchTransport {
	return &autoBenchTransport{
		incoming:  make(chan []byte, 1024),
		responses: make(map[string]any),
	}
}

// setResponse registers a preset result for the given JSON-RPC method.
func (t *autoBenchTransport) setResponse(method string, result any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.responses[method] = result
}

func (t *autoBenchTransport) Start(_ context.Context) error { return nil }

func (t *autoBenchTransport) Send(ctx context.Context, data []byte) error {
	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(data, &req); err != nil || req.ID == nil {
		return nil // notifications — ignore
	}

	t.mu.Lock()
	result, ok := t.responses[req.Method]
	closed := t.closed
	t.mu.Unlock()

	if closed {
		return nil
	}
	if !ok {
		result = struct{}{} // default empty-object response
	}

	resp := finemcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	select {
	case t.incoming <- b:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (t *autoBenchTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case msg := <-t.incoming:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *autoBenchTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

// ── newBenchClient ────────────────────────────────────────────────────

// newBenchClient creates an autoBenchTransport-backed, initialised client
// ready for benchmarking.  It registers default responses for the most
// common MCP methods.  The returned cleanup function closes the client and
// should be deferred.
func newBenchClient(b *testing.B) (*client.Client, func()) {
	b.Helper()

	tr := newAutoBenchTransport()

	// initialize response.
	tr.setResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		Capabilities:    finemcp.ServerCapabilities{},
		ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0.0"},
	})

	// tools/list response.
	tr.setResponse(finemcp.MethodToolsList, finemcp.ListToolsResult{
		Tools: []finemcp.ToolInfo{
			{Name: "echo", Description: "echo tool"},
			{Name: "add", Description: "add tool"},
		},
	})

	// tools/call response — small text.
	tr.setResponse(finemcp.MethodToolsCall, finemcp.CallToolResult{
		Content: []finemcp.Content{
			finemcp.TextContent{Text: "ok"},
		},
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "bench-client", Version: "1.0.0"},
	})
	if err != nil {
		b.Fatalf("newBenchClient: client.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		b.Fatalf("newBenchClient: Initialize: %v", err)
	}

	return c, func() { _ = c.Close() }
}

// newBenchClientWithResponse creates a bench client whose tools/call always
// returns a response that includes the given text payload.
func newBenchClientWithResponse(b *testing.B, toolCallResult finemcp.CallToolResult) (*client.Client, func()) {
	b.Helper()

	tr := newAutoBenchTransport()

	tr.setResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		Capabilities:    finemcp.ServerCapabilities{},
		ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0.0"},
	})
	tr.setResponse(finemcp.MethodToolsList, finemcp.ListToolsResult{
		Tools: []finemcp.ToolInfo{{Name: "echo", Description: "echo tool"}},
	})
	tr.setResponse(finemcp.MethodToolsCall, toolCallResult)

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "bench-client", Version: "1.0.0"},
	})
	if err != nil {
		b.Fatalf("newBenchClientWithResponse: client.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		b.Fatalf("newBenchClientWithResponse: Initialize: %v", err)
	}

	return c, func() { _ = c.Close() }
}

// makePayload returns a string of exactly n bytes composed of 'x' characters.
func makePayload(n int) string {
	return strings.Repeat("x", n)
}
