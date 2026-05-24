package middleware

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Basic retry behavior ────────────────────────────────────────────

func TestRetry_SuccessOnFirstAttempt(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("ok"), nil
	}

	mw := Retry(WithMaxAttempts(3), withRetrySleep(func(_ context.Context, _ time.Duration) {}))
	wrapped := mw(handler)

	out, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 call, got %d", calls.Load())
	}
}

func TestRetry_SuccessAfterRetries(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := calls.Add(1)
		if c < 3 {
			return nil, errors.New("transient")
		}
		return []byte("ok"), nil
	}

	mw := Retry(WithMaxAttempts(5), withRetrySleep(func(_ context.Context, _ time.Duration) {}))
	wrapped := mw(handler)

	out, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
}

func TestRetry_ExhaustsAllAttempts(t *testing.T) {
	t.Parallel()

	failErr := errors.New("permanent")
	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, failErr
	}

	mw := Retry(WithMaxAttempts(4), withRetrySleep(func(_ context.Context, _ time.Duration) {}))
	wrapped := mw(handler)

	_, err := wrapped(context.Background(), nil)
	if !errors.Is(err, failErr) {
		t.Fatalf("expected failErr, got %v", err)
	}
	if calls.Load() != 4 {
		t.Errorf("expected 4 calls, got %d", calls.Load())
	}
}

func TestRetry_NoRetryWithMaxAttempts1(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	mw := Retry(WithMaxAttempts(1), withRetrySleep(func(_ context.Context, _ time.Duration) {}))
	wrapped := mw(handler)

	_, err := wrapped(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 call (no retry), got %d", calls.Load())
	}
}

// ── Backoff delay verification ──────────────────────────────────────

func TestRetry_ExponentialBackoff(t *testing.T) {
	t.Parallel()

	var delays []time.Duration
	var mu sync.Mutex
	sleepFn := func(_ context.Context, d time.Duration) {
		mu.Lock()
		delays = append(delays, d)
		mu.Unlock()
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(4),
		WithBaseDelay(100*time.Millisecond),
		WithMultiplier(2.0),
		WithJitter(0), // no jitter for deterministic test
		WithMaxDelay(10*time.Second),
		withRetrySleep(sleepFn),
	)
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	mu.Lock()
	defer mu.Unlock()

	// 4 attempts → 3 retries → 3 delays
	if len(delays) != 3 {
		t.Fatalf("expected 3 delays, got %d: %v", len(delays), delays)
	}

	// Expected: 100ms, 200ms, 400ms
	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
	}
	for i, want := range expected {
		if delays[i] != want {
			t.Errorf("delay[%d] = %v, want %v", i, delays[i], want)
		}
	}
}

func TestRetry_MaxDelayCap(t *testing.T) {
	t.Parallel()

	var delays []time.Duration
	var mu sync.Mutex
	sleepFn := func(_ context.Context, d time.Duration) {
		mu.Lock()
		delays = append(delays, d)
		mu.Unlock()
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(5),
		WithBaseDelay(1*time.Second),
		WithMultiplier(10.0),
		WithJitter(0),
		WithMaxDelay(5*time.Second),
		withRetrySleep(sleepFn),
	)
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	mu.Lock()
	defer mu.Unlock()

	for i, d := range delays {
		if d > 5*time.Second {
			t.Errorf("delay[%d] = %v exceeds max delay 5s", i, d)
		}
	}
}

func TestRetry_JitterAddsVariance(t *testing.T) {
	t.Parallel()

	var delays []time.Duration
	var mu sync.Mutex
	sleepFn := func(_ context.Context, d time.Duration) {
		mu.Lock()
		delays = append(delays, d)
		mu.Unlock()
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(11),
		WithBaseDelay(100*time.Millisecond),
		WithMultiplier(1.0), // no growth, just base
		WithJitter(0.5),     // 50% jitter
		WithMaxDelay(10*time.Second),
		withRetrySleep(sleepFn),
	)
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	mu.Lock()
	defer mu.Unlock()

	// With jitter, delays should be in [100ms, 150ms].
	// Check bounds and that not all delays are identical (very unlikely with 10 samples).
	allSame := true
	for i, d := range delays {
		if d < 100*time.Millisecond || d > 150*time.Millisecond {
			t.Errorf("delay[%d] = %v, expected [100ms, 150ms]", i, d)
		}
		if i > 0 && d != delays[0] {
			allSame = false
		}
	}
	if allSame && len(delays) > 5 {
		t.Error("all delays identical — jitter appears broken")
	}
}

