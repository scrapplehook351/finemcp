package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

const streamableTestTimeout = 5 * time.Second

// ── Helpers ─────────────────────────────────────────────────────────

func newStreamableServer(t *testing.T, opts ...transport.StreamableOption) (*httptest.Server, *finemcp.Server) {
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
	ts := httptest.NewServer(transport.StreamableHandler(s, opts...))
	t.Cleanup(ts.Close)
	return ts, s
}

func initBody() string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
}

func postStreamable(t *testing.T, url, sessionID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func initStreamable(t *testing.T, url string) string {
	t.Helper()
	resp := postStreamable(t, url, "", initBody())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("initialize: status = %d, body = %s", resp.StatusCode, body)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("initialize: missing Mcp-Session-Id header")
	}
	return sessionID
}

func readStreamableJSON(t *testing.T, resp *http.Response) finemcp.JSONRPCResponse {
	t.Helper()
	var rpc finemcp.JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return rpc
}

func openGETStream(t *testing.T, url, sessionID string) (*bufio.Scanner, context.CancelFunc, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		t.Fatalf("GET SSE: status = %d, want 200", resp.StatusCode)
	}
	return bufio.NewScanner(resp.Body), cancel, resp
}

// readStreamableSSE reads the next SSE event with a timeout. Note: if the
// timeout fires, the goroutine calling readNextSSEEvent is leaked because
// scanner.Scan blocks on the underlying reader. This is acceptable in tests
// because the test process terminates shortly after, and t.Fatal is called
// from the test goroutine (not the spawned one), so it is safe.
func readStreamableSSE(t *testing.T, scanner *bufio.Scanner) sseEvent {
	t.Helper()
	type result struct {
		ev sseEvent
		ok bool
	}
	ch := make(chan result, 1)
	go func() {
		ev, ok := readNextSSEEvent(scanner)
		ch <- result{ev, ok}
	}()
	select {
	case r := <-ch:
		if !r.ok {
			t.Fatal("SSE stream closed unexpectedly")
		}
		return r.ev
	case <-time.After(streamableTestTimeout):
		t.Fatal("timeout waiting for SSE event")
		return sseEvent{}
	}
}

// ── Category 1: POST Basics ────────────────────────────────────────

func TestStreamable_Post_Initialize(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	resp := postStreamable(t, ts.URL, "", initBody())
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Error("missing Mcp-Session-Id header")
	}
	rpc := readStreamableJSON(t, resp)
	if rpc.Error != nil {
		t.Fatalf("init error: %s", rpc.Error.Message)
	}
}

func TestStreamable_Post_InitializeContentType(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	resp := postStreamable(t, ts.URL, "", initBody())
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestStreamable_Post_ToolsList(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ping") {
		t.Errorf("tools/list missing ping: %s", body)
	}
}

func TestStreamable_Post_ToolsCall(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rpc := readStreamableJSON(t, resp)
	if rpc.Error != nil {
		t.Fatalf("call error: %s", rpc.Error.Message)
	}
}

func TestStreamable_Post_Notification_202(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
}

