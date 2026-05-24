package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	errToolNotFound       = errors.New("tool not found")
	errToolAlreadyExists  = errors.New("tool already registered")
	errToolNil            = errors.New("tool must not be nil")
	errNotInitialized     = errors.New("server not initialized")
	errAlreadyInitialized = errors.New("server already initialized")

	errResourceURIEmpty      = errors.New("resource URI must not be empty")
	errResourceNameEmpty     = errors.New("resource name must not be empty")
	errResourceHandlerNil    = errors.New("resource handler must not be nil")
	errResourceAlreadyExists = errors.New("resource already registered")
	errResourceNotFound      = errors.New("resource not found")
	errResourceNil           = errors.New("resource must not be nil")
	errTemplateURIEmpty      = errors.New("resource template URI must not be empty")
	errTemplateNameEmpty     = errors.New("resource template name must not be empty")
	errTemplateHandlerNil    = errors.New("resource template handler must not be nil")
	errTemplateAlreadyExists = errors.New("resource template already registered")
	errTemplateNil           = errors.New("resource template must not be nil")
	errTemplateInvalid       = errors.New("resource template URI has malformed placeholders")

	errPromptNameEmpty     = errors.New("prompt name must not be empty")
	errPromptHandlerNil    = errors.New("prompt handler must not be nil")
	errPromptAlreadyExists = errors.New("prompt already registered")
	errPromptNotFound      = errors.New("prompt not found")
	errPromptNil           = errors.New("prompt must not be nil")

	errSubscriptionsDisabled = errors.New("resource subscriptions are not enabled; use WithResourceSubscriptions()")
	errSenderAlreadyExists   = errors.New("a sender is already registered for this connection ID")
	errNoNotificationSender  = errors.New("no notification sender available")
)

// subscriptionKey identifies a single (subscriber, URI) pair.
type subscriptionKey struct {
	subscriberID string
	uri          string
}

// Server is the core MCP server that manages tool registration and dispatch.
// It is transport-agnostic; transports (stdio, HTTP/SSE) are layered on top.
type Server struct {
	name    string // implementation name
	version string // implementation version

	initialized       atomic.Bool // true after successful initialize handshake
	enableSubs        bool        // true if resource subscriptions are enabled
	supportedVersions []string    // protocol versions accepted during negotiation (latest first); set at construction, never modified afterward — safe to read without lock
	negotiatedVersion string      // the version agreed upon during initialize
	taskStore         *TaskStore  // optional; set via WithTaskStore

	mu              sync.RWMutex
	tools           map[string]*Tool
	resources       map[string]*Resource         // keyed by URI
	templates       map[string]*ResourceTemplate // keyed by URITemplate
	sortedTemplates []*ResourceTemplate          // cached sorted snapshot; invalidated on registration
	sortedTools     []*Tool                      // cached sorted snapshot; invalidated on registration
	sortedResources []*Resource                  // cached sorted snapshot; invalidated on registration
	sortedPrompts   []*Prompt                    // cached sorted snapshot; invalidated on registration
	roots           map[string]*Root             // keyed by URI
	prompts         map[string]*Prompt           // keyed by Name
	middleware      []Middleware

	requestsMu sync.Mutex                    // protects requests only
	requests   map[string]context.CancelFunc // keyed by normalized request ID

	// subscriptions maps (subscriberID, uri) -> NotificationSender for resource subscriptions.
	subscriptions map[subscriptionKey]NotificationSender

	// senders maps connectionID -> NotificationSender for broadcasting list-changed events.
	senders map[string]NotificationSender

	sessionTools        *sessionToolRegistry // per-session tool overlays
	sessionToolShadowCB atomic.Value         // stores SessionToolShadowCallback; lock-free reads

	notifMu              sync.RWMutex
	notificationHandlers map[string][]NotificationHandlerFunc // custom notification handlers
	maxNotifMethods      int                                  // 0 = DefaultMaxNotificationMethods
	maxHandlersPerMethod int                                  // 0 = DefaultMaxHandlersPerMethod
	notifPanicHandler    NotificationPanicHandler             // optional; called on handler panic

	logHandler     LogHandler     // optional handler for logging/setLevel requests
	authChecker    atomic.Value   // stores AuthChecker; lock-free reads in handleRequest
	tenantResolver atomic.Value   // stores TenantResolver; lock-free reads in handleRequest
	inflight       sync.WaitGroup // tracks in-flight HandleMessage calls
	clientCaps     ClientCaps     // capabilities declared by the client during initialize
	streamBufSize  int            // buffer size for ToolStream backpressure; 0 = DefaultStreamBufferSize

	// Extended server info fields (MCP spec 2025-11-25).
	title        string // display name for the server
	description  string // human-readable description
	websiteURL   string // URL for the server's website
	icons        []Icon // visual icons for the server
	instructions string // optional instructions for the client

	lifespan LifespanFunc // optional lifecycle hook
}

// AuthChecker validates that a request context has proper authentication.
// It is called by handleRequest for every JSON-RPC request (except initialize
// and ping) after the server is initialized. If it returns a non-nil error,
// the request is rejected with a JSON-RPC error response using ErrCodeUnauthorized.
//
// Implementations should inspect the context for an AuthInfo value set by
// the transport-level authentication middleware.
type AuthChecker func(ctx context.Context) error

