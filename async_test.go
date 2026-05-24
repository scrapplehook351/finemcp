package finemcp

import (
	"errors"
	"testing"
	"time"
)

// ── JobStore ────────────────────────────────────────────────────────

func TestJobStore_Submit(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	job, err := store.Submit("j1", "greet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.ID != "j1" {
		t.Errorf("id = %q, want %q", job.ID, "j1")
	}
	if job.ToolName != "greet" {
		t.Errorf("tool = %q, want %q", job.ToolName, "greet")
	}
	if job.Status != JobStatusPending {
		t.Errorf("status = %q, want %q", job.Status, JobStatusPending)
	}
}

func TestJobStore_Submit_EmptyID(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	_, err := store.Submit("", "greet")
	if !errors.Is(err, errJobIDEmpty) {
		t.Errorf("got %v, want errJobIDEmpty", err)
	}
}

func TestJobStore_Get(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")

	job, err := store.Get("j1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.ID != "j1" {
		t.Errorf("id = %q, want %q", job.ID, "j1")
	}
}

func TestJobStore_Get_NotFound(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	_, err := store.Get("nonexistent")
	if !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound", err)
	}
}

func TestJobStore_Get_ReturnsCopy(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")

	job1, _ := store.Get("j1")
	job1.Status = JobStatusFailed // mutate the copy

	job2, _ := store.Get("j1")
	if job2.Status != JobStatusPending {
		t.Errorf("got %q, want %q — store returned reference not copy", job2.Status, JobStatusPending)
	}
}

func TestJobStore_MarkRunning(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")

	if err := store.MarkRunning("j1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	job, _ := store.Get("j1")
	if job.Status != JobStatusRunning {
		t.Errorf("status = %q, want %q", job.Status, JobStatusRunning)
	}
}

func TestJobStore_MarkRunning_NotFound(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	if err := store.MarkRunning("x"); !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound", err)
	}
}

func TestJobStore_Complete(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	store := NewJobStore(withJobClock(func() time.Time { return fixedTime }))
	store.Submit("j1", "greet")
	store.MarkRunning("j1") // must be running before complete

	result := NewTextResult("hello")
	if err := store.Complete("j1", result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, _ := store.Get("j1")
	if job.Status != JobStatusComplete {
		t.Errorf("status = %q, want %q", job.Status, JobStatusComplete)
	}
	if job.Result == nil {
		t.Fatal("expected result to be set")
	}
	if job.CompletedAt != fixedTime {
		t.Errorf("completedAt = %v, want %v", job.CompletedAt, fixedTime)
	}
}

func TestJobStore_Complete_NotFound(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	if err := store.Complete("x", nil); !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound", err)
	}
}

func TestJobStore_Fail(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	store.MarkRunning("j1") // must be running before fail

	if err := store.Fail("j1", "boom"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, _ := store.Get("j1")
	if job.Status != JobStatusFailed {
		t.Errorf("status = %q, want %q", job.Status, JobStatusFailed)
	}
	if job.Error != "boom" {
		t.Errorf("error = %q, want %q", job.Error, "boom")
	}
}

func TestJobStore_Fail_NotFound(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	if err := store.Fail("x", "err"); !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound", err)
	}
}

func TestJobStore_Cancel(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")

	if err := store.Cancel("j1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	job, _ := store.Get("j1")
	if job.Status != JobStatusCancelled {
		t.Errorf("status = %q, want %q", job.Status, JobStatusCancelled)
	}
}

func TestJobStore_Cancel_NotFound(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	if err := store.Cancel("x"); !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound", err)
	}
}

func TestJobStore_List(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	store.Submit("j2", "calc")

	jobs := store.List()
	if len(jobs) != 2 {
		t.Fatalf("job count = %d, want 2", len(jobs))
	}
}

func TestJobStore_List_Empty(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	jobs := store.List()
	if len(jobs) != 0 {
		t.Fatalf("job count = %d, want 0", len(jobs))
	}
}

// ── Submit copy semantics & duplicate ID ────────────────────────────

func TestJobStore_Submit_ReturnsCopy(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	job, _ := store.Submit("j1", "greet")
	job.Status = JobStatusFailed // mutate the returned copy

	got, _ := store.Get("j1")
	if got.Status != JobStatusPending {
		t.Errorf("got %q, want %q — Submit returned internal pointer not copy", got.Status, JobStatusPending)
	}
}

func TestJobStore_Submit_DuplicateID(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	_, err := store.Submit("j1", "other")
	if !errors.Is(err, errJobIDExists) {
		t.Errorf("got %v, want errJobIDExists", err)
	}
}

// ── Delete ──────────────────────────────────────────────────────────

func TestJobStore_Delete(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")

	if err := store.Delete("j1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := store.Get("j1")
	if !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound after delete", err)
	}
}

func TestJobStore_Delete_NotFound(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	if err := store.Delete("x"); !errors.Is(err, errJobNotFound) {
		t.Errorf("got %v, want errJobNotFound", err)
	}
}

// ── State transition guards ─────────────────────────────────────────

func TestJobStore_MarkRunning_InvalidFromComplete(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	store.MarkRunning("j1")
	store.Complete("j1", nil)

	if err := store.MarkRunning("j1"); !errors.Is(err, errInvalidTransition) {
		t.Errorf("got %v, want errInvalidTransition", err)
	}
}

func TestJobStore_Complete_InvalidFromPending(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")

	if err := store.Complete("j1", nil); !errors.Is(err, errInvalidTransition) {
		t.Errorf("got %v, want errInvalidTransition", err)
	}
}

func TestJobStore_Fail_InvalidFromCancelled(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	store.Cancel("j1")

	if err := store.Fail("j1", "oops"); !errors.Is(err, errInvalidTransition) {
		t.Errorf("got %v, want errInvalidTransition", err)
	}
}

func TestJobStore_Cancel_InvalidFromComplete(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	store.MarkRunning("j1")
	store.Complete("j1", nil)

	if err := store.Cancel("j1"); !errors.Is(err, errInvalidTransition) {
		t.Errorf("got %v, want errInvalidTransition", err)
	}
}

func TestJobStore_Cancel_FromRunning(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "greet")
	store.MarkRunning("j1")

	if err := store.Cancel("j1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	job, _ := store.Get("j1")
	if job.Status != JobStatusCancelled {
		t.Errorf("status = %q, want %q", job.Status, JobStatusCancelled)
	}
}

func TestJobStore_List_ReturnsCopies(t *testing.T) {
	t.Parallel()

	store := NewJobStore()
	store.Submit("j1", "tool-a")
	store.Submit("j2", "tool-b")
	store.MarkRunning("j1")
	store.Complete("j1", NewTextResult("hello"))

	jobs := store.List()

	// Mutate every returned job.
	for _, j := range jobs {
		j.Status = JobStatusFailed
		j.Error = "mutated"
		if j.Result != nil {
			j.Result.Content = nil
		}
	}

	// Verify the store's internal state is untouched.
	orig, _ := store.Get("j1")
	if orig.Status != JobStatusComplete {
		t.Errorf("j1 status = %q after List mutation, want %q", orig.Status, JobStatusComplete)
	}
	if orig.Result == nil || len(orig.Result.Content) == 0 {
		t.Error("j1 Result.Content was mutated through List copy")
	}

	orig2, _ := store.Get("j2")
	if orig2.Status != JobStatusPending {
		t.Errorf("j2 status = %q after List mutation, want %q", orig2.Status, JobStatusPending)
	}
}
