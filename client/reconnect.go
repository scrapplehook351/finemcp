package client

import (
	"time"
)

// BackoffStrategy determines how long to wait between reconnection attempts.
type BackoffStrategy interface {
	// NextBackoff returns the duration to wait before the next reconnection attempt.
	// attempt is 0-indexed (0 = first retry).
	NextBackoff(attempt int) time.Duration

	// Reset clears any internal state, returning the strategy to its initial configuration.
	Reset()
}

// ReconnectConfig configures automatic reconnection behavior for the client.
type ReconnectConfig struct {
	// Enabled enables automatic reconnection when the transport connection fails.
	Enabled bool

	// MaxRetries is the maximum number of reconnection attempts. 0 means infinite retries.
	MaxRetries int

	// Strategy determines the backoff timing between reconnection attempts.
	// If nil, ExponentialBackoff(1*time.Second, 60*time.Second) is used.
	Strategy BackoffStrategy

	// OnReconnecting is called when a reconnection attempt is about to start.
	OnReconnecting func(attempt int, err error)

	// OnReconnected is called after a successful reconnection.
	OnReconnected func()

	// OnFailed is called when all reconnection attempts have been exhausted.
	OnFailed func(err error)
}

// exponentialBackoff implements exponential backoff with jitter.
type exponentialBackoff struct {
	initial time.Duration
	max     time.Duration
}

// ExponentialBackoff creates a backoff strategy that increases the wait time exponentially.
// initial is the first retry delay, max is the maximum delay between attempts.
func ExponentialBackoff(initial, max time.Duration) BackoffStrategy {
	if initial <= 0 {
		initial = 1 * time.Second
	}
	if max <= 0 {
		max = 60 * time.Second
	}
	return &exponentialBackoff{initial: initial, max: max}
}

// NextBackoff implements [BackoffStrategy] with exponential increase,
// capping at max. Uses bit-shift arithmetic to avoid float overflow.
func (b *exponentialBackoff) NextBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	// Cap attempt to prevent overflow (2^62 is last safe value)
	if attempt > 62 {
		return b.max
	}

	// Use bit shift instead of math.Pow to avoid float overflow
	multiplier := int64(1) << uint(attempt)
	backoff := time.Duration(multiplier) * b.initial

	// Check for overflow (negative duration means overflow occurred)
	if backoff < 0 || backoff > b.max {
		return b.max
	}

	return backoff
}

// Reset implements [BackoffStrategy]. exponentialBackoff is stateless; nothing to reset.
func (b *exponentialBackoff) Reset() {}

// linearBackoff implements a constant backoff interval.
type linearBackoff struct {
	interval time.Duration
}

// LinearBackoff creates a backoff strategy with a constant wait time between attempts.
func LinearBackoff(interval time.Duration) BackoffStrategy {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &linearBackoff{interval: interval}
}

// NextBackoff implements [BackoffStrategy], always returning the configured constant interval.
func (b *linearBackoff) NextBackoff(attempt int) time.Duration {
	return b.interval
}

// Reset implements [BackoffStrategy]. linearBackoff is stateless; nothing to reset.
func (b *linearBackoff) Reset() {}

// noBackoff implements immediate retry with no delay.
type noBackoff struct{}

// NoBackoff creates a backoff strategy that retries immediately with no delay.
func NoBackoff() BackoffStrategy {
	return &noBackoff{}
}

// NextBackoff implements [BackoffStrategy], always returning zero (immediate retry).
func (b *noBackoff) NextBackoff(attempt int) time.Duration {
	return 0
}

// Reset implements [BackoffStrategy]. noBackoff is stateless; nothing to reset.
func (b *noBackoff) Reset() {}