// SetAuthChecker registers a protocol-level authentication checker.
// The checker is called for ALL JSON-RPC requests (except initialize and ping)
// after JSON-RPC parsing but before method routing. If it returns an error,
// the request is rejected with a JSON-RPC error response (code -32001).
//
// This provides defense-in-depth: the HTTP middleware layer rejects
// unauthenticated connections, while the auth checker ensures every protocol
// message is associated with a verified identity.
//
// Safe to call concurrently; uses atomic storage internally.
//
// Usage:
//
//	server.SetAuthChecker(middleware.RequireAuth())
func (s *Server) SetAuthChecker(checker AuthChecker) {
	s.authChecker.Store(checker)
}

// ItemFilter controls per-request visibility of server items for multi-tenant
// isolation. Each function returns true to allow the item, false to hide/deny it.
// A nil function field means "allow all" for that item type.
//
// Callers should use the nil-safe Allow* methods rather than calling the
// function fields directly.
type ItemFilter struct {
	// TenantID is the resolved tenant identifier. Set by the TenantResolver
	// so the dispatch layer can inject it into context via WithTenantID.
	// Empty if tenancy is not active.
	TenantID string

	// Tool returns true if the item should be visible to the caller.
	// nil means all tools are visible.
	Tool func(*Tool) bool

	// Resource returns true if the item should be visible to the caller.
	// nil means all resources are visible.
	Resource func(*Resource) bool

	// ResourceTemplate returns true if the item should be visible to the caller.
	// nil means all resource templates are visible.
	ResourceTemplate func(*ResourceTemplate) bool

	// Prompt returns true if the item should be visible to the caller.
	// nil means all prompts are visible.
	Prompt func(*Prompt) bool
}

// AllowTool reports whether the filter permits the given tool.
// Returns true when f is nil or f.Tool is nil (allow-all).
// Panics in the filter function are recovered and treated as deny (fail-secure).
func (f *ItemFilter) AllowTool(t *Tool) (allowed bool) {
	if f == nil || f.Tool == nil {
		return true
	}
	defer func() { _ = recover() }()
	return f.Tool(t)
}

// AllowResource reports whether the filter permits the given resource.
// Returns true when f is nil or f.Resource is nil (allow-all).
// Panics in the filter function are recovered and treated as deny (fail-secure).
func (f *ItemFilter) AllowResource(r *Resource) (allowed bool) {
	if f == nil || f.Resource == nil {
		return true
	}
	defer func() { _ = recover() }()
	return f.Resource(r)
}

// AllowResourceTemplate reports whether the filter permits the given resource template.
// Returns true when f is nil or f.ResourceTemplate is nil (allow-all).
// Panics in the filter function are recovered and treated as deny (fail-secure).
func (f *ItemFilter) AllowResourceTemplate(t *ResourceTemplate) (allowed bool) {
	if f == nil || f.ResourceTemplate == nil {
		return true
	}
	defer func() { _ = recover() }()
	return f.ResourceTemplate(t)
}

// AllowPrompt reports whether the filter permits the given prompt.
// Returns true when f is nil or f.Prompt is nil (allow-all).
// Panics in the filter function are recovered and treated as deny (fail-secure).
func (f *ItemFilter) AllowPrompt(p *Prompt) (allowed bool) {
	if f == nil || f.Prompt == nil {
		return true
	}
	defer func() { _ = recover() }()
	return f.Prompt(p)
}

// TenantResolver resolves the request context into an ItemFilter that controls
// what the caller can see and access. It is called once per request by the
// dispatch layer, after authentication but before method routing.
//
// The resolver should:
//  1. Extract a tenant identifier from the context (e.g. from AuthInfo).
//  2. Look up the tenant's configuration (allowed tools, resources, etc.).
//  3. Return an *ItemFilter. Return nil to allow everything (no filtering).
//
// If the resolver returns a non-nil error, the request is rejected with
// a JSON-RPC error response using ErrCodeTenantRequired (-32002). Error
// messages are scrubbed to a generic "tenant identification required" to
// prevent enumeration of valid tenant IDs.
//
// The resolver must be safe for concurrent use from multiple goroutines.
type TenantResolver func(ctx context.Context) (*ItemFilter, error)

// SetTenantResolver registers a tenant resolution hook for multi-tenant isolation.
// The resolver is called for ALL JSON-RPC requests (except initialize and ping)
// after authentication but before method routing. If it returns an error,
// the request is rejected with a JSON-RPC error (code -32002).
//
// This provides multi-tenant isolation: each request is associated with a
// tenant, and only that tenant's permitted tools, resources, and prompts are
// visible through list operations and accessible through call/read/get operations.
//
// Safe to call concurrently; uses atomic storage internally.
//
// Usage:
//
//	server.SetTenantResolver(middleware.NewTenantResolver(
//	    middleware.TenantFromAuthSubject(),
//	    middleware.NewStaticTenantStore(configs),
//	))
func (s *Server) SetTenantResolver(resolver TenantResolver) {
	s.tenantResolver.Store(resolver)
}

