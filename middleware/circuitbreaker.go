package middleware

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Public errors ───────────────────────────────────────────────────

// ErrCircuitOpen is returned when the circuit breaker is in the open state
// and is rejecting calls to protect the downstream from further failures.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// ── Circuit states ──────────────────────────────────────────────────

// CircuitState represents the current state of a circuit breaker.
type CircuitState int

const (
	// StateClosed is the normal operating state. Requests pass through
	// and failures are counted.
	StateClosed CircuitState = iota

	// StateOpen means the failure threshold has been exceeded. Requests
	// are rejected immediately without calling the handler.
	StateOpen

	// StateHalfOpen allows a limited number of probe requests through to
	// test whether the downstream has recovered.
	StateHalfOpen
)

// String returns the human-readable state name.
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ── Configuration ───────────────────────────────────────────────────

// CircuitBreakerOption configures the circuit breaker middleware.
type CircuitBreakerOption func(*circuitBreakerConfig)

type circuitBreakerConfig struct {
	failureThreshold int                   // failures to trip from closed → open
	successThreshold int                   // successes in half-open to close
	timeout          time.Duration         // how long to stay open before half-open
	maxHalfOpen      int                   // max concurrent probe requests in half-open
	keyFunc          CircuitBreakerKeyFunc // per-key circuit breaking
	isFailure        func(error) bool      // custom failure classifier
	onStateChange    StateChangeCallback   // optional state transition hook
	now              func() time.Time      // clock override for testing
}

// CircuitBreakerKeyFunc extracts a circuit breaker key from the context.
// Each distinct key gets its own independent circuit breaker.
// Return "" to use a single global breaker (the default).
type CircuitBreakerKeyFunc func(ctx context.Context) string

// StateChangeCallback is called when a circuit breaker transitions between states.
type StateChangeCallback func(key string, from, to CircuitState)

// WithFailureThreshold sets the number of consecutive failures required
// to trip the circuit from closed to open. Defaults to 5.
func WithFailureThreshold(n int) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		if n > 0 {
			c.failureThreshold = n
		}
	}
}

// WithSuccessThreshold sets the number of consecutive successes in
// half-open state required to close the circuit. Defaults to 2.
func WithSuccessThreshold(n int) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		if n > 0 {
			c.successThreshold = n
		}
	}
}

// WithResetTimeout sets how long the circuit stays open before transitioning
// to half-open. Defaults to 30 seconds.
func WithResetTimeout(d time.Duration) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithMaxHalfOpen sets the maximum number of concurrent probe requests
// allowed in the half-open state. Defaults to 1.
func WithMaxHalfOpen(n int) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		if n > 0 {
			c.maxHalfOpen = n
		}
	}
}

// WithCircuitBreakerKeyFunc sets a function to derive per-request keys.
// Each distinct key maintains its own independent circuit breaker state.
// Passing nil reverts to the default single global breaker.
//
// Example — per-tool circuit breaking:
//
//	middleware.WithCircuitBreakerKeyFunc(func(ctx context.Context) string {
//	    return finemcp.ToolName(ctx)
//	})
func WithCircuitBreakerKeyFunc(fn CircuitBreakerKeyFunc) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		c.keyFunc = fn // nil is valid: reverts to global breaker
	}
}

// WithIsFailure sets a custom function to classify whether an error
// should count as a failure. When set, the function is called for every
// completed call (including when err is nil), giving full control over
// failure classification. By default (no custom function), any non-nil
// error is a failure. Passing nil reverts to the default behavior.
//
// Example — only count timeout errors:
//
//	middleware.WithIsFailure(func(err error) bool {
//	    return errors.Is(err, context.DeadlineExceeded)
//	})
func WithIsFailure(fn func(error) bool) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		c.isFailure = fn // nil is valid: reverts to default (any error = failure)
	}
}

// WithOnStateChange registers a callback that fires when any circuit
// breaker transitions between states.
func WithOnStateChange(fn StateChangeCallback) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		c.onStateChange = fn
	}
}

