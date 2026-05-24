package finemcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestShutdown_ImmediateWhenIdle(t *testing.T) {
	s := NewServer("test", "1.0")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := s.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown on idle server should return nil, got: %v", err)
	}
}

func TestShutdown_WaitsForInflight(t *testing.T) {
	s := NewServer("test", "1.0")

	// Simulate an in-flight request by holding the WaitGroup.
	s.inflight.Add(1)

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- s.Shutdown(ctx)
	}()

	// Shutdown should be blocked.
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned before in-flight completed")
	case <-time.After(50 * time.Millisecond):
		// Expected — still waiting.
	}

	// Complete the in-flight request.
	s.inflight.Done()

	err := <-shutdownDone
	if err != nil {
		t.Errorf("Shutdown should return nil after drain, got: %v", err)
	}
}

func TestShutdown_TimesOut(t *testing.T) {
	s := NewServer("test", "1.0")

	// Simulate a stuck in-flight request.
	s.inflight.Add(1)
	defer s.inflight.Done() // cleanup

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := s.Shutdown(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestInflight_TrackedByHandleMessage(t *testing.T) {
	s := NewServer("test", "1.0")

	var wg sync.WaitGroup
	blocked := make(chan struct{})

	tool, _ := NewTool("slow", func(_ context.Context, _ []byte) ([]byte, error) {
		blocked <- struct{}{} // signal we're inside
		<-blocked             // wait for release
		return []byte("done"), nil
	})
	s.RegisterTool(tool)
	s.initialized.Store(true)

	// Start a tool call in the background.
	wg.Add(1)
	go func() {
		defer wg.Done()
		callMsg := jsonrpcReq(1, "tools/call", map[string]any{"name": "slow"})
		s.HandleMessage(context.Background(), callMsg)
	}()

	// Wait until the handler is running.
	<-blocked

	// Shutdown should block because handler is in-flight.
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- s.Shutdown(ctx)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned while handler still running")
	case <-time.After(50 * time.Millisecond):
		// Good — still waiting.
	}

	// Release the handler.
	blocked <- struct{}{}

	err := <-shutdownDone
	if err != nil {
		t.Errorf("Shutdown should return nil, got: %v", err)
	}

	wg.Wait()
}

// --- Start / Lifespan tests ---

func TestStart_WithoutLifespan(t *testing.T) {
	s := NewServer("test", "1.0")

	var calledCtx context.Context
	err := s.Start(context.Background(), func(ctx context.Context) error {
		calledCtx = ctx
		return nil
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if calledCtx == nil {
		t.Fatal("runFn was not called")
	}
}

func TestStart_WithLifespan(t *testing.T) {
	type ctxKey struct{}
	var cleanupCalled bool

	s := NewServer("test", "1.0", WithLifespan(func(ctx context.Context, _ *Server) (context.Context, func(), error) {
		return context.WithValue(ctx, ctxKey{}, "enriched"), func() { cleanupCalled = true }, nil
	}))

	var gotValue any
	err := s.Start(context.Background(), func(ctx context.Context) error {
		gotValue = ctx.Value(ctxKey{})
		return nil
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if gotValue != "enriched" {
		t.Errorf("context value = %v, want %q", gotValue, "enriched")
	}
	if !cleanupCalled {
		t.Error("cleanup function was not called")
	}
}

func TestStart_LifespanError(t *testing.T) {
	lifespanErr := errors.New("lifespan init failed")

	s := NewServer("test", "1.0", WithLifespan(func(_ context.Context, _ *Server) (context.Context, func(), error) {
		return nil, nil, lifespanErr
	}))

	runCalled := false
	err := s.Start(context.Background(), func(_ context.Context) error {
		runCalled = true
		return nil
	})
	if err != lifespanErr {
		t.Errorf("expected lifespan error, got: %v", err)
	}
	if runCalled {
		t.Error("runFn should not be called when lifespan fails")
	}
}

func TestStart_CleanupCalledOnRunError(t *testing.T) {
	var cleanupCalled bool

	s := NewServer("test", "1.0", WithLifespan(func(ctx context.Context, _ *Server) (context.Context, func(), error) {
		return ctx, func() { cleanupCalled = true }, nil
	}))

	runErr := errors.New("transport failed")
	err := s.Start(context.Background(), func(_ context.Context) error {
		return runErr
	})
	if err != runErr {
		t.Errorf("expected run error, got: %v", err)
	}
	if !cleanupCalled {
		t.Error("cleanup should be called even when runFn fails")
	}
}

func TestStart_NilCleanup(t *testing.T) {
	s := NewServer("test", "1.0", WithLifespan(func(ctx context.Context, _ *Server) (context.Context, func(), error) {
		return ctx, nil, nil // nil cleanup should not panic
	}))

	err := s.Start(context.Background(), func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestStart_NilEnrichedCtx(t *testing.T) {
	s := NewServer("test", "1.0", WithLifespan(func(_ context.Context, _ *Server) (context.Context, func(), error) {
		return nil, nil, nil // nil context should fall back to original
	}))

	var calledCtx context.Context
	baseCtx := context.Background()
	err := s.Start(baseCtx, func(ctx context.Context) error {
		calledCtx = ctx
		return nil
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if calledCtx != baseCtx {
		t.Error("nil enrichedCtx should fall back to original context")
	}
}

func TestStart_NilContext(t *testing.T) {
	s := NewServer("test", "1.0")

	//nolint:staticcheck // SA1012: intentionally testing nil context behavior
	err := s.Start(nil, func(_ context.Context) error {
		t.Fatal("runFn should not be called with nil context")
		return nil
	})
	if err == nil {
		t.Fatal("expected error for nil context, got nil")
	}
}
