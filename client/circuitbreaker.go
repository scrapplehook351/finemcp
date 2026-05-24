package client

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// StateClosed means the circuit breaker is allowing requests normally.
	// Consecutive failures are counted and may transition to StateOpen.
	StateClosed CircuitState = iota

	// StateOpen means the circuit breaker is rejecting requests to fail fast.
	// After OpenTimeout, transitions to StateHalfOpen for recovery testing.
	StateOpen

	// StateHalfOpen means the circuit breaker is testing recovery with limited requests.
	// Success transitions to StateClosed, failure back to StateOpen.
	StateHalfOpen
)

// String returns a human-readable representation of the circuit state.
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "Half-Open"
	default:
		return "Unknown"
	}
}

// Circuit breaker errors.
var (
	// ErrCircuitOpen is returned when the circuit breaker rejects a request
	// because the circuit is open (too many failures detected).
	ErrCircuitOpen = errors.New("circuit breaker: circuit is open")
)

// CircuitBreakerConfig configures circuit breaker behavior.
//
// The circuit breaker wraps a Transport and tracks failures. When consecutive
// failures reach MaxFailures, the circuit "opens" and rejects requests with
// ErrCircuitOpen for OpenTimeout duration. After the timeout, the circuit
// enters "half-open" state to test recovery with limited requests.
type CircuitBreakerConfig struct {
	// MaxFailures is the number of consecutive failures before opening the circuit.
	// Zero or negative values default to 5.
	//
	// Default: 5
	MaxFailures int

	// OpenTimeout is how long the circuit stays open before transitioning to Half-Open.
	// Zero or negative values default to 30 seconds.
	//
	// Default: 30s
	OpenTimeout time.Duration

	// HalfOpenMaxRequests is the number of test requests allowed in Half-Open state.
	// Zero or negative values default to 1.
	//
	// This follows the standard circuit breaker pattern of allowing a single test
	// request. Higher values allow more concurrent testing but may cause more
	// failures if the server hasn't recovered.
	//
	// Default: 1
	HalfOpenMaxRequests int

	// ShouldTrip is a predicate that determines if an error should count as a failure.
	// If nil, the default predicate is used which excludes context cancellation
	// and client-side errors (ErrNotInitialized, ErrAlreadyInit).
	//
	// Use this to customize which errors should trip the circuit breaker.
	// Return true for errors that indicate transport/server failures,
	// false for errors that should not count (e.g., validation errors).
	//
	// Default: defaultShouldTrip (excludes context errors and client errors)
	ShouldTrip func(err error) bool

	// OnStateChange is called whenever the circuit breaker changes state.
	// The callback is invoked without holding the state machine lock, so it's
	// safe to perform logging, metrics collection, or other operations.
	//
	// The callback is called in a goroutine to prevent blocking state transitions.
	// Panics within the callback are recovered to prevent application crashes.
	//
	// Callbacks should not panic. Design callbacks to handle errors gracefully.
	//
	// Optional.
	OnStateChange func(from, to CircuitState)

	// OnFailure is called for each failure detected by the circuit breaker.
	// This is useful for metrics collection and logging.
	//
	// The callback is invoked without holding the state machine lock and is
	// called in a goroutine to prevent blocking.
	// Panics within the callback are recovered to prevent application crashes.
	//
	// Callbacks should not panic. Design callbacks to handle errors gracefully.
	//
	// Optional.
	OnFailure func(err error)
}

