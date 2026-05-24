---
url: "/docs/middleware/recovery/"
title: "Recovery"
description: "Catch panics and convert to error responses"
weight: 4
---

The recovery middleware catches panics in tool handlers and converts them to error responses instead of crashing the server.

## Usage

```go
s.Use(middleware.Recovery())
```

If a tool handler panics:

```go
tool, _ := finemcp.NewTool("risky",
    func(ctx context.Context, input []byte) ([]byte, error) {
        panic("something went wrong") // Caught by Recovery
    },
)
```

The client receives an error response instead of the server crashing.

## Custom Panic Handler

Customize the error message returned to the client:

```go
s.Use(middleware.RecoveryWithHandler(func(ctx context.Context, panicVal any) string {
    log.Printf("PANIC: %v", panicVal)
    return "Internal server error. Please try again."
}))
```

The default handler returns a generic "internal error" message.

## Best Practice

Always register `Recovery()` as the **outermost** middleware:

```go
s.Use(middleware.Recovery())       // First = outermost
s.Use(middleware.Logging(logger))
s.Use(middleware.Validation())
// ... other middleware
```

This ensures panics in any layer are caught.
