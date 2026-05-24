# 09 — Progress

Demonstrates how tools report **incremental progress** to the client during long-running operations.

## How It Works

Tools call `finemcp.ReportProgress(ctx, current, total)` to send progress notifications. The client receives these as `notifications/progress` messages and can display a progress bar or percentage.

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
    finemcp.WithDescription("Process data with progress updates"),
)
```

This sends 11 progress notifications (0, 10, 20, ... 100) before returning the final result.

## Progress API

```go
finemcp.ReportProgress(ctx context.Context, current float64, total float64)
```

| Parameter | Description |
|-----------|-------------|
| `ctx` | Context from the tool handler |
| `current` | Current progress value |
| `total` | Total expected value (used to calculate percentage) |

Progress notifications are sent as JSON-RPC notifications:
```json
{
  "jsonrpc": "2.0",
  "method": "notifications/progress",
  "params": { "progressToken": "...", "progress": 50, "total": 100 }
}
```

## Testing with curl

```bash
go run ./09-progress

# Initialize
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Call process-data (progress notifications are sent as separate messages)
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "process-data" }
}'
```

> **Note:** With `StartHTTP`, progress notifications cannot be delivered mid-request. Use `StartStreamable` to see real-time progress via SSE.
