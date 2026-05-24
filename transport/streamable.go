package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Options ─────────────────────────────────────────────────────────

// StreamableOption configures the Streamable HTTP handler.
type StreamableOption func(*streamableHandler)

// WithStreamableKeepAlive sets the interval for SSE keepalive comments on
// GET streams. Set to 0 to disable. Default: 30s.
func WithStreamableKeepAlive(d time.Duration) StreamableOption {
	return func(h *streamableHandler) { h.keepAlive = d }
}

// WithStreamableMaxBody sets the maximum allowed size (in bytes) for incoming
// JSON-RPC request bodies. Default: 4 MB.
func WithStreamableMaxBody(n int64) StreamableOption {
	return func(h *streamableHandler) { h.maxBodySize = n }
}

// WithStreamableSessionTimeout sets the idle timeout for sessions. Sessions
// with no activity (POST, GET, DELETE) within this duration are automatically
// terminated and cleaned up. Set to 0 to disable. Default: 10 minutes.
func WithStreamableSessionTimeout(d time.Duration) StreamableOption {
	return func(h *streamableHandler) { h.sessionTimeout = d }
}

// WithStreamableGETBufferSize sets the per-stream channel buffer size for GET
// SSE connections. If notification throughput is high or clients are slow
// readers, increase this value. Values ≤ 0 are ignored and the default of 64
// is retained. Default: 64.
func WithStreamableGETBufferSize(n int) StreamableOption {
	return func(h *streamableHandler) {
		if n > 0 {
			h.getBufferSize = n
		}
	}
}

// WithStreamableOriginValidator sets a function that validates the Origin header
// on incoming requests. The function receives the Origin value and returns true
// if the origin is allowed. When nil (default), all origins are accepted.
//
// Security: The MCP spec requires servers to validate Origin headers to prevent
// DNS rebinding attacks. Note that requests without an Origin header are always
// allowed, as non-browser clients (CLIs, SDKs) legitimately omit it.
func WithStreamableOriginValidator(fn func(origin string) bool) StreamableOption {
	return func(h *streamableHandler) { h.originValidator = fn }
}

// ── Session ─────────────────────────────────────────────────────────

// streamableSession represents a single Streamable HTTP client session.
//
// Notification delivery follows at-most-once semantics: notifications are
// sent to exactly one open GET SSE stream. If no streams are open, or all
// streams have full buffers, the notification is silently dropped. Clients
// must reconcile state by re-fetching after receiving list-changed events.
type streamableSession struct {
	id         string
	done       chan struct{} // closed when session is terminated
	closeOnce  sync.Once
	mu         sync.Mutex               // protects getStreams
	getStreams []*getStream             // active GET SSE streams
	idleTimer  *time.Timer              // nil when session timeout is disabled
	pending    *finemcp.PendingRequests // tracks server-to-client requests
}

// getStream represents a single GET SSE connection within a session.
type getStream struct {
	events chan []byte
	done   chan struct{}
}

// close terminates the session and all its GET streams.
func (s *streamableSession) close() {
	s.closeOnce.Do(func() {
		if s.idleTimer != nil {
			s.idleTimer.Stop()
		}
		if s.pending != nil {
			s.pending.CloseAll()
		}
		close(s.done)
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, gs := range s.getStreams {
			select {
			case <-gs.done:
			default:
				close(gs.done)
			}
		}
	})
}

// addGetStream registers a new GET SSE stream and returns it.
func (s *streamableSession) addGetStream(bufSize int) *getStream {
	gs := &getStream{
		events: make(chan []byte, bufSize),
		done:   make(chan struct{}),
	}
	s.mu.Lock()
	s.getStreams = append(s.getStreams, gs)
	s.mu.Unlock()
	return gs
}

// removeGetStream removes a GET stream from the session.
func (s *streamableSession) removeGetStream(gs *getStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, g := range s.getStreams {
		if g == gs {
			last := len(s.getStreams) - 1
			s.getStreams[i] = s.getStreams[last]
			s.getStreams[last] = nil // clear for GC
			s.getStreams = s.getStreams[:last]
			return
		}
	}
}

// sendToOneGetStream sends data to exactly one GET stream (round-robin is not
// required — the spec says the server must send each message on only one stream).
// Returns false if there are no active GET streams.
func (s *streamableSession) sendToOneGetStream(data []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, gs := range s.getStreams {
		select {
		case gs.events <- data:
			return true
		case <-gs.done:
			continue
		default:
			// This stream's buffer is full; try next.
			continue
		}
	}
	return false
}

// ── Handler ─────────────────────────────────────────────────────────

