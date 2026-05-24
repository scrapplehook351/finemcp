package transport

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/gorilla/websocket"
)

// WebSocketOption configures the WebSocket handler.
type WebSocketOption func(*websocketHandler)

// WithWebSocketPath sets the URL path for WebSocket connections (default: "/ws").
func WithWebSocketPath(path string) WebSocketOption {
	return func(h *websocketHandler) { h.path = path }
}

// WithWebSocketMaxMessageSize sets the maximum allowed size (in bytes) for
// incoming WebSocket messages. Messages exceeding this limit cause the
// connection to be closed. Default: 4 MB.
func WithWebSocketMaxMessageSize(n int64) WebSocketOption {
	return func(h *websocketHandler) { h.maxMessageSize = n }
}

// WithWebSocketCheckOrigin overrides the default origin check used during the
// WebSocket upgrade. By default, all origins are allowed. In production
// environments, callers should provide a stricter policy.
func WithWebSocketCheckOrigin(f func(r *http.Request) bool) WebSocketOption {
	return func(h *websocketHandler) { h.upgrader.CheckOrigin = f }
}

const (
	defaultWebSocketMaxMessageSize int64 = 4 << 20
	defaultWebSocketPingInterval         = 30 * time.Second
	defaultWebSocketPongWait             = 60 * time.Second
	defaultWebSocketWriteTimeout         = 10 * time.Second
)

type websocketHandler struct {
	server *finemcp.Server

	path           string
	maxMessageSize int64

	pingInterval time.Duration
	pongWait     time.Duration
	writeTimeout time.Duration

	upgrader websocket.Upgrader
}

// WebSocketHandler returns an http.Handler that implements the MCP WebSocket
// transport. Each WebSocket text message must contain a single JSON-RPC 2.0
// message. Responses and notifications are sent as JSON-RPC objects in
// WebSocket text messages.
//
// Usage:
//
//	handler := transport.WebSocketHandler(server)
//	http.ListenAndServe(":8080", handler)
func WebSocketHandler(s *finemcp.Server, opts ...WebSocketOption) http.Handler {
	h := &websocketHandler{
		server:         s,
		path:           "/ws",
		maxMessageSize: defaultWebSocketMaxMessageSize,
		pingInterval:   defaultWebSocketPingInterval,
		pongWait:       defaultWebSocketPongWait,
		writeTimeout:   defaultWebSocketWriteTimeout,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// StartWebSocket starts an HTTP server on the given address with the
// WebSocket handler. This is a convenience for standalone mode. It blocks
// until the server stops.
func StartWebSocket(s *finemcp.Server, addr string, opts ...WebSocketOption) error {
	return http.ListenAndServe(addr, WebSocketHandler(s, opts...)) // #nosec G114 -- convenience function; users needing timeouts should use http.Server directly
}

type wsSession struct {
	id string

	conn *websocket.Conn

	writeTimeout time.Duration

	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan struct{}
}

func newWSSession(conn *websocket.Conn, id string, writeTimeout time.Duration) *wsSession {
	return &wsSession{
		id:           id,
		conn:         conn,
		writeTimeout: writeTimeout,
		done:         make(chan struct{}),
	}
}

func (s *wsSession) close() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()
	})
}

// writeMessage serializes and writes a WebSocket message, enforcing a
// write deadline and closing the session on error.
func (s *wsSession) writeMessage(messageType int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		s.close()
		return err
	}
	if err := s.conn.WriteMessage(messageType, data); err != nil {
		s.close()
		return err
	}
	return nil
}

// pingLoop periodically sends WebSocket ping frames until the session is
// closed. Any write error will close the session.
func (s *wsSession) pingLoop(interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if err := s.writeMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ServeHTTP upgrades the connection to WebSocket and starts the full-duplex
// MCP transport.
func (h *websocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != h.path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an appropriate HTTP error response.
		return
	}

	maxSize := h.maxMessageSize
	if maxSize <= 0 {
		maxSize = defaultWebSocketMaxMessageSize
	}
	conn.SetReadLimit(maxSize)

	_ = conn.SetReadDeadline(time.Now().Add(h.pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(h.pongWait))
	})

	sessionID, err := generateSessionID()
	if err != nil {
		// Cannot use http.Error here — the HTTP response has already been
		// upgraded to a WebSocket connection (status 101).
		_ = conn.Close()
		return
	}

	session := newWSSession(conn, sessionID, h.writeTimeout)

	sender := func(n *finemcp.JSONRPCNotification) {
		data, err := json.Marshal(n)
		if err != nil {
			// Marshaling a notification should never fail unless there is a
			// bug in the server. Close the session to make the problem
			// observable rather than silently dropping notifications.
			session.close()
			return
		}
		_ = session.writeMessage(websocket.TextMessage, data)
	}

	if err := h.server.AddSender(sessionID, sender); err != nil {
		// Cannot use http.Error here — the HTTP response has already been
		// upgraded to a WebSocket connection (status 101).
		session.close()
		return
	}

	defer func() {
		session.close()
		h.server.RemoveSender(sessionID)
		h.server.UnsubscribeAll(sessionID)
		h.server.RemoveSessionTools(sessionID)
	}()

	go session.pingLoop(h.pingInterval)

	ctx := r.Context()

	// Read messages in a separate goroutine so that the main select can
	// also observe context cancellation (e.g. server shutdown).
	type readResult struct {
		msg []byte
		err error
	}
	msgCh := make(chan readResult, 1)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			msgCh <- readResult{msg: msg, err: err}
			if err != nil {
				return
			}
		}
	}()

	// Set up pending-request tracking for server-to-client requests
	// (e.g. sampling/createMessage) over the WebSocket connection.
	pr := finemcp.NewPendingRequests(func(data []byte) error {
		return session.writeMessage(websocket.TextMessage, data)
	})
	defer pr.CloseAll()

	for {
		select {
		case <-ctx.Done():
			return
		case r := <-msgCh:
			if r.err != nil {
				// Connection closed or read error; end the session.
				return
			}
			if len(r.msg) == 0 {
				continue
			}

			// Check if this is a client response to a server-initiated request.
			if finemcp.IsResponse(r.msg) {
				pr.Deliver(r.msg)
				continue
			}

			msgCtx := finemcp.WithNotificationSender(ctx, sender)
			msgCtx = finemcp.WithSubscriberID(msgCtx, sessionID)
			msgCtx = finemcp.WithRequestSender(msgCtx, pr.Send)

			resp, err := h.server.HandleMessage(msgCtx, r.msg)
			if err != nil {
				// Treat transport-level errors as fatal for this connection.
				return
			}
			if resp == nil {
				// Notification: no response to send.
				continue
			}

			data, err := json.Marshal(resp)
			if err != nil {
				return
			}
			if err := session.writeMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}
