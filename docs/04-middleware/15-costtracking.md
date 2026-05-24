---
url: "/docs/middleware/costtracking/"
title: "Cost Tracking"
description: "Per-tool usage and cost metrics"
weight: 14
---

The cost tracking middleware records per-tool cost and usage metrics.

## Usage

```go
s.Use(middleware.CostTracking(
    middleware.WithCostCollector(middleware.CostCollectorFunc(func(ctx context.Context, record middleware.CostRecord) {
        fmt.Printf("[COST] tool=%s duration=%v cost=%.4f %s\n",
            record.ToolName, record.Duration, record.Cost, record.CostUnit)
    })),
))
```

## CostRecord

| Field | Type | Description |
|-------|------|-------------|
| `Timestamp` | `time.Time` | When the call started |
| `ToolName` | `string` | Tool name |
| `RequestID` | `any` | JSON-RPC request ID |
| `InputSize` | `int` | Input size in bytes |
| `OutputSize` | `int` | Output size in bytes |
| `Duration` | `time.Duration` | Execution time |
| `Success` | `bool` | Whether the call succeeded |
| `Cost` | `float64` | Computed cost |
| `CostUnit` | `string` | Cost unit label |
| `Metadata` | `map[string]any` | Additional metadata |

## Options

| Option | Description |
|--------|-------------|
| `WithCostCollector(c)` | Where to send cost records |
| `WithDefaultCostFunc(fn)` | Default cost computation function |
| `WithToolCostFunc(name, fn)` | Per-tool cost computation |
| `WithCostIncludeTools(names...)` | Only track these tools |
| `WithCostExcludeTools(names...)` | Skip these tools |
| `WithCostOnError(fn)` | Error handler for collector failures |

## Custom Cost Functions

```go
middleware.WithDefaultCostFunc(func(record middleware.CostRecord) middleware.CostRecord {
    record.Cost = float64(record.Duration.Milliseconds()) * 0.001
    record.CostUnit = "credits"
    return record
})

middleware.WithToolCostFunc("expensive-tool", func(record middleware.CostRecord) middleware.CostRecord {
    record.Cost = 10.0
    record.CostUnit = "credits"
    return record
})
```