// streamableHandler implements the MCP Streamable HTTP transport (spec 2025-03-26).
type streamableHandler struct {
	server          *finemcp.Server
	keepAlive       time.Duration
	maxBodySize     int64
	sessionTimeout  time.Duration
	getBufferSize   int
	originValidator func(origin string) bool
	corsOpts        *CORSOptions
	sessions        sync.Map // sessionID -> *streamableSession
}

// StreamableHandler returns an http.Handler that implements the MCP Streamable
// HTTP transport as defined in the MCP specification revision 2025-03-26.
//
// The handler serves a single endpoint that supports:
//   - POST — client sends JSON-RPC messages; server responds with JSON or SSE stream
//   - GET  — client opens an SSE stream for server-initiated messages
//   - DELETE — client terminates its session
//
// Session lifecycle:
//  1. Client POSTs an "initialize" request (no session ID required).
//  2. Server responds with InitializeResult + Mcp-Session-Id header.
//  3. Client includes Mcp-Session-Id on all subsequent requests.
//  4. Client may GET to open an SSE stream for server-initiated notifications.
//  5. Client DELETEs to terminate the session.
//
// Usage:
//
//	mux.Handle("/mcp", transport.StreamableHandler(server))
//	// or standalone:
//	transport.StartStreamable(ctx, server, ":8080")
func StreamableHandler(s *finemcp.Server, opts ...StreamableOption) http.Handler {
	h := &streamableHandler{
		server:         s,
		keepAlive:      30 * time.Second,
		sessionTimeout: 10 * time.Minute,
		getBufferSize:  64,
	}
	for _, opt := range opts {
		opt(h)
	}
	var handler http.Handler = h
	if h.corsOpts != nil {
		handler = CORS(handler, *h.corsOpts)
	}
	return handler
}

// StartStreamable starts a standalone HTTP server with the Streamable HTTP handler.
// It blocks until ctx is cancelled and then gracefully shuts down.
// The returned error is non-nil if listening fails or graceful shutdown does not
// complete within the 5-second shutdown timeout.
func StartStreamable(ctx context.Context, s *finemcp.Server, addr string, opts ...StreamableOption) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           StreamableHandler(s, opts...),
		ReadHeaderTimeout: 10 * time.Second, // mitigate Slowloris-style attacks
	}
	listenFailed := make(chan struct{})
	shutdownErr := make(chan error, 1)
	go func() { // #nosec G118 -- shutdown goroutine must outlive request context
		select {
		case <-ctx.Done():
		case <-listenFailed:
			return // ListenAndServe failed; nothing to shut down.
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		close(listenFailed)
		return err
	}
	// Return the shutdown error (nil on clean shutdown, non-nil on timeout).
	return <-shutdownErr
}

const (
	headerSessionID   = "Mcp-Session-Id"
	headerContentType = "Content-Type"
)

