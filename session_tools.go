package finemcp

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ── Errors ──────────────────────────────────────────────────────────

var (
	errSessionIDEmpty       = errors.New("session ID must not be empty")
	errSessionToolNil       = errors.New("session tool must not be nil")
	errSessionToolExists    = errors.New("session tool already registered")
	errSessionToolNotFound  = errors.New("session tool not found")
	errSessionToolNameEmpty = errors.New("session tool name must not be empty")
	errSessionToolNoHandler = errors.New("session tool handler must not be nil")
	errSessionClosed        = errors.New("session has been closed")
	errSessionToolDenied    = errors.New("session tool rejected by tenant filter")
	errSessionToolLimit     = errors.New("session tool limit exceeded")
)

// ── Defaults ────────────────────────────────────────────────────────

const (
	// DefaultMaxSessionTools is the default maximum number of tools per session.
	DefaultMaxSessionTools = 100

	// DefaultMaxSessions is the default maximum number of concurrent sessions
	// that can have session tools. Prevents unbounded memory growth from
	// rogue transport connections.
	DefaultMaxSessions = 10000

	// maxClosedSessions caps the closed-session set to prevent unbounded
	// memory growth. When exceeded, the oldest entries are evicted.
	maxClosedSessions = 10000
)

// ── ShadowCallback ──────────────────────────────────────────────────

// SessionToolShadowCallback is called when a session tool shadows a global
// tool with the same name. This is informational — the registration proceeds
// regardless. Implementations should be lightweight and safe for concurrent
// use.
type SessionToolShadowCallback func(sessionID, toolName string)

// ── sessionToolRegistry ─────────────────────────────────────────────

// sessionToolRegistry holds per-session tool overlays. Each session can
// have zero or more tools that overlay (and take priority over or add to)
// the global tool registry. The registry is safe for concurrent use.
//
// Tools registered here are visible only to their owning session. A session
// tool whose name matches a global tool shadows the global one for that
// session — the session-level handler wins for both tools/list and tools/call.
type sessionToolRegistry struct {
	mu              sync.RWMutex
	sessions        map[string]*sessionTools // sessionID → tools
	maxToolsPerSess int                      // 0 = use DefaultMaxSessionTools
	maxSessions     int                      // 0 = use DefaultMaxSessions

	// closed tracks recently cleaned-up sessions so that late in-flight
	// adds are rejected. Implemented as a bounded ring buffer backed by
	// a map + slice to avoid unbounded memory growth.
	// Lazy-initialized on first removeAll call.
	closed    map[string]struct{}
	closedRng []string // ring buffer of sessionIDs in insertion order
	closedIdx int      // next write position in closedRng
}

// sessionTools holds the per-session tool map and cached sorted snapshot.
type sessionTools struct {
	tools  map[string]*Tool // keyed by tool Name
	sorted []*Tool          // cached sorted snapshot; nil = invalidated
}

// errSessionLimitReached is returned when the maximum number of concurrent
// sessions with session tools has been reached.
var errSessionLimitReached = errors.New("session limit reached")

func newSessionToolRegistry() *sessionToolRegistry {
	return &sessionToolRegistry{
		sessions: make(map[string]*sessionTools),
		// closed map and closedRng are lazy-initialized on first removeAll
		// call to avoid ~570KB upfront allocation when session tools are
		// never used.
	}
}