// ServerOption is a functional option for configuring a Server.
type ServerOption func(*Server)

// WithResourceSubscriptions enables support for resource subscriptions.
// When enabled, the server advertises the "subscribe" capability, allowing
// clients to subscribe to resource updates.
func WithResourceSubscriptions() ServerOption {
	return func(s *Server) {
		s.enableSubs = true
	}
}

// WithTaskStore enables spec-compliant task support (tasks/get, tasks/result,
// tasks/cancel, tasks/list) and allows task-augmented tools/call requests.
// Panics if ts is nil.
func WithTaskStore(ts *TaskStore) ServerOption {
	if ts == nil {
		panic("finemcp: WithTaskStore requires a non-nil TaskStore")
	}
	return func(s *Server) {
		s.taskStore = ts
	}
}

// WithSupportedVersions overrides the default set of MCP protocol versions
// that the server accepts during the initialize handshake. Versions must be
// in descending chronological order (latest first). The first entry is the
// server's preferred version and is returned when the client requests an
// unsupported version.
//
// Panics if no versions are provided — at least one version is required.
// Panics if versions are not in descending order (YYYY-MM-DD strings).
// If not set, the server defaults to DefaultSupportedVersions().
func WithSupportedVersions(versions ...string) ServerOption {
	if len(versions) == 0 {
		panic("finemcp: WithSupportedVersions requires at least one version")
	}
	for i := 0; i < len(versions)-1; i++ {
		if versions[i] < versions[i+1] {
			panic("finemcp: WithSupportedVersions requires descending order (latest first)")
		}
	}
	return func(s *Server) {
		cp := make([]string, len(versions))
		copy(cp, versions)
		s.supportedVersions = cp
	}
}

// WithMaxSessionTools sets the maximum number of tools a single session can
// register via AddSessionTool. The default is DefaultMaxSessionTools (100).
// Use this to tune the limit for your deployment — lower values reduce memory
// exposure from misbehaving clients.
func WithMaxSessionTools(n int) ServerOption {
	if n <= 0 {
		panic("finemcp: WithMaxSessionTools requires a positive value")
	}
	return func(s *Server) {
		s.sessionTools.maxToolsPerSess = n
	}
}

// WithMaxSessions sets the maximum number of concurrent sessions that can
// register session tools. The default is DefaultMaxSessions (10,000).
// Prevents unbounded memory growth from rogue transport connections.
func WithMaxSessions(n int) ServerOption {
	if n <= 0 {
		panic("finemcp: WithMaxSessions requires a positive value")
	}
	return func(s *Server) {
		s.sessionTools.maxSessions = n
	}
}

// WithStreamBufferSize sets the number of content chunks that a ToolStream
// can buffer before Send blocks, providing backpressure to the tool handler.
// The default is DefaultStreamBufferSize (16). Use a larger value for tools
// that produce many small chunks rapidly.
func WithStreamBufferSize(n int) ServerOption {
	if n <= 0 {
		panic("finemcp: WithStreamBufferSize requires a positive value")
	}
	return func(s *Server) {
		s.streamBufSize = n
	}
}

// WithMaxNotificationMethods sets the maximum number of distinct notification
// methods that can have handlers registered via OnNotification.
// The default is DefaultMaxNotificationMethods (1000).
// Prevents unbounded memory growth from excessive handler registrations.
func WithMaxNotificationMethods(n int) ServerOption {
	if n <= 0 {
		panic("finemcp: WithMaxNotificationMethods requires a positive value")
	}
	return func(s *Server) {
		s.maxNotifMethods = n
	}
}

// WithMaxHandlersPerNotification sets the maximum number of handlers that
// can be registered for a single notification method via OnNotification.
// The default is DefaultMaxHandlersPerMethod (100).
func WithMaxHandlersPerNotification(n int) ServerOption {
	if n <= 0 {
		panic("finemcp: WithMaxHandlersPerNotification requires a positive value")
	}
	return func(s *Server) {
		s.maxHandlersPerMethod = n
	}
}

// WithNotificationPanicHandler registers a callback that is invoked whenever
// a notification handler panics. Without this option, panics are recovered
// and silently discarded. The callback receives the notification method name
// and the recovered value.
func WithNotificationPanicHandler(h NotificationPanicHandler) ServerOption {
	if h == nil {
		panic("finemcp: WithNotificationPanicHandler requires a non-nil handler")
	}
	return func(s *Server) {
		s.notifPanicHandler = h
	}
}

// WithServerTitle sets the server's display name. This is included in the
// serverInfo of the initialize response and can be used by clients for
// display purposes.
func WithServerTitle(title string) ServerOption {
	return func(s *Server) { s.title = title }
}

// WithServerDescription sets the server's human-readable description. This is
// included in the serverInfo of the initialize response.
func WithServerDescription(desc string) ServerOption {
	return func(s *Server) { s.description = desc }
}

// WithWebsiteURL sets the server's website URL. This is included in the
// serverInfo of the initialize response.
func WithWebsiteURL(url string) ServerOption {
	return func(s *Server) { s.websiteURL = url }
}

