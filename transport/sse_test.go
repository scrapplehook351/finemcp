package transport_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

const sseTestTimeout = 5 * time.Second

type sseEvent struct {
	Event string
	Data  string
}

func readNextSSEEvent(scanner *bufio.Scanner) (sseEvent, bool) {
	var ev sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if ev.Event != "" || ev.Data != "" {
				return ev, true
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			ev.Event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if ev.Data != "" {
				ev.Data += "\n"
			}
			ev.Data += strings.TrimPrefix(line, "data: ")
		}
	}
	return ev, false
}

type sseConn struct {
	EndpointURL string
	scanner     *bufio.Scanner
	resp        *http.Response
	cancel      context.CancelFunc
	closeOnce   sync.Once
}

func (c *sseConn) readEvent(t *testing.T) sseEvent {
	t.Helper()
	type result struct {
		ev sseEvent
		ok bool
	}
	ch := make(chan result, 1)
	go func() {
		ev, ok := readNextSSEEvent(c.scanner)
		ch <- result{ev, ok}
	}()
	select {
	case r := <-ch:
		if !r.ok {
			t.Fatal("SSE stream closed unexpectedly")
		}
		return r.ev
	case <-time.After(sseTestTimeout):
		t.Fatal("timeout waiting for SSE event")
		return sseEvent{}
	}
}

func (c *sseConn) close() {
	c.closeOnce.Do(func() {
		c.cancel()
		c.resp.Body.Close()
	})
}

func dialSSE(t *testing.T, serverURL string) *sseConn {
	return dialSSEAt(t, serverURL, "/sse")
}

func dialSSEAt(t *testing.T, serverURL, ssePath string) *sseConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+ssePath, nil)
	if err != nil {
		cancel()
		t.Fatalf("create SSE request: %v", err)
	}
	// Use a transport with a ResponseHeaderTimeout so a deadlocked handler
	// fails fast instead of hanging until the go-test timeout. This only
	// limits the time to receive the first response header, not the body
	// stream, so it's safe for long-lived SSE connections.
	client := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: sseTestTimeout,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("SSE dial: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	conn := &sseConn{
		scanner: bufio.NewScanner(resp.Body),
		resp:    resp,
		cancel:  cancel,
	}
	t.Cleanup(conn.close)
	ev := conn.readEvent(t)
	if ev.Event != "endpoint" {
		t.Fatalf("first SSE event = %q, want endpoint", ev.Event)
	}
	conn.EndpointURL = ev.Data
	return conn
}

func newSSETestServer(t *testing.T, opts ...transport.SSEOption) *httptest.Server {
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
	ts := httptest.NewServer(transport.SSEHandler(s, opts...))
	t.Cleanup(ts.Close)
	return ts
}

func initViaSSE(t *testing.T, ts *httptest.Server, conn *sseConn) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	resp := postToSSE(t, ts.URL, conn.EndpointURL, body)
	resp.Body.Close()
	ev := conn.readEvent(t)
	if ev.Event != "message" {
		t.Fatalf("init response event = %q, want message", ev.Event)
	}
	var rpc finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(ev.Data), &rpc); err != nil {
		t.Fatalf("unmarshal init response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("init error: %s", rpc.Error.Message)
	}
}

func postToSSE(t *testing.T, serverURL, endpointURL, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(serverURL+endpointURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp
}

func TestSSEHandler_Connect_EndpointEvent(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	if !strings.Contains(conn.EndpointURL, "sessionId=") {
		t.Errorf("endpoint URL missing sessionId: %s", conn.EndpointURL)
	}
	if !strings.HasPrefix(conn.EndpointURL, "/message?") {
		t.Errorf("endpoint URL prefix = %q, want /message?", conn.EndpointURL)
	}
}

func TestSSEHandler_Connect_SSEHeaders(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), sseTestTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestSSEHandler_Initialize_ViaSSE(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	resp := postToSSE(t, ts.URL, conn.EndpointURL, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST status = %d, want 202", resp.StatusCode)
	}
	ev := conn.readEvent(t)
	if ev.Event != "message" {
		t.Fatalf("event type = %q, want message", ev.Event)
	}
	var rpc finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(ev.Data), &rpc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("init error: %s", rpc.Error.Message)
	}
}

