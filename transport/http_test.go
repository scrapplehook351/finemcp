package transport_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

// initHTTPServer creates an initialized server with a "ping" tool for HTTP tests.
func initHTTPServer(t *testing.T) *finemcp.Server {
	t.Helper()
	s := finemcp.NewServer("test", "1.0")

	tool, err := finemcp.NewTool("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("pong"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTool(tool); err != nil {
		t.Fatal(err)
	}

	initReq := "{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"" + testProtocolVersion + "\",\"capabilities\":{},\"clientInfo\":{\"name\":\"test\",\"version\":\"0.1\"}}}"
	resp, _ := s.HandleMessage(context.Background(), []byte(initReq))
	if resp.Error != nil {
		t.Fatalf("init failed: %s", resp.Error.Message)
	}

	return s
}

func TestHandler_PostInitialize(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	handler := transport.Handler(s)

	body := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"" + testProtocolVersion + "\",\"capabilities\":{},\"clientInfo\":{\"name\":\"curl\",\"version\":\"0.1\"}}}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandler_ToolsCall(t *testing.T) {
	t.Parallel()

	s := initHTTPServer(t)
	handler := transport.Handler(s)

	body := "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"ping\"}}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %s", resp.Error.Message)
	}
}

func TestHandler_ToolsList(t *testing.T) {
	t.Parallel()

	s := initHTTPServer(t)
	handler := transport.Handler(s)

	body := "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/list\"}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %s", resp.Error.Message)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	handler := transport.Handler(s)

	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, m := range methods {
		req := httptest.NewRequest(m, "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", m, rec.Code)
		}
	}
}

func TestHandler_EmptyBody(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	handler := transport.Handler(s)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	handler := transport.Handler(s)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC parse error is still a response)", rec.Code)
	}

	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != finemcp.ErrCodeParseError {
		t.Errorf("expected parse error, got %+v", resp.Error)
	}
}

func TestHandler_Notification(t *testing.T) {
	t.Parallel()

	s := initHTTPServer(t)
	handler := transport.Handler(s)

	body := "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestHandler_ReadError(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	handler := transport.Handler(s)

	req := httptest.NewRequest(http.MethodPost, "/mcp", &failReader{})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

// failReader always returns an error on Read.
type failReader struct{}

func (*failReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestHandler_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	// Use a tiny limit of 16 bytes.
	handler := transport.Handler(s, 16)

	// Send a body larger than 16 bytes.
	body := strings.Repeat("x", 100)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandler_CustomMaxBodySize(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	// Set a generous custom limit (8 MB).
	handler := transport.Handler(s, 8<<20)

	// A valid initialize request should work fine.
	body := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"" + testProtocolVersion + "\",\"capabilities\":{},\"clientInfo\":{\"name\":\"curl\",\"version\":\"0.1\"}}}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandler_DefaultMaxBodySize(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	// No custom limit — default 4 MB applies.
	handler := transport.Handler(s)

	// A normal request well under 4 MB should succeed.
	body := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"" + testProtocolVersion + "\",\"capabilities\":{},\"clientInfo\":{\"name\":\"test\",\"version\":\"0.1\"}}}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
