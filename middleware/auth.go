// Package middleware provides tool-level and transport-level middleware for
// finemcp MCP servers.
//
// # Authentication
//
// Authentication is implemented as a three-layer defense-in-depth architecture:
//
//  1. HTTP middleware ([HTTPAuth]) validates credentials at the transport level.
//     It extracts Bearer tokens or API keys from HTTP headers/query params,
//     validates them via a [TokenVerifier], and injects the verified [finemcp.AuthInfo]
//     into the request context. Invalid or missing credentials receive HTTP 401.
//
//  2. Protocol-level checker ([finemcp.AuthChecker] via [RequireAuth] or
//     [RequireAuthWithRoles]) runs inside the JSON-RPC dispatch loop for every
//     request (except initialize and ping). It inspects the context for a verified
//     identity and rejects unauthenticated requests with a proper JSON-RPC error
//     (code -32001). This ensures that even if a transport bypasses the HTTP
//     middleware, no unauthenticated request reaches a method handler.
//
//  3. Tool-level RBAC (the existing [RBAC] middleware) applies fine-grained,
//     per-tool role requirements. It reads roles from the context set by the
//     auth layer.
//
// Typical setup:
//
//	verifier := middleware.StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
//	    "secret": {Subject: "svc-a", Roles: []string{"admin"}},
//	})
//	server.SetAuthChecker(middleware.RequireAuth())
//	handler := middleware.HTTPAuth(verifier, transport.Handler(server))
//	http.ListenAndServe(":8080", handler)
package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/finemcp/finemcp"
)

// AuthScheme identifies the type of credential presented by the caller.
type AuthScheme string

const (
	// SchemeBearerToken is the Authorization: Bearer <token> scheme.
	SchemeBearerToken AuthScheme = "Bearer"

	// SchemeAPIKey is the X-API-Key header (or query parameter) scheme.
	SchemeAPIKey AuthScheme = "APIKey"
)

var (
	// ErrInvalidCredentials is returned when credentials are syntactically
	// valid but do not match any known identity.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrMissingCredentials is returned when no credentials are present
	// and anonymous access is not allowed.
	ErrMissingCredentials = errors.New("missing credentials")

	// ErrAuthenticationFailed is a generic authentication failure used when
	// a TokenVerifier panics to avoid leaking internal details.
	ErrAuthenticationFailed = errors.New("authentication failed")
)

// TokenVerifier validates a credential and returns the verified caller identity.
//
// Implementations must:
//   - Use constant-time comparison for static secrets to prevent timing attacks.
//   - Never include the raw token in error messages (it may appear in logs).
//   - Return a fresh AuthInfo per call; the framework defensively copies Roles
//     and Meta, but verifiers should not rely on this.
//   - Respect context cancellation by checking ctx.Done() for long-running
//     verifications (e.g. database or LDAP lookups).
//
// The scheme parameter indicates whether the credential was presented as a
// Bearer token or an API key, allowing a single verifier to handle both.
//
// Note on AuthInfo.Meta: the framework performs a shallow copy of the Meta map.
// If Meta values contain pointers, slices, or nested maps, the verifier should
// return freshly-allocated values to prevent shared mutable state.
type TokenVerifier interface {
	Verify(ctx context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error)
}

// TokenVerifierFunc adapts a plain function to the TokenVerifier interface.
type TokenVerifierFunc func(ctx context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error)

// Verify calls f(ctx, scheme, token).
func (f TokenVerifierFunc) Verify(ctx context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error) {
	return f(ctx, scheme, token)
}

// ── Static verifiers ────────────────────────────────────────────────

// staticVerifier validates credentials against a fixed set of keys using
// constant-time comparison. Thread-safe (read-only after construction).
type staticVerifier struct {
	scheme AuthScheme
	keys   []string           // ordered key list for constant-time iteration
	infos  []finemcp.AuthInfo // parallel to keys
}

// Verify checks the token against all stored keys using constant-time
// comparison to prevent timing side-channel attacks.
//
// All keys are always checked (no early return) so the response time does not
// reveal which key index matched or how many keys exist.
//
// Note: crypto/subtle.ConstantTimeCompare returns 0 immediately for
// different-length inputs; this is inherent to the Go stdlib and typically
// acceptable for fixed-length API keys / bearer tokens.
func (v *staticVerifier) Verify(_ context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error) {
	if scheme != v.scheme {
		return finemcp.AuthInfo{}, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidCredentials, scheme)
	}

	tokenBytes := []byte(token)
	matchIdx := -1
	for i, key := range v.keys {
		if subtle.ConstantTimeCompare(tokenBytes, []byte(key)) == 1 {
			matchIdx = i
		}
	}
	if matchIdx >= 0 {
		return v.infos[matchIdx], nil
	}
	return finemcp.AuthInfo{}, ErrInvalidCredentials
}