func TestStreamable_Post_EmptyBody_400(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStreamable_Post_InvalidJSON(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID, `{not json}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC error response)", resp.StatusCode)
	}
	rpc := readStreamableJSON(t, resp)
	if rpc.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if rpc.Error.Code != finemcp.ErrCodeParseError {
		t.Errorf("error code = %d, want %d", rpc.Error.Code, finemcp.ErrCodeParseError)
	}
}

func TestStreamable_Post_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL, strings.NewReader("{}"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// ── Category 2: Session Management ─────────────────────────────────

func TestStreamable_Session_IDOnInitialize(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	resp := postStreamable(t, ts.URL, "", initBody())
	defer resp.Body.Close()

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Error("missing Mcp-Session-Id on initialize response")
	}
	if len(sessionID) < 16 {
		t.Errorf("session ID too short: %q", sessionID)
	}
}

func TestStreamable_Session_IDRequired(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	resp := postStreamable(t, ts.URL, "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStreamable_Session_UnknownID_404(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	resp := postStreamable(t, ts.URL, "nonexistent-session-id",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStreamable_Session_Delete(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("DELETE status = %d, want 200", resp.StatusCode)
	}
}

func TestStreamable_Session_DeletePreventsReuse(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp2 := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("POST after DELETE: status = %d, want 404", resp2.StatusCode)
	}
}

func TestStreamable_Session_DeleteWithoutID_400(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStreamable_Session_DeleteUnknown_404(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", "does-not-exist")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ── Category 3: GET SSE Stream ─────────────────────────────────────

func TestStreamable_Get_OpenSSEStream(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestStreamable_Get_ToolsListChanged(t *testing.T) {
	t.Parallel()
	ts, s := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	scanner, cancel, sseResp := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel(); sseResp.Body.Close() }()

	newTool, err := finemcp.NewTool("echo", func(_ context.Context, in []byte) ([]byte, error) {
		return in, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTool(newTool); err != nil {
		t.Fatal(err)
	}
	// RegisterTool auto-notifies; no manual NotifyToolsListChanged needed.

	ev := readStreamableSSE(t, scanner)
	if ev.Event != "message" {
		t.Fatalf("event = %q, want message", ev.Event)
	}
	if !strings.Contains(ev.Data, "notifications/tools/list_changed") {
		t.Errorf("expected tools/list_changed notification, got: %s", ev.Data)
	}
}

func TestStreamable_Get_WithoutSession_400(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestStreamable_Get_UnknownSession_404(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", "nonexistent")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStreamable_Get_WrongAccept_406(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	// GET with Accept that doesn't include text/event-stream should be rejected.
	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406", resp.StatusCode)
	}
}

func TestStreamable_Get_Keepalive(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t, transport.WithStreamableKeepAlive(50*time.Millisecond))
	sessionID := initStreamable(t, ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	found := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), ": keepalive") {
				found <- true
				return
			}
		}
		found <- false
	}()

	select {
	case ok := <-found:
		if !ok {
			t.Error("stream closed without keepalive")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for keepalive")
	}
}

func TestStreamable_Get_SessionDeleteClosesStream(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	scanner, cancel, sseResp := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel(); sseResp.Body.Close() }()

	time.Sleep(50 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	closed := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
		}
		closed <- true
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Error("GET SSE stream did not close after DELETE")
	}
}

// ── Category 4: Progress Notifications via GET ─────────────────────

func TestStreamable_Progress_ViaGETStream(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	worker, err := finemcp.NewTool("worker", func(ctx context.Context, _ []byte) ([]byte, error) {
		finemcp.ReportProgress(ctx, 50, 100)
		return []byte(`"done"`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTool(worker); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(transport.StreamableHandler(s))
	t.Cleanup(ts.Close)

	sessionID := initStreamable(t, ts.URL)

	scanner, cancel, sseResp := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel(); sseResp.Body.Close() }()

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"worker"}}`)
	defer resp.Body.Close()

	rpc := readStreamableJSON(t, resp)
	if rpc.Error != nil {
		t.Fatalf("tool call error: %s", rpc.Error.Message)
	}

	ev := readStreamableSSE(t, scanner)
	if ev.Event != "message" {
		t.Fatalf("event = %q, want message", ev.Event)
	}
	if !strings.Contains(ev.Data, "notifications/progress") {
		t.Errorf("expected progress notification, got: %s", ev.Data)
	}
}

// ── Category 5: Resource Subscriptions ─────────────────────────────

