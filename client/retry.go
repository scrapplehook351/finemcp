package client

// retry.go — automatic retry with exponential backoff and optional idempotency keys.
//
// When Options.MaxRetries > 0 the client wraps every callDirect invocation in a
// retry loop.  On a retryable error (see Options.RetryableErrors) it waits for
// an exponentially growing delay with ±25 % jitter and re-sends the exact same
// JSON-RPC request, including the same idempotency key when
// Options.EnableIdempotency is true.
//
// Idempotency keys are injected as params._meta.idempotencyKey (a 32-char hex
// random string) so that servers can deduplicate duplicate requests that arrive
// because a response was lost in transit.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand"
	"time"
)

// ErrMaxRetriesExceeded is returned when all retry attempts fail.
var ErrMaxRetriesExceeded = errors.New("client: max retries exceeded")

// DefaultRetryableErrors contains the JSON-RPC error codes that map to
// transient HTTP conditions and are safe to retry.
//
// 408 Request Timeout, 429 Too Many Requests, 500 Internal Server Error,
// 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout.
var DefaultRetryableErrors = []int{408, 429, 500, 502, 503, 504}

// retrier executes requests with automatic retry on failure.
type retrier struct {
	maxRetries     int
	retryableCodes map[int]bool
	baseDelay      time.Duration
	maxDelay       time.Duration
	rng            *mathrand.Rand
}

// newRetrier creates a retrier with the given maximum retry count and set of
// retryable JSON-RPC error codes.  baseDelay is 100 ms and maxDelay 30 s.
func newRetrier(maxRetries int, codes []int) *retrier {
	codeMap := make(map[int]bool, len(codes))
	for _, c := range codes {
		codeMap[c] = true
	}

	// Seed from crypto/rand for unpredictable jitter across processes.
	var seed [8]byte
	_, _ = rand.Read(seed[:])
	s := int64(seed[0]) | int64(seed[1])<<8 | int64(seed[2])<<16 | int64(seed[3])<<24 |
		int64(seed[4])<<32 | int64(seed[5])<<40 | int64(seed[6])<<48 | int64(seed[7])<<56
	rng := mathrand.New(mathrand.NewSource(s)) // #nosec G404 -- seeded with crypto/rand; used only for backoff jitter

	return &retrier{
		maxRetries:     maxRetries,
		retryableCodes: codeMap,
		baseDelay:      100 * time.Millisecond,
		maxDelay:       30 * time.Second,
		rng:            rng,
	}
}

// isRetryable reports whether err should trigger a retry.
//
// A ResponseError is retryable when its Code appears in retryableCodes.
// ErrClosed is retryable because the request may not have been processed yet.
func (r *retrier) isRetryable(err error) bool {
	var re *ResponseError
	if errors.As(err, &re) {
		return r.retryableCodes[re.Code]
	}
	return errors.Is(err, ErrClosed)
}

// sleepDuration returns the backoff duration for the given attempt index
// (0-based), applying full jitter within [0, computed_cap].
func (r *retrier) sleepDuration(attempt int) time.Duration {
	// Exponential cap: base * 2^attempt, clamped to maxDelay.
	cap := time.Duration(float64(r.baseDelay) * math.Pow(2, float64(attempt)))
	if cap > r.maxDelay {
		cap = r.maxDelay
	}
	// Full jitter: uniform random within [0, cap].
	return time.Duration(r.rng.Int63n(int64(cap) + 1))
}

// callWithRetry executes method with automatic retry on retryable errors.
// An idempotency key is generated once and reused across all attempts when
// opts.EnableIdempotency is true.
func (c *Client) callWithRetry(ctx context.Context, method string, params any, dest any) error {
	// Inject idempotency key once for all retry attempts.
	effectiveParams := params
	if c.opts.EnableIdempotency {
		key, err := generateIdempotencyKey()
		if err != nil {
			return fmt.Errorf("client: generate idempotency key: %w", err)
		}
		enriched, err := injectIdempotencyKey(params, key)
		if err != nil {
			return fmt.Errorf("client: inject idempotency key: %w", err)
		}
		effectiveParams = enriched
	}

	var lastErr error
	for attempt := 0; attempt <= c.retrier.maxRetries; attempt++ {
		if attempt > 0 {
			d := c.retrier.sleepDuration(attempt - 1)
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := c.callDirect(ctx, method, effectiveParams, dest)
		if err == nil {
			return nil
		}

		if !c.retrier.isRetryable(err) {
			return err
		}

		lastErr = err
	}

	return fmt.Errorf("%w after %d attempt(s): %w",
		ErrMaxRetriesExceeded, c.retrier.maxRetries+1, lastErr)
}

// generateIdempotencyKey returns a 32-character lowercase hex string derived
// from 16 cryptographically random bytes.
func generateIdempotencyKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// injectIdempotencyKey sets params._meta.idempotencyKey = key in a copy of
// params and returns the enriched JSON.  Existing _meta fields are preserved.
// If params is not a JSON object (e.g. an array) it is returned unchanged.
func injectIdempotencyKey(params any, key string) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("retry: marshal params: %w", err)
	}

	var m map[string]any
	if params == nil || string(raw) == "null" {
		m = make(map[string]any)
	} else {
		if err := json.Unmarshal(raw, &m); err != nil {
			// Not a JSON object — return as-is without injection.
			return raw, nil
		}
	}

	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["idempotencyKey"] = key
	m["_meta"] = meta

	enriched, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("retry: re-marshal params: %w", err)
	}
	return enriched, nil
}
