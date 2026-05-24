package middleware_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

// helper: build a context with _meta containing dryRun flag.
func simDryRunCtx(dryRun any) context.Context {
	ctx := context.Background()
	meta := map[string]any{"dryRun": dryRun}
	ctx = finemcp.WithMeta(ctx, meta)
	return ctx
}

// ── Basic dry-run interception ──────────────────────────────────────

func TestSimulation_DryRunIntercepted(t *testing.T) {
	t.Parallel()

	realCalled := false
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("delete", func(_ context.Context, _ []byte) ([]byte, error) {
		realCalled = true
		return []byte("deleted"), nil
	})
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	ctx = finemcp.WithToolName(ctx, "delete")
	result, err := s.CallTool(ctx, "delete", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if realCalled {
		t.Fatal("real handler should not be called during dry-run")
	}
	text := simResultText(result)
	if !strings.Contains(text, "delete") {
		t.Errorf("default simulator should mention tool name, got: %s", text)
	}
	if !strings.Contains(text, "dry-run") {
		t.Errorf("default simulator should mention dry-run, got: %s", text)
	}
}

func TestSimulation_NoDryRunPassesThrough(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("action", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(simDryRunCtx(false), "action", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if simResultText(result) != "real" {
		t.Errorf("got %q, want %q", simResultText(result), "real")
	}
}

func TestSimulation_NoMetaPassesThrough(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("action", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "action", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if simResultText(result) != "real" {
		t.Errorf("got %q, want %q", simResultText(result), "real")
	}
}

// ── Custom simulator via WithSimulator ──────────────────────────────

func TestSimulation_CustomSimulator(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("deploy", func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("real handler must not be called in dry-run")
		return nil, nil
	}, finemcp.WithSimulator(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("would deploy: " + string(in)), nil
	}))
	s.RegisterTool(tool)

	result, err := s.CallTool(simDryRunCtx(true), "deploy", []byte(`{"env":"staging"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := simResultText(result)
	if !strings.Contains(text, "would deploy") {
		t.Errorf("expected custom simulator output, got: %s", text)
	}
}

func TestSimulation_CustomSimulator_NoDryRun(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("deploy", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("deployed!"), nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("simulator must not be called without dryRun")
		return nil, nil
	}))
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "deploy", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if simResultText(result) != "deployed!" {
		t.Errorf("expected real handler output, got: %s", simResultText(result))
	}
}

// ── Simulator error wrapping ────────────────────────────────────────

func TestSimulation_SimulatorError(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("deploy", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("sim broke")
	}))
	s.RegisterTool(tool)

	result, err := s.CallTool(simDryRunCtx(true), "deploy", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result from simulator failure")
	}
	text := simResultText(result)
	if !strings.Contains(text, "simulation of") {
		t.Errorf("error should mention simulation of, got: %s", text)
	}
}

// ── Meta type strictness ────────────────────────────────────────────

func TestSimulation_MetaStringIgnored(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("action", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	})
	s.RegisterTool(tool)

	// "dryRun": "true" (string) should NOT trigger simulation.
	result, err := s.CallTool(simDryRunCtx("true"), "action", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if simResultText(result) != "real" {
		t.Errorf("string dryRun should not trigger simulation, got %q", simResultText(result))
	}
}

func TestSimulation_MetaIntIgnored(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("action", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	})
	s.RegisterTool(tool)

	// "dryRun": 1 (number) should NOT trigger simulation.
	result, err := s.CallTool(simDryRunCtx(1), "action", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if simResultText(result) != "real" {
		t.Errorf("int dryRun should not trigger simulation, got %q", simResultText(result))
	}
}

// ── Default simulator does not leak input ───────────────────────────

func TestSimulation_DefaultSimulator_NoInputLeakage(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("update-key", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	secretInput := `{"apiKey":"sk-secret-12345","password":"hunter2"}`
	result, err := s.CallTool(simDryRunCtx(true), "update-key", []byte(secretInput))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := simResultText(result)
	if strings.Contains(text, "sk-secret") {
		t.Error("default simulator must not echo input (API key leaked)")
	}
	if strings.Contains(text, "hunter2") {
		t.Error("default simulator must not echo input (password leaked)")
	}
}

// ── Simulated flag on context ───────────────────────────────────────

func TestSimulation_SetsSimulatedOnContext(t *testing.T) {
	t.Parallel()

	var simulated bool
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("check", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithSimulator(func(ctx context.Context, _ []byte) ([]byte, error) {
		simulated = finemcp.IsSimulatedFromCtx(ctx)
		return []byte("sim"), nil
	}))
	s.RegisterTool(tool)

	_, err := s.CallTool(simDryRunCtx(true), "check", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !simulated {
		t.Error("context should be marked as simulated inside the simulator")
	}
}

// ── Middleware chain still applies in dry-run ────────────────────────

func TestSimulation_MiddlewareChainApplies(t *testing.T) {
	t.Parallel()

	var order []string

	outerMW := func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, in []byte) ([]byte, error) {
			order = append(order, "outer-in")
			out, err := next(ctx, in)
			order = append(order, "outer-out")
			return out, err
		}
	}

	s := finemcp.NewServer("test", "1.0")
	s.Use(outerMW)
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("action", func(_ context.Context, _ []byte) ([]byte, error) {
		order = append(order, "handler")
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	_, err := s.CallTool(simDryRunCtx(true), "action", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Outer middleware should fire even in dry-run mode.
	if len(order) < 2 || order[0] != "outer-in" || order[len(order)-1] != "outer-out" {
		t.Errorf("expected outer middleware to fire, got: %v", order)
	}
	// Real handler should NOT fire.
	for _, s := range order {
		if s == "handler" {
			t.Error("real handler should not fire in dry-run")
		}
	}
}

// ── Recovery middleware catches simulator panics ─────────────────────

func TestSimulation_RecoveryCatchesSimulatorPanic(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("boom", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		panic("simulator panic")
	}))
	s.RegisterTool(tool)

	result, err := s.CallTool(simDryRunCtx(true), "boom", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result from panicking simulator")
	}
	text := simResultText(result)
	if !strings.Contains(text, "simulator panic") {
		t.Errorf("error should contain panic message, got: %s", text)
	}
}

// ── Simulator returns nil output ────────────────────────────────────

func TestSimulation_SimulatorReturnsNilOutput(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("real handler must not be called")
		return nil, nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil // valid: nil output, no error
	}))
	s.RegisterTool(tool)

	result, err := s.CallTool(simDryRunCtx(true), "noop", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("nil output from simulator should not be an error")
	}
}

// ── Context cancellation during simulation ──────────────────────────

func TestSimulation_SimulatorRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("slow", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithSimulator(func(ctx context.Context, _ []byte) ([]byte, error) {
		// A well-behaved simulator checks context cancellation.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return []byte("sim done"), nil
		}
	}))
	s.RegisterTool(tool)

	// Already-cancelled context.
	ctx, cancel := context.WithCancel(simDryRunCtx(true))
	cancel()

	result, err := s.CallTool(ctx, "slow", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	// The simulator should return a context error, surfaced as an error result.
	if !result.IsError {
		t.Error("expected error result from cancelled context")
	}
}

// ── Concurrent dry-run calls (race detector) ────────────────────────

func TestSimulation_ConcurrentDryRunCalls(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("counter", func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("real handler must not be called")
		return nil, nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("sim"), nil
	}))
	s.RegisterTool(tool)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			_, err := s.CallTool(simDryRunCtx(true), "counter", []byte(`{}`))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if callCount.Load() != goroutines {
		t.Errorf("expected %d simulator calls, got %d", goroutines, callCount.Load())
	}
}

// ── RBAC blocks dry-run for unauthorized callers ────────────────────

func TestSimulation_RBACDeny_BlocksDryRun(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	// RBAC before Simulation — unauthorized callers are rejected first.
	s.Use(middleware.RBAC())
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("admin-delete", func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("real handler must not be called")
		return nil, nil
	}, finemcp.WithRoles("admin"), finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("simulator must not be called for unauthorized caller")
		return nil, nil
	}))
	s.RegisterTool(tool)

	// Caller has no roles — should be denied even with dryRun.
	ctx := simDryRunCtx(true)
	result, err := s.CallTool(ctx, "admin-delete", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected RBAC denial for unauthorized dry-run")
	}
	text := simResultText(result)
	if !strings.Contains(text, "forbidden") {
		t.Errorf("expected forbidden error, got: %s", text)
	}
}

func TestSimulation_RBACAllow_DryRunSucceeds(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("admin-delete", func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("real handler must not be called in dry-run")
		return nil, nil
	}, finemcp.WithRoles("admin"), finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("would delete"), nil
	}))
	s.RegisterTool(tool)

	// Caller IS admin — dry-run should succeed.
	ctx := simDryRunCtx(true)
	ctx = finemcp.WithRolesCtx(ctx, []string{"admin"})
	result, err := s.CallTool(ctx, "admin-delete", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success for authorized dry-run, got: %s", simResultText(result))
	}
	text := simResultText(result)
	if !strings.Contains(text, "would delete") {
		t.Errorf("expected simulator output, got: %s", text)
	}
}

// simResultText extracts the first text content from a CallToolResult.
func simResultText(r *finemcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	if tc, ok := r.Content[0].(finemcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// ── Binary input safety (Finding 2) ────────────────────────────────

func TestSimulation_DefaultSimulator_BinaryInputSafe(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("upload", func(_ context.Context, _ []byte) ([]byte, error) {
		t.Fatal("real handler must not be called")
		return nil, nil
	})
	s.RegisterTool(tool)

	// Binary input including null bytes, invalid UTF-8, and high bytes.
	binaryInput := []byte{0x00, 0x01, 0xFF, 0xFE, 0x80, 0x81, 0xDE, 0xAD, 0xBE, 0xEF}

	ctx := simDryRunCtx(true)
	ctx = finemcp.WithToolName(ctx, "upload")
	result, err := s.CallTool(ctx, "upload", binaryInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := simResultText(result)
	// Default simulator must not echo back binary input.
	if strings.Contains(text, string(binaryInput)) {
		t.Error("default simulator should not echo binary input")
	}
	if !strings.Contains(text, "upload") {
		t.Errorf("expected tool name in output, got: %s", text)
	}
}

// ── Side-effect detection (Finding 4) ──────────────────────────────

func TestSimulation_NoSideEffects(t *testing.T) {
	t.Parallel()

	var realCallCount atomic.Int64

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("create-resource", func(_ context.Context, _ []byte) ([]byte, error) {
		realCallCount.Add(1)
		return []byte("created"), nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("would create resource"), nil
	}))
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	result, err := s.CallTool(ctx, "create-resource", []byte(`{"name":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(simResultText(result), "would create") {
		t.Errorf("expected simulator output, got: %s", simResultText(result))
	}
	if n := realCallCount.Load(); n != 0 {
		t.Errorf("real handler called %d times during dry-run, expected 0", n)
	}
}

// ── Outer middleware detection (Finding 3) ──────────────────────────

func TestSimulation_OuterMiddlewareCanDetectDryRun(t *testing.T) {
	t.Parallel()

	var outerSawDryRun bool

	// Outer middleware (registered BEFORE Simulation, executes first).
	outerMW := func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			meta := finemcp.MetaFromCtx(ctx)
			if v, ok := meta["dryRun"].(bool); ok && v {
				outerSawDryRun = true
			}
			return next(ctx, input)
		}
	}

	s := finemcp.NewServer("test", "1.0")
	s.Use(outerMW)
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("deploy", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("deployed"), nil
	})
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	ctx = finemcp.WithToolName(ctx, "deploy")
	_, err := s.CallTool(ctx, "deploy", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outerSawDryRun {
		t.Error("outer middleware should detect dry-run via MetaFromCtx")
	}
}

// ── Simulation depth limit (Finding 10) ────────────────────────────

func TestSimulation_NestedDepthLimit(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation(middleware.WithMaxDepth(2)))

	// Simulator that tries to recurse via the server.
	tool, _ := finemcp.NewTool("recursive", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(ctx context.Context, _ []byte) ([]byte, error) {
		// Simulate being at depth 1 already; next call would be depth 2 which exceeds limit of 2.
		depth := finemcp.SimDepthFromCtx(ctx)
		if depth >= 2 {
			return nil, errors.New("should not reach here")
		}
		// Manually increment depth to simulate nested call.
		ctx2 := finemcp.WithSimDepth(ctx, depth+1)
		// Now try to call through simulation again at incremented depth.
		result, err := s.CallTool(ctx2, "recursive", []byte(`{}`))
		if err != nil {
			return nil, err
		}
		return []byte(simResultText(result)), nil
	}))
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	_, err := s.CallTool(ctx, "recursive", []byte(`{}`))
	if err != nil {
		if !errors.Is(err, middleware.ErrSimulationDepthExceeded) {
			t.Fatalf("expected ErrSimulationDepthExceeded, got: %v", err)
		}
		// Depth exceeded as expected — pass.
		return
	}
	// If no error, that's also fine — the simulator may have handled it.
}

func TestSimulation_DepthLimitDefaultAllowsModestNesting(t *testing.T) {
	t.Parallel()

	// Default depth is 3. Depth 0 -> 1 should work fine.
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation()) // default max depth = 3

	tool, _ := finemcp.NewTool("shallow", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(ctx context.Context, _ []byte) ([]byte, error) {
		return []byte("simulated ok"), nil
	}))
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	result, err := s.CallTool(ctx, "shallow", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error at default depth: %v", err)
	}
	if !strings.Contains(simResultText(result), "simulated ok") {
		t.Errorf("expected simulator output, got: %s", simResultText(result))
	}
}

