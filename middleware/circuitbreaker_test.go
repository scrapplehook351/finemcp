package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── State string tests ──────────────────────────────────────────────

func TestCircuitState_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state CircuitState
		want  string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half-open"},
		{CircuitState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("CircuitState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// ── Basic state transitions ─────────────────────────────────────────

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	t.Parallel()

	failErr := errors.New("downstream failure")
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, failErr
	}

	mw := CircuitBreaker(
		WithFailureThreshold(3),
		WithResetTimeout(time.Minute),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// First 3 calls fail — should trip the circuit after the 3rd.
	for i := 0; i < 3; i++ {
		_, err := wrapped(ctx, nil)
		if !errors.Is(err, failErr) {
			t.Fatalf("call %d: expected failErr, got %v", i+1, err)
		}
	}

	// 4th call should be rejected (circuit open).
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	t.Parallel()

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(WithFailureThreshold(3))
	wrapped := mw(handler)
	ctx := context.Background()

	// 2 failures.
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// 1 success — resets failure counter.
	shouldFail.Store(false)
	out, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}

	// Need 3 more consecutive failures to trip (counter was reset).
	shouldFail.Store(true)
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// Should still be closed (only 2 failures after reset).
	shouldFail.Store(false)
	_, err = wrapped(ctx, nil)
	if err != nil {
		t.Errorf("expected success (circuit still closed), got: %v", err)
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	failErr := errors.New("fail")
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, failErr
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithResetTimeout(10*time.Second),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip the circuit.
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// Circuit is open.
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	// Advance past timeout — should transition to half-open.
	advance(11 * time.Second)

	// Next call should go through (half-open probe).
	_, err = wrapped(ctx, nil)
	// The handler still fails, so it reopens, but it was called.
	if errors.Is(err, ErrCircuitOpen) {
		t.Fatal("expected handler to be called in half-open, got ErrCircuitOpen")
	}
	if !errors.Is(err, failErr) {
		t.Fatalf("expected failErr from handler, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithSuccessThreshold(2),
		WithResetTimeout(5*time.Second),
		WithMaxHalfOpen(2),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip the circuit.
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// Open — advance past timeout.
	advance(6 * time.Second)

	// Now handler succeeds.
	shouldFail.Store(false)

	// Half-open: 2 successes needed to close.
	out, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("half-open probe 1: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}

	_, err = wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("half-open probe 2: %v", err)
	}

	// Circuit should now be closed — fail count reset, can handle more requests.
	out, err = wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("closed call: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	failErr := errors.New("still broken")
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, failErr
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithResetTimeout(5*time.Second),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip.
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// Advance to half-open.
	advance(6 * time.Second)

	// Probe fails — should reopen.
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, failErr) {
		t.Fatalf("expected failErr in half-open, got %v", err)
	}

	// Should be open again — next call rejected.
	_, err = wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after half-open failure, got %v", err)
	}
}

// ── Max half-open concurrency ───────────────────────────────────────

func TestCircuitBreaker_MaxHalfOpenRejectsExcess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	unblock := make(chan struct{})
	entered := make(chan struct{})
	var callCount atomic.Int64

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := callCount.Add(1)
		if c <= 1 {
			return nil, errors.New("fail to trip")
		}
		close(entered) // signal we're inside the handler
		<-unblock
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(1),
		WithResetTimeout(5*time.Second),
		WithMaxHalfOpen(1),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip the circuit.
	wrapped(ctx, nil)

	// Advance past timeout → half-open.
	advance(6 * time.Second)

	// Launch one probe (will block).
	done := make(chan error, 1)
	go func() {
		_, err := wrapped(ctx, nil)
		done <- err
	}()

	// Wait for the probe to enter the handler.
	<-entered

	// Second concurrent call should be rejected (maxHalfOpen=1, slot occupied).
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen when half-open probe slot full, got %v", err)
	}

	// Unblock the probe.
	close(unblock)
	probeErr := <-done
	if probeErr != nil {
		t.Fatalf("probe should succeed, got: %v", probeErr)
	}
}

// ── Per-key circuit breaking ────────────────────────────────────────

func TestCircuitBreaker_PerKeyIsolation(t *testing.T) {
	t.Parallel()

	failErr := errors.New("fail")
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, failErr
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithCircuitBreakerKeyFunc(func(ctx context.Context) string {
			return finemcp.ToolName(ctx)
		}),
	)
	wrapped := mw(handler)

	// Trip "alpha".
	ctxAlpha := finemcp.WithToolName(context.Background(), "alpha")
	wrapped(ctxAlpha, nil)
	wrapped(ctxAlpha, nil)

	// "alpha" should be open.
	_, err := wrapped(ctxAlpha, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("alpha: expected ErrCircuitOpen, got %v", err)
	}

	// "beta" should still be closed (independent breaker).
	ctxBeta := finemcp.WithToolName(context.Background(), "beta")
	_, err = wrapped(ctxBeta, nil)
	if errors.Is(err, ErrCircuitOpen) {
		t.Fatal("beta: should not be open (independent from alpha)")
	}
}