// WithCircuitBreaker wraps a Transport with circuit breaker protection.
//
// The circuit breaker implements the Transport interface and tracks failures
// in Send and Receive operations. When consecutive failures reach the configured
// threshold, the circuit "opens" and fails fast by rejecting requests without
// calling the underlying transport.
//
// After a timeout period, the circuit enters "half-open" state to test if the
// server has recovered. Successful test requests close the circuit and resume
// normal operation. Failed test requests reopen the circuit.
//
// The returned Transport is safe for concurrent use and implements the same
// interface as the wrapped transport.
//
// Example:
//
//	rawTransport := stdio.New(stdio.Config{Command: "mcp-server"})
//	transport := client.WithCircuitBreaker(rawTransport, client.CircuitBreakerConfig{
//	    MaxFailures: 5,
//	    OpenTimeout: 30 * time.Second,
//	    OnStateChange: func(from, to CircuitState) {
//	        log.Printf("Circuit breaker: %s → %s", from, to)
//	    },
//	})
//	c, err := client.New(transport, client.Options{...})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Circuit breaker can be combined with other transport wrappers:
//
//	// Combine with reconnect for automatic recovery
//	transport := client.WithCircuitBreaker(
//	    client.WithReconnect(baseTransport, reconnectCfg),
//	    circuitCfg,
//	)
func WithCircuitBreaker(tr Transport, cfg CircuitBreakerConfig) Transport {
	// Apply defaults
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxRequests <= 0 {
		cfg.HalfOpenMaxRequests = 1
	}
	if cfg.ShouldTrip == nil {
		cfg.ShouldTrip = defaultShouldTrip
	}

	return &circuitBreakerTransport{
		inner:               tr,
		cfg:                 cfg,
		state:               StateClosed,
		consecutiveFailures: 0,
		lastFailureTime:     time.Time{},
		halfOpenRequests:    0,
	}
}

// defaultShouldTrip is the default predicate for determining if an error should trip the circuit.
// It excludes context cancellation errors and client-side protocol errors.
func defaultShouldTrip(err error) bool {
	if err == nil {
		return false
	}

	// Don't count context cancellation as transport failure
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Don't count client-side protocol errors
	if errors.Is(err, ErrNotInitialized) || errors.Is(err, ErrAlreadyInit) {
		return false
	}

	// All other errors count as failures
	return true
}

// circuitBreakerTransport wraps a Transport with circuit breaker logic.
type circuitBreakerTransport struct {
	inner Transport
	cfg   CircuitBreakerConfig

	mu                  sync.RWMutex
	state               CircuitState
	consecutiveFailures int
	lastFailureTime     time.Time
	halfOpenRequests    int // Number of in-flight requests in Half-Open state
}

// Start establishes the connection to the MCP server.
//
// Circuit breaker does NOT track Start failures; only Send and Receive
// operations are monitored for circuit breaker behavior.
func (cb *circuitBreakerTransport) Start(ctx context.Context) error {
	return cb.inner.Start(ctx)
}

// Send writes a JSON-RPC message to the server, subject to circuit breaker state.
//
// If the circuit is open, returns ErrCircuitOpen without calling the underlying
// transport. If the circuit is closed or half-open (with capacity), delegates
// to the underlying transport and updates circuit state based on the result.
//
// Thread-safe for concurrent use.
func (cb *circuitBreakerTransport) Send(ctx context.Context, data []byte) error {
	// Check if request is allowed based on circuit state
	if err := cb.beforeRequest(); err != nil {
		return err
	}

	// Execute request on underlying transport
	err := cb.inner.Send(ctx, data)

	// Update circuit state based on result
	cb.afterRequest(err)

	return err
}

// Receive blocks until a JSON-RPC message is available, subject to circuit breaker state.
//
// If the circuit is open, returns ErrCircuitOpen without calling the underlying
// transport. If the circuit is closed or half-open (with capacity), delegates
// to the underlying transport and updates circuit state based on the result.
//
// Thread-safe for concurrent use with Send.
func (cb *circuitBreakerTransport) Receive(ctx context.Context) ([]byte, error) {
	// Check if request is allowed based on circuit state
	if err := cb.beforeRequest(); err != nil {
		return nil, err
	}

	// Execute request on underlying transport
	data, err := cb.inner.Receive(ctx)

	// Update circuit state based on result
	cb.afterRequest(err)

	return data, err
}

// Close shuts down the underlying transport.
func (cb *circuitBreakerTransport) Close() error {
	return cb.inner.Close()
}

// beforeRequest checks if the request should be allowed based on circuit state.
//
// Returns nil if the request is allowed, ErrCircuitOpen if rejected.
// Must be called before executing each request.
func (cb *circuitBreakerTransport) beforeRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		// Normal operation, allow request
		return nil

	case StateOpen:
		// Check if it's time to try recovery
		if time.Since(cb.lastFailureTime) >= cb.cfg.OpenTimeout {
			// Transition to Half-Open to test recovery
			cb.transitionTo(StateHalfOpen)
			cb.halfOpenRequests = 1
			return nil
		}
		// Still in timeout period, reject request to fail fast
		return ErrCircuitOpen

	case StateHalfOpen:
		// Allow limited number of test requests
		if cb.halfOpenRequests < cb.cfg.HalfOpenMaxRequests {
			cb.halfOpenRequests++
			return nil
		}
		// Too many concurrent requests in Half-Open, reject to prevent overload
		return ErrCircuitOpen

	default:
		// Unknown state, fail safe by rejecting
		return ErrCircuitOpen
	}
}