// ServeHTTP routes requests based on HTTP method.
func (h *streamableHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Origin validation (MCP spec security requirement).
	if h.originValidator != nil {
		origin := r.Header.Get("Origin")
		if origin != "" && !h.originValidator(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
	}

	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ── POST ────────────────────────────────────────────────────────────

// handlePost processes POST requests containing JSON-RPC messages.
func (h *streamableHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	// Validate Content-Type (MCP spec requires "application/json").
	// Use mime.ParseMediaType for correct handling of parameters like
	// charset while rejecting unrelated types (e.g., application/jsonl).
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get(headerContentType))
	if mediaType != "application/json" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	// Validate Accept header.
	// Use case-insensitive comparison per RFC 7231 §3.1.1.1.
	// An empty Accept header means the client accepts any media type.
	if accept := strings.ToLower(r.Header.Get("Accept")); accept != "" &&
		!strings.Contains(accept, "application/json") &&
		!strings.Contains(accept, "text/event-stream") &&
		!strings.Contains(accept, "*/*") {
		http.Error(w, "not acceptable", http.StatusNotAcceptable)
		return
	}

	maxSize := h.maxBodySize
	if maxSize <= 0 {
		maxSize = defaultMaxBodySize
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)
	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	if len(body) == 0 {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	// Detect whether this is an initialize request (no session required).
	isInit := h.isInitializeRequest(body)

	// Session validation: all non-initialize requests require a valid session.
	var session *streamableSession
	sessionID := r.Header.Get(headerSessionID)

	if isInit {
		// Initialize creates a new session. A repeated initialize from the same
		// client creates a separate session; the previous session remains until
		// it is explicitly DELETEd or expires via idle timeout.
		session, err = h.createSession()
		if err != nil {
			http.Error(w, "failed to create session", http.StatusInternalServerError)
			return
		}
	} else {
		if sessionID == "" {
			http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
			return
		}
		sessionVal, ok := h.sessions.Load(sessionID)
		if !ok {
			http.Error(w, "unknown or expired session", http.StatusNotFound)
			return
		}
		session = sessionVal.(*streamableSession)
	}

	// Guard against using a session that was concurrently closed
	// (e.g., by idle timeout or DELETE) between the map lookup and here.
	select {
	case <-session.done:
		http.Error(w, "session terminated", http.StatusNotFound)
		return
	default:
	}

	// Reset session idle timer on activity.
	h.resetSessionTimer(session)

	// Check if this POST is a response to a server-initiated request.
	// Handled before building the handler context to avoid unnecessary allocations.
	if finemcp.IsResponse(body) {
		if session.pending != nil {
			session.pending.Deliver(body)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Build context with notification sender and subscriber ID.
	// The per-request sender delegates to sendNotification, which is the
	// same logic used by the session-level sender registered in createSession.
	ctx := finemcp.WithNotificationSender(r.Context(), func(n *finemcp.JSONRPCNotification) {
		sendNotification(session, n)
	})
	ctx = finemcp.WithSubscriberID(ctx, session.id)
	if session.pending != nil {
		ctx = finemcp.WithRequestSender(ctx, session.pending.Send)
	}

	// Process the JSON-RPC message.
	resp, handleErr := h.server.HandleMessage(ctx, body)
	if handleErr != nil {
		// If this was an initialize request, the session was already stored in
		// the map but the client will never learn its ID. Clean up to avoid
		// an orphaned session that lingers until idle timeout.
		if isInit {
			h.sessions.Delete(session.id)
			session.close()
			h.server.RemoveSender(session.id)
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Notification: no JSON-RPC response expected.
	if resp == nil {
		// Defense-in-depth: if this was somehow classified as an init but
		// yielded no response (e.g., sent as a notification), clean up the
		// orphaned session. In normal flow isInitializeRequest requires an
		// "id" field so this path should not be reached for init.
		if isInit {
			h.sessions.Delete(session.id)
			session.close()
			h.server.RemoveSender(session.id)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Response: return as JSON with session ID header.
	data, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}

	if isInit {
		w.Header().Set(headerSessionID, session.id)
	}
	w.Header().Set(headerContentType, "application/json")
	// Write error is intentionally ignored — headers are already sent so
	// there is no way to communicate a failure to the client.
	_, _ = w.Write(data)
}

// isInitializeRequest peeks at the JSON to detect if this is an "initialize"
// method call (a JSON-RPC *request*, not a notification). It requires both
// "method":"initialize" and a non-absent "id" field. An initialize
// notification (no ID) must not trigger session creation — doing so would
// create an orphaned session the client can never use.
func (h *streamableHandler) isInitializeRequest(body []byte) bool {
	var peek struct {
		Method string           `json:"method"`
		ID     *json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		return false
	}
	return peek.Method == "initialize" && peek.ID != nil
}

// sendNotification marshals a JSON-RPC notification and delivers it to an open
// GET SSE stream on the session. If no stream is open or all stream buffers are
// full, the notification is silently dropped.
//
// This is an intentional design trade-off: dropping is preferred over blocking
// (which could stall the server) or closing the session (which is disruptive).
// Clients are expected to reconcile state by re-fetching (e.g., tools/list)
// after receiving a list-changed notification, so a single dropped notification
// does not cause permanent inconsistency — the next successful notification
// will trigger the reconciliation.
func sendNotification(session *streamableSession, n *finemcp.JSONRPCNotification) {
	data, err := json.Marshal(n)
	if err != nil {
		return
	}
	session.sendToOneGetStream(data)
}

// createSession generates a new session and registers its notification sender.
func (h *streamableHandler) createSession() (*streamableSession, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session ID: %w", err)
	}

	session := &streamableSession{
		id:   id,
		done: make(chan struct{}),
	}

	// Set up pending-request tracking for server-to-client requests
	// (e.g. sampling/createMessage). Outgoing requests are delivered via
	// the session's GET SSE stream; responses arrive as POST requests.
	session.pending = finemcp.NewPendingRequests(func(data []byte) error {
		if !session.sendToOneGetStream(data) {
			return fmt.Errorf("no active GET stream for session %s", id)
		}
		return nil
	})

	// Register a notification sender for this session.
	// Uses the shared sendNotification helper so that both the session-level
	// sender and the per-request sender in handlePost have identical logic.
	sender := func(n *finemcp.JSONRPCNotification) {
		sendNotification(session, n)
	}

	if err := h.server.AddSender(id, sender); err != nil {
		session.close()
		return nil, fmt.Errorf("register sender: %w", err)
	}

	h.sessions.Store(id, session)

	// Arm the idle timer *after* storing in the map. A concurrent POST that
	// arrives between Store and AfterFunc will call resetSessionTimer, see
	// idleTimer == nil, and return harmlessly — the timer will be armed
	// moments later with the full timeout period. Arming after Store avoids
	// a theoretical orphan: if the timer fired before Store (with an
	// absurdly short timeout), LoadAndDelete would miss the session.
	if h.sessionTimeout > 0 {
		session.idleTimer = time.AfterFunc(h.sessionTimeout, func() {
			defer func() {
				if r := recover(); r != nil {
					// Swallow panic during session cleanup to avoid crashing
					// the timer goroutine. The session is already being torn down.
					_ = r
				}
			}()
			if _, loaded := h.sessions.LoadAndDelete(id); !loaded {
				return // Already deleted (e.g., by explicit DELETE).
			}
			session.close()
			h.server.RemoveSender(id)
			h.server.UnsubscribeAll(id)
			h.server.RemoveSessionTools(id)
		})
	}

	return session, nil
}

// resetSessionTimer resets the idle timeout for a session.
//
// Note: there is a benign TOCTOU race between the session.done check and
// the Reset call — the timer callback could fire in between. This is safe
// because the callback guards with LoadAndDelete: if Reset restarts a
// timer whose callback already ran, the second callback invocation will
// see !loaded and return immediately.
func (h *streamableHandler) resetSessionTimer(session *streamableSession) {
	if h.sessionTimeout <= 0 || session.idleTimer == nil {
		return
	}
	// Guard against resetting a timer for a session that was concurrently
	// closed between the caller's map lookup and this call.
	select {
	case <-session.done:
		return
	default:
	}
	session.idleTimer.Reset(h.sessionTimeout)
}

// ── GET ─────────────────────────────────────────────────────────────

// handleGet opens an SSE stream for server-initiated messages.
func (h *streamableHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Validate Accept header (MCP spec requires text/event-stream for GET).
	// An empty Accept header is treated as accepting any media type.
	if accept := strings.ToLower(r.Header.Get("Accept")); accept != "" &&
		!strings.Contains(accept, "text/event-stream") &&
		!strings.Contains(accept, "*/*") {
		http.Error(w, "not acceptable", http.StatusNotAcceptable)
		return
	}

	sessionID := r.Header.Get(headerSessionID)
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		return
	}

	sessionVal, ok := h.sessions.Load(sessionID)
	if !ok {
		http.Error(w, "unknown or expired session", http.StatusNotFound)
		return
	}
	session := sessionVal.(*streamableSession)

	// Guard against a session closed concurrently (idle timeout / DELETE).
	select {
	case <-session.done:
		http.Error(w, "session terminated", http.StatusNotFound)
		return
	default:
	}

	// Reset session idle timer on activity.
	h.resetSessionTimer(session)

	// Register this GET connection as a stream within the session.
	// On exit (client disconnect, session.done, or SSE write error),
	// removeGetStream cleans up. If session.close() runs concurrently,
	// it first closes session.done (causing the select loop below to
	// return), then closes each getStream.done as a safety net.
	gs := session.addGetStream(h.getBufferSize)
	defer session.removeGetStream(gs)

	// Set SSE headers.
	w.Header().Set(headerContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Connection: keep-alive is meaningful only for HTTP/1.x; HTTP/2+
	// prohibits connection-specific headers (RFC 9113 §8.2.2).
	if r.ProtoMajor == 1 {
		w.Header().Set("Connection", "keep-alive")
	}
	flusher.Flush()

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
			return
		case data := <-gs.events:
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", data); err != nil {
				return // Client disconnected.
			}
			flusher.Flush()
		case <-tickCh:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return // Client disconnected.
			}
			flusher.Flush()
		}
	}
}

// ── DELETE ──────────────────────────────────────────────────────────

// handleDelete terminates a client session.
func (h *streamableHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(headerSessionID)
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		return
	}

	sessionVal, ok := h.sessions.LoadAndDelete(sessionID)
	if !ok {
		http.Error(w, "unknown or expired session", http.StatusNotFound)
		return
	}
	session := sessionVal.(*streamableSession)

	// Clean up: close session, remove sender, unsubscribe all resources.
	session.close()
	h.server.RemoveSender(sessionID)
	h.server.UnsubscribeAll(sessionID)
	h.server.RemoveSessionTools(sessionID)

	w.WriteHeader(http.StatusOK)
}
