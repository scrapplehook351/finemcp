package transport

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CORSOptions configures Cross-Origin Resource Sharing (CORS) behavior.
//
// This is essential for browser-based MCP clients that connect to HTTP-based
// MCP servers from a different origin. Without CORS headers, browsers block
// cross-origin requests.
type CORSOptions struct {
	// AllowedOrigins lists the origins permitted to access the resource.
	// Use []string{"*"} to allow any origin (not recommended with credentials).
	// When empty, no Access-Control-Allow-Origin header is set.
	AllowedOrigins []string

	// AllowedMethods lists the HTTP methods allowed for cross-origin requests.
	// Defaults to POST, GET, DELETE, OPTIONS if empty.
	AllowedMethods []string

	// AllowedHeaders lists the HTTP headers the client is allowed to send.
	// Defaults to Content-Type, Accept, Mcp-Session-Id, Last-Event-ID,
	// Mcp-Protocol-Version if empty.
	AllowedHeaders []string

	// ExposedHeaders lists the response headers the browser is allowed to access.
	// Defaults to Mcp-Session-Id if empty.
	ExposedHeaders []string

	// MaxAge indicates how long (in seconds) the preflight response can be
	// cached. Defaults to 86400 (24 hours) if zero.
	MaxAge int

	// AllowCredentials indicates whether the response to the request can be
	// exposed when the credentials flag is true. When true, AllowedOrigins
	// must not contain "*".
	AllowCredentials bool
}

// defaultAllowedMethods are the HTTP methods used by the MCP Streamable HTTP transport.
var defaultAllowedMethods = []string{"POST", "GET", "DELETE", "OPTIONS"}

// defaultAllowedHeaders are the headers MCP clients typically send.
var defaultAllowedHeaders = []string{
	"Content-Type",
	"Accept",
	"Mcp-Session-Id",
	"Last-Event-ID",
	"Mcp-Protocol-Version",
}

// defaultExposedHeaders are the response headers MCP clients need to read.
var defaultExposedHeaders = []string{"Mcp-Session-Id"}

const defaultMaxAge = 86400 // 24 hours

// CORS wraps an http.Handler with CORS support. It handles preflight OPTIONS
// requests and sets the appropriate Access-Control-* response headers on all
// requests.
//
// Usage:
//
//	handler := transport.StreamableHandler(server)
//	corsHandler := transport.CORS(handler, transport.CORSOptions{
//	    AllowedOrigins: []string{"https://example.com"},
//	})
//	http.ListenAndServe(":8080", corsHandler)
//
// For the simple HTTP handler:
//
//	handler := transport.Handler(server)
//	corsHandler := transport.CORS(handler, transport.CORSOptions{
//	    AllowedOrigins: []string{"*"},
//	})
func CORS(handler http.Handler, opts CORSOptions) http.Handler {
	if len(opts.AllowedOrigins) == 0 {
		panic("transport: CORSOptions.AllowedOrigins must not be empty")
	}

	allowWildcard := len(opts.AllowedOrigins) == 1 && opts.AllowedOrigins[0] == "*"
	if allowWildcard && opts.AllowCredentials {
		panic("transport: CORSOptions.AllowCredentials must not be true when AllowedOrigins is wildcard (\"*\")")
	}

	methods := opts.AllowedMethods
	if len(methods) == 0 {
		methods = defaultAllowedMethods
	}
	headers := opts.AllowedHeaders
	if len(headers) == 0 {
		headers = defaultAllowedHeaders
	}
	exposed := opts.ExposedHeaders
	if len(exposed) == 0 {
		exposed = defaultExposedHeaders
	}
	maxAge := opts.MaxAge
	if maxAge < 0 {
		maxAge = 0
	}
	if maxAge == 0 {
		maxAge = defaultMaxAge
	}

	// Defensive copy of AllowedOrigins to prevent caller mutation.
	originSet := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, o := range opts.AllowedOrigins {
		originSet[o] = struct{}{}
	}

	methodsStr := strings.Join(methods, ", ")
	headersStr := strings.Join(headers, ", ")
	exposedStr := strings.Join(exposed, ", ")
	maxAgeStr := strconv.Itoa(maxAge)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Non-browser request — no CORS headers needed.
			handler.ServeHTTP(w, r)
			return
		}

		// Reject origins with control characters to prevent header injection.
		if !isValidOrigin(origin) {
			http.Error(w, "invalid origin", http.StatusBadRequest)
			return
		}

		// Check if origin is allowed.
		allowed := allowWildcard
		if !allowed {
			_, allowed = originSet[origin]
		}
		if !allowed {
			handler.ServeHTTP(w, r)
			return
		}

		// Set the allowed origin (echo back the specific origin, or "*").
		if allowWildcard && !opts.AllowCredentials {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		if opts.AllowCredentials {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if exposedStr != "" {
			w.Header().Set("Access-Control-Expose-Headers", exposedStr)
		}

		// Handle preflight.
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", methodsStr)
			w.Header().Set("Access-Control-Allow-Headers", headersStr)
			w.Header().Set("Access-Control-Max-Age", maxAgeStr)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		handler.ServeHTTP(w, r)
	})
}

// WithStreamableCORS adds CORS support to the Streamable HTTP handler.
// This is a convenience that applies the CORS middleware internally; it is
// equivalent to wrapping the handler with CORS() but keeps configuration
// co-located with other streamable options.
func WithStreamableCORS(opts CORSOptions) StreamableOption {
	return func(h *streamableHandler) {
		h.corsOpts = &opts
	}
}

// isValidOrigin checks that the origin header contains only valid characters
// and no control characters that could be used for header injection.
func isValidOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	for i := 0; i < len(origin); i++ {
		c := origin[i]
		if c < 0x20 || c == 0x7f {
			return false // control characters
		}
	}
	return utf8.ValidString(origin)
}
