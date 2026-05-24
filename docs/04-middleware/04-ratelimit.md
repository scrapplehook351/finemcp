---
url: "/docs/middleware/ratelimit/"
title: "Rate Limit"
description: "Token bucket rate limiting for tool invocations"
weight: 10
---

The rate limit middleware throttles tool invocations using a token bucket algorithm.

## Usage

```go
s.Use(middleware.RateLimit(10, middleware.WithBurst(20)))
// 10 requests/second, burst up to 20
```

## Options

| Option | Description |
|--------|-------------|
| `WithBurst(n)` | Maximum burst size |
| `WithKeyFunc(fn)` | Custom rate limit key (e.g., per-user, per-tenant) |

## Per-User Rate Limiting

```go
s.Use(middleware.RateLimit(10,
    middleware.WithBurst(20),
    middleware.WithKeyFunc(func(ctx context.Context) string {
        auth := finemcp.AuthInfoFromCtx(ctx)
        if auth != nil {
            return auth.Subject
        }
        return "anonymous"
    }),
))
```

## Error

When rate limited:

```go
var middleware.ErrRateLimited
```

The client receives an error response indicating the rate limit was exceeded.