// maxInstructionsLength is the maximum allowed length for the instructions string.
const maxInstructionsLength = 100 * 1024 // 100 KB

// allowedIconSchemes lists the URL schemes permitted for Icon.Src to prevent XSS.
var allowedIconSchemes = map[string]bool{
	"http":  true,
	"https": true,
	"data":  true,
}

// WithIcons sets the server's icons. These are included in the serverInfo
// of the initialize response and can be used by clients (e.g. Claude Desktop)
// to display a visual representation of the server.
//
// Panics if any icon has a Src URL with an unsafe scheme (only http, https,
// and data are allowed). Relative URLs and protocol-relative URLs are rejected.
// Data URLs must use an image/* MIME type.
func WithIcons(icons ...Icon) ServerOption {
	// Validate icon URLs to prevent XSS via javascript: or other unsafe schemes.
	for _, icon := range icons {
		if icon.Src == "" {
			continue
		}
		u, err := url.Parse(icon.Src)
		if err != nil {
			panic("finemcp: WithIcons: invalid icon URL: " + icon.Src)
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme == "" {
			panic("finemcp: WithIcons: icon URL must have an explicit scheme (http, https, or data): " + icon.Src)
		}
		if !allowedIconSchemes[scheme] {
			panic("finemcp: WithIcons: disallowed icon URL scheme " + scheme + " in " + icon.Src)
		}
		// Validate data: URLs only allow image MIME types.
		if scheme == "data" {
			validateDataURLMIME(icon.Src)
		}
	}
	return func(s *Server) {
		cp := make([]Icon, len(icons))
		copy(cp, icons)
		s.icons = cp
	}
}

// validateDataURLMIME ensures a data: URL uses an image/* MIME type.
// Panics if the MIME type is not an image type.
func validateDataURLMIME(src string) {
	// data:[<mediatype>][;base64],<data>
	const prefix = "data:"
	rest := src[len(prefix):]
	commaIdx := strings.Index(rest, ",")
	if commaIdx == -1 {
		panic("finemcp: WithIcons: data URL missing comma separator: " + src)
	}
	mediaAndParams := rest[:commaIdx]
	// Strip parameters like ";base64"
	mediaType := strings.SplitN(mediaAndParams, ";", 2)[0]
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	if mediaType == "" {
		mediaType = "text/plain" // RFC 2397 default
	}
	if !strings.HasPrefix(mediaType, "image/") {
		panic("finemcp: WithIcons: data URL must use image/* MIME type, got " + mediaType + " in " + src)
	}
}

// WithInstructions sets optional instructions for the client. These are
// included in the initialize response and can guide client behavior.
//
// Panics if the instructions string exceeds 100 KB.
func WithInstructions(instructions string) ServerOption {
	if len(instructions) > maxInstructionsLength {
		panic(fmt.Sprintf("finemcp: WithInstructions: length %d exceeds maximum %d", len(instructions), maxInstructionsLength))
	}
	return func(s *Server) { s.instructions = instructions }
}

// LifespanFunc is a lifecycle hook called by Server.Start before the server
// begins handling requests. It receives the base context and the server
// instance, and returns an enriched context that is passed to all handlers.
// The returned cleanup function is called during shutdown to release resources.
//
// Use this to initialize shared resources (database connections, caches, etc.)
// that tool handlers need via context values.
//
// Example:
//
//	finemcp.WithLifespan(func(ctx context.Context, s *finemcp.Server) (context.Context, func(), error) {
//	    db, err := sql.Open("postgres", dsn)
//	    if err != nil {
//	        return nil, nil, err
//	    }
//	    ctx = context.WithValue(ctx, dbKey, db)
//	    return ctx, func() { db.Close() }, nil
//	})
type LifespanFunc func(ctx context.Context, s *Server) (context.Context, func(), error)

// WithLifespan registers a lifecycle hook that is called by Server.Start
// before the server begins handling requests. The hook can initialize
// shared resources and enrich the context. The returned cleanup function
// is called during shutdown.
func WithLifespan(fn LifespanFunc) ServerOption {
	return func(s *Server) { s.lifespan = fn }
}

// NewServer creates a new MCP server with the given implementation name, version, and options.
func NewServer(name, version string, opts ...ServerOption) *Server {
	s := &Server{
		name:                 name,
		version:              version,
		supportedVersions:    DefaultSupportedVersions(),
		tools:                make(map[string]*Tool),
		resources:            make(map[string]*Resource),
		templates:            make(map[string]*ResourceTemplate),
		roots:                make(map[string]*Root),
		prompts:              make(map[string]*Prompt),
		requests:             make(map[string]context.CancelFunc),
		subscriptions:        make(map[subscriptionKey]NotificationSender),
		senders:              make(map[string]NotificationSender),
		sessionTools:         newSessionToolRegistry(),
		notificationHandlers: make(map[string][]NotificationHandlerFunc),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Name returns the server's implementation name.
func (s *Server) Name() string { return s.name }

// Version returns the server's implementation version.
func (s *Server) Version() string { return s.version }

// NegotiatedVersion returns the protocol version agreed upon during the
// initialize handshake. Returns "" if the handshake has not completed.
func (s *Server) NegotiatedVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.negotiatedVersion
}

// RegisterTool adds a tool to the server's registry. The tool must not be nil,
// its name must be a valid MCP tool name (non-empty, ≤128 characters, only
// [A-Za-z0-9_\-.] allowed), and its Handler must not be nil.
//
// Returns:
//   - errToolNil if tool is nil
//   - errToolNameEmpty, errToolNameTooLong, or errToolNameChars if the name is invalid
//   - errToolHandlerNil if the handler is nil
//   - errToolAlreadyExists if a tool with the same name is already registered
//
// On success a notifications/tools/list_changed notification is broadcast to
// all connected clients.
func (s *Server) RegisterTool(tool *Tool) error {
	if tool == nil {
		return errToolNil
	}
	if err := validateToolName(tool.Name); err != nil {
		return err
	}
	if err := validateHandler(tool.Handler); err != nil {
		return err
	}

	s.mu.Lock()
	if _, exists := s.tools[tool.Name]; exists {
		s.mu.Unlock()
		return errToolAlreadyExists
	}

	s.tools[tool.Name] = tool
	s.sortedTools = nil // invalidate cache
	s.mu.Unlock()

	s.NotifyToolsListChanged()
	return nil
}

// RemoveTool removes a tool from the server's registry.
// Returns errToolNotFound if no tool with the given name is registered.
// After a successful removal, a notifications/tools/list_changed notification is
// broadcast to all connected clients.
func (s *Server) RemoveTool(name string) error {
	if name == "" {
		return errToolNotFound
	}

	s.mu.Lock()
	if _, exists := s.tools[name]; !exists {
		s.mu.Unlock()
		return errToolNotFound
	}

	delete(s.tools, name)
	s.sortedTools = nil // invalidate cache
	s.mu.Unlock()

	s.NotifyToolsListChanged()
	return nil
}

// RegisterTools registers multiple tools atomically and broadcasts a single
// notifications/tools/list_changed to all connected clients.
// Returns a validation or duplicate error on the first failing tool; on error,
// no tools from the batch are registered (all-or-nothing).
func (s *Server) RegisterTools(tools ...*Tool) error {
	if len(tools) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		if t == nil {
			return errToolNil
		}
		if err := validateToolName(t.Name); err != nil {
			return err
		}
		if err := validateHandler(t.Handler); err != nil {
			return err
		}
		if _, dup := seen[t.Name]; dup {
			return errToolAlreadyExists
		}
		seen[t.Name] = struct{}{}
	}

	s.mu.Lock()
	for _, t := range tools {
		if _, exists := s.tools[t.Name]; exists {
			s.mu.Unlock()
			return errToolAlreadyExists
		}
	}
	for _, t := range tools {
		s.tools[t.Name] = t
	}
	s.sortedTools = nil // invalidate cache
	s.mu.Unlock()

	s.NotifyToolsListChanged()
	return nil
}

// ListTools returns a snapshot of all registered tools, sorted by name.
// The returned slice is a copy; mutating it does not affect the server.
// Sorting ensures deterministic ordering for cursor-based pagination.
func (s *Server) ListTools() []*Tool {
	sorted := s.getSortedTools()
	out := make([]*Tool, len(sorted))
	copy(out, sorted)
	return out
}

// getSortedTools returns the cached sorted tool slice, building it on demand
// if it has been invalidated. Uses a read-lock fast path to avoid write-lock
// contention on the common (cache-warm) path.
func (s *Server) getSortedTools() []*Tool {
	s.mu.RLock()
	cached := s.sortedTools
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sortedTools != nil {
		return s.sortedTools
	}
	result := make([]*Tool, 0, len(s.tools))
	for _, t := range s.tools {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	s.sortedTools = result
	return result
}

// CallTool looks up a tool by name and executes its handler.
// Session-specific tools take priority over global tools for the calling
// session (identified by SubscriberIDFromCtx). Returns errToolNotFound if
// no tool with the given name is registered, or if a TenantResolver is active
// and the tool is filtered out for the current tenant (indistinguishable from
// not found to prevent enumeration).
func (s *Server) CallTool(ctx context.Context, name string, input []byte) (*CallToolResult, error) {
	// Check session tools first — they shadow global tools for this session.
	sessionID := SubscriberIDFromCtx(ctx)
	tool := s.sessionTools.lookup(sessionID, name)

	if tool == nil {
		// Fall back to the global registry.
		s.mu.RLock()
		tool = s.tools[name]
		s.mu.RUnlock()
	}

	if tool == nil {
		return nil, errToolNotFound
	}

	// Tenant-level filter: reject tools hidden from this tenant.
	// Done in the same code path as the lookup to avoid TOCTOU races.
	if f := itemFilterFromCtx(ctx); !f.AllowTool(tool) {
		return nil, errToolNotFound
	}

	// Attach tool's required roles so RBAC middleware can inspect them.
	if len(tool.Roles) > 0 {
		ctx = withToolRoles(ctx, tool.Roles)
	}

	// Attach tool's schema so validation middleware can access it.
	if tool.InputSchema != nil {
		ctx = withToolSchema(ctx, tool.InputSchema)
	}

	// Attach skip-validation flag if the tool opts out.
	if tool.SkipValidation {
		ctx = withSkipValidation(ctx, true)
	}

	// Attach simulator so Simulation middleware can access it.
	if tool.Simulator != nil {
		ctx = withToolSimulator(ctx, tool.Simulator)
	}

	handler := s.buildChain(tool.Handler)

	output, err := handler(ctx, input)
	if err != nil {
		return NewErrorResult(err.Error()), nil
	}

	return NewTextResult(string(output)), nil
}

// normalizeRequestID converts a JSON-RPC request ID to a canonical, injective
// string key. Type prefixes ("s:" for strings, "n:" for numbers) prevent
// collisions between, e.g., string "123" and number 123.
// Supports string, float64, and json.Number. Returns ok == false for nil
// or any other unsupported type.
func normalizeRequestID(id any) (string, bool) {
	switch v := id.(type) {
	case string:
		return "s:" + v, true
	case json.Number:
		return "n:" + v.String(), true
	case float64:
		return fmt.Sprintf("n:%v", v), true
	default:
		return "", false
	}
}

// trackRequest registers a cancellation function for an in-flight request.
func (s *Server) trackRequest(id any, cancel context.CancelFunc) {
	key, ok := normalizeRequestID(id)
	if !ok {
		return
	}

	s.requestsMu.Lock()
	s.requests[key] = cancel
	s.requestsMu.Unlock()
}

// untrackRequest removes a cancellation function without cancelling it.
func (s *Server) untrackRequest(id any) {
	key, ok := normalizeRequestID(id)
	if !ok {
		return
	}

	s.requestsMu.Lock()
	delete(s.requests, key)
	s.requestsMu.Unlock()
}

// cancelRequest looks up a request by ID and invokes its cancellation function.
func (s *Server) cancelRequest(id any) {
	key, ok := normalizeRequestID(id)
	if !ok {
		return
	}

	s.requestsMu.Lock()
	cancel, found := s.requests[key]
	if found {
		delete(s.requests, key)
	}
	s.requestsMu.Unlock()

	if found && cancel != nil {
		cancel()
	}
}

// ── Resource registration ───────────────────────────────────────────

// RegisterResource adds a resource to the server's registry.
// Returns an error if the resource is nil, has missing required fields,
// or a resource with the same URI already exists.
func (s *Server) RegisterResource(r *Resource) error {
	if r == nil {
		return errResourceNil
	}
	if r.URI == "" {
		return errResourceURIEmpty
	}
	if r.Name == "" {
		return errResourceNameEmpty
	}
	if r.Handler == nil {
		return errResourceHandlerNil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.resources[r.URI]; exists {
		return errResourceAlreadyExists
	}

	s.resources[r.URI] = r
	s.sortedResources = nil // invalidate cache
	return nil
}

// RegisterResourceTemplate adds a resource template to the server's registry.
// Returns an error if the template is nil, has a malformed URI template, or a
// template with the same URI already exists.
func (s *Server) RegisterResourceTemplate(t *ResourceTemplate) error {
	if t == nil {
		return errTemplateNil
	}
	if t.URITemplate == "" {
		return errTemplateURIEmpty
	}
	if t.Name == "" {
		return errTemplateNameEmpty
	}
	if t.Handler == nil {
		return errTemplateHandlerNil
	}
	if !isValidURITemplate(t.URITemplate) {
		return errTemplateInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.templates[t.URITemplate]; exists {
		return errTemplateAlreadyExists
	}

	// Ensure parsedSegs is populated (may be nil if the template was
	// created via direct struct literal rather than NewResourceTemplate).
	if t.parsedSegs == nil {
		t.parsedSegs = parseTemplate(t.URITemplate)
	}

	s.templates[t.URITemplate] = t
	s.sortedTemplates = nil // invalidate cache
	return nil
}

// ListResources returns a snapshot of all registered resources, sorted by URI.
// Sorting ensures deterministic ordering for cursor-based pagination.
func (s *Server) ListResources() []*Resource {
	sorted := s.getSortedResources()
	out := make([]*Resource, len(sorted))
	copy(out, sorted)
	return out
}

// getSortedResources returns the cached sorted resource slice, building it on
// demand if it has been invalidated. Uses a read-lock fast path to avoid
// write-lock contention on the common (cache-warm) path.
func (s *Server) getSortedResources() []*Resource {
	s.mu.RLock()
	cached := s.sortedResources
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sortedResources != nil {
		return s.sortedResources
	}
	result := make([]*Resource, 0, len(s.resources))
	for _, r := range s.resources {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].URI < result[j].URI
	})
	s.sortedResources = result
	return result
}

// ListResourceTemplates returns a snapshot of all registered resource templates,
// sorted by URI template. Sorting ensures deterministic ordering for cursor-based pagination.
func (s *Server) ListResourceTemplates() []*ResourceTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sortedTemplates != nil {
		// Return a copy of the cached slice so callers cannot mutate it.
		out := make([]*ResourceTemplate, len(s.sortedTemplates))
		copy(out, s.sortedTemplates)
		return out
	}

	result := make([]*ResourceTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].URITemplate < result[j].URITemplate
	})

	// Upgrade to write lock to cache.  We must re-check because another
	// goroutine may have populated the cache between the RUnlock and Lock.
	// For simplicity we populate the cache lazily on the next write-lock
	// opportunity (RegisterResourceTemplate already holds a write lock);
	// here we just return the freshly sorted slice without caching under
	// the read lock to avoid a lock upgrade.
	return result
}

