package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// unmarshalStreamParams is a test helper that JSON round-trips n.Params
// into a streamProgressParams, validating the wire format rather than
// relying on in-memory Go type assertions.
func unmarshalStreamParams(t *testing.T, n *JSONRPCNotification) streamProgressParams {
	t.Helper()
	raw, err := json.Marshal(n.Params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var sp streamProgressParams
	if err := json.Unmarshal(raw, &sp); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return sp
}

func TestStreamFromCtx_Nil(t *testing.T) {
	t.Parallel()
	s := StreamFromCtx(context.Background())
	if s != nil {
		t.Errorf("expected nil stream from bare context, got %v", s)
	}
}

func TestStreamFromCtx_Present(t *testing.T) {
	t.Parallel()
	sender := func(_ *JSONRPCNotification) {}
	stream := newToolStream(context.Background(), sender, "tok", DefaultStreamBufferSize)
	defer stream.close()

	ctx := withToolStream(context.Background(), stream)
	got := StreamFromCtx(ctx)
	if got != stream {
		t.Error("expected same stream instance from context")
	}
}

func TestToolStream_SendText(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var notifications []*JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		mu.Lock()
		notifications = append(notifications, n)
		mu.Unlock()
	}

	stream := newToolStream(context.Background(), sender, "test-token", DefaultStreamBufferSize)

	if err := stream.SendText("chunk-1"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := stream.SendText("chunk-2"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := stream.SendText("chunk-3"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	stream.close()

	mu.Lock()
	defer mu.Unlock()
	if len(notifications) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(notifications))
	}

	for i, n := range notifications {
		if n.Method != methodProgress {
			t.Errorf("notification[%d] method = %q, want %q", i, n.Method, methodProgress)
		}
		params := unmarshalStreamParams(t, n)
		if params.ProgressToken != "test-token" {
			t.Errorf("notification[%d] token = %v, want test-token", i, params.ProgressToken)
		}
		if params.Data == nil {
			t.Fatalf("notification[%d] data is nil", i)
		}
		if params.Data.Sequence != int64(i+1) {
			t.Errorf("notification[%d] sequence = %d, want %d", i, params.Data.Sequence, i+1)
		}

		// Verify the content is valid TextContent JSON.
		var tc struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params.Data.Content, &tc); err != nil {
			t.Fatalf("notification[%d] content decode: %v", i, err)
		}
		if tc.Type != "text" {
			t.Errorf("notification[%d] content type = %q, want text", i, tc.Type)
		}
	}
}

func TestToolStream_Send_ImageContent(t *testing.T) {
	t.Parallel()

	var received *JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		received = n
	}

	stream := newToolStream(context.Background(), sender, "img-tok", DefaultStreamBufferSize)
	if err := stream.Send(NewImageContent("image/png", []byte("fake-png"))); err != nil {
		t.Fatalf("Send: %v", err)
	}
	stream.close()

	if received == nil {
		t.Fatal("expected notification")
	}
	params := unmarshalStreamParams(t, received)
	if params.Data == nil {
		t.Fatal("data is nil")
	}
	if params.Data.Sequence != 1 {
		t.Errorf("sequence = %d, want 1", params.Data.Sequence)
	}
}

func TestToolStream_Sequence(t *testing.T) {
	t.Parallel()
	sender := func(_ *JSONRPCNotification) {}
	stream := newToolStream(context.Background(), sender, "tok", DefaultStreamBufferSize)

	if seq := stream.Sequence(); seq != 0 {
		t.Errorf("initial sequence = %d, want 0", seq)
	}

	stream.SendText("a")
	stream.SendText("b")
	stream.close() // flush drain before asserting

	if seq := stream.Sequence(); seq != 2 {
		t.Errorf("sequence after 2 sends = %d, want 2", seq)
	}
}

