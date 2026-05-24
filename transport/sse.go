package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// SSEOption configures the SSE handler.
type SSEOption func(*sseHandler)

// WithSSEPath sets the URL path for SSE connections (default: "/sse").
func WithSSEPath(path string) SSEOption {
	return func(h *sseHandler) { h.ssePath = path }
}

// WithMessagePath sets the URL path for message POSTs (default: "/message").
func WithMessagePath(path string) SSEOption {
	return func(h *sseHandler) { h.messagePath = path }
}

// WithKeepAlive sets the interval for SSE keepalive comments.
// Set to 0 to disable. Default: 30s.
func WithKeepAlive(d time.Duration) SSEOption {
	return func(h *sseHandler) { h.keepAlive = d }
}

// WithMaxBodySize sets the maximum allowed size (in bytes) for incoming
// JSON-RPC request bodies. Requests exceeding this limit receive a
// 413 Request Entity Too Large response. Default: 4 MB.
func WithMaxBodySize(n int64) SSEOption {
	return func(h *sseHandler) { h.maxBodySize = n }
}

// sseSession represents a single SSE client connection.
type sseSession struct {
	id        string
	events    chan []byte
	done      chan struct{} // closed when the SSE stream ends
	closeOnce sync.Once
	pending   *finemcp.PendingRequests // tracks server-to-client requests
}

// sseHandler implements the MCP SSE transport.
type sseHandler struct {
	server      *finemcp.Server
	ssePath     string
	messagePath string
	keepAlive   time.Duration
	maxBodySize int64    // max request body in bytes; 0 means use default
	sessions    sync.Map // sessionID -> *sseSession
}

// defaultMaxBodySize is the default limit for incoming JSON-RPC bodies (4 MB).
const defaultMaxBodySize int64 = 4 << 20

