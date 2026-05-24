package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/finemcp/finemcp"
)

// ── HTTP Auth Middleware ────────────────────────────────────────────

// AuthErrorHandler is called when authentication fails at the HTTP layer.
// It receives the response writer, request, and the error, and is responsible
// for writing the HTTP response. If nil, the default handler writes a
// JSON-RPC-style error body with HTTP 401.
type AuthErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// HTTPAuthOption configures the HTTP authentication middleware.
type HTTPAuthOption func(*httpAuthConfig)

type httpAuthConfig struct {
	apiKeyHeader string           // header name for API key auth (default: "X-API-Key")
	apiKeyQuery  string           // query param for API key auth (default: disabled)
	allowAnon    bool             // if true, requests without credentials are allowed
	anonIdentity finemcp.AuthInfo // identity for anonymous requests
	errorHandler AuthErrorHandler // custom error handler
}

// WithAnonymousIdentity permits requests with no credentials, assigning the
// provided identity. Subject must be non-empty.
//
// When anonymous access is allowed and no credentials are present, the
// anonymous identity is injected into context. RBAC tools with no required
// roles will be accessible to anonymous callers — configure tool roles
// explicitly to restrict access.
//
// Panics if identity.Subject is empty.
func WithAnonymousIdentity(identity finemcp.AuthInfo) HTTPAuthOption {
	if identity.Subject == "" {
		panic("middleware: WithAnonymousIdentity requires a non-empty Subject")
	}
	return func(c *httpAuthConfig) {
		c.allowAnon = true
		c.anonIdentity = identity
	}
}

// WithAPIKeyHeader sets the HTTP header name used for API key authentication.
// Default: "X-API-Key".
func WithAPIKeyHeader(name string) HTTPAuthOption {
	return func(c *httpAuthConfig) {
		c.apiKeyHeader = http.CanonicalHeaderKey(name)
	}
}

// WithAPIKeyQuery enables API key authentication via a URL query parameter.
// Disabled by default.
//
// WARNING: API keys in query parameters are logged by web servers, proxies,
// and CDNs, and may be cached by HTTP intermediaries. Use header-based
// authentication whenever possible. This option exists for legacy client
// compatibility only.
func WithAPIKeyQuery(param string) HTTPAuthOption {
	return func(c *httpAuthConfig) {
		c.apiKeyQuery = param
	}
}

// WithAuthErrorHandler sets a custom handler for authentication errors.
// The handler is responsible for writing the complete HTTP response.
func WithAuthErrorHandler(fn AuthErrorHandler) HTTPAuthOption {
	return func(c *httpAuthConfig) {
		c.errorHandler = fn
	}
}

