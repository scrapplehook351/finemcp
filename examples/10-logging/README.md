# 10 — Logging

Demonstrates the MCP server logging API for sending **structured log messages** to the client.

## How It Works

Servers send log messages to clients via `SendLogMessage`. An optional `SetLogHandler` controls which log levels are allowed — returning an error suppresses that log.

```go
s := finemcp.NewServer("logging", "1.0.0")

// Suppress debug-level logs
s.SetLogHandler(func(ctx context.Context, level finemcp.LogLevel) error {
    if level == finemcp.LogLevelDebug {
        return fmt.Errorf("debug suppressed")
    }
    return nil
})
```

## Log Levels

| Constant | Description |
|----------|-------------|
| `finemcp.LogLevelDebug` | Detailed diagnostic info |
| `finemcp.LogLevelInfo` | General operational messages |
| `finemcp.LogLevelWarning` | Potential issues |
| `finemcp.LogLevelError` | Error conditions |

## Example

The `do-work` tool emits logs at all four levels:

```go
tool, _ := finemcp.NewTool("do-work",
    func(ctx context.Context, input []byte) ([]byte, error) {
        s.SendLogMessage(ctx, finemcp.LogLevelDebug, "worker",
            map[string]any{"msg": "starting work", "step": 0})
        s.SendLogMessage(ctx, finemcp.LogLevelInfo, "worker",
            map[string]any{"msg": "processing", "step": 1})
        s.SendLogMessage(ctx, finemcp.LogLevelWarning, "worker",
            map[string]any{"msg": "slow query detected", "ms": 450})
        s.SendLogMessage(ctx, finemcp.LogLevelError, "worker",
            map[string]any{"msg": "retrying failed operation", "attempt": 2})
        return []byte("Work done. Check logs."), nil
    },
    finemcp.WithDescription("Perform work and emit log messages"),
)
```

## SendLogMessage API

```go
s.SendLogMessage(ctx context.Context, level LogLevel, logger string, data any) error
```

| Parameter | Description |
|-----------|-------------|
| `level` | Log severity level |
| `logger` | Logger name (e.g., `"worker"`, `"db"`) |
| `data` | Structured data (maps, strings, etc.) sent as JSON |

Log messages are delivered as `notifications/message`:
```json
{
  "jsonrpc": "2.0",
  "method": "notifications/message",
  "params": { "level": "warning", "logger": "worker", "data": { "msg": "slow query", "ms": 450 } }
}
```

## Testing with curl

```bash
go run ./10-logging

# Initialize
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Trigger logging
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "do-work" }
}'
```