// afterRequest processes the request result and updates circuit state.
//
// Called after each request completes (success or failure).
// Must be called for every request that passed beforeRequest.
func (cb *circuitBreakerTransport) afterRequest(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check if error should trip circuit breaker
	shouldTrip := cb.cfg.ShouldTrip(err)

	if !shouldTrip {
		// Success or non-tripping error
		if cb.state == StateHalfOpen {
			// Only transition to Closed on actual success, not non-tripping errors
			if err == nil {
				// Successful test request verified server health, close circuit
				cb.transitionTo(StateClosed)
				cb.consecutiveFailures = 0
				cb.halfOpenRequests = 0
			}
			// else: non-tripping error in Half-Open, stay in Half-Open
			// Circuit will eventually timeout and try again, or another test
			// request will complete with success or failure
		} else if cb.state == StateClosed && err == nil {
			// Reset failure counter on success in Closed state
			cb.consecutiveFailures = 0
		}
		return
	}

	// Failure detected that should trip the circuit
	if cb.cfg.OnFailure != nil {
		// Call failure callback without lock to prevent deadlocks
		// Use goroutine to avoid blocking state transitions
		go func(e error) {
			defer func() {
				if r := recover(); r != nil {
					// Callback panicked, but don't crash the application
					// Silently recover to prevent callback bugs from affecting circuit breaker
					_ = r // Explicitly ignore recovered panic
				}
			}()
			cb.cfg.OnFailure(e)
		}(err)
	}

	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		// Increment failure counter
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.cfg.MaxFailures {
			// Threshold exceeded, trip circuit
			cb.transitionTo(StateOpen)
		}

	case StateHalfOpen:
		// Test request failed, reopen circuit
		cb.halfOpenRequests = 0
		cb.transitionTo(StateOpen)

	case StateOpen:
		// Already open, just record failure time
		// (for accurate timeout tracking)
	}
}

// transitionTo changes the circuit state and invokes the state change callback.
//
// Must be called with cb.mu held. The callback is invoked in a goroutine
// without holding the lock to prevent deadlocks.
func (cb *circuitBreakerTransport) transitionTo(newState CircuitState) {
	if cb.state == newState {
		return // No transition needed
	}

	oldState := cb.state
	cb.state = newState

	// Invoke state change callback without holding lock
	// Use goroutine to prevent blocking critical state transitions
	if cb.cfg.OnStateChange != nil {
		go func(from, to CircuitState) {
			defer func() {
				if r := recover(); r != nil {
					// Callback panicked, but don't crash the application
					// Silently recover to prevent callback bugs from affecting circuit breaker
					_ = r // Explicitly ignore recovered panic
				}
			}()
			cb.cfg.OnStateChange(from, to)
		}(oldState, newState)
	}
}

// State returns the current circuit breaker state.
//
// This method is useful for monitoring, debugging, and testing.
// Safe for concurrent use.
func (cb *circuitBreakerTransport) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// CircuitBreakerMetrics contains current circuit breaker metrics for monitoring.
type CircuitBreakerMetrics struct {
	// State is the current circuit breaker state
	State CircuitState

	// ConsecutiveFailures is the number of consecutive failures in Closed state
	ConsecutiveFailures int

	// LastFailureTime is when the most recent failure occurred
	LastFailureTime time.Time

	// HalfOpenRequests is the number of in-flight test requests in Half-Open state
	HalfOpenRequests int
}

// Metrics returns a snapshot of current circuit breaker metrics.
//
// This method is useful for monitoring, observability, and debugging.
// Safe for concurrent use.
func (cb *circuitBreakerTransport) Metrics() CircuitBreakerMetrics {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return CircuitBreakerMetrics{
		State:               cb.state,
		ConsecutiveFailures: cb.consecutiveFailures,
		LastFailureTime:     cb.lastFailureTime,
		HalfOpenRequests:    cb.halfOpenRequests,
	}
}
