package middleware

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
	"golang.org/x/sync/singleflight"
)

// ── Public errors ───────────────────────────────────────────────────

// ErrCacheMiss is returned by CacheBackend.Get when the key is not in the
// cache or the entry has expired.
var ErrCacheMiss = errors.New("cache miss")

// ── CacheBackend interface ──────────────────────────────────────────

// CacheBackend is the storage abstraction for the cache middleware.
// Implementations must be safe for concurrent use.
type CacheBackend interface {
	// Get retrieves a cached value. Returns ErrCacheMiss if the key
	// does not exist or has expired.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value with the given TTL. A zero TTL means no
	// expiration (the entry lives until evicted).
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a single key. Returns nil if the key does not exist.
	Delete(ctx context.Context, key string) error

	// DeleteByPrefix removes all keys that start with prefix. Returns
	// the number of deleted entries. Returns nil error if nothing matched.
	//
	// NOTE: This operation may be expensive for distributed backends
	// (e.g. Redis SCAN). Implementations that cannot support prefix
	// scanning efficiently may return (0, nil) as a no-op.
	DeleteByPrefix(ctx context.Context, prefix string) (int, error)
}

// ── CacheKeyFunc ────────────────────────────────────────────────────

// CacheKeyFunc builds a cache key from the request context. The returned
// string is concatenated with a hash of the input payload to form the
// final cache key. Return "" to use only the tool name (default).
type CacheKeyFunc func(ctx context.Context) string

// ── Configuration ───────────────────────────────────────────────────

// CacheOption configures the cache middleware.
type CacheOption func(*cacheConfig)

type cacheConfig struct {
	ttl       time.Duration
	backend   CacheBackend
	keyFunc   CacheKeyFunc
	skipTools map[string]bool // tools to exclude from caching
	onlyTools map[string]bool // if non-empty, only cache these tools
	setMeta   bool            // set _meta.cached on cache hits
}

// WithCacheTTL sets the time-to-live for cached entries.
// Defaults to 5 minutes. Zero means no expiration.
func WithCacheTTL(ttl time.Duration) CacheOption {
	return func(c *cacheConfig) {
		c.ttl = ttl
	}
}

// WithCacheBackend sets a custom cache backend. If not set, the middleware
// creates an in-memory LRU cache with 1024 entries.
func WithCacheBackend(b CacheBackend) CacheOption {
	return func(c *cacheConfig) {
		c.backend = b
	}
}

// WithCacheKeyFunc sets a function that provides an additional key
// component derived from the context (e.g. user ID, tenant ID).
func WithCacheKeyFunc(fn CacheKeyFunc) CacheOption {
	return func(c *cacheConfig) {
		c.keyFunc = fn
	}
}

// WithCacheSkipTools excludes the named tools from caching.
// Tool calls to any of these tools always hit the handler.
func WithCacheSkipTools(tools ...string) CacheOption {
	return func(c *cacheConfig) {
		if c.skipTools == nil {
			c.skipTools = make(map[string]bool, len(tools))
		}
		for _, t := range tools {
			c.skipTools[t] = true
		}
	}
}

// WithCacheOnlyTools restricts caching to the named tools.
// If set, only calls to these tools are cached; all others pass through.
func WithCacheOnlyTools(tools ...string) CacheOption {
	return func(c *cacheConfig) {
		if c.onlyTools == nil {
			c.onlyTools = make(map[string]bool, len(tools))
		}
		for _, t := range tools {
			c.onlyTools[t] = true
		}
	}
}

// WithCacheMeta controls whether the middleware sets _meta.cached = true
// on cache hits via SetResponseMeta. Defaults to true.
func WithCacheMeta(enabled bool) CacheOption {
	return func(c *cacheConfig) {
		c.setMeta = enabled
	}
}

// ── Cache middleware constructor ─────────────────────────────────────

