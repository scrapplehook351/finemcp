package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Basic allow / deny ──────────────────────────────────────────────

func TestRateLimit_AllowWithinBurst(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

	mw := RateLimit(10, WithBurst(3), withClock(fixedClock()))
	wrapped := mw(handler)

	// Should allow exactly 3 calls (burst size).
	for i := 0; i < 3; i++ {
		_, err := wrapped(context.Background(), []byte("ok"))
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
}

func TestRateLimit_DenyAfterBurst(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

	mw := RateLimit(10, WithBurst(2), withClock(fixedClock()))
	wrapped := mw(handler)

	// Exhaust the burst.
	for i := 0; i < 2; i++ {
		wrapped(context.Background(), []byte("ok"))
	}

	// Third call should be rate limited.
	_, err := wrapped(context.Background(), []byte("ok"))
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got: %v", err)
	}
}

// ── Token refill ────────────────────────────────────────────────────

func TestRateLimit_RefillOverTime(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

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

	// 2 req/s, burst of 2.
	mw := RateLimit(2, WithBurst(2), withClock(clock))
	wrapped := mw(handler)

	// Use both tokens.
	wrapped(context.Background(), []byte("1"))
	wrapped(context.Background(), []byte("2"))

	// Immediately — should be denied.
	_, err := wrapped(context.Background(), []byte("3"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got: %v", err)
	}

	// Advance 600ms — should refill ~1.2 tokens → 1 call allowed.
	advance(600 * time.Millisecond)
	_, err = wrapped(context.Background(), []byte("4"))
	if err != nil {
		t.Fatalf("expected success after refill: %v", err)
	}

	// Immediately again — fractional token should not be enough.
	_, err = wrapped(context.Background(), []byte("5"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit after partial refill, got: %v", err)
	}
}

// ── Per-key bucketing ───────────────────────────────────────────────

func TestRateLimit_PerKeyBucketing(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

	mw := RateLimit(10, WithBurst(1), withClock(fixedClock()),
		WithKeyFunc(func(ctx context.Context) string {
			return finemcp.ToolName(ctx)
		}),
	)
	wrapped := mw(handler)

	// Tool "alpha" gets its own bucket.
	ctxAlpha := finemcp.WithToolName(context.Background(), "alpha")
	_, err := wrapped(ctxAlpha, []byte("a"))
	if err != nil {
		t.Fatalf("alpha call 1: %v", err)
	}

	// alpha exhausted → should be denied.
	_, err = wrapped(ctxAlpha, []byte("a"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("alpha call 2: expected rate limit, got: %v", err)
	}

	// Tool "beta" has a separate bucket → should succeed.
	ctxBeta := finemcp.WithToolName(context.Background(), "beta")
	_, err = wrapped(ctxBeta, []byte("b"))
	if err != nil {
		t.Fatalf("beta call 1: %v", err)
	}
}

// ── Edge cases ──────────────────────────────────────────────────────

func TestRateLimit_PanicOnZeroRate(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for rate <= 0")
		}
	}()
	RateLimit(0)
}

func TestRateLimit_PanicOnNegativeRate(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for negative rate")
		}
	}()
	RateLimit(-5)
}

func TestRateLimit_PanicOnNaNRate(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for NaN rate")
		}
	}()
	RateLimit(math.NaN())
}

func TestRateLimit_PanicOnInfRate(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for +Inf rate")
		}
	}()
	RateLimit(math.Inf(1))
}

func TestRateLimit_FractionalRate(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

	// 0.5 req/s → burst rounds up to 1.
	mw := RateLimit(0.5, withClock(fixedClock()))
	wrapped := mw(handler)

	_, err := wrapped(context.Background(), []byte("ok"))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call should be denied immediately.
	_, err = wrapped(context.Background(), []byte("no"))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected rate limit, got: %v", err)
	}
}

func TestRateLimit_BurstMinIsOne(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

	// Even with burst(0) override, should floor to 1.
	mw := RateLimit(100, WithBurst(0), withClock(fixedClock()))
	wrapped := mw(handler)

	_, err := wrapped(context.Background(), []byte("ok"))
	if err != nil {
		t.Fatalf("expected at least 1 burst: %v", err)
	}
}

// ── Concurrent safety ───────────────────────────────────────────────

func TestRateLimit_Concurrent(t *testing.T) {
	t.Parallel()
	handler := echoHandler()

	mw := RateLimit(1000, WithBurst(100), withClock(fixedClock()))
	wrapped := mw(handler)

	var allowed atomic.Int64
	var denied atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := wrapped(context.Background(), []byte("x"))
			if err == nil {
				allowed.Add(1)
			} else {
				denied.Add(1)
			}
		}()
	}
	wg.Wait()

	if allowed.Load() != 100 {
		t.Errorf("allowed = %d, want 100", allowed.Load())
	}
	if denied.Load() != 100 {
		t.Errorf("denied = %d, want 100", denied.Load())
	}
}

// ── Integration with server stack ───────────────────────────────────

func TestRateLimit_Integration(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(RateLimit(100, WithBurst(2), withClock(fixedClock())))

	tool, _ := finemcp.NewTool("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("pong"), nil
	})
	s.RegisterTool(tool)

	// Init.
	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": finemcp.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	// First two calls succeed (burst=2).
	for i := 0; i < 2; i++ {
		callMsg := jsonrpcReq(i+2, "tools/call", map[string]any{
			"name": "ping",
		})
		resp, _ := s.HandleMessage(context.Background(), callMsg)
		if resp.Error != nil {
			t.Fatalf("call %d: unexpected error: %s", i, resp.Error.Message)
		}
	}

	// Third call should produce a tool error (isError=true), not a protocol error.
	callMsg := jsonrpcReq(10, "tools/call", map[string]any{
		"name": "ping",
	})
	resp, _ := s.HandleMessage(context.Background(), callMsg)
	if resp.Error != nil {
		t.Fatalf("expected tool-level error, got protocol error: %s", resp.Error.Message)
	}
	// The result should have isError=true.
	raw, _ := json.Marshal(resp.Result)
	var result struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(raw, &result)
	if !result.IsError {
		t.Error("expected isError=true in rate-limited response")
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

func fixedClock() func() time.Time {
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func echoHandler() finemcp.ToolHandler {
	return func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	}
}

// jsonrpcReq builds a valid JSON-RPC request byte slice.
func jsonrpcReq(id any, method string, params any) []byte {
	m := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	data, _ := json.Marshal(m)
	return data
}
