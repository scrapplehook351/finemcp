package finemcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// TaskStatus represents the lifecycle state of a task per the MCP specification.
type TaskStatus string

const (
	// TaskStatusWorking means the request is currently being processed.
	TaskStatusWorking TaskStatus = "working"

	// TaskStatusInputRequired means the task is waiting for input
	// (e.g., elicitation or sampling).
	TaskStatusInputRequired TaskStatus = "input_required"

	// TaskStatusCompleted means the request completed successfully and
	// results are available via tasks/result.
	TaskStatusCompleted TaskStatus = "completed"

	// TaskStatusFailed means the request did not complete successfully.
	TaskStatusFailed TaskStatus = "failed"

	// TaskStatusCancelled means the request was cancelled before completion.
	TaskStatusCancelled TaskStatus = "cancelled"
)

var (
	errTaskNotFound       = errors.New("task not found")
	errTaskIDEmpty        = errors.New("task ID must not be empty")
	errTaskIDExists       = errors.New("task ID already exists")
	errTaskInvalidTransit = errors.New("invalid task status transition")
	errTaskNotCompleted   = errors.New("task is not completed")
	errTaskLimitExceeded  = errors.New("task limit exceeded")
)

// Task is the MCP spec-compliant representation of an asynchronous task.
// It is returned in responses for tasks/get, tasks/cancel, tasks/list, and
// as part of CreateTaskResult for task-augmented tools/call.
type Task struct {
	// TaskID is the unique identifier for this task.
	TaskID string `json:"taskId"`

	// Status is the current task state.
	Status TaskStatus `json:"status"`

	// StatusMessage is an optional human-readable message describing the
	// current task state (e.g., error details for failed, reason for cancelled).
	StatusMessage *string `json:"statusMessage,omitempty"`

	// CreatedAt is an ISO 8601 timestamp when the task was created.
	CreatedAt string `json:"createdAt"`

	// LastUpdatedAt is an ISO 8601 timestamp when the task was last updated.
	LastUpdatedAt string `json:"lastUpdatedAt"`

	// TTL is the actual retention duration from creation in milliseconds.
	// nil means unlimited retention.
	TTL *int `json:"ttl"`

	// PollInterval is the suggested polling interval in milliseconds.
	PollInterval *int `json:"pollInterval,omitempty"`
}