// ── Custom retryable classifier ─────────────────────────────────────

func TestRetry_CustomIsRetryable(t *testing.T) {
	t.Parallel()

	permanentErr := errors.New("permanent")
	transientErr := errors.New("transient")

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := calls.Add(1)
		if c == 1 {
			return nil, transientErr
		}
		return nil, permanentErr
	}

	mw := Retry(
		WithMaxAttempts(5),
		WithRetryIsRetryable(func(err error) bool {
			return errors.Is(err, transientErr)
		}),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)

	_, err := wrapped(context.Background(), nil)
	if !errors.Is(err, permanentErr) {
		t.Fatalf("expected permanentErr, got %v", err)
	}
	// Call 1: transient (retried), Call 2: permanent (not retryable, stops).
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", calls.Load())
	}
}

func TestRetry_NilIsRetryable(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	// WithRetryIsRetryable(nil) should revert to default (all errors retryable).
	mw := Retry(
		WithMaxAttempts(3),
		WithRetryIsRetryable(nil),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
}

// ── Context cancellation ────────────────────────────────────────────

func TestRetry_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	ctx, cancel := context.WithCancel(context.Background())

	mw := Retry(
		WithMaxAttempts(10),
		withRetrySleep(func(_ context.Context, d time.Duration) {
			// Cancel context during first sleep.
			if calls.Load() == 1 {
				cancel()
			}
		}),
	)
	wrapped := mw(handler)

	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should stop after detecting cancellation — at most 1 call + cancelled before 2nd.
	if calls.Load() > 2 {
		t.Errorf("expected at most 2 calls, got %d", calls.Load())
	}
}

func TestRetry_AlreadyCancelledContext(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	mw := Retry(
		WithMaxAttempts(5),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)

	// First attempt runs (checking is done between retries), then stops.
	wrapped(ctx, nil)
	if calls.Load() != 1 {
		t.Errorf("expected 1 call with cancelled context, got %d", calls.Load())
	}
}

// ── OnRetry callback ────────────────────────────────────────────────

func TestRetry_OnRetryCallback(t *testing.T) {
	t.Parallel()

	type retryInfo struct {
		attempt int
		errMsg  string
		delay   time.Duration
	}

	var retries []retryInfo
	var mu sync.Mutex

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := calls.Add(1)
		if c < 3 {
			return nil, errors.New("transient")
		}
		return []byte("ok"), nil
	}

	mw := Retry(
		WithMaxAttempts(5),
		WithBaseDelay(50*time.Millisecond),
		WithMultiplier(2.0),
		WithJitter(0),
		WithOnRetry(func(_ context.Context, attempt int, err error, delay time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			retries = append(retries, retryInfo{attempt, err.Error(), delay})
		}),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)

	out, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(retries) != 2 {
		t.Fatalf("expected 2 retry callbacks, got %d", len(retries))
	}

	// Attempt numbers are 1-indexed.
	if retries[0].attempt != 1 || retries[0].errMsg != "transient" {
		t.Errorf("retry[0] = %+v", retries[0])
	}
	if retries[1].attempt != 2 || retries[1].errMsg != "transient" {
		t.Errorf("retry[1] = %+v", retries[1])
	}

	// Delays: 50ms, 100ms.
	if retries[0].delay != 50*time.Millisecond {
		t.Errorf("retry[0].delay = %v, want 50ms", retries[0].delay)
	}
	if retries[1].delay != 100*time.Millisecond {
		t.Errorf("retry[1].delay = %v, want 100ms", retries[1].delay)
	}
}

// ── Default configuration ───────────────────────────────────────────

