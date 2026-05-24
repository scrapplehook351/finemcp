package middleware

import (
	"context"
	"math"
	"math/rand/v2"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Configuration ───────────────────────────────────────────────────

// RetryOption configures the retry middleware.
type RetryOption func(*retryConfig)

type retryConfig struct {
	maxAttempts int                                  // total attempts (1 = no retry)
	baseDelay   time.Duration                        // initial backoff delay
	maxDelay    time.Duration                        // upper bound on backoff delay
	multiplier  float64                              // backoff growth factor
	jitterFrac  float64                              // fraction of delay to jitter [0.0, 1.0]
	isRetryable func(error) bool                     // custom retry classifier
	onRetry     RetryCallback                        // optional per-retry hook
	sleep       func(context.Context, time.Duration) // sleep function (overridable for tests)
}

// RetryCallback is called before each retry attempt.
// attempt is 1-indexed (1 = first retry, not the original call).
type RetryCallback func(ctx context.Context, attempt int, err error, delay time.Duration)

// WithMaxAttempts sets the total number of attempts (including the original).
// Must be >= 1. Defaults to 3.
func WithMaxAttempts(n int) RetryOption {
	return func(c *retryConfig) {
		if n >= 1 {
			c.maxAttempts = n
		}
	}
}

// WithBaseDelay sets the initial backoff delay before the first retry.
// Defaults to 100ms.
func WithBaseDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		if d > 0 {
			c.baseDelay = d
		}
	}
}

// WithMaxDelay sets the upper bound on backoff delay. Defaults to 10s.
func WithMaxDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		if d > 0 {
			c.maxDelay = d
		}
	}
}

// WithMultiplier sets the exponential backoff growth factor.
// Must be >= 1.0. Defaults to 2.0.
func WithMultiplier(m float64) RetryOption {
	return func(c *retryConfig) {
		if m >= 1.0 {
			c.multiplier = m
		}
	}
}

// WithJitter sets the fraction of the computed delay to add as random jitter.
// Must be in [0.0, 1.0]. Defaults to 0.1 (10%).
// For example, with a 1s delay and 0.25 jitter, the actual delay is in [1s, 1.25s].
func WithJitter(frac float64) RetryOption {
	return func(c *retryConfig) {
		if frac >= 0 && frac <= 1.0 {
			c.jitterFrac = frac
		}
	}
}

// WithRetryIsRetryable sets a custom function to classify whether an error
// is retryable. By default, all non-nil errors are retryable.
// Passing nil reverts to the default behavior.
func WithRetryIsRetryable(fn func(error) bool) RetryOption {
	return func(c *retryConfig) {
		c.isRetryable = fn // nil is valid: reverts to default
	}
}

// WithOnRetry registers a callback that fires before each retry attempt.
// Useful for logging or metrics.
func WithOnRetry(fn RetryCallback) RetryOption {
	return func(c *retryConfig) {
		c.onRetry = fn
	}
}

// withRetrySleep overrides the sleep function. Unexported; for tests.
func withRetrySleep(fn func(context.Context, time.Duration)) RetryOption {
	return func(c *retryConfig) {
		c.sleep = fn
	}
}

// ── Retry middleware constructor ─────────────────────────────────────

// Retry returns a middleware that automatically retries failed tool calls
// with exponential backoff and optional jitter.
//
// Only the final error (after all attempts are exhausted) is returned to
// the caller. If any attempt succeeds, its result is returned immediately.
//
// The middleware respects context cancellation: if the context is cancelled
// between retries, the last error is returned without further attempts.
//
// Note: retries are only safe for idempotent operations. Be cautious when
// retrying tool calls that have side effects.
//
// Usage:
//
//	// Default: 3 attempts, 100ms base delay, 2x multiplier
//	server.Use(middleware.Retry())
//
//	// Custom: 5 attempts, 500ms base, 3x multiplier, 20% jitter
//	server.Use(middleware.Retry(
//	    middleware.WithMaxAttempts(5),
//	    middleware.WithBaseDelay(500 * time.Millisecond),
//	    middleware.WithMultiplier(3.0),
//	    middleware.WithJitter(0.2),
//	))
func Retry(opts ...RetryOption) finemcp.Middleware {
	cfg := retryConfig{
		maxAttempts: 3,
		baseDelay:   100 * time.Millisecond,
		maxDelay:    10 * time.Second,
		multiplier:  2.0,
		jitterFrac:  0.1,
		sleep:       defaultRetrySleep,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxAttempts < 1 {
		cfg.maxAttempts = 1
	}
	if cfg.multiplier < 1.0 {
		cfg.multiplier = 1.0
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			var lastErr error
			var lastOut []byte

			for attempt := 0; attempt < cfg.maxAttempts; attempt++ {
				if attempt > 0 {
					// Compute backoff delay.
					delay := computeDelay(cfg.baseDelay, cfg.maxDelay, cfg.multiplier, cfg.jitterFrac, attempt-1)

					// Fire onRetry callback before sleeping (panic-safe).
					safeOnRetry(cfg.onRetry, ctx, attempt, lastErr, delay)

					// Check context before sleeping.
					if ctx.Err() != nil {
						return lastOut, lastErr
					}

					cfg.sleep(ctx, delay)

					// Check context after sleeping.
					if ctx.Err() != nil {
						return lastOut, lastErr
					}
				}

				out, err := next(ctx, input)
				if err == nil {
					return out, nil
				}

				lastOut = out
				lastErr = err

				// Check if the error is retryable (panic-safe).
				if cfg.isRetryable != nil {
					if !safeIsRetryable(cfg.isRetryable, err) {
						return out, err
					}
				}
			}

			return lastOut, lastErr
		}
	}
}

// computeDelay calculates the backoff delay for a given retry iteration.
// iteration is 0-indexed (0 = first retry).
func computeDelay(base, max time.Duration, multiplier, jitterFrac float64, iteration int) time.Duration {
	// Exponential backoff: base * multiplier^iteration
	delay := float64(base) * math.Pow(multiplier, float64(iteration))

	// Cap at max delay.
	if delay > float64(max) {
		delay = float64(max)
	}

	// Guard against overflow/NaN.
	if math.IsNaN(delay) || math.IsInf(delay, 0) || delay < 0 {
		delay = float64(max)
	}

	// Add jitter: delay * [1.0, 1.0+jitterFrac).
	if jitterFrac > 0 {
		jitter := delay * jitterFrac * rand.Float64() // #nosec G404 -- jitter does not need crypto-grade randomness
		delay += jitter
	}

	// Re-cap after jitter.
	if delay > float64(max) {
		delay = float64(max)
	}

	d := time.Duration(delay)
	if d < 0 {
		d = time.Duration(max)
	}
	return d
}

// defaultRetrySleep is the default context-aware sleep function.
func defaultRetrySleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// safeOnRetry calls the onRetry callback in a panic-safe wrapper.
// A misbehaving callback should not crash the server.
func safeOnRetry(fn RetryCallback, ctx context.Context, attempt int, err error, delay time.Duration) {
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(ctx, attempt, err, delay)
}

// safeIsRetryable calls the isRetryable classifier in a panic-safe wrapper.
// On panic, returns false (treat as non-retryable — safest default).
func safeIsRetryable(fn func(error) bool, err error) (retryable bool) {
	defer func() {
		if recover() != nil {
			retryable = false
		}
	}()
	return fn(err)
}