func TestSSEHandler_ToolsCall_ViaSSE(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	initViaSSE(t, ts, conn)
	callBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}`
	resp := postToSSE(t, ts.URL, conn.EndpointURL, callBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	ev := conn.readEvent(t)
	if ev.Event != "message" {
		t.Fatalf("event = %q, want message", ev.Event)
	}
	var rpc finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(ev.Data), &rpc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("call error: %s", rpc.Error.Message)
	}
}

func TestSSEHandler_ToolsList_ViaSSE(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	initViaSSE(t, ts, conn)
	listBody := `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	resp := postToSSE(t, ts.URL, conn.EndpointURL, listBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	ev := conn.readEvent(t)
	if ev.Event != "message" {
		t.Fatalf("event = %q, want message", ev.Event)
	}
	if !strings.Contains(ev.Data, "ping") {
		t.Errorf("tools/list response missing ping: %s", ev.Data)
	}
}

func TestSSEHandler_PostSSE_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	resp, err := http.Post(ts.URL+"/sse", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestSSEHandler_GetMessage_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	resp, err := http.Get(ts.URL + "/message?sessionId=anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestSSEHandler_MissingSessionID(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	resp, err := http.Post(ts.URL+"/message", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSSEHandler_InvalidSessionID(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	resp, err := http.Post(ts.URL+"/message?sessionId=nonexistent", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSSEHandler_EmptyBody(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	resp, err := http.Post(ts.URL+conn.EndpointURL, "application/json", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSSEHandler_UnknownPath(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	resp, err := http.Get(ts.URL + "/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSSEHandler_Notification_Returns202(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	initViaSSE(t, ts, conn)
	notifBody := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	resp := postToSSE(t, ts.URL, conn.EndpointURL, notifBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
}

func TestSSEHandler_CustomPaths(t *testing.T) {
	t.Parallel()
	s := finemcp.NewServer("test", "1.0")
	ts := httptest.NewServer(transport.SSEHandler(s,
		transport.WithSSEPath("/events"),
		transport.WithMessagePath("/rpc"),
	))
	t.Cleanup(ts.Close)
	conn := dialSSEAt(t, ts.URL, "/events")
	if !strings.HasPrefix(conn.EndpointURL, "/rpc?") {
		t.Errorf("endpoint = %q, want prefix /rpc?", conn.EndpointURL)
	}
}

func TestSSEHandler_MultipleClients(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn1 := dialSSE(t, ts.URL)
	conn2 := dialSSE(t, ts.URL)
	if conn1.EndpointURL == conn2.EndpointURL {
		t.Error("two clients received the same session endpoint")
	}
}

func TestSSEHandler_SessionCleanup(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	endpoint := conn.EndpointURL
	conn.close()

	// Poll until the session is cleaned up instead of a fixed sleep.
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Post(ts.URL+endpoint, "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return // session cleaned up as expected
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %d, want 404 (session not cleaned up within deadline)", resp.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSSEHandler_KeepAlive_EmitsComment(t *testing.T) {
	t.Parallel()
	// Use a very short keepalive interval so the test doesn't take long.
	s := finemcp.NewServer("test", "1.0")
	ts := httptest.NewServer(transport.SSEHandler(s,
		transport.WithKeepAlive(50*time.Millisecond),
	))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	// Consume the initial "endpoint" event lines until blank line.
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}

	// Wait for a keepalive comment. Read raw lines until we see one
	// starting with ":" or timeout.
	deadline := time.After(2 * time.Second)
	found := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, ": keepalive") {
				found <- true
				return
			}
		}
		found <- false
	}()

	select {
	case ok := <-found:
		if !ok {
			t.Error("stream closed without receiving keepalive comment")
		}
	case <-deadline:
		t.Error("timeout waiting for keepalive comment")
	}
}

