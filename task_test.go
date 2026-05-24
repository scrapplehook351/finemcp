package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// ── TaskStore unit tests ────────────────────────────────────────────

func TestTaskStore_Submit(t *testing.T) {
	ts := NewTaskStore()
	task, err := ts.Submit("t-1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if task.TaskID != "t-1" {
		t.Errorf("TaskID = %q, want t-1", task.TaskID)
	}
	if task.Status != TaskStatusWorking {
		t.Errorf("Status = %q, want %q", task.Status, TaskStatusWorking)
	}
	if task.CreatedAt == "" {
		t.Error("CreatedAt is empty")
	}
	if task.LastUpdatedAt == "" {
		t.Error("LastUpdatedAt is empty")
	}
}

func TestTaskStore_Submit_EmptyID(t *testing.T) {
	ts := NewTaskStore()
	_, err := ts.Submit("")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestTaskStore_Submit_DuplicateID(t *testing.T) {
	ts := NewTaskStore()
	_, _ = ts.Submit("t-1")
	_, err := ts.Submit("t-1")
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func TestTaskStore_Submit_WithTTL(t *testing.T) {
	ts := NewTaskStore()
	task, err := ts.SubmitWithOptions("t-1", TaskOptions{TTL: intPtr(30000)})
	if err != nil {
		t.Fatalf("SubmitWithOptions: %v", err)
	}
	if task.TTL == nil || *task.TTL != 30000 {
		t.Errorf("TTL = %v, want 30000", task.TTL)
	}
}

func TestTaskStore_Get(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	task, err := ts.Get("t-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.TaskID != "t-1" {
		t.Errorf("TaskID = %q, want t-1", task.TaskID)
	}
}

func TestTaskStore_Get_NotFound(t *testing.T) {
	ts := NewTaskStore()
	_, err := ts.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskStore_Get_ReturnsCopy(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	a, _ := ts.Get("t-1")
	b, _ := ts.Get("t-1")
	a.Status = TaskStatusCancelled
	if b.Status == TaskStatusCancelled {
		t.Error("Get did not return a copy")
	}
}

func TestTaskStore_Complete(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")

	result := &CallToolResult{Content: []Content{TextContent{Text: "done"}}}
	err := ts.Complete("t-1", result)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	task, _ := ts.Get("t-1")
	if task.Status != TaskStatusCompleted {
		t.Errorf("Status = %q, want %q", task.Status, TaskStatusCompleted)
	}
}

func TestTaskStore_Complete_NotFound(t *testing.T) {
	ts := NewTaskStore()
	err := ts.Complete("nonexistent", NewTextResult("done"))
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskStore_Complete_AlreadyCompleted(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	ts.Complete("t-1", NewTextResult("done"))
	err := ts.Complete("t-1", NewTextResult("again"))
	if err == nil {
		t.Fatal("expected error for already-completed task")
	}
}

func TestTaskStore_Fail(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")

	err := ts.Fail("t-1", "something broke")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}

	task, _ := ts.Get("t-1")
	if task.Status != TaskStatusFailed {
		t.Errorf("Status = %q, want %q", task.Status, TaskStatusFailed)
	}
	if task.StatusMessage == nil || *task.StatusMessage != "something broke" {
		t.Error("StatusMessage not set on fail")
	}
}

func TestTaskStore_Cancel(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")

	task, err := ts.Cancel("t-1")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if task.Status != TaskStatusCancelled {
		t.Errorf("Status = %q, want %q", task.Status, TaskStatusCancelled)
	}
}

func TestTaskStore_Cancel_AlreadyCompleted(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	ts.Complete("t-1", NewTextResult("done"))
	_, err := ts.Cancel("t-1")
	if err == nil {
		t.Fatal("expected error cancelling completed task")
	}
}

func TestTaskStore_Cancel_NotFound(t *testing.T) {
	ts := NewTaskStore()
	_, err := ts.Cancel("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskStore_GetResult(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	ts.Complete("t-1", NewTextResult("done"))

	result, err := ts.GetResult("t-1")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("result content length = %d, want 1", len(result.Content))
	}
}

func TestTaskStore_GetResult_NotCompleted(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")

	_, err := ts.GetResult("t-1")
	if err == nil {
		t.Fatal("expected error for non-completed task")
	}
}

func TestTaskStore_GetResult_NotFound(t *testing.T) {
	ts := NewTaskStore()
	_, err := ts.GetResult("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskStore_List(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	ts.Submit("t-2")

	tasks := ts.List()
	if len(tasks) != 2 {
		t.Fatalf("List length = %d, want 2", len(tasks))
	}
}

func TestTaskStore_List_ReturnsCopies(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")

	tasks := ts.List()
	tasks[0].Status = TaskStatusCancelled

	task, _ := ts.Get("t-1")
	if task.Status == TaskStatusCancelled {
		t.Error("List did not return copies")
	}
}

func TestTaskStore_Delete(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")
	err := ts.Delete("t-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = ts.Get("t-1")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestTaskStore_Clock(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	ts := NewTaskStore(withTaskClock(func() time.Time { return now }))
	task, _ := ts.Submit("t-1")
	if task.CreatedAt != "2026-03-11T12:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2026-03-11T12:00:00Z", task.CreatedAt)
	}
}

// ── Task types ──────────────────────────────────────────────────────

func TestTaskStatus_Values(t *testing.T) {
	// Verify all spec-defined statuses exist.
	statuses := []TaskStatus{
		TaskStatusWorking,
		TaskStatusInputRequired,
		TaskStatusCompleted,
		TaskStatusFailed,
		TaskStatusCancelled,
	}
	for _, s := range statuses {
		if s == "" {
			t.Error("empty TaskStatus")
		}
	}
}

func TestTask_JSONSerialization(t *testing.T) {
	task := Task{
		TaskID:        "t-1",
		Status:        TaskStatusWorking,
		CreatedAt:     "2026-03-11T12:00:00Z",
		LastUpdatedAt: "2026-03-11T12:00:00Z",
		TTL:           intPtr(60000),
		PollInterval:  intPtr(1000),
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Task
	json.Unmarshal(data, &decoded)

	if decoded.TaskID != "t-1" {
		t.Errorf("TaskID = %q, want t-1", decoded.TaskID)
	}
	if decoded.Status != TaskStatusWorking {
		t.Errorf("Status = %q, want working", decoded.Status)
	}
	if decoded.TTL == nil || *decoded.TTL != 60000 {
		t.Error("TTL not preserved")
	}
	if decoded.PollInterval == nil || *decoded.PollInterval != 1000 {
		t.Error("PollInterval not preserved")
	}
}

func TestTask_TTL_Null(t *testing.T) {
	task := Task{
		TaskID:        "t-1",
		Status:        TaskStatusWorking,
		CreatedAt:     "2026-03-11T12:00:00Z",
		LastUpdatedAt: "2026-03-11T12:00:00Z",
		TTL:           nil,
	}

	data, _ := json.Marshal(task)
	// TTL:null means unlimited retention per spec.
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if raw["ttl"] != nil {
		t.Errorf("expected ttl=null, got %v", raw["ttl"])
	}
}

// ── WithTaskStore option ────────────────────────────────────────────

func TestWithTaskStore_SetsField(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))
	if s.taskStore != ts {
		t.Error("WithTaskStore did not set the task store")
	}
}

func TestWithTaskStore_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from nil task store")
		}
	}()
	WithTaskStore(nil)
}

