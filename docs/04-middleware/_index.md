---
url: "/docs/middleware/"
title: "Middleware"
description: "Composable middleware for auth, caching, rate limiting, and more"
weight: 4
---

finemcp includes 16 composable middleware that wrap tool handlers with cross-cutting concerns.

## Applying Middleware

```go
import "github.com/finemcp/finemcp/middleware"

s := finemcp.NewServer("my-server", "1.0.0")
s.Use(middleware.Recovery())
s.Use(middleware.Logging(logger))
s.Use(middleware.RateLimit(10, middleware.WithBurst(20)))
```

Middleware executes in registration order: first registered = outermost wrapper.

## Middleware Type

```go
type Middleware func(ToolHandler) ToolHandler
```

Each middleware wraps a `ToolHandler`, optionally executing logic before/after the inner handler.

## Available Middleware

### Security & Access Control

{{< cards >}}
  {{< card link="auth" title="Auth" subtitle="HTTP-level bearer token and API key authentication" >}}
  {{< card link="rbac" title="RBAC" subtitle="Role-based access control for tools" >}}
  {{< card link="multitenant" title="Multi-Tenant" subtitle="Per-tenant tool and resource filtering" >}}
{{< /cards >}}

### Reliability

{{< cards >}}
  {{< card link="recovery" title="Recovery" subtitle="Catch panics and convert to error responses" >}}
  {{< card link="retry" title="Retry" subtitle="Automatic retry with exponential backoff" >}}
  {{< card link="circuitbreaker" title="Circuit Breaker" subtitle="Fail fast after repeated failures" >}}
  {{< card link="sandbox" title="Sandbox" subtitle="Timeout and output size limits" >}}
  {{< card link="validation" title="Validation" subtitle="JSON Schema input validation" >}}
{{< /cards >}}

### Performance

{{< cards >}}
  {{< card link="cache" title="Cache" subtitle="TTL-based result caching" >}}
  {{< card link="ratelimit" title="Rate Limit" subtitle="Token bucket rate limiting" >}}
  {{< card link="async" title="Async" subtitle="Background task execution" >}}
{{< /cards >}}

### Observability

{{< cards >}}
  {{< card link="logging" title="Logging" subtitle="Structured invocation logging" >}}
  {{< card link="auditlog" title="Audit Log" subtitle="Compliance audit trail" >}}
  {{< card link="costtracking" title="Cost Tracking" subtitle="Per-tool usage and cost metrics" >}}
  {{< card link="otel" title="OpenTelemetry" subtitle="Distributed tracing and metrics" >}}
{{< /cards >}}

### Testing

{{< cards >}}
  {{< card link="simulation" title="Simulation" subtitle="Dry-run mode for destructive tools" >}}
{{< /cards >}}

## Recommended Stack

A typical production middleware stack:

```go
s.Use(middleware.Recovery())                    // Catch panics
s.Use(middleware.Logging(logger))               // Log all calls
s.Use(middleware.RateLimit(100, middleware.WithBurst(200)))
s.Use(middleware.Validation())                  // Validate input schemas
s.Use(middleware.Cache(middleware.WithCacheTTL(5 * time.Minute)))
```