func TestStreamable_Subscription_ResourceUpdated(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0", finemcp.WithResourceSubscriptions())
	res, err := finemcp.NewResource("file:///data.json", "data", func(_ context.Context, _ string) ([]finemcp.ResourceContent, error) {
		return []finemcp.ResourceContent{finemcp.NewTextResourceContent("file:///data.json", "hello")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(res); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(transport.StreamableHandler(s))
	t.Cleanup(ts.Close)

	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	scanner, cancel, sseResp := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel(); sseResp.Body.Close() }()

	resp = postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":10,"method":"resources/subscribe","params":{"uri":"file:///data.json"}}`)
	resp.Body.Close()

	s.NotifyResourceUpdated("file:///data.json")

	ev := readStreamableSSE(t, scanner)
	if ev.Event != "message" {
		t.Fatalf("event = %q, want message", ev.Event)
	}
	if !strings.Contains(ev.Data, "notifications/resources/updated") {
		t.Errorf("expected resources/updated, got: %s", ev.Data)
	}
}

func TestStreamable_Subscription_CleanupOnDelete(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0", finemcp.WithResourceSubscriptions())
	res, err := finemcp.NewResource("file:///data.json", "data", func(_ context.Context, _ string) ([]finemcp.ResourceContent, error) {
		return []finemcp.ResourceContent{finemcp.NewTextResourceContent("file:///data.json", "hello")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(res); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(transport.StreamableHandler(s))
	t.Cleanup(ts.Close)

	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	resp = postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":10,"method":"resources/subscribe","params":{"uri":"file:///data.json"}}`)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", sessionID)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()

	// Should not panic after session is deleted.
	s.NotifyResourceUpdated("file:///data.json")
}

// ── Category 6: Edge Cases ─────────────────────────────────────────

func TestStreamable_MaxBodySize(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t, transport.WithStreamableMaxBody(256))
	sessionID := initStreamable(t, ts.URL)

	oversized := `{"jsonrpc":"2.0","id":1,"method":"ping","params":` +
		strings.Repeat(`"x"`, 100) + `}`
	resp := postStreamable(t, ts.URL, sessionID, oversized)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestStreamable_ConcurrentPOSTs(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan string, n)
	wg.Add(n)
	for i := range n {
		go func(id int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, id)
			resp := postStreamable(t, ts.URL, sessionID, body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- "unexpected status"
				return
			}
			rpc := readStreamableJSON(t, resp)
			if rpc.Error != nil {
				errs <- rpc.Error.Message
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent POST error: %s", e)
	}
}

func TestStreamable_OriginValidator(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t, transport.WithStreamableOriginValidator(func(origin string) bool {
		return origin == "https://allowed.example.com"
	}))

	// Blocked origin.
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(initBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("blocked origin: status = %d, want 403", resp.StatusCode)
	}

	// Allowed origin.
	req2, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(initBody()))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	req2.Header.Set("Origin", "https://allowed.example.com")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode == http.StatusForbidden {
		t.Error("allowed origin was blocked")
	}
}

func TestStreamable_OriginValidator_NoOriginHeader(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t, transport.WithStreamableOriginValidator(func(origin string) bool {
		return origin == "https://allowed.example.com"
	}))

	// Request without an Origin header should be allowed (non-browser clients).
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(initBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Deliberately NOT setting Origin.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Error("request without Origin header was incorrectly blocked")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStreamable_ConcurrentDELETEAndPOST(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	// Send notifications/initialized so the session is fully set up.
	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	// Race a DELETE against concurrent POSTs. The session.done guard in
	// handlePost must prevent panics or data races; POSTs may return 200
	// (completed before delete) or 404 (session already gone).
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n + 1) // n POSTs + 1 DELETE

	// Start POSTs.
	for i := range n {
		go func(id int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, id)
			r := postStreamable(t, ts.URL, sessionID, body)
			r.Body.Close()
			// Either 200 (success) or 404 (session gone) is acceptable.
			if r.StatusCode != http.StatusOK && r.StatusCode != http.StatusNotFound {
				t.Errorf("concurrent POST: status = %d, want 200 or 404", r.StatusCode)
			}
		}(i)
	}

	// Start DELETE.
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodDelete, ts.URL, nil)
		req.Header.Set("Mcp-Session-Id", sessionID)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("DELETE failed: %v", err)
			return
		}
		r.Body.Close()
	}()

	wg.Wait()

	// After all goroutines complete, the session must be gone.
	finalResp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":999,"method":"tools/list"}`)
	defer finalResp.Body.Close()
	if finalResp.StatusCode != http.StatusNotFound {
		t.Errorf("POST after DELETE+race: status = %d, want 404", finalResp.StatusCode)
	}
}

func TestStreamable_GET_MultipleStreams(t *testing.T) {
	t.Parallel()
	ts, s := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	scanner1, cancel1, resp1 := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel1(); resp1.Body.Close() }()

	scanner2, cancel2, resp2 := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel2(); resp2.Body.Close() }()

	newTool, err := finemcp.NewTool("echo", func(_ context.Context, in []byte) ([]byte, error) {
		return in, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTool(newTool); err != nil {
		t.Fatal(err)
	}
	// RegisterTool auto-notifies; no manual NotifyToolsListChanged needed.

	received := make(chan string, 2)
	go func() {
		ev, ok := readNextSSEEvent(scanner1)
		if ok && ev.Event == "message" {
			received <- "stream1"
		}
	}()
	go func() {
		ev, ok := readNextSSEEvent(scanner2)
		if ok && ev.Event == "message" {
			received <- "stream2"
		}
	}()

	select {
	case stream := <-received:
		t.Logf("notification received on %s", stream)
	case <-time.After(streamableTestTimeout):
		t.Error("notification not received on any stream")
	}
}

func TestStreamable_Post_UnsupportedContentType(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)
	sessionID := initStreamable(t, ts.URL)

	req, _ := http.NewRequest(http.MethodPost, ts.URL,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", resp.StatusCode)
	}
}

func TestStreamable_Post_MissingContentType_415(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	// POST without Content-Type header should be rejected per MCP spec.
	req, _ := http.NewRequest(http.MethodPost, ts.URL,
		strings.NewReader(initBody()))
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Deliberately not setting Content-Type.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", resp.StatusCode)
	}
}

func TestStreamable_Session_IdleTimeout(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t, transport.WithStreamableSessionTimeout(100*time.Millisecond))
	sessionID := initStreamable(t, ts.URL)

	// Wait for the session to expire.
	time.Sleep(200 * time.Millisecond)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status after timeout = %d, want 404", resp.StatusCode)
	}
}