func TestNewServer_DefaultNoTaskStore(t *testing.T) {
	s := NewServer("test", "1.0")
	if s.taskStore != nil {
		t.Error("default server should not have a task store")
	}
}

// ── Capability advertisement ────────────────────────────────────────

func TestInitialize_NoTaskCapability_WhenNoStore(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	if result.Capabilities.Tasks != nil {
		t.Error("expected no tasks capability when task store not set")
	}
}

func TestInitialize_TaskCapability_WhenStoreSet(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	if result.Capabilities.Tasks == nil {
		t.Fatal("expected tasks capability when task store is set")
	}
}

// ── tasks/get integration tests ─────────────────────────────────────

func taskServer(t *testing.T) (*Server, *TaskStore) {
	t.Helper()
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))
	// Initialize
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatal(resp.Error.Message)
	}
	return s, ts
}

func TestTasksGet_Success(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")

	data := jsonrpcReq(2, "tasks/get", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("tasks/get error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var task Task
	json.Unmarshal(raw, &task)
	if task.TaskID != "t-1" {
		t.Errorf("taskId = %q, want t-1", task.TaskID)
	}
	if task.Status != TaskStatusWorking {
		t.Errorf("status = %q, want working", task.Status)
	}
}

func TestTasksGet_NotFound(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/get", map[string]any{"taskId": "nonexistent"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestTasksGet_MissingTaskId(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/get", map[string]any{})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for missing taskId")
	}
}

