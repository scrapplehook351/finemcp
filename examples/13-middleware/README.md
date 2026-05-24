# 13 — Middleware

Demonstrates 16 middleware types that wrap tool handlers with cross-cutting concerns.

## How Middleware Works

Middleware is applied to the server and wraps all tool invocations:

```go
s := finemcp.NewServer("my-server", "1.0.0")
s.Use(middleware.Recovery())
s.Use(middleware.Logging(logger))
s.Use(middleware.RateLimit(10, middleware.WithBurst(20)))
```

Middleware executes in order: first applied = outermost wrapper.

## Middleware Reference

### recovery/
Catches panics in tool handlers and converts them to error responses.
```go
s.Use(middleware.Recovery())
```

### logging/
Logs every tool invocation with duration and result status.
```go
s.Use(middleware.Logging(logger))
// logger implements: Info(msg string, keysAndValues ...any), Error(msg string, keysAndValues ...any)
```

### auth/
HTTP-level authentication with bearer tokens and API keys.
```go
verifier := middleware.ChainVerifiers(
    middleware.StaticBearerTokenVerifier(map[string]string{"token-123": "user1"}),
    middleware.StaticAPIKeyVerifier(map[string]string{"key-abc": "service1"}),
)
handler := middleware.HTTPAuth(verifier, transport.Handler(s))
// Test with: curl -H "Authorization: Bearer token-123" ...
```

### rbac/
Role-based access control — restricts tools to specific user roles.
```go
s.Use(middleware.RBAC())
// Tools declare required roles:
finemcp.NewTool("admin-tool", handler, finemcp.WithRoles("admin"))
// Tools without roles are public
```

### ratelimit/
Limits tool invocation rate with token bucket algorithm.
```go
s.Use(middleware.RateLimit(10, middleware.WithBurst(20)))
// 10 requests/sec, burst up to 20
```

### cache/
Caches tool results for a configurable TTL.
```go
s.Use(middleware.Cache(middleware.WithCacheTTL(5 * time.Minute)))
```

### retry/
Automatically retries failed tool invocations.
```go
s.Use(middleware.Retry(middleware.WithMaxAttempts(3)))
```

### circuitbreaker/
Opens circuit after repeated failures to prevent cascading issues.
```go
s.Use(middleware.CircuitBreaker(
    middleware.WithFailureThreshold(5),
    middleware.WithSuccessThreshold(3),
))
```

### sandbox/
Restricts tool execution with timeout and output size limits.
```go
s.Use(middleware.Sandbox(
    middleware.WithTimeout(5 * time.Second),
    middleware.WithMaxOutputSize(1024 * 1024), // 1MB
))
```

### validation/
Validates tool inputs against their JSON schemas before invoking the handler.
```go
s.Use(middleware.Validation())
// Tools must have: finemcp.WithInputSchema(map[string]any{...})
```

### async/
Converts long-running tools into background tasks.
```go
store := finemcp.NewTaskStore()
asyncMiddleware, waiter := middleware.Async()
s.Use(asyncMiddleware)
// Server options: finemcp.WithTaskStore(store)
```

### auditlog/
Logs all tool invocations for audit/compliance.
```go
s.Use(middleware.AuditLog(middleware.WithAuditSink(func(entry middleware.AuditEntry) {
    fmt.Printf("[AUDIT] tool=%s duration=%v success=%v\n", entry.ToolName, entry.Duration, entry.Success)
})))
```

### costtracking/
Tracks per-tool cost and usage metrics.
```go
s.Use(middleware.CostTracking(middleware.WithCostCollector(func(record middleware.CostRecord) {
    fmt.Printf("[COST] tool=%s duration=%v\n", record.ToolName, record.Duration)
})))
```

### simulation/
Dry-run mode for destructive tools.
```go
s.Use(middleware.Simulation())
// Tools marked destructive run in simulation mode:
finemcp.NewTool("delete-db", handler, finemcp.WithDestructive())
// Inside handler, check: finemcp.IsSimulatedFromCtx(ctx)
```

### multitenant/
Tenant isolation with per-tenant tool access control.
```go
resolver := middleware.NewTenantResolver(
    middleware.TenantFromAuthMeta("tenant_id"),
    middleware.NewStaticTenantStore(map[string]middleware.TenantConfig{
        "tenant-a": { ToolFilter: func(name string) bool { return name == "safe-tool" } },
    }),
)
s.SetTenantResolver(resolver)
```

### otel/
OpenTelemetry distributed tracing and metrics.
```go
s.Use(middleware.OTel())
```

## Stacking Middleware

Middleware composes naturally — stack multiple layers:

```go
s.Use(middleware.Recovery())          // outermost: catch panics
s.Use(middleware.Logging(logger))     // log all calls
s.Use(middleware.RateLimit(10))       // throttle
s.Use(middleware.Validation())        // validate input schemas
s.Use(middleware.Cache(middleware.WithCacheTTL(5 * time.Minute)))
```

## Testing with curl

```bash
# Most middleware examples
go run ./13-middleware/recovery

curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Auth example (requires token)
go run ./13-middleware/auth
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{ "jsonrpc": "2.0", "id": 2, "method": "tools/list" }'
```
