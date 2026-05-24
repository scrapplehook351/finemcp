---
url: "/docs/features/logging/"
title: "Logging"
description: "Send structured log messages from the server to the client"
weight: 5
---

Servers can send structured log messages to connected clients. An optional log handler controls which levels are allowed.

## Sending Logs

```go
s.SendLogMessage(ctx, finemcp.LogLevelInfo, "worker",
    map[string]any{"msg": "processing", "step": 1})

s.SendLogMessage(ctx, finemcp.LogLevelWarning, "db",
    map[string]any{"msg": "slow query", "ms": 450})

s.SendLogMessage(ctx, finemcp.LogLevelError, "auth",
    map[string]any{"msg": "invalid token", "attempts": 3})
```

## SendLogMessage API

```go
func (s *Server) SendLogMessage(ctx context.Context, level LogLevel, logger string, data any) error
```

| Parameter | Description |
|-----------|-------------|
| `level` | Log severity |
| `logger` | Logger name (e.g., `"worker"`, `"db"`, `"auth"`) |
| `data` | Structured data sent as JSON (maps, strings, etc.) |

## Log Levels

| Constant | Severity |
|----------|----------|
| `LogLevelDebug` | Detailed diagnostic info |
| `LogLevelInfo` | General operational messages |
| `LogLevelNotice` | Normal but significant events |
| `LogLevelWarning` | Potential issues |
| `LogLevelError` | Error conditions |
| `LogLevelCritical` | Critical failures |
| `LogLevelAlert` | Action required immediately |
| `LogLevelEmergency` | System unusable |

## Log Handler

Control which log levels are sent to clients:

```go
s.SetLogHandler(func(ctx context.Context, level finemcp.LogLevel) error {
    if level == finemcp.LogLevelDebug {
        return fmt.Errorf("debug suppressed") // Block debug logs
    }
    return nil // Allow all other levels
})
```

Return `nil` to allow the log, return an error to suppress it.

## Wire Format

Logs are sent as `notifications/message`:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/message",
  "params": {
    "level": "warning",
    "logger": "db",
    "data": { "msg": "slow query", "ms": 450 }
  }
}
```
