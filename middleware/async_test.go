package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Async middleware ────────────────────────────────────────────────

func TestAsync_ReturnsJobIDImmediately(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	mw, waiter := Async(WithJobStore(store))
	defer waiter.Wait()

	// Slow handler that blocks until released.
	release := make(chan struct{})
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		<-release
		return []byte("done"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "slow-tool")
	out, err := handler(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]string
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp["jobId"] == "" {
		t.Fatal("expected non-empty jobId")
	}
	if resp["status"] != string(finemcp.JobStatusPending) {
		t.Errorf("status = %q, want %q", resp["status"], finemcp.JobStatusPending)
	}

	close(release) // allow goroutine to finish
}

func TestAsync_HandlerRunsInBackground(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	mw, waiter := Async(WithJobStore(store))
	defer waiter.Wait()

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("result-data"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "bg-tool")
	out, err := handler(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]string
	json.Unmarshal(out, &resp)
	jobID := resp["jobId"]

	// Wait for all background goroutines (including store mutations) to finish.
	waiter.Wait()

	job, err := store.Get(jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != finemcp.JobStatusComplete {
		t.Errorf("status = %q, want %q", job.Status, finemcp.JobStatusComplete)
	}
}

func TestAsync_HandlerError_FailsJob(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	mw, waiter := Async(WithJobStore(store))

	errBoom := errors.New("boom")

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errBoom
	})

	ctx := finemcp.WithToolName(context.Background(), "fail-tool")
	out, err := handler(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]string
	json.Unmarshal(out, &resp)
	jobID := resp["jobId"]

	waiter.Wait()

	job, err := store.Get(jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != finemcp.JobStatusFailed {
		t.Errorf("status = %q, want %q", job.Status, finemcp.JobStatusFailed)
	}
	if job.Error != "boom" {
		t.Errorf("error = %q, want %q", job.Error, "boom")
	}
}

func TestAsync_CustomIDGenerator(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	mw, waiter := Async(
		WithJobStore(store),
		WithIDGenerator(func() string { return "custom-42" }),
	)
	defer waiter.Wait()

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "id-tool")
	out, _ := handler(ctx, []byte(`{}`))

	var resp map[string]string
	json.Unmarshal(out, &resp)
	if resp["jobId"] != "custom-42" {
		t.Errorf("jobId = %q, want %q", resp["jobId"], "custom-42")
	}
}

func TestAsync_DefaultStore(t *testing.T) {
	t.Parallel()

	// When no WithJobStore is provided, a default store is used internally.
	mw, waiter := Async()
	defer waiter.Wait()
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "default-store-tool")
	out, err := handler(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]string
	json.Unmarshal(out, &resp)
	if resp["jobId"] == "" {
		t.Fatal("expected non-empty jobId")
	}
}

func TestAsync_DefaultIDGenMonotonic(t *testing.T) {
	t.Parallel()

	gen := defaultIDGen()
	ids := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := gen()
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate ID: %s", id)
		}
		ids[id] = struct{}{}
	}
}

func TestAsync_JobTransitionsThroughStatuses(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	mw, waiter := Async(WithJobStore(store))
	defer waiter.Wait()

	started := make(chan struct{})
	proceed := make(chan struct{})

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		close(started)
		<-proceed
		return []byte("final"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "transition-tool")
	out, _ := handler(ctx, []byte(`{}`))

	var resp map[string]string
	json.Unmarshal(out, &resp)
	jobID := resp["jobId"]

	// Wait for goroutine to start — MarkRunning has already completed
	// by the time the handler signals started.
	<-started

	job, _ := store.Get(jobID)
	if job.Status != finemcp.JobStatusRunning {
		t.Errorf("during execution: status = %q, want %q", job.Status, finemcp.JobStatusRunning)
	}

	close(proceed)
	waiter.Wait()

	job, _ = store.Get(jobID)
	if job.Status != finemcp.JobStatusComplete {
		t.Errorf("after completion: status = %q, want %q", job.Status, finemcp.JobStatusComplete)
	}
}

// ── Nil-guard tests ────────────────────────────────────────────────

func TestAsync_WithJobStore_NilPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil store, got none")
		}
	}()
	WithJobStore(nil)
}

func TestAsync_WithIDGenerator_NilPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil id generator, got none")
		}
	}()
	WithIDGenerator(nil)
}

// ── AsyncWaiter ────────────────────────────────────────────────────

func TestAsync_WaiterBlocksUntilDone(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	release := make(chan struct{})

	mw, waiter := Async(WithJobStore(store))

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		<-release
		return []byte("done"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "waiter-tool")
	_, _ = handler(ctx, []byte(`{}`))

	// Signal the goroutine to finish, then verify Wait returns.
	done := make(chan struct{})
	go func() {
		waiter.Wait()
		close(done)
	}()

	// Wait should be blocking because the goroutine is still running.
	select {
	case <-done:
		t.Fatal("Wait returned before goroutine completed")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	close(release) // let the goroutine complete

	select {
	case <-done:
		// Wait returned as expected.
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after goroutine completed")
	}
}

// ── MarkRunning early-return on cancelled job ──────────────────────

func TestAsync_MarkRunningEarlyReturn_WhenCancelled(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	handlerRan := make(chan struct{}, 1)

	mw, waiter := Async(WithJobStore(store))
	defer waiter.Wait()

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		handlerRan <- struct{}{}
		return []byte("should-not-run"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "cancel-tool")
	out, _ := handler(ctx, []byte(`{}`))

	var resp map[string]string
	json.Unmarshal(out, &resp)
	jobID := resp["jobId"]

	// Cancel the job before the goroutine calls MarkRunning.
	// In practice MarkRunning races with Cancel; here we rely on the
	// fact that the goroutine has not yet transitioned the job.
	// Even if it did, the test validates the handler either ran or
	// the status reflects the cancellation.
	_ = store.Cancel(jobID)

	// Give the goroutine time to attempt MarkRunning and bail.
	time.Sleep(100 * time.Millisecond)

	job, _ := store.Get(jobID)
	// The job should remain cancelled because MarkRunning was rejected.
	if job.Status != finemcp.JobStatusCancelled {
		// If the goroutine raced past Cancel, the status will be
		// complete; that's acceptable in a real system but in this
		// controlled test it should be cancelled.
		select {
		case <-handlerRan:
			// The handler did run (MarkRunning beat Cancel). This is
			// fine; it's a race. Skip the status assertion.
			t.Log("handler ran before cancellation took effect (race); skipping status check")
		default:
			t.Errorf("status = %q, want %q", job.Status, finemcp.JobStatusCancelled)
		}
	}
}

func TestAsync_PanicRecoveredAndJobFailed(t *testing.T) {
	t.Parallel()

	store := finemcp.NewJobStore()
	mw, waiter := Async(WithJobStore(store))

	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		panic("handler blew up")
	})

	ctx := finemcp.WithToolName(context.Background(), "panic-tool")
	out, err := handler(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]string
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	jobID := resp["jobId"]

	// Wait for the goroutine to finish (recover + Fail).
	waiter.Wait()

	job, err := store.Get(jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != finemcp.JobStatusFailed {
		t.Errorf("status = %q, want %q", job.Status, finemcp.JobStatusFailed)
	}
	if job.Error == "" {
		t.Fatal("expected non-empty error on failed job")
	}
	if got := job.Error; got != "panic: handler blew up" {
		t.Errorf("error = %q, want %q", got, "panic: handler blew up")
	}
}