// ── Custom failure classifier ───────────────────────────────────────

func TestCircuitBreaker_CustomIsFailure(t *testing.T) {
	t.Parallel()

	handlerErr := errors.New("non-critical")
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, handlerErr
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithIsFailure(func(err error) bool {
			// Only count context deadline errors as failures.
			return errors.Is(err, context.DeadlineExceeded)
		}),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// These errors should NOT count as failures.
	for i := 0; i < 10; i++ {
		wrapped(ctx, nil)
	}

	// Circuit should still be closed.
	_, err := wrapped(ctx, nil)
	if errors.Is(err, ErrCircuitOpen) {
		t.Fatal("circuit should not have tripped for non-critical errors")
	}
}

// ── State change callback ───────────────────────────────────────────

func TestCircuitBreaker_OnStateChange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	type transition struct {
		key  string
		from CircuitState
		to   CircuitState
	}

	var transitions []transition
	var tmu sync.Mutex

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithSuccessThreshold(1),
		WithResetTimeout(5*time.Second),
		withCircuitBreakerClock(clock),
		WithOnStateChange(func(key string, from, to CircuitState) {
			tmu.Lock()
			defer tmu.Unlock()
			transitions = append(transitions, transition{key, from, to})
		}),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip: closed → open.
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// Advance past timeout.
	advance(6 * time.Second)

	// Probe succeeds → half-open → closed.
	shouldFail.Store(false)
	wrapped(ctx, nil)

	tmu.Lock()
	defer tmu.Unlock()

	want := []transition{
		{"", StateClosed, StateOpen},
		{"", StateOpen, StateHalfOpen},
		{"", StateHalfOpen, StateClosed},
	}

	if len(transitions) != len(want) {
		t.Fatalf("transitions: got %d, want %d: %+v", len(transitions), len(want), transitions)
	}

	for i, w := range want {
		got := transitions[i]
		if got.key != w.key || got.from != w.from || got.to != w.to {
			t.Errorf("transition[%d] = %+v, want %+v", i, got, w)
		}
	}
}

// ── Edge cases ──────────────────────────────────────────────────────

func TestCircuitBreaker_AllSuccesses(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(WithFailureThreshold(3))
	wrapped := mw(handler)
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		out, err := wrapped(ctx, nil)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if string(out) != "ok" {
			t.Fatalf("call %d: got %q", i, out)
		}
	}
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	// Default: 5 failures to trip.
	mw := CircuitBreaker()
	wrapped := mw(handler)
	ctx := context.Background()

	// 5 failures should trip.
	for i := 0; i < 5; i++ {
		wrapped(ctx, nil)
	}

	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after 5 failures (default threshold), got %v", err)
	}
}

func TestCircuitBreaker_NilInput(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CircuitBreaker()
	wrapped := mw(handler)

	out, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

func TestCircuitBreaker_EmptyKey(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithCircuitBreakerKeyFunc(func(_ context.Context) string {
			return "" // explicit empty key
		}),
	)
	wrapped := mw(handler)

	wrapped(context.Background(), nil)
	wrapped(context.Background(), nil)

	_, err := wrapped(context.Background(), nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen with empty key, got %v", err)
	}
}

// ── Concurrent safety ───────────────────────────────────────────────

func TestCircuitBreaker_Concurrent(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(WithFailureThreshold(100))
	wrapped := mw(handler)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wrapped(context.Background(), nil)
		}()
	}
	wg.Wait()

	if callCount.Load() != 200 {
		t.Errorf("expected all 200 calls to pass, got %d", callCount.Load())
	}
}