func TestToolStream_Sequence_NotIncrementedOnDrop(t *testing.T) {
	t.Parallel()

	// Sender signals when it's entered, then blocks until released.
	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	sender := func(_ *JSONRPCNotification) {
		once.Do(func() { close(entered) })
		<-gate
	}

	// Use a non-cancellable context so close() reliably waits for drain.
	stream := newToolStream(context.Background(), sender, "tok", 1)

	// First send fills the buffer (size 1).
	if err := stream.SendText("chunk-1"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	// Wait until drain picks chunk-1 and is blocked in sender.
	<-entered

	// Second send refills the now-empty buffer.
	if err := stream.SendText("chunk-2"); err != nil {
		t.Fatalf("second send: %v", err)
	}

	// Unblock sender and close the stream; close() blocks until drain
	// has flushed chunk-2 and exited.
	close(gate)
	stream.close()

	// Send after close must fail and must not affect the sequence counter.
	err := stream.SendText("dropped")
	if !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("expected ErrStreamClosed, got %v", err)
	}

	// Sequence should be 2: only chunks that entered the buffer
	// (chunk-1, chunk-2) were dispatched. "dropped" was never enqueued.
	if seq := stream.Sequence(); seq != 2 {
		t.Errorf("sequence = %d, want 2 (dropped chunk should not increment)", seq)
	}
}

