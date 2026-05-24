package client_test

// retry_test.go — integration tests for N2 request retry with idempotency keys.
//
// These tests operate at the Client level (package client_test), using the
// same mockTransport / autoResponder infrastructure from client_test.go.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// ── helpers ─────────────────────────────────────────────────────────────────

// newRetryClient creates an initialized client with retry enabled.
// The autoResponder goroutine is started against mt.
func newRetryClient(t *testing.T, maxRetries int, enableIdempotency bool) (*client.Client, *mockTransport) {
	t.Helper()
	mt := newMockTransport()
	autoResponder(t, mt)

	c, err := client.New(mt, client.Options{
		MaxRetries:        maxRetries,
		EnableIdempotency: enableIdempotency,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, mt
}

// autoResponderWithErrors starts an autoResponder that returns server-error
// responses for the first `failCount` non-initialize invocations of `method`,
// then returns success thereafter.
func autoResponderWithErrors(t *testing.T, mt *mockTransport, method string, failCount int, errCode int) {
	t.Helper()
	var seen atomic.Int32
	go func() {
		idx := 0
		for {
			mt.mu.Lock()
			closed := mt.closed
			count := len(mt.sent)
			mt.mu.Unlock()
			if closed {
				return
			}
			if count <= idx {
				time.Sleep(time.Millisecond)
				continue
			}
			for ; idx < count; idx++ {
				mt.mu.Lock()
				data := mt.sent[idx]
				mt.mu.Unlock()

				var msg struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.Unmarshal(data, &msg); err != nil || msg.ID == nil {
					continue
				}

				switch msg.Method {
				case "initialize":
					mt.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result: finemcp.InitializeResult{
							ProtocolVersion: finemcp.ProtocolVersion,
							Capabilities:    finemcp.ServerCapabilities{},
							ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
						},
					})
				case method:
					n := int(seen.Add(1))
					if n <= failCount {
						mt.enqueueJSON(finemcp.JSONRPCResponse{
							JSONRPC: "2.0",
							ID:      msg.ID,
							Error: &finemcp.JSONRPCError{
								Code:    errCode,
								Message: "transient error",
							},
						})
					} else {
						// success response
						mt.enqueueJSON(finemcp.JSONRPCResponse{
							JSONRPC: "2.0",
							ID:      msg.ID,
							Result: finemcp.ListToolsResult{
								Tools: []finemcp.ToolInfo{
									{Name: "echo"},
								},
							},
						})
					}
				}
			}
		}
	}()
}

// ── tests ────────────────────────────────────────────────────────────────────

// TestRetry_SucceedsOnFirstAttempt verifies that when no errors occur the
// retry layer adds no overhead and exactly 1 wire request is sent.
func TestRetry_SucceedsOnFirstAttempt(t *testing.T) {
	c, mt := newRetryClient(t, 3, false)

	ctx := context.Background()
	result, err := c.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	// Only 1 tools/list request should have been sent (no retries needed).
	sent := countSentRequestsByMethod(mt, "tools/list")
	if sent != 1 {
		t.Errorf("expected 1 tools/list wire request, got %d", sent)
	}
}

