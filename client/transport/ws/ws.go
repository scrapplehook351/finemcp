// Package ws provides a client-side WebSocket transport for MCP.
package ws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config configures the WebSocket client transport.
type Config struct {
	// URL is the WebSocket URL to connect to (ws:// or wss://).
	URL string

	// Headers are extra HTTP headers to include in the handshake.
	Headers map[string]string

	// PingInterval is the interval for sending WebSocket pings.
	// Defaults to 30 seconds if zero.
	PingInterval time.Duration

	// Dialer is the WebSocket dialer to use. If nil, a default dialer is used.
	Dialer *websocket.Dialer
}

// Transport implements client.Transport for WebSocket-based MCP servers.
type Transport struct {
	cfg  Config
	conn *websocket.Conn

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// New creates a new WebSocket client transport.
func New(cfg Config) *Transport {
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 30 * time.Second
	}
	return &Transport{
		cfg:  cfg,
		done: make(chan struct{}),
	}
}

// Start establishes the WebSocket connection to the MCP server.
func (t *Transport) Start(ctx context.Context) error {
	dialer := t.cfg.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}

	header := http.Header{}
	for k, v := range t.cfg.Headers {
		header.Set(k, v)
	}

	conn, _, err := dialer.DialContext(ctx, t.cfg.URL, header)
	if err != nil {
		return fmt.Errorf("ws transport: dial: %w", err)
	}
	t.conn = conn

	// Set up pong handler.
	t.conn.SetPongHandler(func(string) error {
		return t.conn.SetReadDeadline(time.Now().Add(t.cfg.PingInterval * 2))
	})

	// Start ping loop.
	go t.pingLoop()

	return nil
}

// Send writes a JSON-RPC message to the WebSocket connection.
func (t *Transport) Send(_ context.Context, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return errors.New("ws transport: closed")
	}

	if err := t.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("ws transport: write: %w", err)
	}
	return nil
}

// Receive reads the next JSON-RPC message from the WebSocket connection.
func (t *Transport) Receive(_ context.Context) ([]byte, error) {
	_, msg, err := t.conn.ReadMessage()
	if err != nil {
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("ws transport: read: %w", err)
	}
	return msg, nil
}

// Close shuts down the WebSocket connection.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	close(t.done)
	t.mu.Unlock()

	// Send close frame.
	deadline := time.Now().Add(5 * time.Second)
	_ = t.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		deadline,
	)
	return t.conn.Close()
}

func (t *Transport) pingLoop() {
	ticker := time.NewTicker(t.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.mu.Lock()
			if t.closed {
				t.mu.Unlock()
				return
			}
			deadline := time.Now().Add(10 * time.Second)
			err := t.conn.WriteControl(websocket.PingMessage, nil, deadline)
			t.mu.Unlock()
			if err != nil {
				return
			}
		case <-t.done:
			return
		}
	}
}