func TestToolStream_ChunkNotLostAfterCtxCancel(t *testing.T) {
	t.Parallel()

	const chunks = 8
	var mu sync.Mutex
	var received int
	sender := func(_ *JSONRPCNotification) {
		mu.Lock()
		received++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := newToolStream(ctx, sender, "tok", chunks)

	// Fill the buffer so all sends succeed before drain empties it.
	for i := 0; i < chunks; i++ {
		if err := stream.SendText("chunk"); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	// Cancel context while chunks are still buffered.
	cancel()

	// close() must deliver all already-buffered chunks via flushRemaining.
	stream.close()

	// close() may return before drain exits when the context is cancelled
	// and drain is still flushing. Wait for drain to guarantee all chunks
	// have been sent before asserting.
	<-stream.done

	mu.Lock()
	defer mu.Unlock()
	if received != chunks {
		t.Errorf("got %d notifications, want %d (chunk lost after ctx cancel)", received, chunks)
	}
}

// badContent is a Content type whose MarshalJSON always fails.
// It must remain in package finemcp (not finemcp_test) to implement
// the unexported contentType() method on the sealed Content interface.
type badContent struct{}

func (badContent) contentType() string { return "bad" }
func (badContent) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal boom")
}

func TestToolStream_Send_UnmarshalableContent(t *testing.T) {
	t.Parallel()

	sender := func(_ *JSONRPCNotification) {
		t.Error("sender must not be called for unmarshalable content")
	}

	stream := newToolStream(context.Background(), sender, "tok", 4)
	defer stream.close()

	err := stream.Send(badContent{})
	if err == nil {
		t.Fatal("expected error for unmarshalable content, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "not marshallable") {
		t.Errorf("error message %q does not mention 'not marshallable'", got)
	}

	// Sequence must remain 0 — nothing was dispatched.
	if seq := stream.Sequence(); seq != 0 {
		t.Errorf("sequence = %d, want 0 (bad content should not be enqueued)", seq)
	}
}

func TestToolStream_Send_NilContent(t *testing.T) {
	t.Parallel()

	sender := func(_ *JSONRPCNotification) {
		t.Error("sender must not be called for nil content")
	}

	stream := newToolStream(context.Background(), sender, "tok", 4)
	defer stream.close()

	err := stream.Send(nil)
	if err == nil {
		t.Fatal("expected error for nil content, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "nil") {
		t.Errorf("error message %q does not mention 'nil'", got)
	}

	if seq := stream.Sequence(); seq != 0 {
		t.Errorf("sequence = %d, want 0 (nil content should not be enqueued)", seq)
	}
}

func TestToolStream_Send_TypedNilContent(t *testing.T) {
	t.Parallel()

	sender := func(_ *JSONRPCNotification) {
		t.Error("sender must not be called for typed-nil content")
	}

	stream := newToolStream(context.Background(), sender, "tok", 4)
	defer stream.close()

	cases := []struct {
		name string
		c    Content
	}{
		{"*TextContent", (*TextContent)(nil)},
		{"*ImageContent", (*ImageContent)(nil)},
		{"*AudioContent", (*AudioContent)(nil)},
		{"*EmbeddedResource", (*EmbeddedResource)(nil)},
	}
	for _, tc := range cases {
		err := stream.Send(tc.c)
		if err == nil {
			t.Errorf("[%s] expected error, got nil", tc.name)
		} else if !strings.Contains(err.Error(), "nil") {
			t.Errorf("[%s] error %q does not mention 'nil'", tc.name, err.Error())
		}
	}

	if seq := stream.Sequence(); seq != 0 {
		t.Errorf("sequence = %d, want 0 (typed-nil content should not be enqueued)", seq)
	}
}

func TestToolStream_SendAfterClose(t *testing.T) {
	t.Parallel()
	sender := func(_ *JSONRPCNotification) {}
	stream := newToolStream(context.Background(), sender, "tok", DefaultStreamBufferSize)
	stream.close()

	err := stream.SendText("should fail")
	if err != ErrStreamClosed {
		t.Errorf("expected ErrStreamClosed, got %v", err)
	}
	if !errors.Is(err, ErrStreamClosed) {
		t.Errorf("errors.Is(err, ErrStreamClosed) = false, want true")
	}
}

// TestToolStream_TwoChunks_BlockedSender verifies that both enqueued chunks
// are delivered even when the sender blocks between them. The straggler sweep
// in close() is also exercised, though the exact-straggler race is too narrow
// to reproduce deterministically.
func TestToolStream_TwoChunks_BlockedSender(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var sent int64
	// Sender blocks on first call so drain is occupied while close() fires.
	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	sender := func(_ *JSONRPCNotification) {
		once.Do(func() { close(entered) })
		<-gate
		mu.Lock()
		sent++
		mu.Unlock()
	}

	stream := newToolStream(context.Background(), sender, "tok", 1)

	// First Send fills the buffer; drain picks it up and blocks in sender.
	if err := stream.SendText("first"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	<-entered // drain is now stuck in sender

	// Second Send refills the buffer slot.
	if err := stream.SendText("second"); err != nil {
		t.Fatalf("second send: %v", err)
	}

	// Unblock sender to let drain resume and exit via quit.
	close(gate)
	stream.close()

	mu.Lock()
	count := sent
	mu.Unlock()

	// Both chunks must have been delivered.
	if count != 2 {
		t.Errorf("sent = %d, want 2", count)
	}
	if seq := stream.Sequence(); seq != 2 {
		t.Errorf("sequence = %d, want 2", seq)
	}
}

func TestToolStream_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	sender := func(_ *JSONRPCNotification) {}
	stream := newToolStream(context.Background(), sender, "tok", DefaultStreamBufferSize)

	// Multiple calls to close must not panic.
	stream.close()
	stream.close()
	stream.close()
}

func TestToolStream_CloseIsIdempotent_AfterCtxCancel(t *testing.T) {
	t.Parallel()
	sender := func(_ *JSONRPCNotification) {}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before creating the stream

	stream := newToolStream(ctx, sender, "tok", DefaultStreamBufferSize)

	// Multiple calls to close on a cancelled-context stream must not panic.
	stream.close()
	stream.close()
	stream.close()
}

func TestToolStream_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Sender signals when it's entered, then blocks until released.
	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	sender := func(_ *JSONRPCNotification) {
		once.Do(func() { close(entered) })
		<-gate
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := newToolStream(ctx, sender, "tok", 1)

	// First send: enters buffer (size 1).
	if err := stream.SendText("fill"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	// Wait until drain picks "fill" and is blocked in sender.
	<-entered

	// Second send: refills the now-empty buffer (channel capacity 1).
	if err := stream.SendText("fill-2"); err != nil {
		t.Fatalf("second send: %v", err)
	}

	// Buffer is full, drain is stuck in sender.
	// Cancel the context — the next Send must get context.Canceled
	// because the channel is full and ctx.Done is the only ready case.
	cancel()

	err := stream.SendText("should-cancel")
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Unblock sender and close.
	close(gate)
	stream.close()
}

func TestToolStream_Backpressure(t *testing.T) {
	t.Parallel()

	// Sender signals when it starts processing, then blocks until released.
	started := make(chan struct{}, 10)
	gate := make(chan struct{})
	var sent atomic.Int64
	sender := func(_ *JSONRPCNotification) {
		started <- struct{}{}
		<-gate
		sent.Add(1)
	}

	stream := newToolStream(context.Background(), sender, "tok", 1)

	// Send "a" — drainer picks it up and blocks in sender.
	stream.SendText("a")
	<-started // wait until drainer is in the sender call

	// Channel is now empty. Send "b" — fills the buffer (size 1).
	stream.SendText("b")

	// Send "c" in a goroutine — should block because the channel is full
	// and the drainer is stuck in the sender.
	blocked := make(chan error, 1)
	go func() {
		blocked <- stream.SendText("c")
	}()

	// Give the goroutine time to block.
	time.Sleep(50 * time.Millisecond)
	select {
	case <-blocked:
		t.Fatal("third Send should have blocked but returned immediately")
	default:
		// Expected — it's blocked.
	}

	// Release the gate — drainer processes remaining chunks, unblocking the third send.
	close(gate)

	select {
	case err := <-blocked:
		if err != nil {
			t.Errorf("third Send returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("third Send did not unblock after releasing gate")
	}

	stream.close()

	if got := sent.Load(); got != 3 {
		t.Errorf("expected 3 notifications sent, got %d", got)
	}
}

func TestToolStream_ConcurrentSends(t *testing.T) {
	t.Parallel()

	var sent atomic.Int64
	sender := func(_ *JSONRPCNotification) {
		sent.Add(1)
	}

	stream := newToolStream(context.Background(), sender, "tok", 100)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream.SendText("chunk")
		}()
	}
	wg.Wait()
	stream.close()

	if got := sent.Load(); got != 50 {
		t.Errorf("expected 50 notifications sent, got %d", got)
	}
	if seq := stream.Sequence(); seq != 50 {
		t.Errorf("expected sequence 50, got %d", seq)
	}
}

func TestToolStream_ConcurrentSendAndClose(t *testing.T) {
	t.Parallel()

	sender := func(_ *JSONRPCNotification) {}
	stream := newToolStream(context.Background(), sender, "tok", 4)

	// Spawn goroutines that Send in a tight loop.
	// Use a start gate so we know at least one goroutine is actively
	// sending before we call close(), eliminating the timing dependency.
	ready := make(chan struct{})
	var readyOnce sync.Once
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			readyOnce.Do(func() { close(ready) })
			for j := 0; j < 100; j++ {
				err := stream.SendText("data")
				if err != nil {
					// Only ErrStreamClosed and context errors are acceptable.
					if !errors.Is(err, ErrStreamClosed) && !errors.Is(err, context.Canceled) {
						t.Errorf("unexpected error: %v", err)
					}
					return
				}
			}
		}()
	}

	// Wait until at least one goroutine is running, then close.
	<-ready
	stream.close()
	wg.Wait()
}

func TestToolStream_SlowSender_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Sender blocks until gate is closed — simulates a stalled transport.
	gate := make(chan struct{})
	sender := func(_ *JSONRPCNotification) {
		<-gate
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := newToolStream(ctx, sender, "tok", 2)

	// Enqueue chunks so the drainer starts calling the blocked sender.
	stream.SendText("a")
	stream.SendText("b")
	time.Sleep(20 * time.Millisecond) // let drainer enter sender

	// Cancel the context — close should return promptly.
	cancel()

	done := make(chan struct{})
	go func() {
		stream.close()
		close(done)
	}()

	select {
	case <-done:
		// Good — close returned promptly.
	case <-time.After(2 * time.Second):
		t.Fatal("stream.close() hung despite cancelled context")
	}

	// Unblock the sender so the drain goroutine can exit.
	close(gate)

	// Verify the drain goroutine exits — no goroutine leak.
	// Use the stream's done channel directly instead of a process-wide
	// NumGoroutine check, which is unreliable under t.Parallel().
	select {
	case <-stream.done:
		// drain exited — no leak.
	case <-time.After(2 * time.Second):
		t.Fatal("drain goroutine leaked: stream.done not closed after sender unblocked")
	}
}

func TestToolStream_E2E_HandleMessage(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	tool, _ := NewTool("streamer", func(ctx context.Context, _ []byte) ([]byte, error) {
		stream := StreamFromCtx(ctx)
		if stream == nil {
			return []byte("no-stream"), nil
		}
		stream.SendText("line-1")
		stream.SendText("line-2")
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	// Initialize the server.
	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	var mu sync.Mutex
	var streamNotifications []*JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		if n.Method == methodProgress {
			mu.Lock()
			streamNotifications = append(streamNotifications, n)
			mu.Unlock()
		}
	}

	ctx := WithNotificationSender(context.Background(), sender)
	ctx = WithSubscriberID(ctx, "session-1")

	s.HandleMessage(ctx, initMsg)

	// Call the streaming tool.
	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "streamer",
		"arguments": map[string]any{},
	})

	resp, err := s.HandleMessage(ctx, callMsg)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	// Stream notifications should have been sent before the response.
	mu.Lock()
	defer mu.Unlock()
	if len(streamNotifications) != 2 {
		t.Fatalf("expected 2 stream notifications, got %d", len(streamNotifications))
	}

	for i, n := range streamNotifications {
		sp := unmarshalStreamParams(t, n)
		if sp.Data == nil {
			t.Errorf("notification[%d] data is nil", i)
			continue
		}
		if sp.Data.Sequence != int64(i+1) {
			t.Errorf("notification[%d] sequence = %d, want %d", i, sp.Data.Sequence, i+1)
		}
	}
}

func TestToolStream_E2E_NoSender(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var streamWasNil bool
	tool, _ := NewTool("no-stream", func(ctx context.Context, _ []byte) ([]byte, error) {
		streamWasNil = StreamFromCtx(ctx) == nil
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	// No NotificationSender in context — streaming should be unavailable.
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "no-stream",
		"arguments": map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callMsg)
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}
	if !streamWasNil {
		t.Error("expected StreamFromCtx to return nil when no sender is available")
	}
}

func TestToolStream_E2E_WithProgressToken(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	tool, _ := NewTool("tok-streamer", func(ctx context.Context, _ []byte) ([]byte, error) {
		stream := StreamFromCtx(ctx)
		if stream != nil {
			stream.SendText("data")
		}
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	var capturedToken any
	sender := func(n *JSONRPCNotification) {
		if n.Method == methodProgress {
			sp := unmarshalStreamParams(t, n)
			if sp.Data != nil {
				capturedToken = sp.ProgressToken
			}
		}
	}

	ctx := WithNotificationSender(context.Background(), sender)
	ctx = WithSubscriberID(ctx, "s1")
	s.HandleMessage(ctx, initMsg)

	callMsg := jsonrpcReq(42, "tools/call", map[string]any{
		"name":      "tok-streamer",
		"arguments": map[string]any{},
		"_meta":     map[string]any{"progressToken": "custom-tok"},
	})

	s.HandleMessage(ctx, callMsg)

	if capturedToken != "custom-tok" {
		t.Errorf("expected progressToken = custom-tok, got %v", capturedToken)
	}
}

func TestWithStreamBufferSize(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0", WithStreamBufferSize(4))
	if s.streamBufSize != 4 {
		t.Errorf("streamBufSize = %d, want 4", s.streamBufSize)
	}
}

func TestWithStreamBufferSize_Panic(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for zero buffer size")
		}
	}()
	WithStreamBufferSize(0)
}

func TestToolStream_ChunksAreFlushedBeforeResponse(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	tool, _ := NewTool("flush-test", func(ctx context.Context, _ []byte) ([]byte, error) {
		stream := StreamFromCtx(ctx)
		if stream != nil {
			for i := 0; i < 10; i++ {
				stream.SendText("chunk")
			}
		}
		return []byte("final"), nil
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	var mu sync.Mutex
	var chunkCount int
	sender := func(n *JSONRPCNotification) {
		if n.Method == methodProgress {
			mu.Lock()
			chunkCount++
			mu.Unlock()
		}
	}

	ctx := WithNotificationSender(context.Background(), sender)
	ctx = WithSubscriberID(ctx, "s1")
	s.HandleMessage(ctx, initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "flush-test",
		"arguments": map[string]any{},
	})

	// HandleMessage returns the final response — by that point, all chunks
	// should have been flushed by stream.close() (deferred in handleToolsCall).
	resp, _ := s.HandleMessage(ctx, callMsg)
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	mu.Lock()
	defer mu.Unlock()
	// 10 stream chunks + possibly progress notifications from ProgressReporter
	// but we only set up stream, not manual ReportProgress.
	if chunkCount != 10 {
		t.Errorf("expected 10 chunks flushed before response, got %d", chunkCount)
	}
}
