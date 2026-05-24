package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Helpers ─────────────────────────────────────────────────────────

func fixedAuditClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func auditCtx(tool string) context.Context {
	return finemcp.WithToolName(context.Background(), tool)
}

// ── Basic audit behaviour ───────────────────────────────────────────

func TestAuditLog_SuccessfulCall(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	sink := &InMemoryAuditSink{}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("hello"), nil
	}

	mw := AuditLog(
		WithAuditSink(sink),
		withAuditClock(fixedAuditClock(now)),
	)
	wrapped := mw(handler)

	out, err := wrapped(auditCtx("test-tool"), []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "hello" {
		t.Errorf("got %q, want %q", out, "hello")
	}

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.ToolName != "test-tool" {
		t.Errorf("ToolName = %q, want %q", e.ToolName, "test-tool")
	}
	if !e.Success {
		t.Error("expected Success=true")
	}
	if e.ErrorMessage != "" {
		t.Errorf("expected empty ErrorMessage, got %q", e.ErrorMessage)
	}
	if e.InputSize != len(`{"x":1}`) {
		t.Errorf("InputSize = %d, want %d", e.InputSize, len(`{"x":1}`))
	}
	if e.OutputSize != 5 {
		t.Errorf("OutputSize = %d, want 5", e.OutputSize)
	}
	if !e.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, now)
	}
	if e.Duration != 0 {
		t.Errorf("Duration = %v, want 0 (fixed clock)", e.Duration)
	}

	// Verify input hash.
	h := sha256.Sum256([]byte(`{"x":1}`))
	wantHash := hex.EncodeToString(h[:])
	if e.InputHash != wantHash {
		t.Errorf("InputHash = %q, want %q", e.InputHash, wantHash)
	}
}

func TestAuditLog_FailedCall(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	testErr := errors.New("tool error")

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, testErr
	}

	mw := AuditLog(
		WithAuditSink(sink),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	_, err := wrapped(auditCtx("fail-tool"), []byte("input"))
	if !errors.Is(err, testErr) {
		t.Fatalf("expected testErr, got %v", err)
	}

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Success {
		t.Error("expected Success=false")
	}
	if e.ErrorMessage != "tool error" {
		t.Errorf("ErrorMessage = %q, want %q", e.ErrorMessage, "tool error")
	}
	if e.OutputSize != 0 {
		t.Errorf("OutputSize = %d, want 0", e.OutputSize)
	}
}

// ── No sink → pass-through ──────────────────────────────────────────

func TestAuditLog_NoSink(t *testing.T) {
	t.Parallel()

	var called bool
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	}

	mw := AuditLog() // no sink
	wrapped := mw(handler)

	out, err := wrapped(auditCtx("tool"), nil)
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

// ── Input hashing ───────────────────────────────────────────────────

func TestAuditLog_HashInputDisabled(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}

	mw := AuditLog(
		WithAuditSink(sink),
		WithAuditHashInput(false),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	wrapped(auditCtx("tool"), []byte("secret data"))

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].InputHash != "" {
		t.Errorf("expected empty InputHash when hashing disabled, got %q", entries[0].InputHash)
	}
}

func TestAuditLog_NilInputHash(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}

	mw := AuditLog(
		WithAuditSink(sink),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	wrapped(auditCtx("tool"), nil) // nil input

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].InputHash != "" {
		t.Errorf("expected empty InputHash for nil input, got %q", entries[0].InputHash)
	}
	if entries[0].InputSize != 0 {
		t.Errorf("InputSize = %d, want 0", entries[0].InputSize)
	}
}

// ── Inclusion/Exclusion filters ─────────────────────────────────────