// findTemplateForURI returns the first registered ResourceTemplate whose
// URITemplate matches the given concrete URI using RFC 6570-style matching.
//
// Templates are evaluated in lexicographic URITemplate order so the result
// is deterministic. When multiple templates could match the same URI, the
// one with the smallest URITemplate string (alphabetically) wins. Users
// should avoid registering overlapping templates; the matching order is
// not configurable.
func (s *Server) findTemplateForURI(uri string) *ResourceTemplate {
	sorted := s.getSortedTemplates()
	for _, tmpl := range sorted {
		if matchesParsedTemplate(tmpl.parsedSegs, uri) {
			return tmpl
		}
	}
	return nil
}

// getSortedTemplates returns the cached sorted template slice, building it
// on demand if it has been invalidated. Uses a read-lock fast path to avoid
// write-lock contention on the common (cache-warm) path.
func (s *Server) getSortedTemplates() []*ResourceTemplate {
	s.mu.RLock()
	cached := s.sortedTemplates
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check after acquiring write lock.
	if s.sortedTemplates != nil {
		return s.sortedTemplates
	}
	result := make([]*ResourceTemplate, 0, len(s.templates))
	for _, t := range s.templates {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].URITemplate < result[j].URITemplate
	})
	s.sortedTemplates = result
	return result
}