// CreateTaskResult is the response to a task-augmented request (e.g., tools/call
// with task metadata). It wraps a Task handle.
type CreateTaskResult struct {
	Task Task           `json:"task"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// TaskMetadata holds the parameters for creating a task, sent by the client
// in the "task" field of a task-augmented request.
type TaskMetadata struct {
	TTL *int `json:"ttl,omitempty"`
}

// TaskIdParams is the wire format for requests that take a taskId
// (tasks/get, tasks/result, tasks/cancel).
type TaskIdParams struct {
	TaskID string         `json:"taskId"`
	Meta   map[string]any `json:"_meta,omitempty"`
}

// ListTasksResult is the response for tasks/list.
type ListTasksResult struct {
	Tasks      []Task         `json:"tasks"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// TaskCapability describes the server's task-related capabilities.
type TaskCapability struct {
	// List indicates whether the server supports tasks/list.
	List any `json:"list,omitempty"`

	// Cancel indicates whether the server supports tasks/cancel.
	Cancel any `json:"cancel,omitempty"`

	// Requests specifies which request types can be augmented with tasks.
	Requests *TaskRequestsCapability `json:"requests,omitempty"`
}

// TaskRequestsCapability describes which request types support task augmentation.
type TaskRequestsCapability struct {
	Tools *TaskToolsCapability `json:"tools,omitempty"`
}

// TaskToolsCapability describes task support for tool-related requests.
type TaskToolsCapability struct {
	Call any `json:"call,omitempty"`
}

// ── internalTask holds runtime state not exposed in wire format ─────

type internalTask struct {
	task     Task
	result   *CallToolResult    // set when status == completed
	ownerID  string             // session that created this task (may be empty for stdio)
	cancelFn context.CancelFunc // cancels the background goroutine (may be nil)
}

// ── TaskStore ───────────────────────────────────────────────────────

// TaskStore manages the lifecycle and storage of spec-compliant MCP tasks.
// It is safe for concurrent use.
//
// Warning: TaskStore has no built-in eviction. Tasks are stored indefinitely
// unless manually deleted via [TaskStore.Delete]. For long-running servers,
// callers should periodically delete completed/expired tasks.
type TaskStore struct {
	mu       sync.RWMutex
	tasks    map[string]*internalTask
	now      func() time.Time // clock override for testing
	maxTasks int              // 0 means unlimited
}

// TaskStoreOption configures a TaskStore.
type TaskStoreOption func(*TaskStore)

// withTaskClock overrides the clock for testing.
func withTaskClock(fn func() time.Time) TaskStoreOption {
	return func(ts *TaskStore) { ts.now = fn }
}

// WithMaxTasks sets the maximum number of tasks the store will hold.
// When the limit is reached, new submissions are rejected with an error.
// A value of 0 (the default) means unlimited.
func WithMaxTasks(n int) TaskStoreOption {
	return func(ts *TaskStore) { ts.maxTasks = n }
}

// NewTaskStore creates a new task store.
func NewTaskStore(opts ...TaskStoreOption) *TaskStore {
	ts := &TaskStore{
		tasks: make(map[string]*internalTask),
		now:   time.Now,
	}
	for _, o := range opts {
		o(ts)
	}
	return ts
}

// TaskOptions controls task creation behavior.
type TaskOptions struct {
	TTL          *int   // retention duration in milliseconds; nil for unlimited
	PollInterval *int   // suggested polling interval in milliseconds
	OwnerID      string // session/connection that owns this task; used for access control
}

// Submit creates a new working task with default options.
func (ts *TaskStore) Submit(id string) (*Task, error) {
	return ts.SubmitWithOptions(id, TaskOptions{})
}

// SubmitWithOptions creates a new working task with the given options.
func (ts *TaskStore) SubmitWithOptions(id string, opts TaskOptions) (*Task, error) {
	if id == "" {
		return nil, errTaskIDEmpty
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.tasks[id]; exists {
		return nil, errTaskIDExists
	}

	if ts.maxTasks > 0 && len(ts.tasks) >= ts.maxTasks {
		return nil, errTaskLimitExceeded
	}

	now := ts.now().UTC().Format(time.RFC3339)
	t := Task{
		TaskID:        id,
		Status:        TaskStatusWorking,
		CreatedAt:     now,
		LastUpdatedAt: now,
		TTL:           opts.TTL,
		PollInterval:  opts.PollInterval,
	}

	ts.tasks[id] = &internalTask{task: t, ownerID: opts.OwnerID}
	cp := t
	return &cp, nil
}

// GenerateID returns a unique, cryptographically random task ID.
// The format is "task-" followed by 32 hex characters (128 bits of entropy).
// Safe for concurrent use.
func (ts *TaskStore) GenerateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("finemcp: failed to generate random task ID: " + err.Error())
	}
	return "task-" + hex.EncodeToString(b)
}

// storeCancelFunc associates a cancellation function with an existing task.
// Called by the dispatch layer after launching a background goroutine, so
// that tasks/cancel can abort the running work.
func (ts *TaskStore) storeCancelFunc(id string, fn context.CancelFunc) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if it, ok := ts.tasks[id]; ok {
		it.cancelFn = fn
	}
}

// ownerOf returns the owner ID for a task, or "" if the task does not exist
// or has no owner. Used by the dispatch layer for access-control checks.
func (ts *TaskStore) ownerOf(id string) string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if it, ok := ts.tasks[id]; ok {
		return it.ownerID
	}
	return ""
}

