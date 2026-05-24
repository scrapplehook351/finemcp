package client_test

// streaming_test.go exercises the CallToolStreaming and
// CallToolStreamingWithResult methods introduced by the N4 feature.
//
// Tests are structured around the shared mockTransport / autoResponder
// helpers already defined in client_test.go (same package).

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// ── helpers ───────────────────────────────────────────────────────────

// streamingAutoResponder is like autoResponder but does NOT auto-reply to
// tools/call requests so streaming tests can inject their own responses.
func streamingAutoResponder(t *testing.T, mt *mockTransport) {
	t.Helper()
	go func() {
		seen := 0
		for {
			mt.mu.Lock()
			closed := mt.closed
			count := len(mt.sent)
			mt.mu.Unlock()
			if closed {
				return
			}
			if count <= seen {
				time.Sleep(time.Millisecond)
				continue
			}
			for ; seen < count; seen++ {
				mt.mu.Lock()
				data := mt.sent[seen]
				mt.mu.Unlock()

				var msg struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				if msg.ID == nil {
					continue // notification
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
				case "ping":
					mt.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  struct{}{},
					})
					// tools/call intentionally NOT handled here — tests inject their own responses.
				}
			}
		}
	}()
}

// mustInitStreaming creates and initialises a Client backed by a fresh
// mockTransport that uses streamingAutoResponder (no auto tools/call reply).
func mustInitStreaming(t *testing.T, opts client.Options) (*client.Client, *mockTransport) {
	t.Helper()
	mt := newMockTransport()
	streamingAutoResponder(t, mt)

	c, err := client.New(mt, opts)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c, mt
}

// progressNotification builds a notifications/progress JSON payload.
// token is the progress token; contentItems is the list of content blocks
// (each a map ready to be marshalled as MCP content JSON).
func progressNotification(token string, progress float64, contentItems ...map[string]any) []byte {
	var rawContent []json.RawMessage
	for _, item := range contentItems {
		b, _ := json.Marshal(item)
		rawContent = append(rawContent, b)
	}
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params": map[string]any{
			"progressToken": token,
			"progress":      progress,
			"content":       rawContent,
		},
	}
	b, _ := json.Marshal(msg)
	return b
}

