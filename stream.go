package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
)

// DefaultStreamBufferSize is the default number of content chunks buffered
// before Send blocks, providing backpressure to the tool handler.
const DefaultStreamBufferSize = 16

// ErrStreamClosed is returned by [ToolStream.Send] and [ToolStream.SendText]
// when the stream has already been closed by the framework. Tool handlers
// can use errors.Is(err, finemcp.ErrStreamClosed) to distinguish this from
// context cancellation or marshaling errors.
var ErrStreamClosed = errors.New("stream closed")

// ToolStream delivers incremental content chunks to the client during tool
// execution via notifications/progress. Tool handlers obtain a stream from
// context using [StreamFromCtx] and call [ToolStream.Send] or
// [ToolStream.SendText] to push chunks; the final result is still returned
// normally from the handler.
//
// Streaming is supported on stdio, WebSocket, SSE, and Streamable HTTP
// transports — all of which inject a NotificationSender into the context.
// Plain HTTP is the only transport where StreamFromCtx returns nil, as it
// has no persistent server-to-client channel.
//
// Backpressure: Send blocks when the internal buffer is full. The buffer size
// defaults to [DefaultStreamBufferSize] (16) and can be changed with
// [WithStreamBufferSize]. Context cancellation unblocks a waiting Send.
//
// Example:
//
//	tool, _ := finemcp.NewTool("tail-logs", func(ctx context.Context, input []byte) ([]byte, error) {
//	    stream := finemcp.StreamFromCtx(ctx)
//	    if stream == nil {
//	        return []byte("streaming not supported"), nil
//	    }
//	    for line := range logLines(ctx) {
//	        if err := stream.SendText(line); err != nil {
//	            return nil, err
//	        }
//	    }
//	    return []byte(`{"done":true}`), nil
//	})
type ToolStream struct {
	ctx    context.Context
	sender NotificationSender
	token  any
	seq    atomic.Int64 // owned exclusively by drain; only read by Sequence
	ch     chan streamChunk
	quit   chan struct{} // closed by close() to signal Send and drain
	done   chan struct{} // closed by drain() when it exits
	closed atomic.Bool
}

// streamChunk is an internal message queued in the ToolStream buffer.
// The content is pre-marshalled in Send() to avoid double-marshalling.
type streamChunk struct {
	data json.RawMessage
}

// StreamChunkData is the payload carried in the "data" field of a streaming
// progress notification. Clients that understand streaming read content chunks
// from this field; non-streaming clients simply ignore the extra field.
type StreamChunkData struct {
	// Content is the JSON-encoded content chunk (e.g. TextContent, ImageContent).
	Content json.RawMessage `json:"content"`
	// Sequence is the 1-based ordinal of this chunk within the stream.
	Sequence int64 `json:"sequence"`
}

// streamProgressParams extends [ProgressParams] with a Data field for carrying
// content chunks. The notifications/progress method is reused for backward
// compatibility — the additional "data" field is ignored by non-streaming
// clients.
type streamProgressParams struct {
	ProgressParams
	Data *StreamChunkData `json:"data,omitempty"`
}

