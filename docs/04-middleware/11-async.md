---
url: "/docs/middleware/async/"
title: "Async"
description: "Convert long-running tools into background tasks"
weight: 11
---

The async middleware converts tool invocations into background tasks that return immediately with a job ID.

## Usage

```go
store := finemcp.NewJobStore()
asyncMiddleware, waiter := middleware.Async(
    middleware.WithJobStore(store),
)
s.Use(asyncMiddleware)
```

Tools return immediately with a job ID. The actual work runs in the background.

## Options

| Option | Description |
|--------|-------------|
| `WithJobStore(store)` | Custom job store (required) |
| `WithIDGenerator(fn)` | Custom job ID generator |

## AsyncWaiter

The `waiter` returned by `Async()` lets you wait for all background jobs to complete:

```go
asyncMiddleware, waiter := middleware.Async()
s.Use(asyncMiddleware)

// On shutdown
waiter.Wait()
```

## JobStore

Track background jobs:

```go
store := finemcp.NewJobStore()

// Get job status
job, err := store.Get("job-id")
fmt.Println(job.Status) // JobStatusPending, Running, Complete, Failed, Cancelled

// List all jobs
jobs := store.List()
```

### Job Statuses

| Status | Description |
|--------|-------------|
| `JobStatusPending` | Queued, not started |
| `JobStatusRunning` | Currently executing |
| `JobStatusComplete` | Finished successfully |
| `JobStatusFailed` | Finished with error |
| `JobStatusCancelled` | Cancelled |

## TaskStore (Spec-Compliant)

For spec-compliant async tasks, use `TaskStore`:

```go
store := finemcp.NewTaskStore(
    finemcp.WithMaxTasks(1000),
)
s := finemcp.NewServer("async", "1.0.0",
    finemcp.WithTaskStore(store),
)
```

### TaskStore Methods

```go
task, _ := store.Submit("task-id")
task, _ := store.SubmitWithOptions("task-id", finemcp.TaskOptions{
    TTL:         intPtr(3600),
    PollInterval: intPtr(5),
    OwnerID:     "user-123",
})

store.Complete("task-id", result)
store.Fail("task-id", "error message")
store.Cancel("task-id")
store.Get("task-id")
store.GetResult("task-id")
store.List()
store.ListByOwner("user-123")
store.Delete("task-id")
```