// SSEHandler returns an http.Handler that implements the MCP SSE transport.
//
// The handler serves two endpoints:
//   - GET  /sse     — opens an SSE stream; sends an "endpoint" event with the
//     message URL, then streams "message" events for JSON-RPC responses.
//   - POST /message — accepts JSON-RPC requests; the response is delivered
//     asynchronously on the client's SSE stream.
//
// Session lifecycle:
//  1. Client connects via GET /sse.
//  2. Server assigns a session ID and sends: event: endpoint\ndata: /message?sessionId=<id>
//  3. Client POSTs JSON-RPC messages to the endpoint URL.
//  4. Server processes the message and pushes the response on the SSE stream.
//  5. When the client disconnects, the session is cleaned up.
//
// Usage:
//
//	handler := transport.SSEHandler(server)
//	http.ListenAndServe(":8080", handler)
func SSEHandler(s *finemcp.Server, opts ...SSEOption) http.Handler {
	h := &sseHandler{
		server:      s,
		ssePath:     "/sse",
		messagePath: "/message",
		keepAlive:   30 * time.Second,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// StartSSE starts an HTTP server with the SSE handler on the given address.
// This is a convenience for standalone mode. It blocks until the server stops.
func StartSSE(s *finemcp.Server, addr string, opts ...SSEOption) error {
	return http.ListenAndServe(addr, SSEHandler(s, opts...)) // #nosec G114 -- convenience function; users needing timeouts should use http.Server directly
}

// ServeHTTP routes requests to the SSE or message handler based on path.
func (h *sseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case h.ssePath:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.handleSSE(w, r)
	case h.messagePath:
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.handleMessage(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleSSE establishes an SSE stream for a new client session.
func (h *sseHandler) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sessionID, err := generateSessionID()
	if err != nil {
		http.Error(w, "failed to generate session ID", http.StatusInternalServerError)
		return
	}
	session := &sseSession{
		id:     sessionID,
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}

	// Set up pending-request tracking for server-to-client requests
	// (e.g. sampling/createMessage). Outgoing requests are delivered via
	// the SSE stream; responses arrive as POST requests.
	session.pending = finemcp.NewPendingRequests(func(data []byte) error {
		select {
		case session.events <- data:
			return nil
		case <-session.done:
			return fmt.Errorf("session closed")
		default:
			return fmt.Errorf("SSE event buffer full")
		}
	})

	h.sessions.Store(sessionID, session)

	sender := func(n *finemcp.JSONRPCNotification) {
		data, err := json.Marshal(n)
		if err != nil {
			return
		}
		select {
		case session.events <- data:
		case <-session.done:
		default:
			// Events channel is full; the client is not consuming fast enough.
			// Terminate the session so the SSE stream closes and the client can
			// reconnect, making the drop observable — consistent with the HTTP 503
			// response returned when the request-response path overflows.
			session.closeOnce.Do(func() { close(session.done) })
		}
	}
	if err := h.server.AddSender(sessionID, sender); err != nil {
		// Clean up the session that was already stored so it doesn't leak as
		// an orphaned entry that accepts /message POST requests indefinitely.
		session.closeOnce.Do(func() { close(session.done) })
		h.sessions.Delete(sessionID)
		http.Error(w, "failed to register session sender", http.StatusInternalServerError)
		return
	}

	defer func() {
		session.closeOnce.Do(func() { close(session.done) })
		if session.pending != nil {
			session.pending.CloseAll()
		}
		h.sessions.Delete(sessionID)
		h.server.RemoveSender(sessionID)
		h.server.UnsubscribeAll(sessionID)
		h.server.RemoveSessionTools(sessionID)
	}()

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Connection: keep-alive is meaningful only for HTTP/1.x; HTTP/2+
	// prohibits connection-specific headers (RFC 9113 §8.2.2).
	if r.ProtoMajor == 1 {
		w.Header().Set("Connection", "keep-alive")
	}

	// Send the endpoint event so the client knows where to POST messages.
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s?sessionId=%s\n\n", h.messagePath, sessionID)
	flusher.Flush()

	// Stream events until the client disconnects.
	ctx := r.Context()

	var ticker *time.Ticker
	var tickCh <-chan time.Time
	if h.keepAlive > 0 {
		ticker = time.NewTicker(h.keepAlive)
		defer ticker.Stop()
		tickCh = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-session.done:
			// Session was closed (e.g. backpressure in the sender): exit the
			// loop so that the deferred RemoveSender/UnsubscribeAll run and
			// the HTTP stream is torn down.
			return
		case data := <-session.events:
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(data))
			flusher.Flush()
		case <-tickCh:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// handleMessage processes a POST request with a JSON-RPC message.
func (h *sseHandler) handleMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "missing sessionId query parameter", http.StatusBadRequest)
		return
	}

	sessionVal, ok := h.sessions.Load(sessionID)
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	session := sessionVal.(*sseSession)

	maxSize := h.maxBodySize
	if maxSize <= 0 {
		maxSize = defaultMaxBodySize
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	defer func() { _ = r.Body.Close() }()

	if len(body) == 0 {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	// Check if this POST is a response to a server-initiated request.
	// Handled before building the handler context to avoid unnecessary allocations.
	if finemcp.IsResponse(body) {
		if session.pending != nil {
			session.pending.Deliver(body)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Process the JSON-RPC message.
	// Inject a notification sender so that tool handlers can emit progress
	// via finemcp.ReportProgress; notifications are enqueued on the SSE stream.
	sndCtx := finemcp.WithNotificationSender(r.Context(), func(n *finemcp.JSONRPCNotification) {
		data, err := json.Marshal(n)
		if err != nil {
			return
		}
		select {
		case session.events <- data:
		case <-session.done:
		default:
			// Events channel is full; close the session to make the drop
			// observable and let the client reconnect.
			session.closeOnce.Do(func() { close(session.done) })
		}
	})
	sndCtx = finemcp.WithSubscriberID(sndCtx, sessionID)
	if session.pending != nil {
		sndCtx = finemcp.WithRequestSender(sndCtx, session.pending.Send)
	}

	resp, err := h.server.HandleMessage(sndCtx, body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// If there's a response (not a notification), send it on the SSE stream.
	// Non-blocking, context-aware enqueue to avoid blocking the handler
	// indefinitely if the SSE client is slow or has disconnected.
	if resp != nil {
		data, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
			return
		}
		select {
		case session.events <- data:
			// enqueued successfully
		case <-session.done:
			http.Error(w, "session closed", http.StatusGone)
			return
		case <-r.Context().Done():
			http.Error(w, "request canceled", http.StatusRequestTimeout)
			return
		default:
			http.Error(w, "session is not ready to receive events", http.StatusServiceUnavailable)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// generateSessionID returns a cryptographically random hex string.
// It retries up to 3 times if crypto/rand fails, returning an error
// rather than falling back to a predictable source.
func generateSessionID() (string, error) {
	b := make([]byte, 16)
	const maxRetries = 3
	for range maxRetries {
		if _, err := rand.Read(b); err == nil {
			return hex.EncodeToString(b), nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", fmt.Errorf("crypto/rand unavailable after %d retries", maxRetries)
}