// Cache returns a middleware that caches tool call results by input hash.
//
// The cache key is: <toolName>:<optionalKeyFunc>:<sha256(input)>.
//
// Cache hits short-circuit the handler chain and return the stored
// result immediately. A _meta.cached = true flag is set on hits
// (unless disabled via WithCacheMeta(false)).
//
// Cache misses invoke the handler normally and store successful
// (non-error) results.
//
// Usage:
//
//	// Default: in-memory LRU (1024 entries), 5 min TTL
//	server.Use(middleware.Cache())
//
//	// Custom TTL, skip write-tools
//	server.Use(middleware.Cache(
//	    middleware.WithCacheTTL(10 * time.Minute),
//	    middleware.WithCacheSkipTools("create_user", "delete_user"),
//	))
//
//	// Custom backend (e.g. Redis)
//	server.Use(middleware.Cache(
//	    middleware.WithCacheBackend(myRedisBackend),
//	))
func Cache(opts ...CacheOption) finemcp.Middleware {
	cfg := cacheConfig{
		ttl:     5 * time.Minute,
		setMeta: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	if len(cfg.skipTools) > 0 && len(cfg.onlyTools) > 0 {
		panic("middleware.Cache: cannot use both WithCacheSkipTools and WithCacheOnlyTools")
	}

	if cfg.backend == nil {
		cfg.backend = NewLRUCache(1024)
	}

	var sf singleflight.Group

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			toolName := finemcp.ToolName(ctx)

			// Check skip/only filters.
			if !cfg.shouldCache(toolName) {
				return next(ctx, input)
			}

			key := cfg.buildKey(ctx, toolName, input)

			// Try cache hit.
			if cached, err := cfg.backend.Get(ctx, key); err == nil {
				if cfg.setMeta {
					finemcp.SetResponseMeta(ctx, "cached", true)
				}
				return cached, nil
			}

			// Deduplicate concurrent calls for the same cache key
			// (cache stampede / thundering herd protection).
			v, err, shared := sf.Do(key, func() (any, error) {
				// Double-check after acquiring the flight.
				if cached, err := cfg.backend.Get(ctx, key); err == nil {
					return cached, nil
				}

				out, err := next(ctx, input)
				if err != nil {
					return nil, err
				}

				// Store result (best-effort; ignore storage errors).
				_ = cfg.backend.Set(ctx, key, out, cfg.ttl)
				return out, nil
			})
			if err != nil {
				return nil, err
			}

			// Copy the result so concurrent callers don't share the same slice.
			src := v.([]byte)
			result := make([]byte, len(src))
			copy(result, src)

			// Mark as cache hit for coalesced followers (shared=true means
			// this goroutine piggybacked on another's in-flight call).
			if shared && cfg.setMeta {
				finemcp.SetResponseMeta(ctx, "cached", true)
			}

			return result, nil
		}
	}
}

// shouldCache returns true if the tool should be cached according to
// the skip/only configuration.
func (c *cacheConfig) shouldCache(toolName string) bool {
	if len(c.onlyTools) > 0 {
		return c.onlyTools[toolName]
	}
	return !c.skipTools[toolName]
}

// buildKey constructs a cache key from the tool name, optional key func result,
// and a SHA-256 hash of the input payload.
func (c *cacheConfig) buildKey(ctx context.Context, toolName string, input []byte) string {
	h := sha256.Sum256(input)
	inputHash := hex.EncodeToString(h[:])

	prefix := toolName
	if c.keyFunc != nil {
		if extra := c.keyFunc(ctx); extra != "" {
			prefix = toolName + ":" + extra
		}
	}

	return prefix + ":" + inputHash
}

// ── Invalidation helpers ────────────────────────────────────────────

// CacheInvalidator provides manual cache invalidation for a Cache middleware.
// Create one by calling NewCacheInvalidator with the same backend used by
// the middleware.
type CacheInvalidator struct {
	backend CacheBackend
}

// NewCacheInvalidator returns an invalidator backed by the given CacheBackend.
func NewCacheInvalidator(b CacheBackend) *CacheInvalidator {
	return &CacheInvalidator{backend: b}
}

// InvalidateKey removes a single cache key.
func (inv *CacheInvalidator) InvalidateKey(ctx context.Context, key string) error {
	return inv.backend.Delete(ctx, key)
}

// InvalidateTool removes all cached entries for the given tool name.
// This works by deleting all keys with the prefix "<toolName>:".
func (inv *CacheInvalidator) InvalidateTool(ctx context.Context, toolName string) (int, error) {
	return inv.backend.DeleteByPrefix(ctx, toolName+":")
}

// ── In-memory LRU cache ─────────────────────────────────────────────

// lruEntry stores a cached value with its expiration time.
type lruEntry struct {
	key       string
	value     []byte
	expiresAt time.Time // zero value means no expiration
}

