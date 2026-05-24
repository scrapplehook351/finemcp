package client

import (
	"testing"
	"time"
)

func TestExponentialBackoff_NextBackoff(t *testing.T) {
	tests := []struct {
		name     string
		initial  time.Duration
		max      time.Duration
		attempt  int
		expected time.Duration
	}{
		{
			name:     "first attempt",
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			attempt:  1,
			expected: 200 * time.Millisecond, // 2^1 * 100ms
		},
		{
			name:     "second attempt",
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			attempt:  2,
			expected: 400 * time.Millisecond, // 2^2 * 100ms
		},
		{
			name:     "third attempt",
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			attempt:  3,
			expected: 800 * time.Millisecond, // 2^3 * 100ms
		},
		{
			name:     "capped at max",
			initial:  1 * time.Second,
			max:      5 * time.Second,
			attempt:  10,
			expected: 5 * time.Second, // Would be 1024s, but capped at 5s
		},
		{
			name:     "zero attempt",
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			attempt:  0,
			expected: 100 * time.Millisecond, // 2^0 * 100ms = 100ms
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff := ExponentialBackoff(tt.initial, tt.max)

			got := backoff.NextBackoff(tt.attempt)
			if got != tt.expected {
				t.Errorf("NextBackoff(%d) = %v, want %v", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestExponentialBackoff_Reset(t *testing.T) {
	backoff := ExponentialBackoff(100*time.Millisecond, 10*time.Second)

	// Make some calculations to advance state (if any)
	backoff.NextBackoff(5)

	// Reset should clear any internal state
	backoff.Reset()

	// After reset, NextBackoff(1) should give same result as fresh backoff
	got := backoff.NextBackoff(1)
	expected := 200 * time.Millisecond // 2^1 * 100ms
	if got != expected {
		t.Errorf("after Reset(), NextBackoff(1) = %v, want %v", got, expected)
	}
}

func TestExponentialBackoff_Overflow(t *testing.T) {
	backoff := ExponentialBackoff(1*time.Second, 1*time.Hour)

	// Attempt 100 would cause 2^100 overflow if not handled
	got := backoff.NextBackoff(100)

	// Should be capped at Max
	if got != 1*time.Hour {
		t.Errorf("NextBackoff(100) = %v, want %v (should be capped at Max)", got, 1*time.Hour)
	}
}

func TestLinearBackoff_NextBackoff(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		attempt  int
		expected time.Duration
	}{
		{
			name:     "first attempt",
			interval: 500 * time.Millisecond,
			attempt:  1,
			expected: 500 * time.Millisecond,
		},
		{
			name:     "tenth attempt",
			interval: 500 * time.Millisecond,
			attempt:  10,
			expected: 500 * time.Millisecond, // Always constant
		},
		{
			name:     "zero attempt",
			interval: 500 * time.Millisecond,
			attempt:  0,
			expected: 500 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff := LinearBackoff(tt.interval)

			got := backoff.NextBackoff(tt.attempt)
			if got != tt.expected {
				t.Errorf("NextBackoff(%d) = %v, want %v", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestLinearBackoff_Reset(t *testing.T) {
	backoff := LinearBackoff(500 * time.Millisecond)

	backoff.NextBackoff(10)
	backoff.Reset()

	// Linear backoff should still return same interval
	got := backoff.NextBackoff(1)
	expected := 500 * time.Millisecond
	if got != expected {
		t.Errorf("after Reset(), NextBackoff(1) = %v, want %v", got, expected)
	}
}

func TestNoBackoff_NextBackoff(t *testing.T) {
	backoff := NoBackoff()

	attempts := []int{0, 1, 10, 100}
	for _, attempt := range attempts {
		got := backoff.NextBackoff(attempt)
		if got != 0 {
			t.Errorf("NoBackoff.NextBackoff(%d) = %v, want 0", attempt, got)
		}
	}
}

func TestNoBackoff_Reset(t *testing.T) {
	backoff := NoBackoff()

	backoff.NextBackoff(10)
	backoff.Reset()

	got := backoff.NextBackoff(1)
	if got != 0 {
		t.Errorf("after Reset(), NextBackoff(1) = %v, want 0", got)
	}
}

func TestBackoffStrategy_Interface(t *testing.T) {
	// Verify all backoff types implement BackoffStrategy interface
	var _ BackoffStrategy = ExponentialBackoff(1*time.Second, 10*time.Second)
	var _ BackoffStrategy = LinearBackoff(5 * time.Second)
	var _ BackoffStrategy = NoBackoff()
}

func TestExponentialBackoff_DefaultValues(t *testing.T) {
	// Zero/negative values should be replaced with defaults
	backoff := ExponentialBackoff(0, 0)

	got := backoff.NextBackoff(1)
	// Should use defaults (1s initial, 60s max)
	expected := 2 * time.Second // 2^1 * 1s
	if got != expected {
		t.Errorf("NextBackoff(1) with zero values = %v, want %v (default behavior)", got, expected)
	}
}

func TestExponentialBackoff_NegativeAttempt(t *testing.T) {
	backoff := ExponentialBackoff(100*time.Millisecond, 10*time.Second)

	// Negative attempt should behave reasonably
	got := backoff.NextBackoff(-1)

	// 2^-1 = 0.5, so 50ms is expected, but it might clamp to 0
	if got < 0 {
		t.Errorf("NextBackoff(-1) = %v, should not be negative", got)
	}
}

func TestExponentialBackoff_MaxLessThanInitial(t *testing.T) {
	backoff := ExponentialBackoff(10*time.Second, 1*time.Second)

	// Even on first attempt, should be capped at Max
	got := backoff.NextBackoff(1)
	if got > 1*time.Second {
		t.Errorf("NextBackoff(1) = %v, want <= 1s (capped at Max)", got)
	}
}

func TestLinearBackoff_ZeroInterval(t *testing.T) {
	// Zero/negative interval should be replaced with default
	backoff := LinearBackoff(0)

	got := backoff.NextBackoff(1)
	// Should use default (5s)
	expected := 5 * time.Second
	if got != expected {
		t.Errorf("NextBackoff(1) with zero interval = %v, want %v (default)", got, expected)
	}
}

func TestReconnectConfig_DefaultStrategy(t *testing.T) {
	config := &ReconnectConfig{
		Enabled:  true,
		Strategy: nil,
	}

	// Nil strategy should be handled gracefully by reconnect logic
	if !config.Enabled {
		t.Error("expected Enabled=true")
	}
	if config.Strategy != nil {
		t.Error("expected nil Strategy for test setup")
	}
}

func TestReconnectConfig_AllStrategies(t *testing.T) {
	strategies := []struct {
		name     string
		strategy BackoffStrategy
	}{
		{
			name:     "ExponentialBackoff",
			strategy: ExponentialBackoff(100*time.Millisecond, 10*time.Second),
		},
		{
			name:     "LinearBackoff",
			strategy: LinearBackoff(500 * time.Millisecond),
		},
		{
			name:     "NoBackoff",
			strategy: NoBackoff(),
		},
	}

	for _, tt := range strategies {
		t.Run(tt.name, func(t *testing.T) {
			config := &ReconnectConfig{
				Enabled:  true,
				Strategy: tt.strategy,
			}

			// Verify strategy is set correctly
			if config.Strategy != tt.strategy {
				t.Error("strategy not set correctly")
			}

			// Verify strategy can calculate delays
			delay := config.Strategy.NextBackoff(1)
			if delay < 0 {
				t.Errorf("negative delay: %v", delay)
			}
		})
	}
}

func TestExponentialBackoff_LargeAttempts(t *testing.T) {
	backoff := ExponentialBackoff(1*time.Millisecond, 1*time.Minute)

	// Test with very large attempt numbers
	largeAttempts := []int{50, 100, 1000}
	for _, attempt := range largeAttempts {
		got := backoff.NextBackoff(attempt)

		// Should be capped at Max
		if got > 1*time.Minute {
			t.Errorf("NextBackoff(%d) = %v, want <= 1m (capped at Max)", attempt, got)
		}

		// Should equal Max (not overflow to negative or zero)
		if got != 1*time.Minute {
			t.Errorf("NextBackoff(%d) = %v, want exactly 1m", attempt, got)
		}
	}
}

func TestBackoffStrategies_Comparison(t *testing.T) {
	// Compare different strategies over 5 attempts
	exp := ExponentialBackoff(100*time.Millisecond, 10*time.Second)
	lin := LinearBackoff(500 * time.Millisecond)
	none := NoBackoff()

	for attempt := 1; attempt <= 5; attempt++ {
		expDelay := exp.NextBackoff(attempt)
		linDelay := lin.NextBackoff(attempt)
		noneDelay := none.NextBackoff(attempt)

		t.Logf("Attempt %d: Exponential=%v, Linear=%v, None=%v",
			attempt, expDelay, linDelay, noneDelay)

		// NoBackoff should always be 0
		if noneDelay != 0 {
			t.Errorf("attempt %d: NoBackoff = %v, want 0", attempt, noneDelay)
		}

		// Linear should always be constant
		if linDelay != 500*time.Millisecond {
			t.Errorf("attempt %d: LinearBackoff = %v, want 500ms", attempt, linDelay)
		}

		// Exponential should grow (unless capped)
		if attempt > 1 {
			prevExpDelay := exp.NextBackoff(attempt - 1)
			if expDelay < prevExpDelay {
				t.Errorf("attempt %d: Exponential not growing (%v vs %v)", attempt, expDelay, prevExpDelay)
			}
		}
	}
}
