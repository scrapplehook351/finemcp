---
url: "/docs/middleware/auth/"
title: "Auth"
description: "HTTP-level authentication with bearer tokens and API keys"
weight: 1
---

The auth middleware provides HTTP-level authentication using bearer tokens, API keys, or custom verifiers.

## Architecture

Auth works at two levels:

1. **HTTP layer** — `HTTPAuth` extracts credentials from HTTP headers and attaches `AuthInfo` to the context
2. **Protocol layer** — `RequireAuth` (set via `SetAuthChecker`) rejects unauthenticated requests at the MCP protocol level

## Quick Setup

```go
import "github.com/finemcp/finemcp/middleware"

s := finemcp.NewServer("secure-server", "1.0.0")

// 1. Set protocol-level auth check
s.SetAuthChecker(middleware.RequireAuth())

// 2. Create token verifier
verifier := middleware.ChainVerifiers(
    middleware.StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
        "admin-token": {Subject: "admin", Roles: []string{"admin"}},
        "user-token":  {Subject: "alice", Roles: []string{"user"}},
    }),
    middleware.StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
        "api-key-123": {Subject: "service-a"},
    }),
)

// 3. Wrap the transport handler
handler := middleware.HTTPAuth(verifier, transport.Handler(s))
http.ListenAndServe(":8080", handler)
```

## Token Verifiers

### StaticBearerTokenVerifier

Maps bearer tokens to auth info:

```go
verifier := middleware.StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
    "token-abc": {Subject: "alice", Roles: []string{"admin", "user"}},
})
```

Used with `Authorization: Bearer token-abc` header.

### StaticAPIKeyVerifier

Maps API keys to auth info:

```go
verifier := middleware.StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
    "key-xyz": {Subject: "service", Roles: []string{"service"}},
})
```

Used with `X-API-Key: key-xyz` header (configurable).

### ChainVerifiers

Tries multiple verifiers in order, returning the first successful match:

```go
verifier := middleware.ChainVerifiers(bearerVerifier, apiKeyVerifier)
```

### Custom Verifiers

Implement the `TokenVerifier` interface or use `TokenVerifierFunc`:

```go
type TokenVerifier interface {
    Verify(ctx context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error)
}

// Or as a function:
verifier := middleware.TokenVerifierFunc(func(ctx context.Context, scheme middleware.AuthScheme, token string) (finemcp.AuthInfo, error) {
    // Custom verification logic
    return finemcp.AuthInfo{Subject: "user"}, nil
})
```

## HTTPAuth Options

| Option | Description |
|--------|-------------|
| `WithAnonymousIdentity(info)` | Allow unauthenticated requests with a default identity |
| `WithAPIKeyHeader(name)` | Custom API key header name (default: `X-API-Key`) |
| `WithAPIKeyQuery(param)` | Accept API key from query parameter |
| `WithAuthErrorHandler(fn)` | Custom error response handler |

```go
handler := middleware.HTTPAuth(verifier, next,
    middleware.WithAPIKeyHeader("X-Custom-Key"),
    middleware.WithAnonymousIdentity(finemcp.AuthInfo{Subject: "anonymous", Roles: []string{"guest"}}),
)
```

## Protocol-Level Auth Checkers

### RequireAuth

Rejects any request without auth info in context:

```go
s.SetAuthChecker(middleware.RequireAuth())
```

### RequireAuthWithRoles

Requires specific roles:

```go
s.SetAuthChecker(middleware.RequireAuthWithRoles("admin"))
```

## AuthInfo

The `AuthInfo` struct is attached to the context after successful authentication:

```go
type AuthInfo struct {
    Subject string            // User or service identifier
    Roles   []string          // Assigned roles
    Meta    map[string]any    // Additional metadata
}
```

Access it in tool handlers:

```go
auth := finemcp.AuthInfoFromCtx(ctx)
if auth != nil {
    fmt.Println(auth.Subject, auth.Roles)
}
```

## Auth Schemes

| Constant | Header Format |
|----------|---------------|
| `SchemeBearerToken` | `Authorization: Bearer <token>` |
| `SchemeAPIKey` | `X-API-Key: <key>` |

## Testing

```bash
# Bearer token
curl -X POST http://localhost:8080 \
  -H "Authorization: Bearer admin-token" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'

# API key
curl -X POST http://localhost:8080 \
  -H "X-API-Key: api-key-123" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```
