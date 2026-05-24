---
url: "/docs/features/progress/"
title: "Progress"
description: "Report incremental progress during long-running tool operations"
weight: 4
---

Tools can report progress to the client during long-running operations. The client receives `notifications/progress` messages and can display a progress indicator.

## Usage

```go
tool, _ := finemcp.NewTool("process-data",
    func(ctx context.Context, input []byte) ([]byte, error) {
        total := 100.0
        for i := 0; i <= int(total); i += 10 {
            finemcp.ReportProgress(ctx, float64(i), total)
            time.Sleep(100 * time.Millisecond)
        }
        return []byte("Processing complete!"), nil
    },
    finemcp.WithDescription("Process data with progress"),
)
```

## API

```go
finemcp.ReportProgress(ctx context.Context, progress float64, total float64)
```

| Parameter | Description |
|-----------|-------------|
| `ctx` | Context from the tool handler |
| `progress` | Current progress value |
| `total` | Total expected value (for percentage calculation) |

## Wire Format

Progress is sent as a JSON-RPC notification:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/progress",
  "params": {
    "progressToken": "request-id",
    "progress": 50,
    "total": 100
  }
}
```

## Transport Considerations

Progress notifications are delivered via the transport's notification channel:

| Transport | Progress Delivery |
|-----------|------------------|
| `StartHTTP` | Not delivered (no notification channel) |
| `StartStreamable` | Via GET SSE connection |
| `StartSSE` | Via SSE stream |
| `StartWebSocket` | Via WebSocket frame |
| `ServeStdio` | Written to stdout |

For HTTP transport, progress calls are silently ignored. Use `StartStreamable` or another session-aware transport to see real-time progress.
