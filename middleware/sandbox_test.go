package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Timeout behaviour ───────────────────────────────────────────────

func TestSandbox_FastHandlerSucceeds(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithTimeout(time.Second))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("fast"), nil
	})

	out, err := handler(context.Background(), []byte("input"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "fast" {
		t.Errorf("got %q, want %q", out, "fast")
	}
}

func TestSandbox_SlowHandlerTimesOut(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithTimeout(50 * time.Millisecond))
	handler := mw(func(ctx context.Context, _ []byte) ([]byte, error) {
		select {
		case <-time.After(5 * time.Second):
			return []byte("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	_, err := handler(context.Background(), []byte("input"))
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrSandboxTimeout) {
		t.Errorf("expected ErrSandboxTimeout, got: %v", err)
	}
}

func TestSandbox_TimeoutPreservesParentCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mw := Sandbox(WithTimeout(10 * time.Second))
	handler := mw(func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	_, err := handler(ctx, []byte("input"))
	if err == nil {
		t.Fatal("expected error from cancelled parent")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from parent, got: %v", err)
	}
	if errors.Is(err, ErrSandboxTimeout) {
		t.Fatalf("did not expect ErrSandboxTimeout for already-cancelled parent, got: %v", err)
	}
}

func TestSandbox_ZeroTimeoutMeansNoLimit(t *testing.T) {
	t.Parallel()

	mw := Sandbox() // no options
	handler := mw(func(ctx context.Context, _ []byte) ([]byte, error) {
		_, hasDeadline := ctx.Deadline()
		if hasDeadline {
			return nil, errors.New("unexpected deadline on context")
		}
		return []byte("ok"), nil
	})

	out, err := handler(context.Background(), []byte("input"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
}

func TestSandbox_TimeoutRace_SuccessAfterDeadline(t *testing.T) {
	// When the handler returns a successful result but the context has
	// already expired, the sandbox must still return ErrSandboxTimeout
	// to enforce the timeout deterministically.
	t.Parallel()

	mw := Sandbox(WithTimeout(10 * time.Millisecond))
	handler := mw(func(ctx context.Context, _ []byte) ([]byte, error) {
		// Sleep longer than the timeout, then return success.
		time.Sleep(50 * time.Millisecond)
		return []byte("late success"), nil
	})

	_, err := handler(context.Background(), nil)
	if err == nil {
		t.Fatal("expected timeout error even though handler returned success")
	}
	if !errors.Is(err, ErrSandboxTimeout) {
		t.Errorf("expected ErrSandboxTimeout, got: %v", err)
	}
}

// ── Output size limiting ────────────────────────────────────────────

func TestSandbox_OutputWithinLimit(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithMaxOutputSize(1024))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("small"), nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "small" {
		t.Errorf("got %q, want %q", out, "small")
	}
}

func TestSandbox_OutputTruncatedWhenOversized(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithMaxOutputSize(10))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("this is a very long output that should be truncated"), nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > 10 {
		t.Errorf("output length = %d, want <= 10", len(out))
	}
}

func TestSandbox_OutputTruncatedIncludesMarker(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithMaxOutputSize(20))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(strings.Repeat("x", 100)), nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "[truncated]") {
		t.Errorf("truncated output should contain [truncated] marker, got: %q", out)
	}
}

func TestSandbox_ZeroMaxOutputMeansNoLimit(t *testing.T) {
	t.Parallel()

	bigOutput := strings.Repeat("x", 1_000_000)
	mw := Sandbox(WithMaxOutputSize(0))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte(bigOutput), nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1_000_000 {
		t.Errorf("output length = %d, want 1000000", len(out))
	}
}

// ── Error passthrough ───────────────────────────────────────────────

func TestSandbox_HandlerErrorPassedThrough(t *testing.T) {
	t.Parallel()

	handlerErr := errors.New("tool failed")
	mw := Sandbox(WithTimeout(time.Second))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, handlerErr
	})

	_, err := handler(context.Background(), nil)
	if !errors.Is(err, handlerErr) {
		t.Errorf("expected original error, got: %v", err)
	}
}

func TestSandbox_NilOutputNotTruncated(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithMaxOutputSize(10))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil output, got %q", out)
	}
}

// ── Combined timeout + output limit ─────────────────────────────────

func TestSandbox_TimeoutAndOutputLimit(t *testing.T) {
	t.Parallel()

	mw := Sandbox(
		WithTimeout(time.Second),
		WithMaxOutputSize(5),
	)
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("hello world"), nil
	})

	out, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) > 5 {
		t.Errorf("output length = %d, want <= 5", len(out))
	}
}

// ── Concurrent safety ───────────────────────────────────────────────

func TestSandbox_ConcurrentCalls(t *testing.T) {
	t.Parallel()

	mw := Sandbox(WithTimeout(time.Second), WithMaxOutputSize(100))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return in, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := handler(context.Background(), []byte("ping"))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if string(out) != "ping" {
				t.Errorf("got %q, want %q", out, "ping")
			}
		}()
	}
	wg.Wait()
}

// ── Integration: sandbox with server middleware chain ────────────────

func TestSandbox_Integration(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(Sandbox(
		WithTimeout(50*time.Millisecond),
		WithMaxOutputSize(1024),
	))

	fastTool, _ := finemcp.NewTool("fast", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("quick"), nil
	})
	s.RegisterTool(fastTool)

	slowTool, _ := finemcp.NewTool("slow", func(ctx context.Context, _ []byte) ([]byte, error) {
		select {
		case <-time.After(10 * time.Second):
			return []byte("late"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	s.RegisterTool(slowTool)

	// Init server.
	initMsg := sandboxJSONRPCReq(1, "initialize", map[string]any{
		"protocolVersion": finemcp.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	// Fast tool succeeds.
	fastCall := sandboxJSONRPCReq(2, "tools/call", map[string]any{"name": "fast"})
	resp, _ := s.HandleMessage(context.Background(), fastCall)
	if resp.Error != nil {
		t.Fatalf("fast tool: unexpected protocol error: %s", resp.Error.Message)
	}

	// Slow tool produces an error result (isError=true).
	slowCall := sandboxJSONRPCReq(3, "tools/call", map[string]any{"name": "slow"})
	resp, _ = s.HandleMessage(context.Background(), slowCall)
	if resp.Error != nil {
		t.Fatalf("slow tool: unexpected protocol error: %s", resp.Error.Message)
	}

	// Verify the result has isError=true and mentions the sandbox timeout.
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal slow tool result: %v", err)
	}
	var slowResult struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resultBytes, &slowResult); err != nil {
		t.Fatalf("failed to unmarshal slow tool result: %v", err)
	}
	if !slowResult.IsError {
		t.Error("expected isError=true for timed-out tool")
	}
	if len(slowResult.Content) == 0 || !strings.Contains(slowResult.Content[0].Text, "sandbox") {
		t.Errorf("expected timeout message mentioning sandbox, got: %v", slowResult.Content)
	}
}

// ── Option validation ───────────────────────────────────────────────

func TestSandbox_NegativeTimeoutPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for negative timeout")
		}
	}()
	Sandbox(WithTimeout(-1 * time.Second))
}

func TestSandbox_NegativeMaxOutputPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for negative max output size")
		}
	}()
	Sandbox(WithMaxOutputSize(-1))
}

// ── Helpers ─────────────────────────────────────────────────────────

// sandboxJSONRPCReq builds a valid JSON-RPC request byte slice.
func sandboxJSONRPCReq(id any, method string, params any) []byte {
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