// toolsCallResponse builds a tools/call JSON-RPC response.
func toolsCallResponse(id any, contentItems ...map[string]any) []byte {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": contentItems,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// toolsCallErrorResponse builds a tools/call JSON-RPC error response.
func toolsCallErrorResponse(id any, code int, msg string) []byte {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// drainContent collects all items from contentCh within timeout and returns them.
func drainContent(t *testing.T, contentCh <-chan finemcp.Content, timeout time.Duration) []finemcp.Content {
	t.Helper()
	var out []finemcp.Content
	deadline := time.After(timeout)
	for {
		select {
		case block, ok := <-contentCh:
			if !ok {
				return out
			}
			out = append(out, block)
		case <-deadline:
			t.Fatalf("timed out draining content channel after %s (got %d blocks so far)", timeout, len(out))
		}
	}
}

// waitToolsCall blocks until a tools/call request appears in mt.sent and
// returns the request ID as a string.
func waitToolsCall(t *testing.T, mt *mockTransport, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for tools/call request")
			return ""
		case <-time.After(time.Millisecond):
		}
		mt.mu.Lock()
		for _, raw := range mt.sent {
			var m struct {
				ID     any    `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &m) == nil && m.Method == "tools/call" {
				mt.mu.Unlock()
				return fmt.Sprintf("%v", m.ID)
			}
		}
		mt.mu.Unlock()
	}
}

// extractStreamToken reads the progressToken embedded in the most recent
// tools/call request recorded in mt.sent.
func extractStreamToken(t *testing.T, mt *mockTransport) string {
	t.Helper()
	mt.mu.Lock()
	defer mt.mu.Unlock()
	for _, raw := range mt.sent {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Meta map[string]any `json:"_meta"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &req) == nil && req.Method == "tools/call" {
			if tok, ok := req.Params.Meta["progressToken"].(string); ok {
				return tok
			}
		}
	}
	t.Fatal("no progressToken found in tools/call request")
	return ""
}

// ── Tests ─────────────────────────────────────────────────────────────

// TestCallToolStreaming_NonStreamingServer verifies that CallToolStreaming
// works correctly when the server does not send content-in-progress
// notifications; all content arrives in the final result.
func TestCallToolStreaming_NonStreamingServer(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start the streaming call.
	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "gen"})

	// Respond with a regular (non-streaming) tools/call result.
	id := waitToolsCall(t, mt, 3*time.Second)
	mt.enqueue(toolsCallResponse(id,
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "text", "text": "world"},
	))

	// All content should arrive after the final result.
	blocks := drainContent(t, contentCh, 3*time.Second)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}

	texts := []string{
		blocks[0].(finemcp.TextContent).Text,
		blocks[1].(finemcp.TextContent).Text,
	}
	if texts[0] != "hello" || texts[1] != "world" {
		t.Errorf("unexpected texts: %v", texts)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCallToolStreaming_StreamingServer verifies that content blocks
// embedded in progress notifications arrive before the final result, and
// the final result's content is appended afterward.
func TestCallToolStreaming_StreamingServer(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "stream"})

	id := waitToolsCall(t, mt, 3*time.Second)
	token := extractStreamToken(t, mt)

	// Server sends two progress notifications with content before the final reply.
	mt.enqueue(progressNotification(token, 0.33,
		map[string]any{"type": "text", "text": "chunk-1"},
	))
	mt.enqueue(progressNotification(token, 0.66,
		map[string]any{"type": "text", "text": "chunk-2"},
	))

	// Collect the two streaming chunks.
	var streamed []finemcp.Content
	deadline := time.After(3 * time.Second)
	for len(streamed) < 2 {
		select {
		case block, ok := <-contentCh:
			if !ok {
				t.Fatalf("contentCh closed prematurely with only %d blocks", len(streamed))
			}
			streamed = append(streamed, block)
		case <-deadline:
			t.Fatalf("timed out waiting for streaming chunks (got %d)", len(streamed))
		}
	}

	// Verify chunk order.
	if streamed[0].(finemcp.TextContent).Text != "chunk-1" {
		t.Errorf("block[0] = %q, want chunk-1", streamed[0].(finemcp.TextContent).Text)
	}
	if streamed[1].(finemcp.TextContent).Text != "chunk-2" {
		t.Errorf("block[1] = %q, want chunk-2", streamed[1].(finemcp.TextContent).Text)
	}

	// Server sends the final result (with an extra block).
	mt.enqueue(toolsCallResponse(id,
		map[string]any{"type": "text", "text": "final"},
	))

	// The final block should still arrive via the channel.
	remaining := drainContent(t, contentCh, 3*time.Second)
	if len(remaining) != 1 || remaining[0].(finemcp.TextContent).Text != "final" {
		t.Errorf("unexpected trailing blocks: %v", remaining)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCallToolStreaming_ServerError ensures that a JSON-RPC error response
// is propagated through errCh and the content channel is closed cleanly.
func TestCallToolStreaming_ServerError(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "fail"})

	id := waitToolsCall(t, mt, 3*time.Second)
	mt.enqueue(toolsCallErrorResponse(id, -32001, "tool exploded"))

	// contentCh must be closed without any blocks.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case block, ok := <-contentCh:
			if !ok {
				goto errCheck
			}
			t.Errorf("unexpected content block: %v", block)
		case <-deadline:
			t.Fatal("timed out waiting for contentCh to close")
		}
	}
errCheck:
	err := <-errCh
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should be a ResponseError.
	var rErr *client.ResponseError
	if !errorsAs(err, &rErr) {
		t.Errorf("error type = %T, want *client.ResponseError", err)
	}
}