// Get returns a copy of the task.
func (ts *TaskStore) Get(id string) (*Task, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	it, ok := ts.tasks[id]
	if !ok {
		return nil, errTaskNotFound
	}
	cp := it.task
	return &cp, nil
}

// Complete transitions a task from working to completed with the given result.
func (ts *TaskStore) Complete(id string, result *CallToolResult) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	it, ok := ts.tasks[id]
	if !ok {
		return errTaskNotFound
	}
	if it.task.Status != TaskStatusWorking {
		return fmt.Errorf("%w: cannot transition from %s to %s",
			errTaskInvalidTransit, it.task.Status, TaskStatusCompleted)
	}

	it.task.Status = TaskStatusCompleted
	it.task.LastUpdatedAt = ts.now().UTC().Format(time.RFC3339)
	it.result = result
	return nil
}

// Fail transitions a task from working to failed with the given error message.
func (ts *TaskStore) Fail(id string, errMsg string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	it, ok := ts.tasks[id]
	if !ok {
		return errTaskNotFound
	}
	if it.task.Status != TaskStatusWorking {
		return fmt.Errorf("%w: cannot transition from %s to %s",
			errTaskInvalidTransit, it.task.Status, TaskStatusFailed)
	}

	it.task.Status = TaskStatusFailed
	it.task.StatusMessage = &errMsg
	it.task.LastUpdatedAt = ts.now().UTC().Format(time.RFC3339)
	return nil
}

// Cancel transitions a working task to cancelled.
// If a cancel function was registered (e.g., from a background goroutine),
// it is called to abort the in-flight work.
// Returns the updated task copy.
func (ts *TaskStore) Cancel(id string) (*Task, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	it, ok := ts.tasks[id]
	if !ok {
		return nil, errTaskNotFound
	}
	if it.task.Status != TaskStatusWorking {
		return nil, fmt.Errorf("%w: cannot transition from %s to %s",
			errTaskInvalidTransit, it.task.Status, TaskStatusCancelled)
	}

	// Abort the background goroutine if one is running.
	if it.cancelFn != nil {
		it.cancelFn()
	}

	it.task.Status = TaskStatusCancelled
	it.task.LastUpdatedAt = ts.now().UTC().Format(time.RFC3339)
	cp := it.task
	return &cp, nil
}

// GetResult returns the result of a completed task.
// Returns errTaskNotCompleted if the task is not in completed state.
func (ts *TaskStore) GetResult(id string) (*CallToolResult, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	it, ok := ts.tasks[id]
	if !ok {
		return nil, errTaskNotFound
	}
	if it.task.Status != TaskStatusCompleted || it.result == nil {
		return nil, fmt.Errorf("%w: task %s is in %s state", errTaskNotCompleted, id, it.task.Status)
	}

	// Deep-copy the result. The slice is copied but individual Content
	// elements are not deep-copied; callers must not mutate them.
	cp := *it.result
	if cp.Content != nil {
		cp.Content = append(cp.Content[:0:0], cp.Content...)
	}
	return &cp, nil
}

// List returns all tasks sorted by task ID (lexicographic order).
// Each task is a copy.
func (ts *TaskStore) List() []Task {
	return ts.ListByOwner("")
}

// ListByOwner returns tasks visible to the given owner, sorted by task ID.
// When ownerID is empty every task is returned (no filtering).
// A task is visible if it has no owner or its owner matches ownerID.
// Each task is a copy.
func (ts *TaskStore) ListByOwner(ownerID string) []Task {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make([]Task, 0, len(ts.tasks))
	for _, it := range ts.tasks {
		if ownerID != "" && it.ownerID != "" && it.ownerID != ownerID {
			continue
		}
		result = append(result, it.task)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TaskID < result[j].TaskID
	})
	return result
}

// Delete removes a task from the store.
func (ts *TaskStore) Delete(id string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, ok := ts.tasks[id]; !ok {
		return errTaskNotFound
	}
	delete(ts.tasks, id)
	return nil
}
