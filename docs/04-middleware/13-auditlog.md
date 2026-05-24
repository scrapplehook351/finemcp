---
url: "/docs/middleware/auditlog/"
title: "Audit Log"
description: "Compliance audit trail for tool invocations"
weight: 13
---

The audit log middleware records all tool invocations with detailed metadata for compliance and debugging.

## Usage

```go
s.Use(middleware.AuditLog(
    middleware.WithAuditSink(middleware.AuditSinkFunc(func(ctx context.Context, entry middleware.AuditEntry) {
        fmt.Printf("[AUDIT] tool=%s duration=%v success=%v\n",
            entry.ToolName, entry.Duration, entry.Success)
    })),
))
```

## AuditEntry

| Field | Type | Description |
|-------|------|-------------|
| `Timestamp` | `time.Time` | When the call started |
| `ToolName` | `string` | Name of the tool |
| `RequestID` | `any` | JSON-RPC request ID |
| `InputHash` | `string` | Hash of the input (if enabled) |
| `InputSize` | `int` | Input size in bytes |
| `OutputSize` | `int` | Output size in bytes |
| `Duration` | `time.Duration` | Execution time |
| `Success` | `bool` | Whether the call succeeded |
| `ErrorMessage` | `string` | Error message if failed |

## Options

| Option | Description |
|--------|-------------|
| `WithAuditSink(s)` | Where to send audit entries |
| `WithAuditIncludeTools(names...)` | Only audit these tools |
| `WithAuditExcludeTools(names...)` | Skip these tools |
| `WithAuditHashInput(bool)` | Include input hash in entries |
| `WithAuditOnError(fn)` | Error handler for sink failures |

## Audit Sink Interface

```go
type AuditSink interface {
    Log(ctx context.Context, entry AuditEntry)
}

// Or as a function:
type AuditSinkFunc func(ctx context.Context, entry AuditEntry)
```