// ReadResource looks up a resource by URI and invokes its handler.
// Returns errResourceNotFound if no resource or matching template with the
// given URI is registered, or if a TenantResolver is active and the item is
// filtered out for the current tenant (indistinguishable from not found).
func (s *Server) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	f := itemFilterFromCtx(ctx)

	// Prefer an exact resource match when one exists.
	s.mu.RLock()
	resource := s.resources[uri]
	s.mu.RUnlock()

	if resource != nil {
		// Tenant-level filter for concrete resource.
		if !f.AllowResource(resource) {
			return nil, errResourceNotFound
		}
		contents, err := resource.Handler(ctx, uri)
		if err != nil {
			return nil, err
		}
		return &ReadResourceResult{Contents: contents}, nil
	}

	// Fall back to matching registered URI templates (RFC 6570-style).
	tmpl := s.findTemplateForURI(uri)
	if tmpl == nil {
		return nil, errResourceNotFound
	}

	// Tenant-level filter for template-matched resource.
	if !f.AllowResourceTemplate(tmpl) {
		return nil, errResourceNotFound
	}

	contents, err := tmpl.Handler(ctx, uri)
	if err != nil {
		return nil, err
	}

	return &ReadResourceResult{Contents: contents}, nil
}

// ── Prompt registration ─────────────────────────────────────────────