// add registers a tool for the given session. Returns an error if:
//   - the session has been closed (cleaned up via removeAll),
//   - a session tool with the same name already exists for this session,
//   - the per-session tool limit would be exceeded.
//
// Global name conflicts are intentionally allowed — the session tool shadows
// the global one.
func (r *sessionToolRegistry) add(sessionID string, tool *Tool) error {
	if sessionID == "" {
		return errSessionIDEmpty
	}
	if tool == nil {
		return errSessionToolNil
	}
	if tool.Name == "" {
		return errSessionToolNameEmpty
	}
	if tool.Handler == nil {
		return errSessionToolNoHandler
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reject adds to sessions that have been cleaned up (disconnect raced
	// with a late in-flight request).
	if r.closed != nil {
		if _, ok := r.closed[sessionID]; ok {
			return errSessionClosed
		}
	}

	st := r.sessions[sessionID]
	if st == nil {
		// Enforce total session limit.
		limit := r.maxSessions
		if limit <= 0 {
			limit = DefaultMaxSessions
		}
		if len(r.sessions) >= limit {
			return errSessionLimitReached
		}
		st = &sessionTools{tools: make(map[string]*Tool)}
		r.sessions[sessionID] = st
	}

	// Enforce per-session tool limit.
	limit := r.maxToolsPerSess
	if limit <= 0 {
		limit = DefaultMaxSessionTools
	}
	if len(st.tools) >= limit {
		return errSessionToolLimit
	}

	if _, exists := st.tools[tool.Name]; exists {
		return errSessionToolExists
	}

	st.tools[tool.Name] = tool
	st.sorted = nil // invalidate sorted cache
	return nil
}

// remove unregisters a session tool by name. Returns errSessionToolNotFound if
// the tool was not registered for this session.
func (r *sessionToolRegistry) remove(sessionID, name string) error {
	if sessionID == "" {
		return errSessionIDEmpty
	}
	if name == "" {
		return errSessionToolNameEmpty
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.sessions[sessionID]
	if st == nil {
		return errSessionToolNotFound
	}

	if _, exists := st.tools[name]; !exists {
		return errSessionToolNotFound
	}

	delete(st.tools, name)
	st.sorted = nil // invalidate sorted cache

	// Garbage-collect empty sessions.
	if len(st.tools) == 0 {
		delete(r.sessions, sessionID)
	}

	return nil
}

// removeAll removes all session tools for the given session and marks the
// session as closed. Subsequent add calls for this session will return
// errSessionClosed. This is called by transports during disconnect cleanup.
// Returns silently if the session has no tools.
//
// The closed-session set is bounded: when it reaches maxClosedSessions,
// the oldest entry is evicted from the ring buffer to make room.
func (r *sessionToolRegistry) removeAll(sessionID string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)

	// Lazy-initialize the closed-session ring buffer on first use.
	if r.closed == nil {
		r.closed = make(map[string]struct{}, maxClosedSessions)
		r.closedRng = make([]string, maxClosedSessions)
	}

	// Guard against double-call: if already marked closed, skip.
	if _, already := r.closed[sessionID]; already {
		return
	}

	// Add to bounded ring buffer. Evict the oldest entry if full.
	if old := r.closedRng[r.closedIdx]; old != "" {
		delete(r.closed, old) // evict oldest
	}
	r.closedRng[r.closedIdx] = sessionID
	r.closedIdx = (r.closedIdx + 1) % len(r.closedRng)
	r.closed[sessionID] = struct{}{}
}

// lookup returns the session tool for the given session and tool name, or nil
// if no such session tool exists.
func (r *sessionToolRegistry) lookup(sessionID, name string) *Tool {
	if sessionID == "" || name == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := r.sessions[sessionID]
	if st == nil {
		return nil
	}
	return st.tools[name]
}

// sortedTools returns a cached sorted snapshot of session tools for the given
// session. Returns nil if the session has no tools.
func (r *sessionToolRegistry) sortedTools(sessionID string) []*Tool {
	if sessionID == "" {
		return nil
	}

	r.mu.RLock()
	st := r.sessions[sessionID]
	if st == nil {
		r.mu.RUnlock()
		return nil
	}
	if cached := st.sorted; cached != nil {
		r.mu.RUnlock()
		return cached
	}
	r.mu.RUnlock()

	// Build and cache under write lock.
	r.mu.Lock()
	defer r.mu.Unlock()
	st = r.sessions[sessionID]
	if st == nil {
		return nil
	}
	if st.sorted != nil {
		return st.sorted
	}

	result := make([]*Tool, 0, len(st.tools))
	for _, t := range st.tools {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	st.sorted = result
	return result
}

// hasSession reports whether any session tools are registered for the given session.
func (r *sessionToolRegistry) hasSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := r.sessions[sessionID]
	return st != nil && len(st.tools) > 0
}

// isClosed reports whether a session has been cleaned up.
func (r *sessionToolRegistry) isClosed(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed == nil {
		return false
	}
	_, ok := r.closed[sessionID]
	return ok
}

// ── Server methods ──────────────────────────────────────────────────

// AddSessionTool registers a tool that is visible only to the given session.
// The sessionID should match the SubscriberID used by the transport for that
// connection.
//
// The ctx parameter is used for tenant-level validation: when a TenantResolver
// is active, the tool is checked against the resolved tenant's filter before
// registration. Pass context.Background() if no tenant filtering is needed.
//
// Session tools overlay the global tool registry: if a session tool has the
// same name as a global tool, the session tool takes priority for that session.
// When this occurs, the server's SessionToolShadowCallback (if configured via
// OnSessionToolShadow) is invoked.
//
// After a successful add, a notifications/tools/list_changed notification is
// sent to the affected session only (other sessions are not affected).
//
// Returns an error if:
//   - the context is cancelled,
//   - the session has been closed (disconnected),
//   - the session ID is empty,
//   - the tool is nil/invalid or its name fails validation,
//   - a session tool with the same name is already registered,
//   - the per-session tool limit is exceeded,
//   - the tenant filter rejects the tool.
//
// Safe for concurrent use.
func (s *Server) AddSessionTool(ctx context.Context, sessionID string, tool *Tool) error {
	if tool == nil {
		return errSessionToolNil
	}
	if tool.Handler == nil {
		return errSessionToolNoHandler
	}

	// Validate tool name using the same rules as NewTool.
	if err := validateToolName(tool.Name); err != nil {
		return err
	}

	// Reject structurally invalid calls before consulting the resolver — the
	// resolver may have side effects (rate-limiter, audit log, DB) that should
	// not fire for operations that are guaranteed to fail.
	if sessionID == "" {
		return errSessionIDEmpty
	}
	if s.sessionTools.isClosed(sessionID) {
		return errSessionClosed
	}

	// Honour context cancellation. If the caller already cancelled,
	// bail out before doing any work.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Tenant-level validation: prefer the filter already resolved by the
	// dispatch layer (avoids a redundant resolver round-trip).
	// Fall back to calling the resolver directly when the context has no
	// filter (e.g. AddSessionTool called outside a request handler).
	if f := itemFilterFromCtx(ctx); f != nil {
		if !f.AllowTool(tool) {
			return errSessionToolDenied
		}
	} else if resolver, _ := s.tenantResolver.Load().(TenantResolver); resolver != nil {
		filter, err := resolver(ctx)
		if err != nil {
			return errSessionToolDenied
		}
		if filter != nil && !filter.AllowTool(tool) {
			return errSessionToolDenied
		}
	}

	if err := s.sessionTools.add(sessionID, tool); err != nil {
		return err
	}

	// Notify shadow callback if this tool shadows a global one.
	if cb, _ := s.sessionToolShadowCB.Load().(SessionToolShadowCallback); cb != nil {
		s.mu.RLock()
		_, shadows := s.tools[tool.Name]
		s.mu.RUnlock()
		if shadows {
			cb(sessionID, tool.Name)
		}
	}

	// Notify only the affected session.
	s.notifySessionToolsChanged(sessionID)
	return nil
}

// RemoveSessionTool removes a session-specific tool by name.
// Returns errSessionToolNotFound if no such session tool exists.
//
// After a successful removal, a notifications/tools/list_changed notification
// is sent to the affected session only.
//
// Safe for concurrent use.
func (s *Server) RemoveSessionTool(sessionID, name string) error {
	if err := s.sessionTools.remove(sessionID, name); err != nil {
		return err
	}

	// Notify only the affected session.
	s.notifySessionToolsChanged(sessionID)
	return nil
}

// RemoveSessionTools removes all session-specific tools for the given session
// and marks the session as closed. Subsequent AddSessionTool calls for this
// session will return errSessionClosed.
//
// This should be called by transports when a client disconnects. It is safe
// to call even if the session has no tools.
//
// Unlike Add/Remove, this does NOT send a list_changed notification since the
// session is being torn down.
//
// Safe for concurrent use.
func (s *Server) RemoveSessionTools(sessionID string) {
	s.sessionTools.removeAll(sessionID)
}

// SessionTools returns a snapshot of tools registered for the given session,
// sorted by name. Does not include global tools. Returns nil if the session
// has no session-specific tools.
func (s *Server) SessionTools(sessionID string) []*Tool {
	sorted := s.sessionTools.sortedTools(sessionID)
	if sorted == nil {
		return nil
	}
	out := make([]*Tool, len(sorted))
	copy(out, sorted)
	return out
}

// OnSessionToolShadow registers a callback that is invoked when a session
// tool shadows a global tool with the same name. This is informational only;
// the registration proceeds regardless. Set to nil to disable.
//
// The callback must be safe for concurrent use and should return quickly.
//
// Safe to call concurrently; uses atomic storage internally.
func (s *Server) OnSessionToolShadow(cb SessionToolShadowCallback) {
	if cb == nil {
		// Store typed nil so atomic.Value doesn't panic on untyped nil.
		s.sessionToolShadowCB.Store(SessionToolShadowCallback(nil))
	} else {
		s.sessionToolShadowCB.Store(cb)
	}
}

// notifySessionToolsChanged sends a notifications/tools/list_changed only to
// the specified session's NotificationSender.
func (s *Server) notifySessionToolsChanged(sessionID string) {
	if sessionID == "" {
		return
	}

	s.mu.RLock()
	sender := s.senders[sessionID]
	s.mu.RUnlock()

	if sender == nil {
		return
	}

	sender(&JSONRPCNotification{
		JSONRPC: jsonrpcVersion,
		Method:  methodToolsListChanged,
	})
}

// mergeSessionTools merges two already-sorted tool slices (global and session)
// into a single sorted result using an O(N+M) merge. Session tools shadow
// global tools with the same name.
func mergeSessionTools(global, session []*Tool) []*Tool {
	merged := make([]*Tool, 0, len(global)+len(session))
	gi, si := 0, 0
	for gi < len(global) && si < len(session) {
		gn := global[gi].Name
		sn := session[si].Name
		switch {
		case gn < sn:
			merged = append(merged, global[gi])
			gi++
		case gn > sn:
			merged = append(merged, session[si])
			si++
		default:
			// Shadow: session tool wins, skip global.
			merged = append(merged, session[si])
			gi++
			si++
		}
	}
	merged = append(merged, global[gi:]...)
	merged = append(merged, session[si:]...)
	return merged
}

// mergeSessionToolsFiltered merges and filters in a single pass, writing only
// tools that pass the filter into the result slice. This avoids the intermediate
// allocation that a separate merge+filter would require.
func mergeSessionToolsFiltered(global, session []*Tool, f *ItemFilter) []*Tool {
	result := make([]*Tool, 0, len(global)+len(session))
	gi, si := 0, 0
	for gi < len(global) && si < len(session) {
		gn := global[gi].Name
		sn := session[si].Name
		switch {
		case gn < sn:
			if f.AllowTool(global[gi]) {
				result = append(result, global[gi])
			}
			gi++
		case gn > sn:
			if f.AllowTool(session[si]) {
				result = append(result, session[si])
			}
			si++
		default:
			// Shadow: session tool wins, skip global.
			if f.AllowTool(session[si]) {
				result = append(result, session[si])
			}
			gi++
			si++
		}
	}
	for ; gi < len(global); gi++ {
		if f.AllowTool(global[gi]) {
			result = append(result, global[gi])
		}
	}
	for ; si < len(session); si++ {
		if f.AllowTool(session[si]) {
			result = append(result, session[si])
		}
	}
	return result
}
