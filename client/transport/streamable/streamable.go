// Package streamable provides a client-side Streamable HTTP transport for MCP.
//
// It connects to an MCP server over HTTP, using POST for requests and
// GET for server-sent events (SSE) for server-initiated messages.
package streamable

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Config configures the Streamable HTTP client transport.
type Config struct {
	// URL is the base URL of the MCP server endpoint.
	URL string

	// Headers are extra HTTP headers to include on every request.
	Headers map[string]string

	// HTTPClient is the HTTP client to use. If nil, a default client
	// with a 30-second timeout is used.
	HTTPClient *http.Client
}

// Transport implements client.Transport for Streamable HTTP MCP servers.
type Transport struct {
	cfg       Config
	client    *http.Client
	sessionID string

	// SSE stream for server-initiated messages.
	sseCancel  context.CancelFunc
	sseResp    *http.Response
	sseMu      sync.Mutex
	sseStarted bool

	// Incoming messages from both POST responses and SSE stream.
	inbox chan []byte

	mu     sync.Mutex
	closed bool
}

// New creates a new Streamable HTTP client transport.
func New(cfg Config) *Transport {
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	return &Transport{
		cfg:    cfg,
		client: c,
		inbox:  make(chan []byte, 64),
	}
}

// Start is a no-op for HTTP transports; the connection is established
// lazily on the first request.
func (t *Transport) Start(_ context.Context) error {
	return nil
}

// Send posts a JSON-RPC message to the server. If the response is
// application/json, it is routed to the inbox immediately. If the response
// is text/event-stream, it is consumed asynchronously.
func (t *Transport) Send(ctx context.Context, data []byte) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("streamable transport: closed")
	}
	t.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("streamable transport: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("streamable transport: do request: %w", err)
	}

	// Capture session ID from the server.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionID = sid
	}

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		_ = resp.Body.Close()
		return nil
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return fmt.Errorf("streamable transport: server error %d: %s", resp.StatusCode, string(body))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		// POST returned SSE stream – consume it asynchronously.
		go t.consumeSSE(resp.Body)
		return nil
	}

	// application/json – read the single response and enqueue.
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return fmt.Errorf("streamable transport: read body: %w", err)
	}
	t.enqueue(body)
	return nil
}

// Receive blocks until a JSON-RPC message is available from the server.
func (t *Transport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case msg, ok := <-t.inbox:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close terminates the transport, cancels any SSE stream, and cleans up.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	if t.sseCancel != nil {
		t.sseCancel()
	}
	if t.sseResp != nil {
		_ = t.sseResp.Body.Close()
	}

	// Send DELETE to terminate the session.
	if t.sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.cfg.URL, nil)
		if err == nil {
			req.Header.Set("Mcp-Session-Id", t.sessionID)
			for k, v := range t.cfg.Headers {
				req.Header.Set(k, v)
			}
			resp, err := t.client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}
	}

	close(t.inbox)
	return nil
}

// StartSSE opens a persistent GET SSE stream for server-initiated messages.
// Call this after Initialize if you want to receive notifications, progress,
// and server-initiated requests via the SSE channel.
func (t *Transport) StartSSE(ctx context.Context) error {
	t.sseMu.Lock()
	if t.sseStarted {
		t.sseMu.Unlock()
		return nil
	}
	t.sseStarted = true
	t.sseMu.Unlock()

	sseCtx, cancel := context.WithCancel(ctx)
	t.sseCancel = cancel

	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, t.cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("streamable transport: sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("streamable transport: sse connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return fmt.Errorf("streamable transport: sse status %d: %s", resp.StatusCode, string(body))
	}

	t.sseResp = resp
	go t.consumeSSE(resp.Body)
	return nil
}

func (t *Transport) consumeSSE(body io.ReadCloser) {
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataBuf bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event.
			if dataBuf.Len() > 0 {
				cp := make([]byte, dataBuf.Len())
				copy(cp, dataBuf.Bytes())
				t.enqueue(cp)
				dataBuf.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
		// Ignore "event:", "id:", "retry:", and comment lines.
	}

	// Flush any trailing data.
	if dataBuf.Len() > 0 {
		cp := make([]byte, dataBuf.Len())
		copy(cp, dataBuf.Bytes())
		t.enqueue(cp)
	}
}

func (t *Transport) enqueue(data []byte) {
	if !json.Valid(data) {
		return
	}
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return
	}
	// Guard against the narrow window between the closed check and the send:
	// Close() may race here and close the inbox channel before we send.
	// Only swallow "send on closed channel" panics — re-panic for anything
	// else so real bugs are not silently swallowed.
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(runtime.Error); !ok || e.Error() != "send on closed channel" {
				panic(r)
			}
		}
	}()
	select {
	case t.inbox <- data:
	default:
		// Drop if buffer is full. Callers should use adequately sized inbox.
	}
}