// withCircuitBreakerClock is unexported; used by tests to control time.
func withCircuitBreakerClock(fn func() time.Time) CircuitBreakerOption {
	return func(c *circuitBreakerConfig) {
		c.now = fn
	}
}

// ── Circuit Breaker middleware constructor ───────────────────────────

// CircuitBreaker returns a middleware that implements the circuit breaker
// pattern, preventing cascading failures when downstream tools are degraded.
//
// The breaker has three states:
//
//   - Closed (normal): requests pass through. Consecutive failures are counted.
//     When failures reach the threshold, the circuit trips to open.
//
//   - Open: all requests are immediately rejected with [ErrCircuitOpen].
//     After the timeout elapses, the circuit transitions to half-open.
//
//   - Half-Open: a limited number of probe requests are allowed through.
//     If they succeed (reaching the success threshold), the circuit closes.
//     If any probe fails, the circuit reopens.
//
// Note: when using [WithCircuitBreakerKeyFunc] with high-cardinality keys
// (e.g. per-user IDs), each distinct key creates a breaker that is never
// evicted. Prefer low-cardinality keys such as tool names.
//
// Usage:
//
//	// Global: trip after 5 failures, 30s timeout
//	server.Use(middleware.CircuitBreaker())
//
//	// Per-tool breakers with custom thresholds
//	server.Use(middleware.CircuitBreaker(
//	    middleware.WithFailureThreshold(3),
//	    middleware.WithResetTimeout(10 * time.Second),
//	    middleware.WithCircuitBreakerKeyFunc(func(ctx context.Context) string {
//	        return finemcp.ToolName(ctx)
//	    }),
//	))
func CircuitBreaker(opts ...CircuitBreakerOption) finemcp.Middleware {
	cfg := circuitBreakerConfig{
		failureThreshold: 5,
		successThreshold: 2,
		timeout:          30 * time.Second,
		maxHalfOpen:      1,
		now:              time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.failureThreshold < 1 {
		cfg.failureThreshold = 1
	}
	if cfg.successThreshold < 1 {
		cfg.successThreshold = 1
	}

	cb := &circuitBreakerGroup{
		cfg:      &cfg,
		breakers: make(map[string]*breaker),
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			key := ""
			if cfg.keyFunc != nil {
				key = cfg.keyFunc(ctx)
			}

			b := cb.getOrCreate(key)

			admittedState, err := b.beforeCall()
			if err != nil {
				if key != "" {
					return nil, fmt.Errorf("%w: key=%q", err, key)
				}
				return nil, err
			}

			// Defer afterCall so that panics in the handler still
			// record the outcome and release half-open slots.
			var callErr error
			defer func() {
				isFailure := callErr != nil
				if cfg.isFailure != nil {
					isFailure = cfg.isFailure(callErr)
				}
				b.afterCall(admittedState, isFailure)
			}()

			var out []byte
			out, callErr = next(ctx, input)
			return out, callErr
		}
	}
}

// ── Circuit breaker group (per-key management) ──────────────────────

type circuitBreakerGroup struct {
	cfg      *circuitBreakerConfig
	mu       sync.RWMutex
	breakers map[string]*breaker
}

func (g *circuitBreakerGroup) getOrCreate(key string) *breaker {
	// Fast path: read-lock for existing breakers (common case).
	g.mu.RLock()
	b, ok := g.breakers[key]
	g.mu.RUnlock()
	if ok {
		return b
	}

	// Slow path: write-lock with double-check.
	g.mu.Lock()
	defer g.mu.Unlock()

	b, ok = g.breakers[key]
	if !ok {
		b = &breaker{
			key: key,
			cfg: g.cfg,
		}
		g.breakers[key] = b
	}
	return b
}

// ── Per-key circuit breaker state machine ───────────────────────────