// StaticAPIKeyVerifier returns a TokenVerifier that validates API keys against
// a fixed map. Keys are compared using crypto/subtle.ConstantTimeCompare to
// prevent timing side-channel attacks.
//
// The map is copied at construction time; subsequent mutations have no effect.
// Thread-safe for concurrent use.
//
// Usage:
//
//	verifier := middleware.StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
//	    "my-api-key": {Subject: "service-a", Roles: []string{"admin"}},
//	})
func StaticAPIKeyVerifier(keys map[string]finemcp.AuthInfo) TokenVerifier {
	return newStaticVerifier(SchemeAPIKey, keys)
}

// StaticBearerTokenVerifier returns a TokenVerifier that validates bearer
// tokens against a fixed map. Tokens are compared using
// crypto/subtle.ConstantTimeCompare to prevent timing side-channel attacks.
//
// The map is copied at construction time; subsequent mutations have no effect.
// Thread-safe for concurrent use.
//
// Usage:
//
//	verifier := middleware.StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
//	    "secret-token": {Subject: "user-1", Roles: []string{"reader"}},
//	})
func StaticBearerTokenVerifier(tokens map[string]finemcp.AuthInfo) TokenVerifier {
	return newStaticVerifier(SchemeBearerToken, tokens)
}

func newStaticVerifier(scheme AuthScheme, m map[string]finemcp.AuthInfo) *staticVerifier {
	v := &staticVerifier{
		scheme: scheme,
		keys:   make([]string, 0, len(m)),
		infos:  make([]finemcp.AuthInfo, 0, len(m)),
	}
	for k, info := range m {
		v.keys = append(v.keys, k)
		// Deep copy the AuthInfo so the caller's map doesn't share state.
		cp := finemcp.AuthInfo{
			Subject: info.Subject,
		}
		if info.Roles != nil {
			cp.Roles = make([]string, len(info.Roles))
			copy(cp.Roles, info.Roles)
		}
		if info.Meta != nil {
			cp.Meta = make(map[string]any, len(info.Meta))
			for mk, mv := range info.Meta {
				cp.Meta[mk] = mv
			}
		}
		v.infos = append(v.infos, cp)
	}
	return v
}

// ── Protocol-level auth checkers ────────────────────────────────────

// RequireAuth returns an AuthChecker that rejects requests without a verified
// identity in the context. Use with Server.SetAuthChecker for protocol-level
// authentication enforcement.
//
// Usage:
//
//	server.SetAuthChecker(middleware.RequireAuth())
func RequireAuth() finemcp.AuthChecker {
	return func(ctx context.Context) error {
		info := finemcp.AuthInfoFromCtx(ctx)
		if info == nil {
			return ErrMissingCredentials
		}
		if info.Subject == "" {
			return errors.New("invalid authentication: empty subject")
		}
		return nil
	}
}

// RequireAuthWithRoles returns an AuthChecker that rejects requests without
// a verified identity or without at least one of the specified roles. This
// provides a global minimum-role requirement on top of per-tool RBAC.
//
// If no roles are specified (empty variadic), the checker behaves identically
// to [RequireAuth] — it only requires a verified identity with a non-empty
// Subject.
//
// Usage:
//
//	server.SetAuthChecker(middleware.RequireAuthWithRoles("admin", "operator"))
func RequireAuthWithRoles(required ...string) finemcp.AuthChecker {
	return func(ctx context.Context) error {
		info := finemcp.AuthInfoFromCtx(ctx)
		if info == nil {
			return ErrMissingCredentials
		}
		if info.Subject == "" {
			return errors.New("invalid authentication: empty subject")
		}
		// No specific roles required — any authenticated caller passes.
		if len(required) == 0 {
			return nil
		}
		for _, r := range required {
			for _, c := range info.Roles {
				if c == r {
					return nil
				}
			}
		}
		return fmt.Errorf("forbidden: insufficient roles")
	}
}

// ── Verifier composition ────────────────────────────────────────────

// ChainVerifiers returns a TokenVerifier that tries each verifier in order.
// The first verifier to return a non-error result wins. If all verifiers
// return errors, the error from the last verifier is returned.
//
// This is useful when a server accepts multiple credential sources — for
// example, both static API keys for service-to-service calls and a JWT
// verifier for human users:
//
//	verifier := middleware.ChainVerifiers(
//	    middleware.StaticAPIKeyVerifier(serviceKeys),
//	    jwtVerifier,
//	)
//	handler := middleware.HTTPAuth(verifier, transport.Handler(server))
//
// Panics if no verifiers are provided.
func ChainVerifiers(verifiers ...TokenVerifier) TokenVerifier {
	if len(verifiers) == 0 {
		panic("middleware: ChainVerifiers requires at least one verifier")
	}
	// Fast path: single verifier needs no wrapping.
	if len(verifiers) == 1 {
		return verifiers[0]
	}
	// Defensive copy to prevent caller mutation.
	chain := make([]TokenVerifier, len(verifiers))
	copy(chain, verifiers)
	return TokenVerifierFunc(func(ctx context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error) {
		var lastErr error
		for _, v := range chain {
			info, err := v.Verify(ctx, scheme, token)
			if err == nil {
				return info, nil
			}
			lastErr = err
		}
		return finemcp.AuthInfo{}, lastErr
	})
}
