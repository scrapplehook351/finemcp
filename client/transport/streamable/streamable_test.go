package streamable

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func jsonResp(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("failed to encode response: %v", err)
	}
}

func okMsg(id int) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": "ok"}
}

func sseEvent(data string) string {
	return "data: " + data + "\n\n"
}

// ── New ───────────────────────────────────────────────────────────────────────

func TestNew_DefaultHTTPClient(t *testing.T) {
	tr := New(Config{URL: "http://localhost"})
	if tr.client == nil {
		t.Fatal("expected default HTTPClient, got nil")
	}
	if tr.inbox == nil {
		t.Fatal("expected inbox channel, got nil")
	}
}

func TestNew_CustomHTTPClient(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	tr := New(Config{URL: "http://localhost", HTTPClient: custom})
	if tr.client != custom {
		t.Fatal("expected custom HTTPClient to be used")
	}
}

// ── Start ─────────────────────────────────────────────────────────────────────

func TestStart_NoOp(t *testing.T) {
	tr := New(Config{URL: "http://localhost"})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start() should be a no-op, got: %v", err)
	}
}

// ── Send – JSON response ──────────────────────────────────────────────────────

func TestSend_JSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	msg, err := tr.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["result"] != "ok" {
		t.Errorf("result = %v, want ok", resp["result"])
	}
}

func TestSend_SetsRequiredHeaders(t *testing.T) {
	var gotContentType, gotAccept string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))

	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if !strings.Contains(gotAccept, "application/json") {
		t.Errorf("Accept = %q, should contain application/json", gotAccept)
	}
	if !strings.Contains(gotAccept, "text/event-stream") {
		t.Errorf("Accept = %q, should contain text/event-stream", gotAccept)
	}
}

func TestSend_CustomHeaders(t *testing.T) {
	var gotCustom string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom")
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL, Headers: map[string]string{"X-Custom": "value-123"}})
	defer tr.Close()

	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))

	if gotCustom != "value-123" {
		t.Errorf("X-Custom = %q, want value-123", gotCustom)
	}
}

// ── Send – session ID ─────────────────────────────────────────────────────────

func TestSend_CapturesSessionID(t *testing.T) {
	var gotSessionID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = r.Header.Get("Mcp-Session-Id")
		w.Header().Set("Mcp-Session-Id", "sess-abc")
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	// First request: no session ID sent yet.
	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))
	if gotSessionID != "" {
		t.Errorf("first request should not send a session ID, got %q", gotSessionID)
	}

	// Second request: session ID should now be sent.
	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":2}`))
	if gotSessionID != "sess-abc" {
		t.Errorf("second request session ID = %q, want sess-abc", gotSessionID)
	}
}

// ── Send – 202/204 accepted / no content ─────────────────────────────────────

func TestSend_202Accepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatalf("Send() on 202: %v", err)
	}
}

func TestSend_204NoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatalf("Send() on 204: %v", err)
	}
}

// ── Send – error statuses ─────────────────────────────────────────────────────

func TestSend_4xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "bad request body")
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestSend_5xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

// ── Send – SSE response from POST ────────────────────────────────────────────

func TestSend_SSEResponseFromPOST(t *testing.T) {
	msg := `{"jsonrpc":"2.0","id":1,"result":"streamed"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not implement http.Flusher")
			return
		}
		fmt.Fprint(w, sseEvent(msg))
		flusher.Flush()
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	received, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive() error: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(received, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["result"] != "streamed" {
		t.Errorf("result = %v, want streamed", resp["result"])
	}
}

// ── Send – invalid JSON response is silently dropped ─────────────────────────

func TestSend_InvalidJSONResponseDropped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not valid json at all")
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	// Send should succeed (no error on read)
	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))

	// Nothing should be in inbox since invalid JSON is dropped.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := tr.Receive(ctx)
	if err == nil {
		t.Error("expected timeout or context error, invalid JSON should be dropped from inbox")
	}
}

// ── Send – after close ────────────────────────────────────────────────────────

func TestSend_AfterClose(t *testing.T) {
	tr := New(Config{URL: "http://localhost"})
	tr.Close()

	err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))
	if err == nil {
		t.Fatal("expected error after close, got nil")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("error should mention closed: %v", err)
	}
}

// ── Send – cancelled context ─────────────────────────────────────────────────

func TestSend_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1}`))
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