// Send delivers a content chunk to the client. It blocks if the internal
// buffer is full, providing backpressure to the tool handler. Returns nil
// on success, [ErrStreamClosed] if the stream was closed, or a context
// error if the context was cancelled.
//
// The sequence number is assigned by the drain goroutine at dispatch time,
// so [ToolStream.Sequence] accurately reflects only the chunks that were
// actually delivered (not those dropped due to close or cancellation).
//
// Send is safe for concurrent use by multiple goroutines.
func (s *ToolStream) Send(c Content) error {
	if s.closed.Load() {
		return ErrStreamClosed
	}
	if c == nil {
		return errors.New("stream: content must not be nil")
	}
	if v := reflect.ValueOf(c); v.IsValid() {
		switch v.Kind() {
		case reflect.Ptr, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice:
			if v.IsNil() {
				return errors.New("stream: content must not be nil")
			}
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("stream: content is not marshallable: %w", err)
	}
	chunk := streamChunk{data: data}
	select {
	case s.ch <- chunk:
		return nil
	case <-s.quit:
		return ErrStreamClosed
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// SendText is a convenience that sends a [TextContent] chunk.
func (s *ToolStream) SendText(text string) error {
	return s.Send(TextContent{Text: text})
}

// Sequence returns the number of chunks actually dispatched to the client.
// This can be used by the handler to track progress or include the count in
// the final result. The count only includes chunks that were dequeued and
// sent by the drain goroutine, not chunks still buffered or dropped.
func (s *ToolStream) Sequence() int64 {
	return s.seq.Load()
}

// newToolStream creates a ToolStream backed by a bounded buffer channel.
// A drainer goroutine is started immediately to dispatch chunks as
// notifications/progress to the client.
func newToolStream(ctx context.Context, sender NotificationSender, token any, bufSize int) *ToolStream {
	if bufSize <= 0 {
		bufSize = DefaultStreamBufferSize
	}
	s := &ToolStream{
		ctx:    ctx,
		sender: sender,
		token:  token,
		ch:     make(chan streamChunk, bufSize),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go s.drain()
	return s
}

// drain reads chunks from the buffer and sends them as progress notifications.
// It runs until quit is signalled, then flushes any remaining buffered chunks
// before signalling completion. The drain goroutine owns the sequence counter
// and assigns sequence numbers at dispatch time, ensuring Sequence() reflects
// only chunks that were actually sent.
func (s *ToolStream) drain() {
	defer close(s.done)
	for {
		select {
		case chunk := <-s.ch:
			s.sendProgressChunk(chunk)
		case <-s.quit:
			s.flushRemaining()
			return
		case <-s.ctx.Done():
			// Context cancelled. Drain any buffered chunks before exiting
			// so that chunks accepted by Send() are still delivered.
			s.flushRemaining()
			return
		}
	}
}

// sendProgressChunk assigns a sequence number, builds a progress notification
// for the given chunk, and delivers it via the sender. The chunk's data has
// already been marshalled by Send(), so no second marshal is needed. This is
// the single notification-building path used by both normal dispatch and flush.
//
// Must only be called from the drain goroutine or after drain has exited;
// concurrent calls would race on sequence number assignment.
func (s *ToolStream) sendProgressChunk(chunk streamChunk) {
	seq := s.seq.Add(1)
	n := &JSONRPCNotification{
		JSONRPC: jsonrpcVersion,
		Method:  methodProgress,
		Params: streamProgressParams{
			ProgressParams: ProgressParams{
				ProgressToken: s.token,
				Progress:      float64(seq),
			},
			Data: &StreamChunkData{
				Content:  chunk.data,
				Sequence: seq,
			},
		},
	}
	s.sender(n)
}

// flushRemaining drains any chunks still in the buffer without consulting
// the context, ensuring chunks accepted by Send() are delivered even after
// quit or context cancellation.
//
// Precondition: drain must have exited (i.e. s.done must be closed) or this
// must be called from within the drain goroutine itself. Concurrent calls
// with a live drain goroutine would race on sequence number assignment via
// sendProgressChunk.
func (s *ToolStream) flushRemaining() {
	for {
		select {
		case chunk := <-s.ch:
			s.sendProgressChunk(chunk)
		default:
			return
		}
	}
}

// close signals the stream to stop accepting new chunks, drains remaining
// buffered chunks to the sender, and blocks until the drainer goroutine
// exits. It is idempotent and safe for concurrent use.
//
// If the context has been cancelled and the drainer is stuck in a slow
// sender, close returns immediately. The drainer goroutine will exit on
// its own once the sender unblocks.
func (s *ToolStream) close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.quit)
	}
	// Wait for drain to finish. If context is done, the drainer may be
	// stuck in a blocking sender — don't wait indefinitely.
	select {
	case <-s.done:
		// Normal path: drain exited via quit → its flushRemaining already
		// ran. Sweep any chunk a concurrent Send() goroutine enqueued after
		// drain's flush but before we read s.done. Note: a Send() goroutine
		// that enqueues *after* our own flushRemaining() returns is a
		// residual window inherent in the channel architecture; eliminating
		// it completely would require WaitGroup tracking of in-flight senders.
		s.flushRemaining()
	case <-s.ctx.Done():
		// Context cancelled; drain may be stuck in a slow sender.
		select {
		case <-s.done:
			// Drain exited via its own ctx.Done branch. In theory, a
			// concurrent Send() could slip a chunk into the channel
			// between drain's flushRemaining and our CAS. Flush any
			// stragglers defensively.
			s.flushRemaining()
		default:
			// drain is still running (blocked in sender). Return
			// immediately — drain will flush remaining chunks on its
			// own once the sender unblocks.
		}
	}
}