func TestStreamable_Session_TimeoutResetsOnActivity(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t, transport.WithStreamableSessionTimeout(200*time.Millisecond))
	sessionID := initStreamable(t, ts.URL)

	// Activity within the timeout window should keep the session alive.
	time.Sleep(100 * time.Millisecond)
	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	time.Sleep(100 * time.Millisecond)
	resp = postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (session should still be alive)", resp.StatusCode)
	}
}

func TestStartStreamable_GracefulShutdown(t *testing.T) {
	t.Parallel()
	s := finemcp.NewServer("test", "1.0")

	// Use httptest.NewUnstartedServer to learn a free port without TOCTOU race.
	dummy := httptest.NewUnstartedServer(nil)
	addr := dummy.Listener.Addr().String()
	dummy.Close() // releases the port for StartStreamable

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- transport.StartStreamable(ctx, s, addr)
	}()

	// Poll until the server is accepting connections.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel the context to trigger graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartStreamable returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartStreamable did not shut down within 10 seconds")
	}
}

func TestStreamable_GETBufferSize(t *testing.T) {
	t.Parallel()
	ts, s := newStreamableServer(t, transport.WithStreamableGETBufferSize(2))
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	scanner, cancel, sseResp := openGETStream(t, ts.URL, sessionID)
	defer func() { cancel(); sseResp.Body.Close() }()

	newTool, err := finemcp.NewTool("echo", func(_ context.Context, in []byte) ([]byte, error) {
		return in, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTool(newTool); err != nil {
		t.Fatal(err)
	}
	// RegisterTool auto-notifies; no manual NotifyToolsListChanged needed.

	ev := readStreamableSSE(t, scanner)
	if ev.Event != "message" {
		t.Fatalf("event = %q, want message", ev.Event)
	}
	if !strings.Contains(ev.Data, "notifications/tools/list_changed") {
		t.Errorf("expected tools/list_changed notification, got: %s", ev.Data)
	}
}

func TestStreamable_Post_CaseInsensitiveContentType(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	// Send initialize with uppercase Content-Type; should be accepted.
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(initBody()))
	req.Header.Set("Content-Type", "APPLICATION/JSON")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Error("uppercase Content-Type APPLICATION/JSON was rejected as 415")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStreamable_Post_CaseInsensitiveAccept(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	// Send initialize with mixed-case Accept header; should be accepted.
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(initBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "Application/JSON, Text/Event-Stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotAcceptable {
		t.Error("mixed-case Accept header was rejected as 406")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStreamable_Post_InitializeNotification_NoOrphan(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	// An initialize message without an "id" is a notification, not a request.
	// It must NOT create a session (which would be orphaned).
	resp := postStreamable(t, ts.URL, "",
		`{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"`+
			testProtocolVersion+
			`","capabilities":{},"clientInfo":{"name":"t","version":"0.1"}}}`)
	defer resp.Body.Close()

	// Should be rejected: no session ID header, and no session created.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Error("session ID should not be returned for a notification")
	}
}

func TestStreamable_Post_AcceptWildcard(t *testing.T) {
	t.Parallel()
	ts, _ := newStreamableServer(t)

	// Accept: */* should be accepted (matches the wildcard branch).
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(initBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotAcceptable {
		t.Error("Accept: */* was rejected as 406")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStreamable_GETBufferSize_DropWhenFull(t *testing.T) {
	t.Parallel()
	// Use a buffer of 1 so we can easily overflow it.
	ts, s := newStreamableServer(t, transport.WithStreamableGETBufferSize(1))
	sessionID := initStreamable(t, ts.URL)

	resp := postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp.Body.Close()

	// Open a GET stream but do NOT read from it — let the buffer fill.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	sseResp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); sseResp.Body.Close() }()

	// Register multiple tools and send many notifications to overflow the buffer.
	for i := 0; i < 5; i++ {
		tool, terr := finemcp.NewTool(
			"tool"+strings.Repeat("x", i+1),
			func(_ context.Context, in []byte) ([]byte, error) { return in, nil },
		)
		if terr != nil {
			t.Fatal(terr)
		}
		if err := s.RegisterTool(tool); err != nil {
			t.Fatal(err)
		}
		// RegisterTool auto-notifies once per call. With buffer=1 and 5
		// registrations, notifications 2-5 are dropped — the overflow
		// scenario under test.
	}

	// Give notifications time to be attempted.
	time.Sleep(100 * time.Millisecond)

	// The server should not have panicked or blocked. Verify the session
	// is still alive by making a successful POST request.
	resp = postStreamable(t, ts.URL, sessionID,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status after buffer overflow = %d, want 200 (session should survive)", resp.StatusCode)
	}
}