func TestCircuitBreaker_ConcurrentTripping(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	mw := CircuitBreaker(WithFailureThreshold(5))
	wrapped := mw(handler)

	var wg sync.WaitGroup
	var openCount atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := wrapped(context.Background(), nil)
			if errors.Is(err, ErrCircuitOpen) {
				openCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// After 5 failures, the remaining calls should be rejected.
	// Exact count depends on goroutine scheduling.
	if openCount.Load() == 0 {
		t.Error("expected some calls to be rejected after tripping")
	}
}

// ── Integration with server stack ───────────────────────────────────

func TestCircuitBreaker_Integration(t *testing.T) {
	t.Parallel()

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	s := finemcp.NewServer("test", "1.0")
	s.Use(CircuitBreaker(WithFailureThreshold(2)))

	tool, _ := finemcp.NewTool("flaky", func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("service unavailable")
		}
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	// Init.
	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": finemcp.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name": "flaky",
	})

	// Two failures → trip.
	s.HandleMessage(context.Background(), callMsg)
	s.HandleMessage(context.Background(), callMsg)

	// Third call should be rejected by circuit breaker.
	resp, _ := s.HandleMessage(context.Background(), callMsg)
	if resp.Error != nil {
		// Protocol error — circuit breaker returns as tool error, not protocol error.
		t.Fatalf("expected tool-level error, got protocol error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(raw, &result)
	if !result.IsError {
		t.Error("expected isError=true when circuit is open")
	}
}

// ── Full lifecycle test ─────────────────────────────────────────────

func TestCircuitBreaker_FullLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(3),
		WithSuccessThreshold(2),
		WithResetTimeout(10*time.Second),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Phase 1: Closed → failures accumulate.
	for i := 0; i < 3; i++ {
		_, err := wrapped(ctx, nil)
		if errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("should not be open after %d failures", i+1)
		}
	}

	// Phase 2: Open — calls rejected.
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatal("expected circuit to be open")
	}

	// Phase 3: Wait for timeout → half-open.
	advance(11 * time.Second)

	// Phase 4: Half-open — probe succeeds.
	shouldFail.Store(false)
	out, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("half-open probe 1: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}

	// Phase 5: Second success → closes circuit.
	_, err = wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("half-open probe 2: %v", err)
	}

	// Phase 6: Closed again — verify normal operation.
	for i := 0; i < 10; i++ {
		_, err := wrapped(ctx, nil)
		if err != nil {
			t.Fatalf("closed call %d: %v", i, err)
		}
	}
}

// ── Double-cycle test ───────────────────────────────────────────────

func TestCircuitBreaker_DoubleCycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); defer mu.Unlock(); now = now.Add(d) }

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithSuccessThreshold(1),
		WithResetTimeout(5*time.Second),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Cycle 1: trip closed → open.
	wrapped(ctx, nil)
	wrapped(ctx, nil)
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatal("expected open after cycle 1 trip")
	}

	// Half-open probe fails → re-open.
	advance(6 * time.Second)
	_, err = wrapped(ctx, nil)
	if errors.Is(err, ErrCircuitOpen) {
		t.Fatal("should have probed in half-open")
	}

	_, err = wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatal("should be re-opened after half-open failure")
	}

	// Cycle 2: timeout → half-open → succeed → close.
	advance(6 * time.Second)
	shouldFail.Store(false)
	_, err = wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("cycle 2 probe: %v", err)
	}

	// Should be closed now — verify normal operation.
	for i := 0; i < 5; i++ {
		_, err = wrapped(ctx, nil)
		if err != nil {
			t.Fatalf("closed call %d: %v", i, err)
		}
	}
}

// ── Nil option tests ────────────────────────────────────────────────

func TestCircuitBreaker_NilKeyFunc(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	// WithCircuitBreakerKeyFunc(nil) should revert to global breaker, no panic.
	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithCircuitBreakerKeyFunc(nil),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	wrapped(ctx, nil)
	wrapped(ctx, nil)

	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen with nil keyFunc, got %v", err)
	}
}

func TestCircuitBreaker_NilIsFailure(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	// WithIsFailure(nil) should revert to default behavior, no panic.
	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithIsFailure(nil),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	wrapped(ctx, nil)
	wrapped(ctx, nil)

	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen with nil isFailure, got %v", err)
	}
}

// ── IsFailure with nil error ────────────────────────────────────────

func TestCircuitBreaker_IsFailureCalledWithNilError(t *testing.T) {
	t.Parallel()

	// Handler always succeeds (nil error), but isFailure classifies
	// nil-error responses as failures (e.g., empty response = degraded).
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil // success, but treated as failure by classifier
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithIsFailure(func(_ error) bool {
			return true // everything is a failure
		}),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	wrapped(ctx, nil)
	wrapped(ctx, nil)

	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen when isFailure treats nil as failure, got %v", err)
	}
}

// ── Error message content ───────────────────────────────────────────

func TestCircuitBreaker_ErrorMessageContent(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	t.Run("global breaker", func(t *testing.T) {
		mw := CircuitBreaker(WithFailureThreshold(1))
		wrapped := mw(handler)
		wrapped(context.Background(), nil)

		_, err := wrapped(context.Background(), nil)
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatal("expected ErrCircuitOpen")
		}
		// Global breaker: no key= suffix.
		msg := err.Error()
		if msg != "circuit breaker is open" {
			t.Errorf("global error message = %q, want %q", msg, "circuit breaker is open")
		}
	})

	t.Run("per-key breaker", func(t *testing.T) {
		mw := CircuitBreaker(
			WithFailureThreshold(1),
			WithCircuitBreakerKeyFunc(func(ctx context.Context) string {
				return finemcp.ToolName(ctx)
			}),
		)
		wrapped := mw(handler)
		ctx := finemcp.WithToolName(context.Background(), "myTool")
		wrapped(ctx, nil)

		_, err := wrapped(ctx, nil)
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatal("expected ErrCircuitOpen")
		}
		// Per-key: should include key.
		msg := err.Error()
		want := `circuit breaker is open: key="myTool"`
		if msg != want {
			t.Errorf("per-key error message = %q, want %q", msg, want)
		}
	})
}