// TestRetry_RetriesOnRetryableCode verifies that a 500 error triggers retry
// and the client eventually succeeds.
func TestRetry_RetriesOnRetryableCode(t *testing.T) {
	mt := newMockTransport()
	// Start a custom responder: fail twice with 500, then succeed.
	autoResponderWithErrors(t, mt, "tools/list", 2, 500)

	c, err := client.New(mt, client.Options{
		MaxRetries: 3,
		// Use minimal delay for test speed.
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	result, err := c.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("ListTools: expected success after retries, got: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(result.Tools))
	}

	// Should have sent 3 wire requests (2 failures + 1 success).
	sent := countSentRequestsByMethod(mt, "tools/list")
	if sent != 3 {
		t.Errorf("expected 3 tools/list wire requests (2 retries + 1 success), got %d", sent)
	}
}

// TestRetry_DoesNotRetryNonRetryableCode verifies that a 400 Bad Request is
// returned immediately without any retry attempts.
func TestRetry_DoesNotRetryNonRetryableCode(t *testing.T) {
	mt := newMockTransport()
	// Fail every request with 400 (not in the default retryable list).
	autoResponderWithErrors(t, mt, "tools/list", 100, 400)

	c, err := client.New(mt, client.Options{MaxRetries: 3})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	_, err = c.ListTools(ctx, finemcp.ListParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Only 1 wire request: no retry on 400.
	sent := countSentRequestsByMethod(mt, "tools/list")
	if sent != 1 {
		t.Errorf("expected 1 tools/list wire request (no retry), got %d", sent)
	}
}

// TestRetry_MaxRetriesExceeded verifies that when every attempt fails the
// client returns ErrMaxRetriesExceeded and the correct number of wire
// requests is sent.
func TestRetry_MaxRetriesExceeded(t *testing.T) {
	mt := newMockTransport()
	// Always fail with 503 (retryable).
	autoResponderWithErrors(t, mt, "tools/list", 100, 503)

	c, err := client.New(mt, client.Options{MaxRetries: 2})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	_, err = c.ListTools(ctx, finemcp.ListParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "max retries exceeded") {
		t.Errorf("expected ErrMaxRetriesExceeded in error, got: %v", err)
	}

	// 3 wire requests: initial + 2 retries.
	sent := countSentRequestsByMethod(mt, "tools/list")
	if sent != 3 {
		t.Errorf("expected 3 tools/list wire requests (1 + 2 retries), got %d", sent)
	}
}

// TestRetry_CustomRetryableErrors verifies that using a custom RetryableErrors
// list causes only matching codes to be retried.
func TestRetry_CustomRetryableErrors(t *testing.T) {
	mt := newMockTransport()
	// Fail with 429 which should be in the custom list.
	autoResponderWithErrors(t, mt, "tools/list", 1, 429)

	c, err := client.New(mt, client.Options{
		MaxRetries:      3,
		RetryableErrors: []int{429}, // only retry on 429
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	result, err := c.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("ListTools: expected success, got: %v", err)
	}
	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool")
	}

	// 2 wire requests: 1 failure + 1 success.
	sent := countSentRequestsByMethod(mt, "tools/list")
	if sent != 2 {
		t.Errorf("expected 2 tools/list wire requests, got %d", sent)
	}
}

// TestRetry_ContextCancelledDuringBackoff verifies that when the context is
// cancelled while waiting for a backoff delay, the error is returned promptly.
func TestRetry_ContextCancelledDuringBackoff(t *testing.T) {
	mt := newMockTransport()
	// Always fail with retryable error — we want to hit backoff.
	autoResponderWithErrors(t, mt, "tools/list", 100, 503)

	c, err := client.New(mt, client.Options{MaxRetries: 10})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	baseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(baseCtx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	// Cancel after 50 ms: enough time for one attempt but not all retries.
	ctx, ctxCancel := context.WithTimeout(baseCtx, 50*time.Millisecond)
	defer ctxCancel()

	_, err = c.ListTools(ctx, finemcp.ListParams{})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context-related error, got: %v", err)
	}
}

// TestRetry_IdempotencyKeyInjected verifies that when EnableIdempotency is true
// all retry attempts for the same logical request carry the same idempotencyKey
// in params._meta.
func TestRetry_IdempotencyKeyInjected(t *testing.T) {
	mt := newMockTransport()
	// Fail once then succeed so we have at least one retry.
	autoResponderWithErrors(t, mt, "tools/list", 1, 503)

	c, err := client.New(mt, client.Options{
		MaxRetries:        3,
		EnableIdempotency: true,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// Collect the idempotency keys from all tools/list requests.
	var keys []string
	mt.mu.Lock()
	sent := make([][]byte, len(mt.sent))
	copy(sent, mt.sent)
	mt.mu.Unlock()

	for _, raw := range sent {
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Method != "tools/list" {
			continue
		}
		if key, ok := msg.Params.Meta["idempotencyKey"].(string); ok {
			keys = append(keys, key)
		}
	}

	// Should have exactly 2 requests (1 fail + 1 retry) and both same key.
	if len(keys) != 2 {
		t.Fatalf("expected 2 tools/list requests with idempotency keys, got %d", len(keys))
	}
	if keys[0] != keys[1] {
		t.Errorf("idempotency keys differ across retries: %q vs %q", keys[0], keys[1])
	}
	if len(keys[0]) != 32 {
		t.Errorf("expected 32-char hex idempotency key, got %q (len=%d)", keys[0], len(keys[0]))
	}
}

// TestRetry_IdempotencyKeyUnique verifies that two separate calls produce
// different idempotency keys.
func TestRetry_IdempotencyKeyUnique(t *testing.T) {

	mt2 := newMockTransport()
	autoResponder(t, mt2)
	c2, err := client.New(mt2, client.Options{
		MaxRetries:        1,
		EnableIdempotency: true,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c2.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c2.Close()

	// Make two independent calls.
	_, _ = c2.ListTools(ctx, finemcp.ListParams{})
	_, _ = c2.ListTools(ctx, finemcp.ListParams{})

	// Extract idempotency keys from the two tools/list requests.
	mt2.mu.Lock()
	sent := make([][]byte, len(mt2.sent))
	copy(sent, mt2.sent)
	mt2.mu.Unlock()

	var keys []string
	for _, raw := range sent {
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Method != "tools/list" {
			continue
		}
		if key, ok := msg.Params.Meta["idempotencyKey"].(string); ok {
			keys = append(keys, key)
		}
	}

	if len(keys) < 2 {
		t.Fatalf("expected at least 2 idempotency keys, got %d", len(keys))
	}
	if keys[0] == keys[1] {
		t.Errorf("two separate calls produced the same idempotency key: %q", keys[0])
	}
}

// TestRetry_NoRetryWhenDisabled verifies that when MaxRetries=0 the retry
// layer is inactive and non-retryable errors are returned immediately.
func TestRetry_NoRetryWhenDisabled(t *testing.T) {
	mt := newMockTransport()
	autoResponderWithErrors(t, mt, "tools/list", 100, 503)

	c, err := client.New(mt, client.Options{MaxRetries: 0})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	defer c.Close()

	_, err = c.ListTools(ctx, finemcp.ListParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Only 1 request — no retries.
	sent := countSentRequestsByMethod(mt, "tools/list")
	if sent != 1 {
		t.Errorf("expected 1 tools/list wire request, got %d", sent)
	}
}
