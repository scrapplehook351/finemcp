package middleware

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Helpers ─────────────────────────────────────────────────────────

func costCtx(tool string) context.Context {
	return finemcp.WithToolName(context.Background(), tool)
}

// ── Basic cost tracking behaviour ───────────────────────────────────

func TestCostTracking_SuccessfulCall(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	collector := &InMemoryCostCollector{}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("result"), nil
	}

	mw := CostTracking(
		WithCostCollector(collector),
		withCostClock(func() time.Time { return now }),
	)
	wrapped := mw(handler)

	out, err := wrapped(costCtx("test-tool"), []byte(`{"q":"hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "result" {
		t.Errorf("got %q, want %q", out, "result")
	}

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.ToolName != "test-tool" {
		t.Errorf("ToolName = %q, want %q", r.ToolName, "test-tool")
	}
	if !r.Success {
		t.Error("expected Success=true")
	}
	if r.InputSize != len(`{"q":"hello"}`) {
		t.Errorf("InputSize = %d, want %d", r.InputSize, len(`{"q":"hello"}`))
	}
	if r.OutputSize != 6 {
		t.Errorf("OutputSize = %d, want 6", r.OutputSize)
	}
	if r.Cost != 0 {
		t.Errorf("Cost = %v, want 0 (no cost function)", r.Cost)
	}
}

func TestCostTracking_FailedCall(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	testErr := errors.New("tool error")

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, testErr
	}

	mw := CostTracking(
		WithCostCollector(collector),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	_, err := wrapped(costCtx("fail-tool"), []byte("input"))
	if !errors.Is(err, testErr) {
		t.Fatalf("expected testErr, got %v", err)
	}

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Success {
		t.Error("expected Success=false")
	}
	if records[0].OutputSize != 0 {
		t.Errorf("OutputSize = %d, want 0", records[0].OutputSize)
	}
}

// ── No collector → pass-through ─────────────────────────────────────

func TestCostTracking_NoCollector(t *testing.T) {
	t.Parallel()

	var called bool
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	}

	mw := CostTracking() // no collector
	wrapped := mw(handler)

	out, err := wrapped(costCtx("tool"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler not called")
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

// ── Cost function ───────────────────────────────────────────────────

func TestCostTracking_DefaultCostFunc(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}

	costFn := func(r CostRecord) CostRecord {
		r.Cost = float64(r.InputSize+r.OutputSize) * 0.001
		r.CostUnit = "USD"
		return r
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return make([]byte, 1000), nil
	}

	mw := CostTracking(
		WithCostCollector(collector),
		WithDefaultCostFunc(costFn),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("tool"), make([]byte, 500))

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	// 500 (input) + 1000 (output) = 1500 * 0.001 = 1.5
	if math.Abs(r.Cost-1.5) > 1e-9 {
		t.Errorf("Cost = %v, want 1.5", r.Cost)
	}
	if r.CostUnit != "USD" {
		t.Errorf("CostUnit = %q, want %q", r.CostUnit, "USD")
	}
}

func TestCostTracking_PerToolCostFunc(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}

	defaultFn := func(r CostRecord) CostRecord {
		r.Cost = 1.0
		r.CostUnit = "default"
		return r
	}

	expensiveFn := func(r CostRecord) CostRecord {
		r.Cost = 100.0
		r.CostUnit = "credits"
		return r
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		WithDefaultCostFunc(defaultFn),
		WithToolCostFunc("expensive-tool", expensiveFn),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("normal-tool"), nil)
	wrapped(costCtx("expensive-tool"), nil)

	records := collector.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if records[0].Cost != 1.0 || records[0].CostUnit != "default" {
		t.Errorf("normal-tool: cost=%v, unit=%q", records[0].Cost, records[0].CostUnit)
	}
	if records[1].Cost != 100.0 || records[1].CostUnit != "credits" {
		t.Errorf("expensive-tool: cost=%v, unit=%q", records[1].Cost, records[1].CostUnit)
	}
}

func TestCostTracking_CostFuncMetadata(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}

	costFn := func(r CostRecord) CostRecord {
		r.Cost = 42.0
		r.Metadata = map[string]any{
			"model":       "gpt-4",
			"inputTokens": 100,
		}
		return r
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		WithDefaultCostFunc(costFn),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("tool"), nil)

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Metadata == nil {
		t.Fatal("expected non-nil Metadata")
	}
	if r.Metadata["model"] != "gpt-4" {
		t.Errorf("Metadata[model] = %v", r.Metadata["model"])
	}
	if r.Metadata["inputTokens"] != 100 {
		t.Errorf("Metadata[inputTokens] = %v", r.Metadata["inputTokens"])
	}
}

// ── Inclusion/Exclusion filters ─────────────────────────────────────

func TestCostTracking_IncludeTools(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		WithCostIncludeTools("a", "b"),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("a"), nil)
	wrapped(costCtx("b"), nil)
	wrapped(costCtx("c"), nil)

	if collector.Len() != 2 {
		t.Errorf("expected 2 tracked calls, got %d", collector.Len())
	}
}

func TestCostTracking_ExcludeTools(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		WithCostExcludeTools("noisy-tool"),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("normal-tool"), nil)
	wrapped(costCtx("noisy-tool"), nil)
	wrapped(costCtx("another-tool"), nil)

	if collector.Len() != 2 {
		t.Errorf("expected 2 tracked calls (noisy-tool excluded), got %d", collector.Len())
	}
}

func TestCostTracking_IncludeOverridesExclude(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		WithCostIncludeTools("a"),
		WithCostExcludeTools("a"),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("a"), nil)
	wrapped(costCtx("b"), nil)

	if collector.Len() != 1 {
		t.Errorf("expected 1 tracked call (include overrides exclude), got %d", collector.Len())
	}
}

// ── Collector panic recovery ────────────────────────────────────────

func TestCostTracking_CollectorPanicRecovery(t *testing.T) {
	t.Parallel()

	panicCollector := CostCollectorFunc(func(_ context.Context, _ CostRecord) {
		panic("collector boom")
	})

	var capturedErr error
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CostTracking(
		WithCostCollector(panicCollector),
		WithCostOnError(func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	out, err := wrapped(costCtx("tool"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}

	mu.Lock()
	defer mu.Unlock()
	if capturedErr == nil {
		t.Fatal("expected onError to be called")
	}
	if capturedErr.Error() != "cost collector panic: collector boom" {
		t.Errorf("unexpected error: %v", capturedErr)
	}
}

func TestCostTracking_CollectorPanicNoCallback(t *testing.T) {
	t.Parallel()

	panicCollector := CostCollectorFunc(func(_ context.Context, _ CostRecord) {
		panic("boom")
	})

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CostTracking(
		WithCostCollector(panicCollector),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	// Should not panic.
	out, err := wrapped(costCtx("tool"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

func TestCostTracking_CollectorPanicWithErrorValue(t *testing.T) {
	t.Parallel()

	panicErr := errors.New("collector failure")
	panicCollector := CostCollectorFunc(func(_ context.Context, _ CostRecord) {
		panic(panicErr)
	})

	var capturedErr error
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(panicCollector),
		WithCostOnError(func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)
	wrapped(costCtx("tool"), nil)

	mu.Lock()
	defer mu.Unlock()
	if capturedErr == nil {
		t.Fatal("expected onError to be called")
	}
	if !errors.Is(capturedErr, panicErr) {
		t.Errorf("expected panicErr, got %v", capturedErr)
	}
}

// ── Request ID propagation ──────────────────────────────────────────

func TestCostTracking_RequestID(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "tool")
	ctx = finemcp.WithRequestID(ctx, "req-99")

	wrapped(ctx, nil)

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RequestID != "req-99" {
		t.Errorf("RequestID = %v, want %q", records[0].RequestID, "req-99")
	}
}

// ── Duration tracking ───────────────────────────────────────────────

func TestCostTracking_DurationTracking(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}

	var callIdx atomic.Int64
	clockFn := func() time.Time {
		idx := callIdx.Add(1)
		return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Add(
			time.Duration(idx-1) * 250 * time.Millisecond,
		)
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		withCostClock(clockFn),
	)
	wrapped := mw(handler)
	wrapped(costCtx("tool"), nil)

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Duration != 250*time.Millisecond {
		t.Errorf("Duration = %v, want 250ms", records[0].Duration)
	}
}

// ── Concurrent safety ───────────────────────────────────────────────

func TestCostTracking_Concurrent(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CostTracking(
		WithCostCollector(collector),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wrapped(costCtx("tool"), []byte("data"))
		}()
	}
	wg.Wait()

	if collector.Len() != 100 {
		t.Errorf("expected 100 cost records, got %d", collector.Len())
	}
}

// ── InMemoryCostCollector tests ─────────────────────────────────────

func TestInMemoryCostCollector_TotalCost(t *testing.T) {
	t.Parallel()

	c := &InMemoryCostCollector{}
	c.Collect(context.Background(), CostRecord{Cost: 1.5})
	c.Collect(context.Background(), CostRecord{Cost: 2.5})
	c.Collect(context.Background(), CostRecord{Cost: 3.0})

	total := c.TotalCost()
	if math.Abs(total-7.0) > 1e-9 {
		t.Errorf("TotalCost() = %v, want 7.0", total)
	}
}

func TestInMemoryCostCollector_Reset(t *testing.T) {
	t.Parallel()

	c := &InMemoryCostCollector{}
	c.Collect(context.Background(), CostRecord{Cost: 1.0})
	c.Collect(context.Background(), CostRecord{Cost: 2.0})

	if c.Len() != 2 {
		t.Fatalf("expected 2, got %d", c.Len())
	}

	c.Reset()
	if c.Len() != 0 {
		t.Errorf("expected 0 after reset, got %d", c.Len())
	}
	if c.TotalCost() != 0 {
		t.Errorf("expected 0 total after reset, got %v", c.TotalCost())
	}
}

func TestInMemoryCostCollector_RecordsAreCopy(t *testing.T) {
	t.Parallel()

	c := &InMemoryCostCollector{}
	c.Collect(context.Background(), CostRecord{ToolName: "original"})

	records := c.Records()
	records[0].ToolName = "mutated"

	fresh := c.Records()
	if fresh[0].ToolName != "original" {
		t.Errorf("Records() returned a reference, not a copy")
	}
}

func TestCostCollectorFunc(t *testing.T) {
	t.Parallel()

	var captured CostRecord
	fn := CostCollectorFunc(func(_ context.Context, r CostRecord) {
		captured = r
	})

	fn.Collect(context.Background(), CostRecord{ToolName: "test", Cost: 5.0})

	if captured.ToolName != "test" || captured.Cost != 5.0 {
		t.Errorf("captured = %+v", captured)
	}
}

// ── Nil cost function → no cost computed ────────────────────────────

func TestCostTracking_NilPerToolCostFunc(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := CostTracking(
		WithCostCollector(collector),
		WithDefaultCostFunc(func(r CostRecord) CostRecord {
			r.Cost = 99.0
			return r
		}),
		WithToolCostFunc("special", nil), // nil per-tool func
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	wrapped(costCtx("special"), nil)

	records := collector.Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	// nil per-tool func should fall through to default.
	if records[0].Cost != 99.0 {
		t.Errorf("Cost = %v, want 99.0 (default fallthrough)", records[0].Cost)
	}
}

// ── CostFunc panic recovery ─────────────────────────────────────────

func TestCostTracking_CostFuncPanic(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	var capturedErr error
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CostTracking(
		WithCostCollector(collector),
		WithDefaultCostFunc(func(r CostRecord) CostRecord {
			panic("cost boom")
		}),
		WithCostOnError(func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	// Should not panic.
	out, err := wrapped(costCtx("tool"), []byte("input"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}

	mu.Lock()
	defer mu.Unlock()
	if capturedErr == nil {
		t.Fatal("expected onError to be called for CostFunc panic")
	}

	// Record should still be collected (without cost).
	if collector.Len() != 1 {
		t.Fatalf("expected 1 record, got %d", collector.Len())
	}
	if collector.Records()[0].Cost != 0 {
		t.Errorf("Cost = %v, want 0 (panic => unchanged record)", collector.Records()[0].Cost)
	}
}

func TestCostTracking_PerToolCostFuncPanic(t *testing.T) {
	t.Parallel()

	collector := &InMemoryCostCollector{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := CostTracking(
		WithCostCollector(collector),
		WithToolCostFunc("bad-tool", func(r CostRecord) CostRecord {
			panic("per-tool boom")
		}),
		withCostClock(func() time.Time { return time.Now() }),
	)
	wrapped := mw(handler)

	// Should not panic.
	out, err := wrapped(costCtx("bad-tool"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

// ── Metadata deep copy ─────────────────────────────────────────────

func TestInMemoryCostCollector_MetadataDeepCopy(t *testing.T) {
	t.Parallel()

	c := &InMemoryCostCollector{}
	c.Collect(context.Background(), CostRecord{
		ToolName: "x",
		Metadata: map[string]any{"key": "original"},
	})

	records := c.Records()
	records[0].Metadata["key"] = "mutated"

	fresh := c.Records()
	if fresh[0].Metadata["key"] != "original" {
		t.Error("Metadata not deep-copied — mutation leaked through")
	}
}
