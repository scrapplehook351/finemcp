package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// requestKey uniquely identifies a request for coalescing purposes.
// Two requests with the same method and params have the same key.
type requestKey struct {
	method string
	params string // JSON-serialized params (canonical)
}

// newRequestKey creates a request key from method and params.
// Uses canonical JSON encoding to ensure identical params produce
// identical keys regardless of field order.
func newRequestKey(method string, params any) (requestKey, error) {
	// Canonical JSON: sorted keys, no whitespace
	data, err := json.Marshal(params)
	if err != nil {
		return requestKey{}, fmt.Errorf("marshal params: %w", err)
	}

	// Security: Limit param size to prevent DoS (CVSS 7.5 mitigation)
	// 1MB limit prevents CPU/memory exhaustion from deeply nested structures
	const maxParamsSize = 1 << 20 // 1MB
	if len(data) > maxParamsSize {
		return requestKey{}, fmt.Errorf("params too large: %d bytes exceeds %d byte limit", len(data), maxParamsSize)
	}

	return requestKey{
		method: method,
		params: string(data),
	}, nil
}

// String returns a string representation for use as map key.
// For large params (>256 bytes), uses SHA256 hash to keep key size bounded.
func (k requestKey) String() string {
	fullKey := k.method + ":" + k.params

	// Use hash for large params to keep map keys efficient
	if len(fullKey) > 256 {
		hash := sha256.Sum256([]byte(fullKey))
		return k.method + ":hash:" + hex.EncodeToString(hash[:])
	}

	return fullKey
}

// flight represents a single in-flight request that may have multiple waiters.
type flight struct {
	// Immutable after creation
	key requestKey

	// Request execution state
	mu      sync.Mutex
	callers int           // Number of goroutines waiting
	done    chan struct{} // Closed when result is ready
	result  []byte        // Cached response (JSON)
	err     error         // Cached error

	// Context management
	parentCtx    context.Context
	parentCancel context.CancelFunc
}

// coalescer manages request coalescing for a Client.
type coalescer struct {
	window time.Duration // Coalescing window duration

	// Security: Limit concurrent in-flight requests (CVSS 6.5 mitigation)
	maxInflightRequests int // Default 1000, 0 = unlimited

	mu       sync.Mutex
	inflight map[string]*flight // key.String() → flight
}

// newCoalescer creates a new coalescer with the given window.
func newCoalescer(window time.Duration) *coalescer {
	if window <= 0 {
		window = 10 * time.Millisecond // Default
	}

	return &coalescer{
		window:              window,
		maxInflightRequests: 1000, // Security: Default limit
		inflight:            make(map[string]*flight),
	}
}

// Do executes the given function, coalescing identical concurrent calls.
// Multiple goroutines calling Do with the same key will only execute fn once.
//
// Context handling: ctx is used for per-caller cancellation detection.
// If ctx is cancelled, the caller returns immediately with ctx.Err(),
// but the shared request continues for other waiters.
func (c *coalescer) Do(
	ctx context.Context,
	key requestKey,
	fn func(context.Context) ([]byte, error),
) ([]byte, error) {
	keyStr := key.String()

	// Phase 1: Find or create flight
	c.mu.Lock()
	f := c.inflight[keyStr]
	if f == nil {
		// Security: Check inflight request limit before creating new flight
		if c.maxInflightRequests > 0 && len(c.inflight) >= c.maxInflightRequests {
			c.mu.Unlock()
			return nil, fmt.Errorf("client: too many concurrent requests (%d), limit is %d",
				len(c.inflight), c.maxInflightRequests)
		}
		// First caller: create new flight
		f = c.createFlight(ctx, key, keyStr, fn)
	} else {
		// Subsequent caller: join existing flight
		f.mu.Lock()
		f.callers++
		f.mu.Unlock()
	}
	c.mu.Unlock()

	// Phase 2: Wait for result
	return c.waitForFlight(ctx, f)
}

// createFlight creates a new flight and starts execution.
// MUST be called with c.mu held.
func (c *coalescer) createFlight(
	callerCtx context.Context,
	key requestKey,
	keyStr string,
	fn func(context.Context) ([]byte, error),
) *flight {
	// Preserve request-scoped values such as trace context while detaching the
	// shared request from any individual caller cancellation or deadline.
	parentCtx, parentCancel := context.WithCancel(context.WithoutCancel(callerCtx))

	f := &flight{
		key:          key,
		callers:      1,
		done:         make(chan struct{}),
		parentCtx:    parentCtx,
		parentCancel: parentCancel,
	}

	c.inflight[keyStr] = f

	// Start execution in background
	go c.execute(f, keyStr, fn)

	return f
}

// execute runs the actual request and broadcasts result to all waiters.
func (c *coalescer) execute(
	f *flight,
	keyStr string,
	fn func(context.Context) ([]byte, error),
) {
	// Cleanup flight from map when done
	defer func() {
		c.mu.Lock()
		delete(c.inflight, keyStr)
		c.mu.Unlock()

		f.parentCancel() // Release context resources
	}()

	// Panic recovery to prevent crash
	defer func() {
		if r := recover(); r != nil {
			f.mu.Lock()
			// Security: Sanitize panic messages - don't expose internal details (CVSS 7.8 mitigation)
			// Original panic value may contain sensitive data (stack traces, API keys, etc.)
			f.err = fmt.Errorf("client: request coalescing internal error")
			f.mu.Unlock()
			close(f.done)
			// TODO: Log full panic details internally if logger available
			// This keeps details out of the public API while preserving debugging capability
		}
	}()

	// Wait for coalescing window to accumulate more requests
	select {
	case <-time.After(c.window):
		// Window expired, proceed
	case <-f.parentCtx.Done():
		// All callers cancelled, abort
		f.mu.Lock()
		f.err = f.parentCtx.Err()
		f.mu.Unlock()
		close(f.done)
		return
	}

	// Execute the actual request
	result, err := fn(f.parentCtx)

	// Store result and broadcast to all waiters
	f.mu.Lock()
	f.result = result
	f.err = err
	f.mu.Unlock()

	close(f.done) // Signal all waiters
}

// waitForFlight waits for the flight to complete and returns its result.
// Handles per-caller context cancellation.
func (c *coalescer) waitForFlight(
	ctx context.Context,
	f *flight,
) ([]byte, error) {
	defer func() {
		// Decrement caller count
		f.mu.Lock()
		f.callers--
		isLast := f.callers == 0
		f.mu.Unlock()

		// If last caller and request not started yet, cancel it
		if isLast {
			select {
			case <-f.done:
				// Request already completed
			default:
				// Request still pending, cancel it
				f.parentCancel()
			}
		}
	}()

	// Wait for result or caller cancellation
	select {
	case <-f.done:
		// Result ready
		f.mu.Lock()
		result := f.result
		err := f.err
		f.mu.Unlock()
		return result, err

	case <-ctx.Done():
		// Caller cancelled, return immediately
		// (shared request continues for other waiters)
		return nil, ctx.Err()
	}
}