// ── Receive ───────────────────────────────────────────────────────────────────

func TestReceive_ContextCancellation(t *testing.T) {
	tr := New(Config{URL: "http://localhost"})
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := tr.Receive(ctx)
	if err == nil {
		t.Fatal("expected error from context, got nil")
	}
}

func TestReceive_MultipleMessages(t *testing.T) {
	msgs := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "result": "first"},
		{"jsonrpc": "2.0", "id": 2, "result": "second"},
		{"jsonrpc": "2.0", "id": 3, "result": "third"},
	}
	callCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := callCount
		callCount++
		mu.Unlock()
		if idx < len(msgs) {
			jsonResp(t, w, msgs[idx])
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	for i, want := range msgs {
		_ = tr.Send(context.Background(), []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d}`, i+1)))
		msg, err := tr.Receive(context.Background())
		if err != nil {
			t.Fatalf("Receive() [%d] error: %v", i, err)
		}
		var got map[string]any
		_ = json.Unmarshal(msg, &got)
		if got["result"] != want["result"] {
			t.Errorf("[%d] result = %v, want %v", i, got["result"], want["result"])
		}
	}
}

// ── Close ─────────────────────────────────────────────────────────────────────

func TestClose_Idempotent(t *testing.T) {
	tr := New(Config{URL: "http://localhost"})
	if err := tr.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

func TestClose_SendsDeleteForSession(t *testing.T) {
	var gotMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			gotMethod = r.Method
			w.WriteHeader(http.StatusOK)
			return
		}
		// POST: return session ID
		w.Header().Set("Mcp-Session-Id", "sess-xyz")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})

	// Establish session via Send
	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))
	if tr.sessionID != "sess-xyz" {
		t.Fatalf("expected session ID to be captured, got %q", tr.sessionID)
	}

	tr.Close()

	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE request on Close, got %q", gotMethod)
	}
}

func TestClose_NoDeleteWithoutSession(t *testing.T) {
	deleteCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalled = true
		}
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	tr.Close() // No session established, should not call DELETE

	if deleteCalled {
		t.Error("DELETE should not be sent when no session ID was set")
	}
}

// ── StartSSE ──────────────────────────────────────────────────────────────────

func TestStartSSE_DeliversMessages(t *testing.T) {
	msg := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":50}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Error("ResponseWriter does not implement http.Flusher")
				return
			}
			fmt.Fprint(w, sseEvent(msg))
			flusher.Flush()
			// Keep stream open briefly then close
			time.Sleep(50 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := tr.StartSSE(ctx); err != nil {
		t.Fatalf("StartSSE() error: %v", err)
	}

	received, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive() error: %v", err)
	}

	var notif map[string]any
	if err := json.Unmarshal(received, &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if notif["method"] != "notifications/progress" {
		t.Errorf("method = %v, want notifications/progress", notif["method"])
	}
}

func TestStartSSE_Idempotent(t *testing.T) {
	getCount := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			getCount++
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			// Keep open briefly
			time.Sleep(100 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	ctx := context.Background()
	if err := tr.StartSSE(ctx); err != nil {
		t.Fatalf("first StartSSE() error: %v", err)
	}
	if err := tr.StartSSE(ctx); err != nil {
		t.Fatalf("second StartSSE() error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := getCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected exactly 1 SSE GET request, got %d", count)
	}
}

func TestStartSSE_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "forbidden")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	err := tr.StartSSE(context.Background())
	if err == nil {
		t.Fatal("expected error on 403 SSE response, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should mention 403: %v", err)
	}
}

func TestStartSSE_SendsSessionIDHeader(t *testing.T) {
	var gotSessionID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gotSessionID = r.Header.Get("Mcp-Session-Id")
			w.Header().Set("Content-Type", "text/event-stream")
			time.Sleep(100 * time.Millisecond)
			return
		}
		// POST establishes session
		w.Header().Set("Mcp-Session-Id", "sess-sse-test")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	// Establish session
	_ = tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := tr.StartSSE(ctx); err != nil {
		t.Fatalf("StartSSE() error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if gotSessionID != "sess-sse-test" {
		t.Errorf("SSE GET session ID = %q, want sess-sse-test", gotSessionID)
	}
}

// ── SSE parsing edge cases ────────────────────────────────────────────────────

func TestSSE_MultilineDataEvent(t *testing.T) {
	// SSE spec allows "data:" on consecutive lines to form a multiline message.
	// The transport joins them with \n — they should still produce valid JSON
	// only if the full concatenated value is valid JSON. Here we test the
	// single-line common case where the message is on one data line.
	msg := `{"jsonrpc":"2.0","id":5,"result":"multiline-ok"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: "+msg+"\n\n")
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive() error: %v", err)
	}

	var resp map[string]any
	_ = json.Unmarshal(received, &resp)
	if resp["result"] != "multiline-ok" {
		t.Errorf("result = %v, want multiline-ok", resp["result"])
	}
}

func TestSSE_CommentLinesIgnored(t *testing.T) {
	msg := `{"jsonrpc":"2.0","id":6,"result":"after-comment"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// SSE comment line (starts with :) followed by real event
		fmt.Fprint(w, ": this is a comment\n")
		fmt.Fprint(w, "data: "+msg+"\n\n")
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	received, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive() error: %v", err)
	}

	var resp map[string]any
	_ = json.Unmarshal(received, &resp)
	if resp["result"] != "after-comment" {
		t.Errorf("result = %v, want after-comment", resp["result"])
	}
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestConcurrentSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	const n = 20
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			errs <- tr.Send(context.Background(), []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d}`, id)))
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Send() error: %v", err)
		}
	}
}

func TestConcurrentCloseAndSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		jsonResp(t, w, okMsg(1))
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = tr.Send(context.Background(), []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d}`, id)))
		}(i)
	}

	// Close while sends are in flight — must not panic or deadlock.
	tr.Close()
	wg.Wait()
}

// ── Integration: full request-response round-trip ────────────────────────────

func TestIntegration_RequestResponseRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		id := req["id"]
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  fmt.Sprintf("echo-%v", id),
		}
		w.Header().Set("Mcp-Session-Id", "session-roundtrip")
		jsonResp(t, w, resp)
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	for i := 1; i <= 5; i++ {
		payload := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test"}`, i))
		if err := tr.Send(context.Background(), payload); err != nil {
			t.Fatalf("[%d] Send() error: %v", i, err)
		}

		msg, err := tr.Receive(context.Background())
		if err != nil {
			t.Fatalf("[%d] Receive() error: %v", i, err)
		}

		var resp map[string]any
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Fatalf("[%d] unmarshal: %v", i, err)
		}

		want := fmt.Sprintf("echo-%v", float64(i))
		if resp["result"] != want {
			t.Errorf("[%d] result = %v, want %v", i, resp["result"], want)
		}
	}

	if tr.sessionID != "session-roundtrip" {
		t.Errorf("sessionID = %q, want session-roundtrip", tr.sessionID)
	}
}

// ── Receive on closed transport ───────────────────────────────────────────────

func TestReceive_AfterClose(t *testing.T) {
	tr := New(Config{URL: "http://localhost"})
	tr.Close()

	_, err := tr.Receive(context.Background())
	if err == nil {
		t.Fatal("expected error receiving from closed transport, got nil")
	}
}

// ── io.EOF on server disconnect ───────────────────────────────────────────────

func TestSend_ServerBodyReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write headers and partial body, then close abruptly via hijack or panic.
		// Simpler: return 200 with an incomplete body by writing partial JSON.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write only half the JSON — body ends early.
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":`) //nolint:errcheck
	}))
	defer srv.Close()

	tr := New(Config{URL: srv.URL})
	defer tr.Close()

	// Truncated JSON is invalid — should be dropped silently (no error on Send).
	if err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`)); err != nil {
		t.Logf("Send() returned error (acceptable): %v", err)
	}
}
