---
url: "/docs/middleware/logging/"
title: "Logging"
description: "Structured invocation logging for tool calls"
weight: 12
---

The logging middleware logs every tool invocation with duration and result status.

## Usage

```go
s.Use(middleware.Logging(logger))
```

## Logger Interface

```go
type Logger interface {
    Info(msg string, keysAndValues ...any)
    Error(msg string, keysAndValues ...any)
}
```

Compatible with most structured loggers (slog, zap, zerolog wrappers).

## Example Logger

```go
type simpleLogger struct{}

func (l *simpleLogger) Info(msg string, kv ...any) {
    log.Printf("[INFO] %s %v", msg, kv)
}

func (l *simpleLogger) Error(msg string, kv ...any) {
    log.Printf("[ERROR] %s %v", msg, kv)
}

s.Use(middleware.Logging(&simpleLogger{}))
```

## NopLogger

A no-op logger for testing or when logging is unwanted:

```go
s.Use(middleware.Logging(middleware.NopLogger))
```

## Log Output

Successful calls log with `Info`, failed calls with `Error`. Log messages include:

- Tool name
- Duration
- Success/failure status
