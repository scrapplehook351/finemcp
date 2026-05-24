package transport

import (
	"net/http"
	"strings"
	"sync"
)

// HostRouter routes incoming HTTP requests to different handlers based on the
// Host header. This enables multi-tenant MCP deployments where each tenant
// (or service) has its own hostname pointing to the same server process.
//
// Usage:
//
//	apiServer := finemcp.NewServer("api", "1.0")
//	adminServer := finemcp.NewServer("admin", "1.0")
//
//	router := transport.NewHostRouter()
//	router.Handle("api.example.com", transport.StreamableHandler(apiServer))
//	router.Handle("admin.example.com", transport.StreamableHandler(adminServer))
//
//	log.Fatal(http.ListenAndServe(":8080", router))
//
// Hosts are matched case-insensitively. Port numbers in the Host header are
// stripped before matching (so "api.example.com:8080" matches a rule for
// "api.example.com").
//
// Wildcard subdomains are supported with a "*." prefix:
//
//	router.Handle("*.example.com", tenantHandler) // matches foo.example.com, bar.example.com
//
// If no host matches and no fallback is configured, the router returns
// 421 Misdirected Request.
type HostRouter struct {
	mu           sync.RWMutex
	exact        map[string]http.Handler // lowercase host -> handler
	wildcards    []wildcardEntry         // ordered list of wildcard rules
	fallback     http.Handler
	maxHosts     int // max number of registered hosts; 0 = default (1000)
	notFoundCode int // HTTP status for unmatched hosts; 0 = 421
}

// wildcardEntry stores a wildcard pattern and its handler.
type wildcardEntry struct {
	suffix  string // e.g. ".example.com" (the part after "*")
	handler http.Handler
}

const (
	defaultMaxHosts     = 1000
	defaultNotFoundCode = http.StatusMisdirectedRequest // 421
)

// NewHostRouter creates a new host-based router.
func NewHostRouter() *HostRouter {
	return &HostRouter{
		exact: make(map[string]http.Handler),
	}
}

// HostRouterOption configures a HostRouter.
type HostRouterOption func(*HostRouter)

// WithFallback sets a fallback handler for requests that don't match any
// registered host. Without a fallback, unmatched requests receive 421.
func WithFallback(h http.Handler) HostRouterOption {
	return func(r *HostRouter) { r.fallback = h }
}

// WithMaxHosts sets the maximum number of host rules that can be registered.
// Panics on Handle if the limit is exceeded. Default: 1000.
func WithMaxHosts(n int) HostRouterOption {
	if n <= 0 {
		panic("transport: WithMaxHosts requires a positive value")
	}
	return func(r *HostRouter) { r.maxHosts = n }
}

// WithNotFoundStatus sets the HTTP status code returned for unmatched hosts.
// Default: 421 Misdirected Request.
func WithNotFoundStatus(code int) HostRouterOption {
	return func(r *HostRouter) { r.notFoundCode = code }
}

// NewHostRouterWithOptions creates a new host-based router with options.
func NewHostRouterWithOptions(opts ...HostRouterOption) *HostRouter {
	r := NewHostRouter()
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Handle registers a handler for the given host pattern. The host is
// case-insensitive. Use "*." prefix for wildcard subdomain matching.
//
// Examples:
//
//	router.Handle("api.example.com", apiHandler)     // exact match
//	router.Handle("*.example.com", tenantHandler)    // wildcard subdomain
//
// Panics if host is empty, handler is nil, or the host is already registered.
func (r *HostRouter) Handle(host string, handler http.Handler) {
	if host == "" {
		panic("transport: HostRouter.Handle requires a non-empty host")
	}
	if handler == nil {
		panic("transport: HostRouter.Handle requires a non-nil handler")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	maxHosts := r.maxHosts
	if maxHosts == 0 {
		maxHosts = defaultMaxHosts
	}

	totalHosts := len(r.exact) + len(r.wildcards)
	if totalHosts >= maxHosts {
		panic("transport: HostRouter host limit exceeded")
	}

	normalized := strings.ToLower(host)

	if strings.HasPrefix(normalized, "*.") {
		suffix := normalized[1:] // e.g. ".example.com"
		if !strings.Contains(suffix[1:], ".") {
			panic("transport: HostRouter.Handle: wildcard host must have a domain (e.g. *.example.com)")
		}
		for _, w := range r.wildcards {
			if w.suffix == suffix {
				panic("transport: HostRouter.Handle: duplicate wildcard host: " + host)
			}
		}
		r.wildcards = append(r.wildcards, wildcardEntry{
			suffix:  suffix,
			handler: handler,
		})
		return
	}

	if _, exists := r.exact[normalized]; exists {
		panic("transport: HostRouter.Handle: duplicate host: " + host)
	}
	r.exact[normalized] = handler
}

// Remove unregisters the handler for the given host pattern. Returns true if
// the host was found and removed.
//
// Note: handlers already executing when Remove is called will run to
// completion. This follows the same pattern as Go's http.Server.
func (r *HostRouter) Remove(host string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	normalized := strings.ToLower(host)

	if strings.HasPrefix(normalized, "*.") {
		suffix := normalized[1:]
		for i, w := range r.wildcards {
			if w.suffix == suffix {
				r.wildcards = append(r.wildcards[:i], r.wildcards[i+1:]...)
				return true
			}
		}
		return false
	}

	if _, exists := r.exact[normalized]; exists {
		delete(r.exact, normalized)
		return true
	}
	return false
}

// ServeHTTP routes the request to the handler registered for the request's
// Host header. The read lock is held only during route lookup, not during
// handler execution, so handlers may run concurrently with route changes.
func (r *HostRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := stripPort(strings.ToLower(req.Host))

	r.mu.RLock()
	handler, ok := r.exact[host]
	if !ok {
		handler, ok = r.matchWildcard(host)
	}
	r.mu.RUnlock()

	if ok {
		handler.ServeHTTP(w, req)
		return
	}

	if r.fallback != nil {
		r.fallback.ServeHTTP(w, req)
		return
	}

	code := r.notFoundCode
	if code == 0 {
		code = defaultNotFoundCode
	}
	http.Error(w, "no handler for host", code)
}

// matchWildcard checks wildcard rules against the given host.
// Must be called with r.mu held (at least for reading).
func (r *HostRouter) matchWildcard(host string) (http.Handler, bool) {
	for _, w := range r.wildcards {
		// w.suffix always starts with "." (e.g. ".example.com"), which
		// naturally prevents boundary confusion: "evilexample.com" does
		// NOT end with ".example.com", only "foo.example.com" does.
		if !strings.HasSuffix(host, w.suffix) {
			continue
		}
		// Extract the subdomain label that matched the "*".
		prefix := host[:len(host)-len(w.suffix)]
		// Require exactly one label (no dots) so that *.example.com
		// matches "foo.example.com" but NOT "bar.foo.example.com".
		if len(prefix) > 0 && !strings.Contains(prefix, ".") {
			return w.handler, true
		}
	}
	return nil, false
}

// stripPort removes the port from a host string. If there is no port,
// the host is returned as-is.
func stripPort(host string) string {
	// IPv6: [::1]:8080
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]:"); i != -1 {
			return host[:i+1]
		}
		return host // bracketed IPv6 without port, or malformed
	}
	// Regular: example.com:8080 (exactly one colon means host:port)
	if i := strings.LastIndex(host, ":"); i != -1 && strings.Count(host, ":") == 1 {
		return host[:i]
	}
	return host
}