func TestAuditLog_IncludeTools(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := AuditLog(
		WithAuditSink(sink),
		WithAuditIncludeTools("a", "b"),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	wrapped(auditCtx("a"), nil)
	wrapped(auditCtx("b"), nil)
	wrapped(auditCtx("c"), nil)

	if sink.Len() != 2 {
		t.Errorf("expected 2 audited calls, got %d", sink.Len())
	}
}

func TestAuditLog_ExcludeTools(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := AuditLog(
		WithAuditSink(sink),
		WithAuditExcludeTools("noisy-tool"),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	wrapped(auditCtx("normal-tool"), nil)
	wrapped(auditCtx("noisy-tool"), nil)
	wrapped(auditCtx("another-tool"), nil)

	if sink.Len() != 2 {
		t.Errorf("expected 2 audited calls (noisy-tool excluded), got %d", sink.Len())
	}
}

func TestAuditLog_IncludeOverridesExclude(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := AuditLog(
		WithAuditSink(sink),
		WithAuditIncludeTools("a"),
		WithAuditExcludeTools("a"), // should be ignored
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	wrapped(auditCtx("a"), nil)
	wrapped(auditCtx("b"), nil)

	if sink.Len() != 1 {
		t.Errorf("expected 1 audited call (include overrides exclude), got %d", sink.Len())
	}
}

// ── Sink panic recovery ─────────────────────────────────────────────

func TestAuditLog_SinkPanicRecovery(t *testing.T) {
	t.Parallel()

	panicSink := AuditSinkFunc(func(_ context.Context, _ AuditEntry) {
		panic("boom")
	})

	var capturedErr error
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := AuditLog(
		WithAuditSink(panicSink),
		WithAuditOnError(func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	// Should not panic.
	out, err := wrapped(auditCtx("tool"), nil)
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
	if capturedErr.Error() != "audit sink panic: boom" {
		t.Errorf("unexpected error: %v", capturedErr)
	}
}

func TestAuditLog_SinkPanicNoCallback(t *testing.T) {
	t.Parallel()

	panicSink := AuditSinkFunc(func(_ context.Context, _ AuditEntry) {
		panic("boom")
	})

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := AuditLog(
		WithAuditSink(panicSink),
		// No onError callback — should still not panic.
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	out, err := wrapped(auditCtx("tool"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

func TestAuditLog_SinkPanicWithErrorValue(t *testing.T) {
	t.Parallel()

	panicErr := errors.New("sink failure")
	panicSink := AuditSinkFunc(func(_ context.Context, _ AuditEntry) {
		panic(panicErr)
	})

	var capturedErr error
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := AuditLog(
		WithAuditSink(panicSink),
		WithAuditOnError(func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)
	wrapped(auditCtx("tool"), nil)

	mu.Lock()
	defer mu.Unlock()
	if capturedErr == nil {
		t.Fatal("expected onError to be called")
	}
	if !errors.Is(capturedErr, panicErr) {
		t.Errorf("expected panicErr, got %v", capturedErr)
	}
}

func TestAuditLog_SinkPanicWithIntValue(t *testing.T) {
	t.Parallel()

	panicSink := AuditSinkFunc(func(_ context.Context, _ AuditEntry) {
		panic(42) // non-string, non-error panic value
	})

	var capturedErr error
	var mu sync.Mutex

	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := AuditLog(
		WithAuditSink(panicSink),
		WithAuditOnError(func(err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		}),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)
	wrapped(auditCtx("tool"), nil)

	mu.Lock()
	defer mu.Unlock()
	if capturedErr == nil {
		t.Fatal("expected onError to be called")
	}
	if capturedErr.Error() != "audit sink panic: unknown panic" {
		t.Errorf("unexpected error: %v", capturedErr)
	}
}

// ── Request ID propagation ──────────────────────────────────────────

func TestAuditLog_RequestID(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

	mw := AuditLog(
		WithAuditSink(sink),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "tool")
	ctx = finemcp.WithRequestID(ctx, "req-42")

	wrapped(ctx, nil)

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].RequestID != "req-42" {
		t.Errorf("RequestID = %v, want %q", entries[0].RequestID, "req-42")
	}
}

// ── Concurrent safety ───────────────────────────────────────────────

func TestAuditLog_Concurrent(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := AuditLog(
		WithAuditSink(sink),
		withAuditClock(fixedAuditClock(time.Now())),
	)
	wrapped := mw(handler)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wrapped(auditCtx("tool"), []byte("data"))
		}()
	}
	wg.Wait()

	if sink.Len() != 100 {
		t.Errorf("expected 100 audit entries, got %d", sink.Len())
	}
}

// ── InMemoryAuditSink tests ─────────────────────────────────────────

func TestInMemoryAuditSink_Reset(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	sink.Log(context.Background(), AuditEntry{ToolName: "a"})
	sink.Log(context.Background(), AuditEntry{ToolName: "b"})

	if sink.Len() != 2 {
		t.Fatalf("expected 2, got %d", sink.Len())
	}

	sink.Reset()
	if sink.Len() != 0 {
		t.Errorf("expected 0 after reset, got %d", sink.Len())
	}
}

func TestInMemoryAuditSink_EntriesAreCopy(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}
	sink.Log(context.Background(), AuditEntry{ToolName: "x"})

	entries := sink.Entries()
	entries[0].ToolName = "mutated"

	// Original should not be affected.
	fresh := sink.Entries()
	if fresh[0].ToolName != "x" {
		t.Errorf("Entries() returned a reference, not a copy")
	}
}

// ── AuditSinkFunc adapter ───────────────────────────────────────────

func TestAuditSinkFunc(t *testing.T) {
	t.Parallel()

	var captured AuditEntry
	fn := AuditSinkFunc(func(_ context.Context, e AuditEntry) {
		captured = e
	})

	fn.Log(context.Background(), AuditEntry{ToolName: "test"})

	if captured.ToolName != "test" {
		t.Errorf("captured.ToolName = %q, want %q", captured.ToolName, "test")
	}
}

// ── Duration tracking ───────────────────────────────────────────────

func TestAuditLog_DurationTracking(t *testing.T) {
	t.Parallel()

	sink := &InMemoryAuditSink{}

	var callIdx atomic.Int64
	clockFn := func() time.Time {
		idx := callIdx.Add(1)
		// First call (start): time 0. Second call (end): time + 500ms.
		return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Add(
			time.Duration(idx-1) * 500 * time.Millisecond,
		)
	}

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}

	mw := AuditLog(
		WithAuditSink(sink),
		withAuditClock(clockFn),
	)
	wrapped := mw(handler)
	wrapped(auditCtx("tool"), nil)

	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Duration != 500*time.Millisecond {
		t.Errorf("Duration = %v, want 500ms", entries[0].Duration)
	}
}
