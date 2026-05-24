---
url: "/docs/middleware/sandbox/"
title: "Sandbox"
description: "Timeout and output size limits for tool execution"
weight: 7
---

The sandbox middleware restricts tool execution with timeout and output size limits.

## Usage

```go
s.Use(middleware.Sandbox(
    middleware.WithTimeout(5 * time.Second),
    middleware.WithMaxOutputSize(1024 * 1024), // 1MB
))
```

## Options

| Option | Description |
|--------|-------------|
| `WithTimeout(d)` | Maximum execution time |
| `WithMaxOutputSize(n)` | Maximum output size in bytes |

## Behavior

- **Timeout** — If the tool handler exceeds the timeout, the context is cancelled and `ErrSandboxTimeout` is returned.
- **Output size** — If the tool output exceeds the max size, the result is truncated or an error is returned.

## Error

```go
var middleware.ErrSandboxTimeout
```