// lruCache is a thread-safe, in-memory LRU cache with TTL support.
// It implements CacheBackend.
type lruCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List // front = most recently used
	now      func() time.Time
}

// LRUCacheOption configures the in-memory LRU cache.
type LRUCacheOption func(*lruCache)

// withLRUClock overrides the clock used for TTL expiration.
// Unexported; used by tests to control time.
func withLRUClock(fn func() time.Time) LRUCacheOption {
	return func(c *lruCache) {
		if fn != nil {
			c.now = fn
		}
	}
}

// NewLRUCache creates an in-memory LRU cache with the given maximum
// number of entries. When the cache is full the least recently used
// entry is evicted.
func NewLRUCache(capacity int, opts ...LRUCacheOption) *lruCache {
	if capacity < 1 {
		panic("middleware.NewLRUCache: capacity must be >= 1")
	}
	c := &lruCache{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
		now:      time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Get retrieves a value from the cache. Returns ErrCacheMiss if the
// key does not exist or has expired.
func (c *lruCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, ErrCacheMiss
	}

	entry := el.Value.(*lruEntry)

	// Check expiration.
	if !entry.expiresAt.IsZero() && c.now().After(entry.expiresAt) {
		c.removeLocked(el)
		return nil, ErrCacheMiss
	}

	// Move to front (most recently used).
	c.order.MoveToFront(el)

	// Return a copy to prevent mutation.
	out := make([]byte, len(entry.value))
	copy(out, entry.value)
	return out, nil
}

// Set stores a value in the cache. If the key already exists, its value
// and TTL are updated. If the cache is full, the least recently used
// entry is evicted.
func (c *lruCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = c.now().Add(ttl)
	}

	// Store a copy to prevent mutation.
	val := make([]byte, len(value))
	copy(val, value)

	// Update existing entry.
	if el, ok := c.items[key]; ok {
		entry := el.Value.(*lruEntry)
		entry.value = val
		entry.expiresAt = expiresAt
		c.order.MoveToFront(el)
		return nil
	}

	// Evict LRU if at capacity.
	if c.order.Len() >= c.capacity {
		c.evictLocked()
	}

	entry := &lruEntry{
		key:       key,
		value:     val,
		expiresAt: expiresAt,
	}
	el := c.order.PushFront(entry)
	c.items[key] = el
	return nil
}

// Delete removes a single key from the cache.
func (c *lruCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil
	}
	c.removeLocked(el)
	return nil
}

// DeleteByPrefix removes all keys that start with prefix and returns
// the number of deleted entries.
func (c *lruCache) DeleteByPrefix(_ context.Context, prefix string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var toDelete []*list.Element
	for k, el := range c.items {
		if strings.HasPrefix(k, prefix) {
			toDelete = append(toDelete, el)
		}
	}

	for _, el := range toDelete {
		c.removeLocked(el)
	}

	return len(toDelete), nil
}

// Len returns the number of entries in the cache (including expired
// entries that have not yet been lazily evicted). Exported for
// internal testing only; external callers access the cache through
// the CacheBackend interface which does not include Len.
func (c *lruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// removeLocked removes an element from the cache. Caller must hold c.mu.
func (c *lruCache) removeLocked(el *list.Element) {
	entry := el.Value.(*lruEntry)
	delete(c.items, entry.key)
	c.order.Remove(el)
}

// evictLocked removes the least recently used entry. Caller must hold c.mu.
func (c *lruCache) evictLocked() {
	back := c.order.Back()
	if back != nil {
		c.removeLocked(back)
	}
}

// ── Key builder helper (exported for testing/manual invalidation) ───

// CacheKey builds the same cache key that the Cache middleware uses
// internally. Useful for manual invalidation of specific entries.
//
//	key := middleware.CacheKey("my_tool", []byte(`{"query":"hello"}`))
//	invalidator.InvalidateKey(ctx, key)
func CacheKey(toolName string, input []byte) string {
	h := sha256.Sum256(input)
	return toolName + ":" + hex.EncodeToString(h[:])
}

// CacheKeyWithExtra builds a cache key that includes an extra component,
// matching the key produced when a CacheKeyFunc is configured.
func CacheKeyWithExtra(toolName, extra string, input []byte) string {
	h := sha256.Sum256(input)
	return toolName + ":" + extra + ":" + hex.EncodeToString(h[:])
}