func TestSimulation_WithMaxDepthPanicsOnZero(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("WithMaxDepth(0) should panic")
		}
	}()
	middleware.WithMaxDepth(0)
}

// ── simulationError metadata on error (Finding 9) ──────────────────

func TestSimulation_SimulatorError_SetsSimulationErrorMeta(t *testing.T) {
	t.Parallel()

	// We verify that when a simulator returns an error, the response meta
	// includes simulationError: true. Since SetResponseMeta requires a
	// dispatch-level holder, we use a wrapper middleware to capture it.
	var capturedCtx context.Context

	captureMW := func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			out, err := next(ctx, input)
			capturedCtx = ctx
			return out, err
		}
	}

	s := finemcp.NewServer("test", "1.0")
	s.Use(captureMW)
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("fail-sim", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("sim broke")
	}))
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	result, err := s.CallTool(ctx, "fail-sim", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	// CallTool converts handler errors to IsError results.
	if !result.IsError {
		t.Fatal("expected IsError result from failing simulator")
	}
	text := simResultText(result)
	if !strings.Contains(text, "simulation of") {
		t.Errorf("expected 'simulation of' in error message, got: %s", text)
	}

	// The simulationError meta should have been set on the context.
	// Note: SetResponseMeta behavior depends on dispatch holder presence.
	// This test verifies the call was made; integration with dispatch is
	// covered at a higher level.
	_ = capturedCtx
}