func TestRetry_DefaultConfig(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	// Default: 3 attempts.
	mw := Retry(withRetrySleep(func(_ context.Context, _ time.Duration) {}))
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (default), got %d", calls.Load())
	}
}

// ── Edge cases ──────────────────────────────────────────────────────

func TestRetry_ZeroJitter(t *testing.T) {
	t.Parallel()

	var delays []time.Duration
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(3),
		WithBaseDelay(200*time.Millisecond),
		WithMultiplier(2.0),
		WithJitter(0),
		withRetrySleep(func(_ context.Context, d time.Duration) {
			mu.Lock()
			delays = append(delays, d)
			mu.Unlock()
		}),
	)
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	mu.Lock()
	defer mu.Unlock()

	// No jitter: delays should be exactly 200ms, 400ms.
	want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond}
	for i, w := range want {
		if delays[i] != w {
			t.Errorf("delay[%d] = %v, want %v", i, delays[i], w)
		}
	}
}

func TestRetry_HighMultiplier(t *testing.T) {
	t.Parallel()

	var delays []time.Duration
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(5),
		WithBaseDelay(1*time.Millisecond),
		WithMultiplier(100.0),
		WithJitter(0),
		WithMaxDelay(1*time.Second),
		withRetrySleep(func(_ context.Context, d time.Duration) {
			mu.Lock()
			delays = append(delays, d)
			mu.Unlock()
		}),
	)
	wrapped := mw(handler)
	wrapped(context.Background(), nil)

	mu.Lock()
	defer mu.Unlock()

	for _, d := range delays {
		if d > 1*time.Second {
			t.Errorf("delay %v exceeds max 1s", d)
		}
	}
}

func TestRetry_InvalidOptionsIgnored(t *testing.T) {
	t.Parallel()

	// Negative/zero values should be ignored.
	cfg := &retryConfig{
		maxAttempts: 3,
		baseDelay:   100 * time.Millisecond,
		maxDelay:    10 * time.Second,
		multiplier:  2.0,
		jitterFrac:  0.1,
	}

	WithMaxAttempts(0)(cfg)
	if cfg.maxAttempts != 3 {
		t.Errorf("maxAttempts changed to %d", cfg.maxAttempts)
	}

	WithMaxAttempts(-5)(cfg)
	if cfg.maxAttempts != 3 {
		t.Errorf("maxAttempts changed to %d", cfg.maxAttempts)
	}

	WithBaseDelay(0)(cfg)
	if cfg.baseDelay != 100*time.Millisecond {
		t.Errorf("baseDelay changed to %v", cfg.baseDelay)
	}

	WithBaseDelay(-1)(cfg)
	if cfg.baseDelay != 100*time.Millisecond {
		t.Errorf("baseDelay changed to %v", cfg.baseDelay)
	}

	WithMaxDelay(0)(cfg)
	if cfg.maxDelay != 10*time.Second {
		t.Errorf("maxDelay changed to %v", cfg.maxDelay)
	}

	WithMultiplier(0.5)(cfg)
	if cfg.multiplier != 2.0 {
		t.Errorf("multiplier changed to %v", cfg.multiplier)
	}

	WithJitter(-0.1)(cfg)
	if cfg.jitterFrac != 0.1 {
		t.Errorf("jitterFrac changed to %v", cfg.jitterFrac)
	}

	WithJitter(1.5)(cfg)
	if cfg.jitterFrac != 0.1 {
		t.Errorf("jitterFrac changed to %v", cfg.jitterFrac)
	}
}

// ── computeDelay unit tests ─────────────────────────────────────────

func TestComputeDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		base       time.Duration
		max        time.Duration
		multiplier float64
		jitter     float64
		iteration  int
		wantMin    time.Duration
		wantMax    time.Duration
	}{
		{
			name: "first retry, no jitter",
			base: 100 * time.Millisecond, max: 10 * time.Second,
			multiplier: 2.0, jitter: 0, iteration: 0,
			wantMin: 100 * time.Millisecond, wantMax: 100 * time.Millisecond,
		},
		{
			name: "second retry, no jitter",
			base: 100 * time.Millisecond, max: 10 * time.Second,
			multiplier: 2.0, jitter: 0, iteration: 1,
			wantMin: 200 * time.Millisecond, wantMax: 200 * time.Millisecond,
		},
		{
			name: "capped at max",
			base: 1 * time.Second, max: 2 * time.Second,
			multiplier: 10.0, jitter: 0, iteration: 2,
			wantMin: 2 * time.Second, wantMax: 2 * time.Second,
		},
		{
			name: "with jitter",
			base: 100 * time.Millisecond, max: 10 * time.Second,
			multiplier: 2.0, jitter: 0.5, iteration: 0,
			wantMin: 100 * time.Millisecond, wantMax: 150 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run multiple times for jitter tests.
			for i := 0; i < 100; i++ {
				d := computeDelay(tt.base, tt.max, tt.multiplier, tt.jitter, tt.iteration)
				if d < tt.wantMin || d > tt.wantMax {
					t.Errorf("computeDelay() = %v, want [%v, %v]", d, tt.wantMin, tt.wantMax)
					break
				}
			}
		})
	}
}

// ── Concurrent safety ───────────────────────────────────────────────

func TestRetry_Concurrent(t *testing.T) {
	t.Parallel()

	var totalCalls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := totalCalls.Add(1)
		// Every 3rd call succeeds globally, but per individual goroutine
		// it's unpredictable — that's fine, we just verify no panics/races.
		if c%3 == 0 {
			return []byte("ok"), nil
		}
		return nil, errors.New("fail")
	}

	mw := Retry(WithMaxAttempts(3), withRetrySleep(func(_ context.Context, _ time.Duration) {}))
	wrapped := mw(handler)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wrapped(context.Background(), nil)
		}()
	}
	wg.Wait()

	// Just verify it completed without panic/race.
	if totalCalls.Load() == 0 {
		t.Error("expected some calls")
	}
}

// ── Integration with server stack ───────────────────────────────────

func TestRetry_Integration(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := calls.Add(1)
		if c < 2 {
			return nil, errors.New("transient")
		}
		return []byte(`"success"`), nil
	}

	mw := Retry(
		WithMaxAttempts(3),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)

	out, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != `"success"` {
		t.Errorf("got %q, want %q", out, `"success"`)
	}
}

// ── Panic safety ────────────────────────────────────────────────────

func TestRetry_OnRetryCallbackPanic(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(3),
		WithOnRetry(func(_ context.Context, _ int, _ error, _ time.Duration) {
			panic("onRetry boom")
		}),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)

	// Should not panic.
	_, err := wrapped(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// All 3 attempts should run despite panicking callback.
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", calls.Load())
	}
}

func TestRetry_IsRetryablePanic(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("fail")
	}

	mw := Retry(
		WithMaxAttempts(5),
		WithRetryIsRetryable(func(error) bool {
			panic("classify boom")
		}),
		withRetrySleep(func(_ context.Context, _ time.Duration) {}),
	)
	wrapped := mw(handler)

	// Should not panic. Panic in isRetryable => treat as non-retryable => stop.
	_, err := wrapped(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should stop after first attempt (isRetryable panics => non-retryable).
	if calls.Load() != 1 {
		t.Errorf("expected 1 call (panic = non-retryable), got %d", calls.Load())
	}
}

// ── Context-aware sleep ─────────────────────────────────────────────

func TestRetry_ContextAwareSleep(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Use the real defaultRetrySleep (not overridden).
	mw := Retry(
		WithMaxAttempts(3),
		WithBaseDelay(5*time.Second), // Long delay
	)
	wrapped := mw(handler)

	done := make(chan struct{})
	go func() {
		defer close(done)
		wrapped(ctx, nil)
	}()

	// Cancel while sleeping.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Should return quickly (not wait 5s).
	select {
	case <-done:
		// OK — returned promptly.
	case <-time.After(2 * time.Second):
		t.Fatal("context-aware sleep did not unblock on cancellation")
	}
}
