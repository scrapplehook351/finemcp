// Package clienttest provides test helpers for code that uses the finemcp client SDK.
//
// It offers an in-memory mock server transport with request recording,
// queued responses/errors, and notification simulation.
package clienttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// Request is a recorded client request.
//
// Notifications are not recorded, only requests with an id.
type Request struct {
	Method string
	ID     any
	Params json.RawMessage
}

type queuedResponse struct {
	result any
	err    *finemcp.JSONRPCError
}

// MockServer is an in-memory MCP server facade for client-side testing.
type MockServer struct {
	mu       sync.Mutex
	started  bool
	closed   bool
	requests []Request
	queued   map[string][]queuedResponse
	incoming chan []byte
}

// NewMockServer creates a new mock server.
func NewMockServer() *MockServer {
	return &MockServer{
		requests: make([]Request, 0, 8),
		queued:   make(map[string][]queuedResponse),
		incoming: make(chan []byte, 256),
	}
}

// NewInitializedMockServer creates a server preloaded with a successful
// initialize response.
func NewInitializedMockServer() *MockServer {
	m := NewMockServer()
	m.QueueResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		Capabilities:    finemcp.ServerCapabilities{},
		ServerInfo: finemcp.ProcessInfo{
			Name:    "mock-server",
			Version: "1.0.0",
		},
	})
	return m
}

// AsTransport returns a client transport backed by this mock server.
func (m *MockServer) AsTransport() client.Transport {
	return &Transport{server: m}
}

// QueueResponse appends a successful response for the given method.
func (m *MockServer) QueueResponse(method string, result any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queued[method] = append(m.queued[method], queuedResponse{result: result})
}

// QueueError appends an error response for the given method.
func (m *MockServer) QueueError(method string, code int, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queued[method] = append(m.queued[method], queuedResponse{
		err: &finemcp.JSONRPCError{Code: code, Message: message},
	})
}

// QueueToolsList is a convenience helper for tools/list.
func (m *MockServer) QueueToolsList(tools []finemcp.ToolInfo) {
	m.QueueResponse(finemcp.MethodToolsList, finemcp.ListToolsResult{Tools: tools})
}

// QueueResourcesList is a convenience helper for resources/list.
func (m *MockServer) QueueResourcesList(resources []finemcp.ResourceInfo) {
	m.QueueResponse(finemcp.MethodResourcesList, finemcp.ListResourcesResult{Resources: resources})
}

// QueuePromptsList is a convenience helper for prompts/list.
func (m *MockServer) QueuePromptsList(prompts []finemcp.PromptInfo) {
	m.QueueResponse(finemcp.MethodPromptsList, finemcp.ListPromptsResult{Prompts: prompts})
}

// QueueToolCallText queues a simple tools/call text result.
func (m *MockServer) QueueToolCallText(text string) {
	m.QueueResponse(finemcp.MethodToolsCall, finemcp.CallToolResult{
		Content: []finemcp.Content{finemcp.TextContent{Text: text}},
	})
}

// SendNotification injects a server notification into the receive stream.
func (m *MockServer) SendNotification(method string, params any) error {
	n := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("clienttest: marshal notification: %w", err)
	}
	return m.enqueue(context.Background(), data)
}

// RecordedRequests returns a copy of all recorded requests.
func (m *MockServer) RecordedRequests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, len(m.requests))
	copy(out, m.requests)
	return out
}

// LastRequest returns the most recent recorded request, if any.
func (m *MockServer) LastRequest() *Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	r := m.requests[len(m.requests)-1]
	return &r
}

// AssertRequestCount asserts the number of recorded requests.
func (m *MockServer) AssertRequestCount(t *testing.T, expected int) {
	t.Helper()
	got := len(m.RecordedRequests())
	if got != expected {
		t.Fatalf("clienttest: request count mismatch: got=%d want=%d", got, expected)
	}
}

// AssertMethodCalled asserts that at least one request used the given method.
func (m *MockServer) AssertMethodCalled(t *testing.T, method string) {
	t.Helper()
	for _, r := range m.RecordedRequests() {
		if r.Method == method {
			return
		}
	}
	t.Fatalf("clienttest: expected method %q to be called", method)
}

// Reset clears requests and queued method responses.
func (m *MockServer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = m.requests[:0]
	m.queued = make(map[string][]queuedResponse)
	m.started = false
	m.closed = false
}

func (m *MockServer) enqueue(ctx context.Context, data []byte) error {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return errors.New("clienttest: transport closed")
	}

	select {
	case m.incoming <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *MockServer) popQueued(method string) queuedResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queued[method]
	if len(q) == 0 {
		return queuedResponse{
			err: &finemcp.JSONRPCError{Code: finemcp.ErrCodeMethodNotFound, Message: "method not found"},
		}
	}
	out := q[0]
	m.queued[method] = q[1:]
	return out
}

func (m *MockServer) addRequest(r Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, r)
}

// Transport implements client.Transport for MockServer.
type Transport struct {
	server *MockServer
}

// Start starts the in-memory transport.
func (t *Transport) Start(_ context.Context) error {
	t.server.mu.Lock()
	defer t.server.mu.Unlock()
	if t.server.started {
		return errors.New("clienttest: transport already started")
	}
	if t.server.closed {
		return errors.New("clienttest: transport closed")
	}
	t.server.started = true
	return nil
}

// Send records requests and enqueues corresponding queued responses.
func (t *Transport) Send(ctx context.Context, data []byte) error {
	var req finemcp.JSONRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("clienttest: decode request: %w", err)
	}

	if req.IsNotification() {
		return nil
	}

	t.server.addRequest(Request{Method: req.Method, ID: req.ID, Params: req.Params})
	queued := t.server.popQueued(req.Method)

	resp := finemcp.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID}
	if queued.err != nil {
		resp.Error = queued.err
	} else {
		resp.Result = queued.result
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("clienttest: encode response: %w", err)
	}

	return t.server.enqueue(ctx, respData)
}

// Receive returns queued server messages (responses or notifications).
func (t *Transport) Receive(ctx context.Context) ([]byte, error) {
	for {
		t.server.mu.Lock()
		closed := t.server.closed
		t.server.mu.Unlock()

		if closed {
			select {
			case data := <-t.server.incoming:
				return data, nil
			default:
				return nil, io.EOF
			}
		}

		select {
		case data := <-t.server.incoming:
			return data, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Close closes the in-memory transport.
func (t *Transport) Close() error {
	t.server.mu.Lock()
	defer t.server.mu.Unlock()
	t.server.closed = true
	return nil
}
