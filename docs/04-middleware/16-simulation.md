---
url: "/docs/middleware/simulation/"
title: "Simulation"
description: "Dry-run mode for destructive tool operations"
weight: 16
---

The simulation middleware runs destructive tools in dry-run mode without executing side effects.

## Usage

```go
s.Use(middleware.Simulation())
```

## How It Works

1. Tools marked with `WithDestructive()` are intercepted
2. If a simulator function is set, it runs instead of the real handler
3. If no simulator, the tool checks `IsSimulatedFromCtx(ctx)` and can skip side effects
4. Non-destructive tools run normally

## Marking Destructive Tools

```go
tool, _ := finemcp.NewTool("delete-db", handler,
    finemcp.WithDestructive(),
)
```

## Custom Simulator

Provide a dedicated simulation handler:

```go
tool, _ := finemcp.NewTool("delete-db",
    realHandler,
    finemcp.WithDestructive(),
    finemcp.WithSimulator(func(ctx context.Context, input []byte) ([]byte, error) {
        return []byte("[SIMULATED] Would delete database"), nil
    }),
)
```

## Checking Simulation in Handlers

```go
func handler(ctx context.Context, input []byte) ([]byte, error) {
    if finemcp.IsSimulatedFromCtx(ctx) {
        return []byte("Would perform action (dry run)"), nil
    }
    // Real implementation
    return []byte("Action performed"), nil
}
```

## Options

| Option | Description |
|--------|-------------|
| `WithMaxDepth(n)` | Maximum simulation recursion depth |

## Error

```go
var middleware.ErrSimulationDepthExceeded
```