// RegisterPrompt adds a prompt to the server's registry.
// Returns an error if the prompt is nil, has missing required fields,
// or a prompt with the same name already exists.
func (s *Server) RegisterPrompt(p *Prompt) error {
	if p == nil {
		return errPromptNil
	}
	if p.Name == "" {
		return errPromptNameEmpty
	}
	if p.Handler == nil {
		return errPromptHandlerNil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.prompts[p.Name]; exists {
		return errPromptAlreadyExists
	}

	s.prompts[p.Name] = p
	s.sortedPrompts = nil // invalidate cache
	return nil
}

// ListPrompts returns a snapshot of all registered prompts, sorted by name.
// Sorting ensures deterministic ordering for cursor-based pagination.
func (s *Server) ListPrompts() []*Prompt {
	sorted := s.getSortedPrompts()
	out := make([]*Prompt, len(sorted))
	copy(out, sorted)
	return out
}

// getSortedPrompts returns the cached sorted prompt slice, building it on
// demand if it has been invalidated. Uses a read-lock fast path to avoid
// write-lock contention on the common (cache-warm) path.
func (s *Server) getSortedPrompts() []*Prompt {
	s.mu.RLock()
	cached := s.sortedPrompts
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sortedPrompts != nil {
		return s.sortedPrompts
	}
	result := make([]*Prompt, 0, len(s.prompts))
	for _, p := range s.prompts {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	s.sortedPrompts = result
	return result
}

// GetPrompt looks up a prompt by name and invokes its handler.
// Returns errPromptNotFound if no prompt with the given name is registered
// or if a TenantResolver is active and the prompt is filtered out for the
// current tenant (indistinguishable from not found).
//
// Security note: The maxPromptMessages limit (1000) is enforced AFTER the
// handler returns. Prompt handlers are trusted code and must not allocate
// excessive resources. This limit protects against accidental bugs in handlers,
// not malicious code. Handlers with prompt registration capability are part of
// the server's trusted computing base.
func (s *Server) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	s.mu.RLock()
	prompt, ok := s.prompts[name]
	s.mu.RUnlock()

	if !ok {
		return nil, errPromptNotFound
	}

	// Tenant-level filter: reject prompts hidden from this tenant.
	if f := itemFilterFromCtx(ctx); !f.AllowPrompt(prompt) {
		return nil, errPromptNotFound
	}

	messages, err := prompt.Handler(ctx, args)
	if err != nil {
		return nil, err
	}

	// Enforce maximum message count to prevent memory exhaustion from
	// malicious or buggy prompt handlers.
	const maxPromptMessages = 1000
	if len(messages) > maxPromptMessages {
		return nil, fmt.Errorf("prompt returned too many messages (max %d)", maxPromptMessages)
	}

	// Normalize nil to empty slice so JSON marshals as [] not null.
	if messages == nil {
		messages = []PromptMessage{}
	}

	return &GetPromptResult{
		Description: prompt.Description,
		Messages:    messages,
	}, nil
}

// ── Broadcasters & Subscriptions ────────────────────────────────────

// AddSender registers a connection's NotificationSender for broadcasting list-changed events.
// It returns an error if id is empty, sender is nil, or a sender is already registered under id.
func (s *Server) AddSender(id string, sender NotificationSender) error {
	if id == "" {
		return errors.New("finemcp: AddSender requires a non-empty connection ID")
	}
	if sender == nil {
		return errors.New("finemcp: AddSender requires a non-nil NotificationSender")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.senders[id]; exists {
		return errSenderAlreadyExists
	}
	s.senders[id] = sender
	return nil
}

// RemoveSender removes a connection's NotificationSender.
func (s *Server) RemoveSender(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.senders, id)
}

// Subscribe adds a client's subscription to a specific resource URI.
// It returns [errSubscriptionsDisabled] if the server was not configured with [WithResourceSubscriptions].
func (s *Server) Subscribe(subscriberID, uri string, sender NotificationSender) error {
	if !s.enableSubs {
		return errSubscriptionsDisabled
	}
	if subscriberID == "" {
		return errors.New("finemcp: Subscribe requires a non-empty subscriberID")
	}
	if sender == nil {
		return errors.New("finemcp: Subscribe requires a non-nil sender")
	}
	if uri == "" {
		return errResourceURIEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriptions[subscriptionKey{subscriberID, uri}] = sender
	return nil
}

// Unsubscribe removes a client's subscription to a specific resource URI.
func (s *Server) Unsubscribe(subscriberID, uri string) {
	if subscriberID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscriptions, subscriptionKey{subscriberID, uri})
}

// UnsubscribeAll removes all subscriptions for a specific client connection.
// This should be called by transports when a client disconnects.
func (s *Server) UnsubscribeAll(subscriberID string) {
	if subscriberID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.subscriptions {
		if k.subscriberID == subscriberID {
			delete(s.subscriptions, k)
		}
	}
}

// NotifyResourceUpdated notifies all subscribed clients that a resource has changed.
func (s *Server) NotifyResourceUpdated(uri string) {
	msg := &JSONRPCNotification{
		JSONRPC: jsonrpcVersion,
		Method:  methodResourcesUpdated,
		Params:  ResourceUpdatedParams{URI: uri},
	}

	s.mu.RLock()
	var senders []NotificationSender
	for k, sender := range s.subscriptions {
		if k.uri == uri {
			senders = append(senders, sender)
		}
	}
	s.mu.RUnlock()

	for _, sender := range senders {
		notif := *msg // send each subscriber an independent copy to prevent cross-subscriber mutation
		sender(&notif)
	}
}

// broadcastListChanged is a helper to fan-out a list_changed notification to all connected clients.
func (s *Server) broadcastListChanged(method string) {
	msg := &JSONRPCNotification{
		JSONRPC: jsonrpcVersion,
		Method:  method,
	}

	s.mu.RLock()
	var senders []NotificationSender
	for _, sender := range s.senders {
		senders = append(senders, sender)
	}
	s.mu.RUnlock()

	for _, sender := range senders {
		notif := *msg // send each client an independent copy to prevent cross-client mutation
		sender(&notif)
	}
}

// NotifyToolsListChanged broadcasts notifications/tools/list_changed to all connected clients.
func (s *Server) NotifyToolsListChanged() {
	s.broadcastListChanged(methodToolsListChanged)
}

// NotifyResourcesListChanged broadcasts notifications/resources/list_changed to all connected clients.
func (s *Server) NotifyResourcesListChanged() {
	s.broadcastListChanged(methodResourcesListChanged)
}

// NotifyPromptsListChanged broadcasts notifications/prompts/list_changed to all connected clients.
func (s *Server) NotifyPromptsListChanged() {
	s.broadcastListChanged(methodPromptsListChanged)
}

// NotifyRootsListChanged broadcasts notifications/roots/list_changed to all connected clients.
func (s *Server) NotifyRootsListChanged() {
	s.broadcastListChanged(methodRootsListChanged)
}