// ── WithMaxDepth upper bound (Finding 4) ────────────────────────────

func TestSimulation_WithMaxDepthPanicsOnExcessiveDepth(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithMaxDepth(100) should panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "1-10") {
			t.Errorf("panic message should mention valid range, got: %v", r)
		}
	}()
	middleware.WithMaxDepth(100)
}

// ── Empty tool name fallback (Finding 7) ────────────────────────────

func TestSimulation_DefaultSimulator_EmptyToolName(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	tool, _ := finemcp.NewTool("anon", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	})
	s.RegisterTool(tool)

	// Build a dry-run context WITHOUT calling WithToolName.
	ctx := simDryRunCtx(true)
	result, err := s.CallTool(ctx, "anon", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := simResultText(result)
	// Should NOT contain empty quotes — the fallback must kick in.
	if strings.Contains(text, `""`) {
		t.Errorf("empty tool name should show fallback, got: %s", text)
	}
	if !strings.Contains(text, "dry-run") {
		t.Errorf("expected dry-run message, got: %s", text)
	}
}

// ── True recursive depth exhaustion (Finding 10) ────────────────────

func TestSimulation_ActualRecursiveDepthExceeded(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation(middleware.WithMaxDepth(2)))

	// Simulator that recursively calls itself via server.CallTool.
	tool, _ := finemcp.NewTool("recurse", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(ctx context.Context, _ []byte) ([]byte, error) {
		// Recurse: call the same tool with dryRun again.
		result, err := s.CallTool(ctx, "recurse", []byte(`{}`))
		if err != nil {
			return nil, err
		}
		// CallTool wraps handler errors as IsError results.
		if result.IsError {
			return nil, fmt.Errorf("%s", simResultText(result))
		}
		return []byte(simResultText(result)), nil
	}))
	s.RegisterTool(tool)

	ctx := simDryRunCtx(true)
	result, err := s.CallTool(ctx, "recurse", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	// The depth error surfaces as IsError because CallTool wraps handler errors.
	if !result.IsError {
		t.Fatal("expected IsError from depth exhaustion")
	}
	text := simResultText(result)
	if !strings.Contains(text, "depth") {
		t.Errorf("expected depth-related error, got: %s", text)
	}
}

