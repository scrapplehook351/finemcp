---
url: "/docs/middleware/cache/"
title: "Cache"
description: "TTL-based result caching for tool responses"
weight: 9
---

The cache middleware caches tool results to avoid redundant computations.

## Usage

```go
s.Use(middleware.Cache(
    middleware.WithCacheTTL(5 * time.Minute),
))
```

## Options

| Option | Description |
|--------|-------------|
| `WithCacheTTL(d)` | Time-to-live for cached entries |
| `WithCacheBackend(b)` | Custom cache backend (default: in-memory) |
| `WithCacheKeyFunc(fn)` | Custom cache key generation |
| `WithCacheSkipTools(names...)` | Tools to exclude from caching |
| `WithCacheOnlyTools(names...)` | Only cache these tools |
| `WithCacheMeta(bool)` | Include cache hit/miss info in response metadata |

## Custom Backend

Implement the `CacheBackend` interface for Redis, Memcached, etc:

```go
type CacheBackend interface {
    Get(key string) ([]byte, error)
    Set(key string, value []byte, ttl time.Duration) error
    Delete(key string) error
    DeleteByPrefix(prefix string) error
}
```

Returns `ErrCacheMiss` when a key is not found.

## Custom Key Function

```go
middleware.WithCacheKeyFunc(func(ctx context.Context) string {
    toolName := finemcp.ToolName(ctx)
    tenantID := finemcp.TenantIDFromCtx(ctx)
    return fmt.Sprintf("%s:%s", tenantID, toolName)
})
```

## Selective Caching

```go
// Only cache expensive tools
middleware.WithCacheOnlyTools("expensive-query", "slow-computation")

// Cache everything except volatile tools
middleware.WithCacheSkipTools("random-number", "current-time")
```
