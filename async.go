package finemcp

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Job represents an asynchronous tool execution.
type Job struct {
	// ID is a unique identifier for this job.
	ID string

	// ToolName is the name of the tool that was called.
	ToolName string

	// Status is the current state of the job.
	Status JobStatus

	// Result holds the tool's output once the job is complete.
	Result *CallToolResult

	// Error holds an error message if the job failed.
	Error string

	// CreatedAt is when the job was submitted.
	CreatedAt time.Time

	// CompletedAt is when the job finished (zero if still running).
	CompletedAt time.Time
}

// JobStatus represents the lifecycle state of a job.
type JobStatus string

const (
	// JobStatusPending means the job is queued but not yet started.
	JobStatusPending JobStatus = "pending"

	// JobStatusRunning means the job is currently executing.
	JobStatusRunning JobStatus = "running"

	// JobStatusComplete means the job finished successfully.
	JobStatusComplete JobStatus = "complete"

	// JobStatusFailed means the job finished with an error.
	JobStatusFailed JobStatus = "failed"

	// JobStatusCancelled means the job was cancelled before completion.
	JobStatusCancelled JobStatus = "cancelled"
)

var (
	errJobNotFound       = errors.New("job not found")
	errJobIDEmpty        = errors.New("job ID must not be empty")
	errJobIDExists       = errors.New("job ID already exists")
	errInvalidTransition = errors.New("invalid job status transition")
)

// JobStore manages the lifecycle and storage of asynchronous jobs.
// It is safe for concurrent use.
//
// Warning: JobStore has no built-in eviction. Jobs are stored indefinitely.
// For long-running servers, callers should periodically call [JobStore.Delete]
// to remove completed or expired jobs and prevent unbounded memory growth.
type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*Job
	now  func() time.Time // clock override for testing
}

// JobStoreOption configures a JobStore.
type JobStoreOption func(*JobStore)

// withJobClock overrides the clock for testing.
func withJobClock(fn func() time.Time) JobStoreOption {
	return func(js *JobStore) { js.now = fn }
}

// NewJobStore creates a new job store.
func NewJobStore(opts ...JobStoreOption) *JobStore {
	js := &JobStore{
		jobs: make(map[string]*Job),
		now:  time.Now,
	}
	for _, o := range opts {
		o(js)
	}
	return js
}

// Submit creates a new pending job and returns a copy.
// Returns [errJobIDEmpty] if id is empty and [errJobIDExists] if a job
// with the same ID already exists.
func (js *JobStore) Submit(id, toolName string) (*Job, error) {
	if id == "" {
		return nil, errJobIDEmpty
	}

	js.mu.Lock()
	defer js.mu.Unlock()

	if _, exists := js.jobs[id]; exists {
		return nil, errJobIDExists
	}

	job := &Job{
		ID:        id,
		ToolName:  toolName,
		Status:    JobStatusPending,
		CreatedAt: js.now(),
	}
	js.jobs[id] = job

	// Return a copy to avoid exposing internal mutable state.
	cp := *job
	return &cp, nil
}

// copyJob returns a deep copy of a Job. The Content slice inside
// CallToolResult is copied, but StructuredContent is not — treat it
// as immutable after submission.
func copyJob(j *Job) *Job {
	cp := *j
	if cp.Result != nil {
		resCopy := *cp.Result
		if resCopy.Content != nil {
			resCopy.Content = append(resCopy.Content[:0:0], resCopy.Content...)
		}
		cp.Result = &resCopy
	}
	return &cp
}

// Get returns a copy of the job to prevent data races.
// Note: The Content slice is deep-copied, but StructuredContent is not.
// If StructuredContent holds a mutable reference (map, slice, pointer), callers
// may observe shared state. Treat StructuredContent as immutable after submission.
func (js *JobStore) Get(id string) (*Job, error) {
	js.mu.RLock()
	defer js.mu.RUnlock()

	job, ok := js.jobs[id]
	if !ok {
		return nil, errJobNotFound
	}
	return copyJob(job), nil
}

// MarkRunning transitions a job from pending to running.
// Returns [errInvalidTransition] if the job is not in pending state.
func (js *JobStore) MarkRunning(id string) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	job, ok := js.jobs[id]
	if !ok {
		return errJobNotFound
	}
	if job.Status != JobStatusPending {
		return fmt.Errorf("%w: cannot transition from %s to %s", errInvalidTransition, job.Status, JobStatusRunning)
	}
	job.Status = JobStatusRunning
	return nil
}

// Complete transitions a job from running to completed status with the given result.
// Returns [errInvalidTransition] if the job is not in running state.
// Note: The store does not deep-copy StructuredContent. If you set StructuredContent,
// treat it as immutable after submission to avoid exposing shared mutable state.
func (js *JobStore) Complete(id string, result *CallToolResult) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	job, ok := js.jobs[id]
	if !ok {
		return errJobNotFound
	}
	if job.Status != JobStatusRunning {
		return fmt.Errorf("%w: cannot transition from %s to %s", errInvalidTransition, job.Status, JobStatusComplete)
	}
	job.Status = JobStatusComplete
	job.Result = result
	job.CompletedAt = js.now()
	return nil
}

// Fail transitions a job from running to failed status with the given error message.
// Returns [errInvalidTransition] if the job is not in running state.
func (js *JobStore) Fail(id string, errMsg string) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	job, ok := js.jobs[id]
	if !ok {
		return errJobNotFound
	}
	if job.Status != JobStatusRunning {
		return fmt.Errorf("%w: cannot transition from %s to %s", errInvalidTransition, job.Status, JobStatusFailed)
	}
	job.Status = JobStatusFailed
	job.Error = errMsg
	job.CompletedAt = js.now()
	return nil
}

// Cancel transitions a job to cancelled status.
// Only jobs in pending or running state can be cancelled.
// Returns [errInvalidTransition] if the job is already in a terminal state.
//
// Note: Cancel only updates the job's status in the store. It does not
// stop the background goroutine executing the handler (since the context
// is detached via [context.WithoutCancel]). The goroutine will run to
// completion but its result will be discarded because the store will
// reject the subsequent Complete/Fail transition. Callers who need
// cooperative cancellation should use the Sandbox middleware with a
// timeout or implement cancellation within the handler itself.
func (js *JobStore) Cancel(id string) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	job, ok := js.jobs[id]
	if !ok {
		return errJobNotFound
	}
	if job.Status != JobStatusPending && job.Status != JobStatusRunning {
		return fmt.Errorf("%w: cannot transition from %s to %s", errInvalidTransition, job.Status, JobStatusCancelled)
	}
	job.Status = JobStatusCancelled
	job.CompletedAt = js.now()
	return nil
}

// Delete removes a job from the store.
// Returns [errJobNotFound] if the job does not exist.
func (js *JobStore) Delete(id string) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	if _, ok := js.jobs[id]; !ok {
		return errJobNotFound
	}
	delete(js.jobs, id)
	return nil
}

// List returns all jobs. The returned slice is a snapshot.
// Note: The Content slice is deep-copied, but StructuredContent is not.
// If StructuredContent holds a mutable reference (map, slice, pointer), callers
// may observe shared state. Treat StructuredContent as immutable after submission.
func (js *JobStore) List() []*Job {
	js.mu.RLock()
	defer js.mu.RUnlock()

	result := make([]*Job, 0, len(js.jobs))
	for _, j := range js.jobs {
		result = append(result, copyJob(j))
	}
	return result
}