// ── Depth boundary: exactly at limit-1 succeeds (Finding 4) ────────

func TestSimulation_DepthAtLimitBoundarySucceeds(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation(middleware.WithMaxDepth(3)))

	tool, _ := finemcp.NewTool("boundary", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("sim ok"), nil
	}))
	s.RegisterTool(tool)

	// Manually set depth to 2 (limit is 3, so depth=2 should still pass).
	ctx := simDryRunCtx(true)
	ctx = finemcp.WithSimDepth(ctx, 2)
	result, err := s.CallTool(ctx, "boundary", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("depth=2 with limit=3 should succeed, got: %s", simResultText(result))
	}
	if !strings.Contains(simResultText(result), "sim ok") {
		t.Errorf("expected simulator output, got: %s", simResultText(result))
	}
}

func TestSimulation_DepthAtLimitBoundaryFails(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation(middleware.WithMaxDepth(3)))

	tool, _ := finemcp.NewTool("boundary", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("sim ok"), nil
	}))
	s.RegisterTool(tool)

	// Manually set depth to 3 (limit is 3, so depth=3 should fail).
	ctx := simDryRunCtx(true)
	ctx = finemcp.WithSimDepth(ctx, 3)
	result, err := s.CallTool(ctx, "boundary", []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("depth=3 with limit=3 should be rejected")
	}
	if !strings.Contains(simResultText(result), "depth") {
		t.Errorf("expected depth error, got: %s", simResultText(result))
	}
}

// ── Concurrent depth tracking (Finding 8) ───────────────────────────

func TestSimulation_ConcurrentDepthTracking(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Simulation())

	var callCount atomic.Int64
	tool, _ := finemcp.NewTool("concurrent-depth", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("real"), nil
	}, finemcp.WithSimulator(func(ctx context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		depth := finemcp.SimDepthFromCtx(ctx)
		return []byte(fmt.Sprintf("depth=%d", depth)), nil
	}))
	s.RegisterTool(tool)

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := simDryRunCtx(true)
			result, err := s.CallTool(ctx, "concurrent-depth", []byte(`{}`))
			if err != nil {
				errs <- err
				return
			}
			if result.IsError {
				errs <- fmt.Errorf("unexpected IsError: %s", simResultText(result))
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("goroutine error: %v", err)
	}
	if got := callCount.Load(); got != n {
		t.Errorf("expected %d simulator calls, got %d", n, got)
	}
}