// TestCallToolStreaming_ContextCancellation verifies that cancelling the
// context mid-stream closes both channels without leaking goroutines.
func TestCallToolStreaming_ContextCancellation(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())

	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "slow"})

	// Wait for the request to be sent, then cancel before any response.
	_ = waitToolsCall(t, mt, 3*time.Second)
	cancel()

	// Both channels must close (or deliver an error) within a reasonable timeout.
	deadline := time.After(3 * time.Second)
	contentDrained := false
	errReceived := false
	for !contentDrained || !errReceived {
		select {
		case _, ok := <-contentCh:
			if !ok {
				contentDrained = true
				contentCh = nil // prevent re-entry on closed chan
			}
		case err, ok := <-errCh:
			if ok && err != nil {
				// context cancellation error is expected
				errReceived = true
			} else if !ok {
				errReceived = true
				errCh = nil
			}
		case <-deadline:
			t.Fatalf("channels not closed after context cancel: contentDrained=%v errReceived=%v",
				contentDrained, errReceived)
		}
	}
}

// TestCallToolStreaming_ConcurrentCalls verifies that multiple concurrent
// streaming calls do not cross-contaminate each other's content channels.
func TestCallToolStreaming_ConcurrentCalls(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	const numCalls = 5
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type callState struct {
		id       string
		token    string
		contentC <-chan finemcp.Content
		errC     <-chan error
	}
	states := make([]callState, numCalls)

	// Launch all calls concurrently.
	for i := range states {
		states[i].contentC, states[i].errC = c.CallToolStreaming(ctx,
			finemcp.CallToolParams{Name: fmt.Sprintf("tool-%d", i)})
	}

	// Collect (id, token) pairs for all tools/call requests.
	deadline := time.After(5 * time.Second)
	for {
		mt.mu.Lock()
		for _, raw := range mt.sent {
			var req struct {
				ID     any    `json:"id"`
				Method string `json:"method"`
				Params struct {
					Name string         `json:"name"`
					Meta map[string]any `json:"_meta"`
				} `json:"params"`
			}
			if json.Unmarshal(raw, &req) == nil && req.Method == "tools/call" {
				if tok, ok := req.Params.Meta["progressToken"].(string); ok {
					reqID := fmt.Sprintf("%v", req.ID)
					for i := range states {
						if req.Params.Name == fmt.Sprintf("tool-%d", i) &&
							states[i].id == "" {
							states[i].id = reqID
							states[i].token = tok
						}
					}
				}
			}
		}
		// Count total states that have been resolved.
		found := 0
		for i := range states {
			if states[i].id != "" {
				found++
			}
		}
		mt.mu.Unlock()
		if found == numCalls {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out collecting request IDs (%d/%d collected)", found, numCalls)
		case <-time.After(time.Millisecond):
		}
	}

	// Send a streaming progress notification to each call with a unique text.
	for i, s := range states {
		text := fmt.Sprintf("stream-text-%d", i)
		mt.enqueue(progressNotification(s.token, 0.5,
			map[string]any{"type": "text", "text": text},
		))
	}

	// Send the final result to each call (no extra content).
	for _, s := range states {
		mt.enqueue(toolsCallResponse(s.id))
	}

	// Collect and verify each call received exactly its own content.
	var wg sync.WaitGroup
	wg.Add(numCalls)
	for i, s := range states {
		i, s := i, s
		go func() {
			defer wg.Done()
			blocks := drainContent(t, s.contentC, 5*time.Second)
			if len(blocks) != 1 {
				t.Errorf("call %d: got %d blocks, want 1", i, len(blocks))
				return
			}
			want := fmt.Sprintf("stream-text-%d", i)
			got := blocks[0].(finemcp.TextContent).Text
			if got != want {
				t.Errorf("call %d: text = %q, want %q", i, got, want)
			}
			if err := <-s.errC; err != nil {
				t.Errorf("call %d: unexpected error: %v", i, err)
			}
		}()
	}
	wg.Wait()
}

// TestCallToolStreaming_OnProgressStillFires confirms that the global
// OnProgress callback continues to fire even when content is being streamed.
func TestCallToolStreaming_OnProgressStillFires(t *testing.T) {
	var progressCount atomic.Int32

	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
		OnProgress: func(_ finemcp.ProgressParams) {
			progressCount.Add(1)
		},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "prog"})

	id := waitToolsCall(t, mt, 3*time.Second)
	token := extractStreamToken(t, mt)

	// Fire two progress notifications with content.
	mt.enqueue(progressNotification(token, 0.5,
		map[string]any{"type": "text", "text": "p1"},
	))
	mt.enqueue(progressNotification(token, 1.0,
		map[string]any{"type": "text", "text": "p2"},
	))
	mt.enqueue(toolsCallResponse(id))

	blocks := drainContent(t, contentCh, 3*time.Second)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Give the notification goroutine time to fire OnProgress.
	time.Sleep(50 * time.Millisecond)
	if got := progressCount.Load(); got < 2 {
		t.Errorf("OnProgress fired %d times, want >= 2", got)
	}
}