type breaker struct {
	key string
	cfg *circuitBreakerConfig

	mu              sync.Mutex
	state           CircuitState
	failures        int       // consecutive failures in closed state
	successes       int       // consecutive successes in half-open state
	lastFailureTime time.Time // when the circuit was last tripped open
	halfOpenCount   int       // current in-flight probes in half-open
}

// State returns the current effective state of this breaker. If the circuit
// is open and the timeout has elapsed, this reports StateHalfOpen without
// performing the actual transition (read-only).
func (b *breaker) State() CircuitState {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == StateOpen {
		now := b.cfg.now()
		if now.After(b.lastFailureTime.Add(b.cfg.timeout)) {
			return StateHalfOpen
		}
	}
	return b.state
}

// beforeCall checks whether the request should be allowed through.
// Returns the admitted state and nil if allowed, or ErrCircuitOpen if rejected.
func (b *breaker) beforeCall() (CircuitState, error) {
	b.mu.Lock()
	notify := b.maybeTransitionToHalfOpen()
	state := b.state

	var admittedState CircuitState
	var err error

	switch state {
	case StateClosed:
		admittedState = StateClosed
	case StateOpen:
		admittedState = StateOpen
		err = ErrCircuitOpen
	case StateHalfOpen:
		if b.halfOpenCount >= b.cfg.maxHalfOpen {
			admittedState = StateHalfOpen
			err = ErrCircuitOpen
		} else {
			b.halfOpenCount++
			admittedState = StateHalfOpen
		}
	default:
		admittedState = state
	}
	b.mu.Unlock()

	if notify != nil {
		notify()
	}
	return admittedState, err
}

// maybeTransitionToHalfOpen transitions open → half-open if the timeout
// has elapsed. Returns a callback to fire outside the lock. Caller must hold b.mu.
func (b *breaker) maybeTransitionToHalfOpen() func() {
	if b.state == StateOpen {
		now := b.cfg.now()
		if now.After(b.lastFailureTime.Add(b.cfg.timeout)) {
			return b.transitionLocked(StateHalfOpen)
		}
	}
	return nil
}

// afterCall records the result of a call and transitions state if needed.
// admittedState is the state under which the call was originally admitted.
func (b *breaker) afterCall(admittedState CircuitState, failed bool) {
	var notify func()

	b.mu.Lock()
	switch admittedState {
	case StateClosed:
		if failed {
			b.failures++
			if b.failures >= b.cfg.failureThreshold {
				b.lastFailureTime = b.cfg.now()
				notify = b.transitionLocked(StateOpen)
			}
		} else {
			b.failures = 0
		}

	case StateHalfOpen:
		// Only decrement if the breaker is still in half-open. If another
		// goroutine already tripped it (resetting halfOpenCount to 0),
		// decrementing would go negative.
		if b.state == StateHalfOpen {
			b.halfOpenCount--
		}
		if failed {
			// Any failure in half-open reopens the circuit.
			b.lastFailureTime = b.cfg.now()
			b.successes = 0
			if b.state == StateHalfOpen {
				notify = b.transitionLocked(StateOpen)
			}
		} else {
			if b.state == StateHalfOpen {
				b.successes++
				if b.successes >= b.cfg.successThreshold && b.halfOpenCount == 0 {
					notify = b.transitionLocked(StateClosed)
				}
			}
		}

	case StateOpen:
		// Calls shouldn't reach here (blocked in beforeCall), but
		// handle gracefully in case of state transition timing.
	}
	b.mu.Unlock()

	if notify != nil {
		notify()
	}
}

// transitionLocked performs a state transition and returns a closure to
// fire the onStateChange callback outside the lock. Caller must hold b.mu.
func (b *breaker) transitionLocked(to CircuitState) func() {
	from := b.state
	b.state = to

	// Reset counters on transition.
	b.successes = 0
	b.halfOpenCount = 0
	if to == StateClosed {
		b.failures = 0
	}

	if b.cfg.onStateChange != nil {
		cb := b.cfg.onStateChange
		key := b.key
		return func() { cb(key, from, to) }
	}
	return nil
}