// HTTPAuth returns an http.Handler that authenticates all incoming HTTP
// requests before forwarding them to the next handler.
//
// Credential extraction priority:
//  1. Authorization: Bearer <token> header
//  2. X-API-Key header (or custom header via WithAPIKeyHeader)
//  3. API key query parameter (if enabled via WithAPIKeyQuery)
//
// On successful verification, the verified AuthInfo is injected into the
// request context via finemcp.WithAuthInfo. Downstream handlers (including
// MCP transport handlers) see the authenticated identity.
//
// On failure, the handler responds with HTTP 401 and a JSON body describing
// the error. The raw token never appears in the error response.
//
// The verifier is called with a recover guard: if Verify panics, a generic
// "authentication failed" error is returned instead of crashing the server.
//
// Usage:
//
//	verifier := middleware.StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
//	    "my-key": {Subject: "svc", Roles: []string{"admin"}},
//	})
//	handler := middleware.HTTPAuth(verifier, transport.Handler(server))
//	http.ListenAndServe(":8080", handler)
func HTTPAuth(verifier TokenVerifier, next http.Handler, opts ...HTTPAuthOption) http.Handler {
	if verifier == nil {
		panic("middleware: HTTPAuth requires a non-nil TokenVerifier")
	}
	if next == nil {
		panic("middleware: HTTPAuth requires a non-nil next handler")
	}

	cfg := httpAuthConfig{
		apiKeyHeader: "X-Api-Key", // #nosec G101 -- not a credential; this is the default header name for API key extraction
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token := extractCredentials(r, &cfg)

		// No credentials found.
		if token == "" {
			if cfg.allowAnon {
				ctx := finemcp.WithAuthInfo(r.Context(), cfg.anonIdentity)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			writeAuthError(w, r, cfg.errorHandler, ErrMissingCredentials)
			return
		}

		// Reject multiple Authorization headers (ambiguous identity).
		if len(r.Header.Values("Authorization")) > 1 {
			writeAuthError(w, r, cfg.errorHandler, ErrInvalidCredentials)
			return
		}

		// Verify credentials with panic recovery.
		info, err := safeVerify(verifier, r.Context(), scheme, token)
		if err != nil {
			writeAuthError(w, r, cfg.errorHandler, err)
			return
		}

		// Inject the verified identity (NOT the raw token) into context.
		ctx := finemcp.WithAuthInfo(r.Context(), info)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractCredentials reads authentication credentials from the HTTP request.
// It checks (in order): Authorization header, API key header, API key query.
// Returns the scheme and raw token, or empty strings if no credentials found.
func extractCredentials(r *http.Request, cfg *httpAuthConfig) (AuthScheme, string) {
	// 1. Authorization: Bearer <token>
	// RFC 6750: the auth-scheme is case-insensitive ("bearer", "Bearer", "BEARER").
	if auth := r.Header.Get("Authorization"); len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		token := strings.TrimSpace(auth[7:])
		if token != "" {
			return SchemeBearerToken, token
		}
	}

	// 2. API key header (X-API-Key by default).
	if cfg.apiKeyHeader != "" {
		if key := r.Header.Get(cfg.apiKeyHeader); key != "" {
			return SchemeAPIKey, key
		}
	}

	// 3. API key query parameter (if enabled).
	if cfg.apiKeyQuery != "" {
		if key := r.URL.Query().Get(cfg.apiKeyQuery); key != "" {
			return SchemeAPIKey, key
		}
	}

	return "", ""
}

// safeVerify calls verifier.Verify with a deferred recovery to prevent panics
// in custom TokenVerifier implementations from crashing the server.
func safeVerify(v TokenVerifier, ctx context.Context, scheme AuthScheme, token string) (info finemcp.AuthInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			info = finemcp.AuthInfo{}
			err = ErrAuthenticationFailed
		}
	}()
	return v.Verify(ctx, scheme, token)
}

// writeAuthError writes an authentication error response. If a custom error
// handler is configured, it delegates to that handler. Otherwise, it writes
// a JSON error body with HTTP 401.
//
// Error messages are scrubbed: only well-known sentinel error messages are
// forwarded to the client. Arbitrary verifier errors are replaced with a
// generic "authentication failed" message to prevent leaking internal details.
func writeAuthError(w http.ResponseWriter, r *http.Request, handler AuthErrorHandler, err error) {
	if handler != nil {
		handler(w, r, err)
		return
	}

	// RFC 7235: 401 responses SHOULD include WWW-Authenticate.
	w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	// Scrub the error message: only expose known sentinel messages.
	msg := "authentication failed"
	switch {
	case errors.Is(err, ErrMissingCredentials):
		msg = "missing credentials"
	case errors.Is(err, ErrInvalidCredentials):
		msg = "invalid credentials"
	}

	// Write a JSON-RPC-style error so MCP clients can parse it.
	body := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		JSONRPC: "2.0",
		ID:      nil,
	}
	body.Error.Code = finemcp.ErrCodeUnauthorized
	body.Error.Message = msg

	_ = json.NewEncoder(w).Encode(body) // #nosec G104 -- best-effort error response
}