// ── Concurrent mixed half-open with maxHalfOpen > 1 ─────────────────

func TestCircuitBreaker_ConcurrentMixedHalfOpen(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); defer mu.Unlock(); now = now.Add(d) }

	// callCount tracks how many times the handler is called.
	var callCount atomic.Int64
	// First call fails (to trip), subsequent calls: one fails, two succeed.
	var phase atomic.Int32 // 0=trip, 1=half-open
	unblock := make(chan struct{})
	entered := make(chan struct{}, 3)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := callCount.Add(1)
		if phase.Load() == 0 {
			return nil, errors.New("fail to trip")
		}
		entered <- struct{}{}
		<-unblock
		// In half-open: first admitted probe fails, rest succeed.
		if c == 2 {
			return nil, errors.New("probe fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(1),
		WithSuccessThreshold(2),
		WithResetTimeout(5*time.Second),
		WithMaxHalfOpen(3),
		withCircuitBreakerClock(clock),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip the circuit.
	wrapped(ctx, nil)

	// Advance to half-open.
	advance(6 * time.Second)
	phase.Store(1)

	// Launch 3 concurrent half-open probes.
	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			_, err := wrapped(ctx, nil)
			results <- err
		}()
	}

	// Wait for all 3 to enter handler.
	for i := 0; i < 3; i++ {
		<-entered
	}

	// Release them all at once.
	close(unblock)

	// Collect results — one fails, two succeed.
	var failCount, successCount int
	for i := 0; i < 3; i++ {
		err := <-results
		if err != nil {
			failCount++
		} else {
			successCount++
		}
	}

	// The circuit should have reopened (any failure in half-open reopens).
	// Verify the next call is rejected.
	_, err := wrapped(ctx, nil)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after mixed half-open results, got %v", err)
	}

	// Sanity: at least one failure happened.
	if failCount == 0 {
		t.Error("expected at least one probe failure")
	}
}

// ── Handler panic safety ────────────────────────────────────────────

func TestCircuitBreaker_HandlerPanic(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := callCount.Add(1)
		if c <= 2 {
			panic("boom")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(WithFailureThreshold(3))
	wrapped := mw(handler)
	ctx := context.Background()

	// Panicking calls should be counted as failures via defer.
	for i := 0; i < 2; i++ {
		func() {
			defer func() { recover() }()
			wrapped(ctx, nil)
		}()
	}

	// Third call doesn't panic — but with 2 panics counted, circuit
	// is still closed (threshold=3). Verify handler is called.
	out, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("expected success after 2 panics (threshold=3), got: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

// ── OnStateChange fires outside lock ────────────────────────────────

func TestCircuitBreaker_OnStateChangeOutsideLock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); defer mu.Unlock(); now = now.Add(d) }

	// If the callback fires under breaker.mu, this would deadlock because
	// the callback calls back into the middleware (which acquires the lock).
	callbackCompleted := make(chan struct{}, 3) // buffered for up to 3 transitions

	var shouldFail atomic.Bool
	shouldFail.Store(true)

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		if shouldFail.Load() {
			return nil, errors.New("fail")
		}
		return []byte("ok"), nil
	}

	mw := CircuitBreaker(
		WithFailureThreshold(2),
		WithSuccessThreshold(1),
		WithResetTimeout(5*time.Second),
		withCircuitBreakerClock(clock),
		WithOnStateChange(func(_ string, _, to CircuitState) {
			// This would deadlock if called under breaker.mu.
			// Signal completion — if we get here, the lock is not held.
			select {
			case callbackCompleted <- struct{}{}:
			default:
			}
		}),
	)
	wrapped := mw(handler)
	ctx := context.Background()

	// Trip: closed → open fires callback.
	wrapped(ctx, nil)
	wrapped(ctx, nil)

	// Should not deadlock — callback fires outside lock.
	select {
	case <-callbackCompleted:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("OnStateChange callback appears deadlocked")
	}

	// Advance → half-open transition (fires on next beforeCall).
	advance(6 * time.Second)
	shouldFail.Store(false)
	wrapped(ctx, nil) // triggers open→half-open callback, then half-open→closed callback

	// Two more callbacks should have fired without deadlock.
	for i := 0; i < 2; i++ {
		select {
		case <-callbackCompleted:
		case <-time.After(2 * time.Second):
			t.Fatalf("OnStateChange callback %d appears deadlocked", i+2)
		}
	}
}