func TestSSEHandler_KeepAlive_DisabledNoComment(t *testing.T) {
	t.Parallel()
	// keepAlive=0 should disable keepalive comments entirely.
	s := finemcp.NewServer("test", "1.0")
	ts := httptest.NewServer(transport.SSEHandler(s,
		transport.WithKeepAlive(0),
	))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	// Consume the initial "endpoint" event lines.
	for scanner.Scan() {
		if scanner.Text() == "" {
			break
		}
	}

	// With keepalive disabled, no comment should arrive within a reasonable window.
	gotComment := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, ": keepalive") {
				gotComment <- true
				return
			}
		}
		gotComment <- false
	}()

	select {
	case got := <-gotComment:
		if got {
			t.Error("received keepalive comment despite keepAlive=0")
		}
	case <-time.After(300 * time.Millisecond):
		// Expected: no comment within window. Test passes.
	}
}

func TestSSEHandler_MaxBodySize_DefaultRejectsOversized(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t)
	conn := dialSSE(t, ts.URL)
	defer conn.close()
	initViaSSE(t, ts, conn)

	// Default limit is 4 MB. Send a body slightly over that.
	oversized := strings.Repeat("x", 4<<20+1)
	resp := postToSSE(t, ts.URL, conn.EndpointURL, oversized)
	resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestSSEHandler_MaxBodySize_CustomLimit(t *testing.T) {
	t.Parallel()
	ts := newSSETestServer(t, transport.WithMaxBodySize(256))
	conn := dialSSE(t, ts.URL)
	defer conn.close()
	initViaSSE(t, ts, conn)

	// 257 bytes exceeds the 256-byte limit.
	oversized := strings.Repeat("x", 257)
	resp := postToSSE(t, ts.URL, conn.EndpointURL, oversized)
	resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}

	// A body within the limit should get past the size check.
	small := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	resp2 := postToSSE(t, ts.URL, conn.EndpointURL, small)
	resp2.Body.Close()

	if resp2.StatusCode == http.StatusRequestEntityTooLarge {
		t.Error("small body was rejected by size limit")
	}
}

func TestSSEHandler_ProgressNotification(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	worker, err := finemcp.NewTool("worker", func(ctx context.Context, _ []byte) ([]byte, error) {
		finemcp.ReportProgress(ctx, 25, 100)
		finemcp.ReportProgress(ctx, 75, 100)
		return []byte(`"done"`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterTool(worker)
	ts := httptest.NewServer(transport.SSEHandler(s))
	t.Cleanup(ts.Close)

	conn := dialSSE(t, ts.URL)
	defer conn.close()
	initViaSSE(t, ts, conn)

	// Call the worker tool.
	body := `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"worker"}}`
	resp := postToSSE(t, ts.URL, conn.EndpointURL, body)
	resp.Body.Close()

	// Read SSE events: expect 2 progress notifications + 1 tool response.
	var progressEvents []sseEvent
	var resultEvent sseEvent
	for i := 0; i < 3; i++ {
		ev := conn.readEvent(t)
		var msg map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &msg); err != nil {
			t.Fatalf("event %d: invalid JSON: %v", i, err)
		}
		if msg["method"] == "notifications/progress" {
			progressEvents = append(progressEvents, ev)
		} else {
			resultEvent = ev
		}
	}

	if len(progressEvents) != 2 {
		t.Fatalf("expected 2 progress events, got %d", len(progressEvents))
	}

	// Verify progress notifications have correct structure.
	for i, ev := range progressEvents {
		var msg map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &msg); err != nil {
			t.Fatalf("progress event %d: invalid JSON: %v", i, err)
		}
		if _, hasID := msg["id"]; hasID {
			t.Errorf("progress event %d: must not have an id field", i)
		}
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			t.Fatalf("progress event %d: missing params", i)
		}
		if params["progressToken"] != float64(99) {
			t.Errorf("progress event %d: progressToken = %v, want 99", i, params["progressToken"])
		}
	}

	// Verify the tool result.
	var rpc finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(resultEvent.Data), &rpc); err != nil {
		t.Fatalf("result: invalid JSON: %v", err)
	}
	if rpc.Error != nil {
		t.Errorf("unexpected error in tool response: %s", rpc.Error.Message)
	}
}