// TestCallToolStreaming_EmptyResult verifies that a tool which returns no
// content is handled cleanly: contentCh is closed without any blocks.
func TestCallToolStreaming_EmptyResult(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "empty"})

	id := waitToolsCall(t, mt, 3*time.Second)
	// Respond with an empty content array.
	mt.enqueue(toolsCallResponse(id))

	blocks := drainContent(t, contentCh, 3*time.Second)
	if len(blocks) != 0 {
		t.Errorf("got %d blocks, want 0", len(blocks))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCallToolStreamingWithResult verifies that CallToolStreamingWithResult
// delivers both streaming content and the final CallToolResult.
func TestCallToolStreamingWithResult(t *testing.T) {
	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	contentCh, resultCh, errCh := c.CallToolStreamingWithResult(ctx,
		finemcp.CallToolParams{Name: "wr"})

	id := waitToolsCall(t, mt, 3*time.Second)
	token := extractStreamToken(t, mt)

	// One progress block.
	mt.enqueue(progressNotification(token, 0.5,
		map[string]any{"type": "text", "text": "partial"},
	))
	// Final result with IsError=false.
	finalResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "done"},
			},
			"isError": false,
		},
	}
	b, _ := json.Marshal(finalResp)
	mt.enqueue(b)

	blocks := drainContent(t, contentCh, 3*time.Second)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (partial + done)", len(blocks))
	}
	if blocks[0].(finemcp.TextContent).Text != "partial" {
		t.Errorf("block[0] = %q, want partial", blocks[0].(finemcp.TextContent).Text)
	}
	if blocks[1].(finemcp.TextContent).Text != "done" {
		t.Errorf("block[1] = %q, want done", blocks[1].(finemcp.TextContent).Text)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r := <-resultCh
	if r == nil {
		t.Fatal("expected a CallToolResult, got nil")
	}
	if r.IsError {
		t.Errorf("IsError = true, want false")
	}
}

// TestCallToolStreaming_MultipleProgressWithNoContent verifies that progress
// notifications without Content fields do not produce spurious content blocks
// but do still fire the global OnProgress callback.
func TestCallToolStreaming_MultipleProgressWithNoContent(t *testing.T) {
	var progressCount atomic.Int32

	c, mt := mustInitStreaming(t, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test", Version: "1.0"},
		OnProgress: func(_ finemcp.ProgressParams) {
			progressCount.Add(1)
		},
	})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	contentCh, errCh := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "nostream"})

	id := waitToolsCall(t, mt, 3*time.Second)

	// Send pure-progress notifications (no content, unrelated token).
	for i := range 3 {
		msg := map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/progress",
			"params": map[string]any{
				"progressToken": "some-other-token",
				"progress":      float64(i + 1),
			},
		}
		b, _ := json.Marshal(msg)
		mt.enqueue(b)
	}

	// Final result with content.
	mt.enqueue(toolsCallResponse(id,
		map[string]any{"type": "text", "text": "result"},
	))

	blocks := drainContent(t, contentCh, 3*time.Second)
	if len(blocks) != 1 || blocks[0].(finemcp.TextContent).Text != "result" {
		t.Errorf("unexpected blocks: %v", blocks)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Give a moment for notifications to flush.
	time.Sleep(50 * time.Millisecond)
	if got := progressCount.Load(); got < 3 {
		t.Errorf("OnProgress fired %d times, want >= 3", got)
	}
}

// errorsAs is a local generic helper wrapping errors.As to avoid importing
// "errors" in every test while keeping the test logic readable.
func errorsAs[T any](err error, target *T) bool {
	// errors.As is the idiomatic way; we reproduce the walk manually here so
	// we do not add a new import of "errors" that fmt already provides.
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if v, ok := err.(T); ok {
			*target = v
			return true
		}
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
