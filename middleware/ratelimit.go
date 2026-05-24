package middleware

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// ErrRateLimited is returned when a request is rejected by the rate limiter.
var ErrRateLimited = errors.New("rate limited")

// RateLimitKeyFunc extracts a rate-limit key from the context.
// Requests that share the same key share the same token bucket.
// Return "" to use a single global bucket (the default behaviour).
type RateLimitKeyFunc func(ctx context.Context) string

// RateLimitOption configures the rate limiter.
type RateLimitOption func(*rateLimitConfig)

type rateLimitConfig struct {
	rate    float64          // tokens per second
	burst   int              // max tokens (bucket capacity)
	keyFunc RateLimitKeyFunc // optional per-key bucketing
	now     func() time.Time // clock override for testing
}

// WithBurst sets the maximum burst size (bucket capacity).
// Defaults to the same value as the rate (rounded up to at least 1).
func WithBurst(burst int) RateLimitOption {
	return func(c *rateLimitConfig) {
		if burst > 0 {
			c.burst = burst
		}
	}
}

// WithKeyFunc sets a function to extract a per-request key for bucketing.
// Each distinct key gets its own independent token bucket.
// Useful for per-user or per-tool rate limiting.
//
// Example — per-tool limiting:
//
//	middleware.WithKeyFunc(func(ctx context.Context) string {
//	    return finemcp.ToolName(ctx)
//	})
func WithKeyFunc(fn RateLimitKeyFunc) RateLimitOption {
	return func(c *rateLimitConfig) {
		c.keyFunc = fn
	}
}

// withClock is unexported; used by tests to control time.
func withClock(fn func() time.Time) RateLimitOption {
	return func(c *rateLimitConfig) {
		c.now = fn
	}
}

// RateLimit returns a middleware that enforces a token-bucket rate limit
// on tool calls. Rate is expressed as requests per second.
//
// When a call exceeds the limit the handler returns an error result
// (not a protocol error) so the client receives a clear signal to back off.
//
// Note: when using WithKeyFunc with high-cardinality keys (e.g. per-user IDs),
// each distinct key creates a bucket that is never evicted. Ensure key cardinality
// is bounded, or add an external cleanup mechanism for long-running processes.
//
// Usage:
//
//	// Global: 10 req/s, burst of 20
//	server.Use(middleware.RateLimit(10, middleware.WithBurst(20)))
//
//	// Per-tool: 5 req/s per tool
//	server.Use(middleware.RateLimit(5,
//	    middleware.WithKeyFunc(func(ctx context.Context) string {
//	        return finemcp.ToolName(ctx)
//	    }),
//	))
func RateLimit(rate float64, opts ...RateLimitOption) finemcp.Middleware {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		panic("middleware.RateLimit: rate must be a finite positive number")
	}

	cfg := rateLimitConfig{
		rate:  rate,
		burst: int(math.Ceil(rate)),
		now:   time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.burst < 1 {
		cfg.burst = 1
	}

	rl := &rateLimiter{
		rate:    cfg.rate,
		burst:   cfg.burst,
		keyFunc: cfg.keyFunc,
		now:     cfg.now,
		buckets: make(map[string]*bucket),
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			if !rl.allow(ctx) {
				return nil, fmt.Errorf("%w: too many requests", ErrRateLimited)
			}
			return next(ctx, input)
		}
	}
}

// ── Token Bucket implementation ─────────────────────────────────────

type bucket struct {
	tokens   float64
	lastTime time.Time
}

type rateLimiter struct {
	rate    float64
	burst   int
	keyFunc RateLimitKeyFunc
	now     func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// allow checks whether a single token can be consumed.
func (rl *rateLimiter) allow(ctx context.Context) bool {
	key := ""
	if rl.keyFunc != nil {
		key = rl.keyFunc(ctx)
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{
			tokens:   float64(rl.burst),
			lastTime: now,
		}
		rl.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastTime = now

	if b.tokens < 1 {
		return false
	}

	b.tokens--
	return true
}