func TestTasksGet_NoTaskStore(t *testing.T) {
	// Server without task store should reject tasks/* methods.
	s := NewServer("test", "1.0")
	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	data := jsonrpcReq(2, "tasks/get", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error when no task store")
	}
}

// ── tasks/result integration tests ──────────────────────────────────

func TestTasksResult_Success(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")
	ts.Complete("t-1", NewTextResult("hello world"))

	data := jsonrpcReq(2, "tasks/result", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("tasks/result error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result CallToolResult
	json.Unmarshal(raw, &result)
	if len(result.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(result.Content))
	}
}

func TestTasksResult_NotCompleted(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")

	data := jsonrpcReq(2, "tasks/result", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for non-completed task")
	}
}

func TestTasksResult_NotFound(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/result", map[string]any{"taskId": "nonexistent"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTasksResult_MissingTaskId(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/result", map[string]any{})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for missing taskId")
	}
}

func TestTasksResult_FailedTask(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")
	ts.Fail("t-1", "something broke")

	data := jsonrpcReq(2, "tasks/result", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	// Failed tasks should also return error (no result available).
	if resp.Error == nil {
		t.Fatal("expected error for failed task")
	}
}

// ── tasks/cancel integration tests ──────────────────────────────────

func TestTasksCancel_Success(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")

	data := jsonrpcReq(2, "tasks/cancel", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("tasks/cancel error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var task Task
	json.Unmarshal(raw, &task)
	if task.Status != TaskStatusCancelled {
		t.Errorf("status = %q, want cancelled", task.Status)
	}
}

func TestTasksCancel_AlreadyCompleted(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")
	ts.Complete("t-1", NewTextResult("done"))

	data := jsonrpcReq(2, "tasks/cancel", map[string]any{"taskId": "t-1"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error cancelling completed task")
	}
}

func TestTasksCancel_NotFound(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/cancel", map[string]any{"taskId": "nonexistent"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTasksCancel_MissingTaskId(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/cancel", map[string]any{})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error for missing taskId")
	}
}

// ── tasks/list integration tests ────────────────────────────────────

func TestTasksList_Empty(t *testing.T) {
	s, _ := taskServer(t)

	data := jsonrpcReq(2, "tasks/list", nil)
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("tasks/list error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListTasksResult
	json.Unmarshal(raw, &result)
	if len(result.Tasks) != 0 {
		t.Errorf("tasks length = %d, want 0", len(result.Tasks))
	}
}

func TestTasksList_WithTasks(t *testing.T) {
	s, ts := taskServer(t)
	ts.Submit("t-1")
	ts.Submit("t-2")

	data := jsonrpcReq(2, "tasks/list", nil)
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("tasks/list error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListTasksResult
	json.Unmarshal(raw, &result)
	if len(result.Tasks) != 2 {
		t.Errorf("tasks length = %d, want 2", len(result.Tasks))
	}
}

func TestTasksList_NoTaskStore(t *testing.T) {
	s := NewServer("test", "1.0")
	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	data := jsonrpcReq(2, "tasks/list", nil)
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil {
		t.Fatal("expected error when no task store")
	}
}

// ── tasks/* before init ─────────────────────────────────────────────

func TestTasks_BeforeInit(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	methods := []string{"tasks/get", "tasks/result", "tasks/cancel", "tasks/list"}
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			data := jsonrpcReq(1, m, map[string]any{"taskId": "t-1"})
			resp, _ := s.HandleMessage(context.Background(), data)
			if resp.Error == nil {
				t.Fatalf("%s should fail before init", m)
			}
		})
	}
}

// ── Task-augmented tools/call ───────────────────────────────────────

func TestToolsCall_TaskAugmented_ReturnsTask(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	// Register a slow tool.
	tool, _ := NewTool("slow-tool", func(ctx context.Context, input []byte) ([]byte, error) {
		time.Sleep(100 * time.Millisecond)
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	// Initialize.
	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	// Call with task metadata.
	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "slow-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error != nil {
		t.Fatalf("task-augmented tools/call error: %s", resp.Error.Message)
	}

	// Response should be a CreateTaskResult.
	raw, _ := json.Marshal(resp.Result)
	var result CreateTaskResult
	json.Unmarshal(raw, &result)
	if result.Task.TaskID == "" {
		t.Fatal("expected non-empty taskId in CreateTaskResult")
	}
	if result.Task.Status != TaskStatusWorking {
		t.Errorf("status = %q, want working", result.Task.Status)
	}
}

func TestToolsCall_TaskAugmented_WithTTL(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	tool, _ := NewTool("slow-tool", func(ctx context.Context, input []byte) ([]byte, error) {
		time.Sleep(100 * time.Millisecond)
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "slow-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{"ttl": 30000},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error != nil {
		t.Fatalf("tools/call error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result CreateTaskResult
	json.Unmarshal(raw, &result)
	if result.Task.TTL == nil || *result.Task.TTL != 30000 {
		t.Errorf("TTL = %v, want 30000", result.Task.TTL)
	}
}

func TestToolsCall_TaskAugmented_NoTaskStore(t *testing.T) {
	// Server without task store should reject task-augmented calls.
	s := NewServer("test", "1.0")
	tool, _ := NewTool("my-tool", func(ctx context.Context, input []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "my-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error == nil {
		t.Fatal("expected error for task-augmented call without task store")
	}
}

func TestToolsCall_Normal_StillWorks(t *testing.T) {
	// Ensure normal (non-task-augmented) tools/call still works with a task store.
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	tool, _ := NewTool("my-tool", func(ctx context.Context, input []byte) ([]byte, error) {
		return []byte("hello"), nil
	})
	s.RegisterTool(tool)

	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "my-tool",
		"arguments": map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error != nil {
		t.Fatalf("normal tools/call error: %s", resp.Error.Message)
	}

	// Should be a normal CallToolResult, not CreateTaskResult.
	raw, _ := json.Marshal(resp.Result)
	var result CallToolResult
	json.Unmarshal(raw, &result)
	if len(result.Content) == 0 {
		t.Error("expected content in normal call result")
	}
}

func TestToolsCall_TaskAugmented_ToolNotFound(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))
	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "nonexistent",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestToolsCall_TaskAugmented_PollResult(t *testing.T) {
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	tool, _ := NewTool("fast-tool", func(ctx context.Context, input []byte) ([]byte, error) {
		return []byte("result-data"), nil
	})
	s.RegisterTool(tool)

	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	// Start task-augmented call.
	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "fast-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error != nil {
		t.Fatalf("tools/call error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var createResult CreateTaskResult
	json.Unmarshal(raw, &createResult)
	taskID := createResult.Task.TaskID

	// Wait for completion (fast tool should finish quickly).
	time.Sleep(200 * time.Millisecond)

	// Poll for result.
	resultData := jsonrpcReq(3, "tasks/result", map[string]any{"taskId": taskID})
	resp2, _ := s.HandleMessage(context.Background(), resultData)
	if resp2.Error != nil {
		t.Fatalf("tasks/result error: %s", resp2.Error.Message)
	}
}

// ── Helper ──────────────────────────────────────────────────────────

func intPtr(v int) *int {
	return &v
}

// ── Max tasks limit (Issue 2) ───────────────────────────────────────

func TestTaskStore_Submit_LimitExceeded(t *testing.T) {
	ts := NewTaskStore(WithMaxTasks(2))
	ts.Submit("t-1")
	ts.Submit("t-2")
	_, err := ts.Submit("t-3")
	if err == nil {
		t.Fatal("expected error when task limit exceeded")
	}
	if err.Error() != "task limit exceeded" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTaskStore_Submit_LimitNotHitAfterDelete(t *testing.T) {
	ts := NewTaskStore(WithMaxTasks(2))
	ts.Submit("t-1")
	ts.Submit("t-2")
	ts.Delete("t-1")
	_, err := ts.Submit("t-3")
	if err != nil {
		t.Fatalf("should succeed after delete: %v", err)
	}
}

func TestTaskStore_Submit_UnlimitedByDefault(t *testing.T) {
	ts := NewTaskStore()
	for i := 0; i < 100; i++ {
		_, err := ts.Submit(fmt.Sprintf("t-%d", i))
		if err != nil {
			t.Fatalf("Submit #%d: %v", i, err)
		}
	}
}

// ── Random task IDs (Issue 3) ───────────────────────────────────────

func TestTaskStore_GenerateID_Format(t *testing.T) {
	ts := NewTaskStore()
	id := ts.GenerateID()
	// "task-" + 32 hex chars = 37 chars total
	if len(id) != 37 {
		t.Errorf("GenerateID length = %d, want 37; got %q", len(id), id)
	}
	if id[:5] != "task-" {
		t.Errorf("GenerateID prefix = %q, want 'task-'", id[:5])
	}
}

func TestTaskStore_GenerateID_Unique(t *testing.T) {
	ts := NewTaskStore()
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := ts.GenerateID()
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

// ── Cancel calls cancelFn (Improvement 1) ───────────────────────────

func TestTaskStore_Cancel_InvokesCancelFunc(t *testing.T) {
	ts := NewTaskStore()
	ts.Submit("t-1")

	cancelled := false
	ts.storeCancelFunc("t-1", func() { cancelled = true })

	ts.Cancel("t-1")
	if !cancelled {
		t.Error("Cancel did not invoke the cancel function")
	}
}

// ── Panic recovery (Issue 1) ────────────────────────────────────────

func TestToolsCall_TaskAugmented_PanicRecovery(t *testing.T) {
	tStore := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(tStore))

	tool, _ := NewTool("panic-tool", func(ctx context.Context, input []byte) ([]byte, error) {
		panic("boom")
	})
	s.RegisterTool(tool)

	initData := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initData)

	callData := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "panic-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, _ := s.HandleMessage(context.Background(), callData)
	if resp.Error != nil {
		t.Fatalf("expected CreateTaskResult, got error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result CreateTaskResult
	json.Unmarshal(raw, &result)
	taskID := result.Task.TaskID

	// Wait for goroutine to recover from panic.
	time.Sleep(200 * time.Millisecond)

	task, _ := tStore.Get(taskID)
	if task.Status != TaskStatusFailed {
		t.Errorf("expected failed status after panic, got %q", task.Status)
	}
	if task.StatusMessage == nil || *task.StatusMessage != "panic: boom" {
		t.Errorf("expected panic message, got %v", task.StatusMessage)
	}
}

// ── Pagination for tasks/list (Issue 6) ─────────────────────────────

func TestTaskStore_ListByOwner(t *testing.T) {
	ts := NewTaskStore()

	// Submit tasks with different owners.
	ts.SubmitWithOptions("a", TaskOptions{OwnerID: "alice"})
	ts.SubmitWithOptions("b", TaskOptions{OwnerID: "bob"})
	ts.SubmitWithOptions("c", TaskOptions{}) // no owner

	// Alice sees her own task + unowned.
	aliceTasks := ts.ListByOwner("alice")
	if len(aliceTasks) != 2 {
		t.Fatalf("alice sees %d tasks, want 2", len(aliceTasks))
	}
	ids := []string{aliceTasks[0].TaskID, aliceTasks[1].TaskID}
	if ids[0] != "a" || ids[1] != "c" {
		t.Fatalf("alice got IDs %v, want [a c]", ids)
	}

	// Bob sees his own task + unowned.
	bobTasks := ts.ListByOwner("bob")
	if len(bobTasks) != 2 {
		t.Fatalf("bob sees %d tasks, want 2", len(bobTasks))
	}

	// Empty ownerID sees all tasks (no filtering).
	allTasks := ts.ListByOwner("")
	if len(allTasks) != 3 {
		t.Fatalf("empty owner sees %d tasks, want 3", len(allTasks))
	}
}

func TestTasksList_FiltersByOwnership(t *testing.T) {
	s, ts := taskServer(t)

	// Submit tasks owned by different sessions.
	ts.SubmitWithOptions("task-alice", TaskOptions{OwnerID: "alice"})
	ts.SubmitWithOptions("task-bob", TaskOptions{OwnerID: "bob"})
	ts.SubmitWithOptions("task-none", TaskOptions{}) // unowned

	// Alice's context should only see her task + unowned.
	aliceCtx := WithSubscriberID(context.Background(), "alice")
	data := jsonrpcReq(10, "tasks/list", nil)
	resp, _ := s.HandleMessage(aliceCtx, data)
	if resp.Error != nil {
		t.Fatalf("tasks/list error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListTasksResult
	json.Unmarshal(raw, &result)
	if len(result.Tasks) != 2 {
		t.Fatalf("alice sees %d tasks, want 2", len(result.Tasks))
	}
	for _, task := range result.Tasks {
		if task.TaskID == "task-bob" {
			t.Fatal("alice should not see bob's task")
		}
	}
}

func TestTasksList_Pagination(t *testing.T) {
	s, ts := taskServer(t)
	for i := 0; i < 5; i++ {
		ts.Submit(fmt.Sprintf("task-%02d", i))
	}

	// Request first page of 2.
	data := jsonrpcReq(2, "tasks/list", map[string]any{"limit": 2})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("tasks/list error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListTasksResult
	json.Unmarshal(raw, &result)
	if len(result.Tasks) != 2 {
		t.Fatalf("page 1 length = %d, want 2", len(result.Tasks))
	}
	if result.NextCursor == "" {
		t.Fatal("expected nextCursor for page 1")
	}

	// Request second page.
	data2 := jsonrpcReq(3, "tasks/list", map[string]any{"cursor": result.NextCursor, "limit": 2})
	resp2, _ := s.HandleMessage(context.Background(), data2)
	if resp2.Error != nil {
		t.Fatalf("tasks/list page 2 error: %s", resp2.Error.Message)
	}

	raw2, _ := json.Marshal(resp2.Result)
	var result2 ListTasksResult
	json.Unmarshal(raw2, &result2)
	if len(result2.Tasks) != 2 {
		t.Fatalf("page 2 length = %d, want 2", len(result2.Tasks))
	}
}
