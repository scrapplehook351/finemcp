---
url: "/docs/features/composition/"
title: "Composition"
description: "Pipeline and Parallel handler composition patterns"
weight: 9
---

finemcp provides composition patterns for combining multiple handler functions into a single tool handler.

## Pipeline

Chains handlers sequentially — the output of each handler feeds into the next:

```go
validate := func(ctx context.Context, input []byte) ([]byte, error) {
    // Validate input, return it (or modified) for the next step
    if len(input) == 0 {
        return nil, fmt.Errorf("empty input")
    }
    return input, nil
}

process := func(ctx context.Context, input []byte) ([]byte, error) {
    // Transform data
    return []byte(strings.ToUpper(string(input))), nil
}

enrich := func(ctx context.Context, input []byte) ([]byte, error) {
    // Add metadata
    return []byte(fmt.Sprintf(`{"data": %q, "processed": true}`, input)), nil
}

tool, _ := finemcp.NewTool("pipeline",
    finemcp.Pipeline(validate, process, enrich),
    finemcp.WithDescription("Validate → Process → Enrich"),
)
```

Execution flow:

```
input → validate() → process() → enrich() → output
```

If any step returns an error, the pipeline stops and the error is returned.

## Parallel

Runs multiple named handlers concurrently on the same input:

```go
tool, _ := finemcp.NewTool("analyze",
    finemcp.Parallel(
        finemcp.NamedHandler{Name: "sentiment", Handler: analyzeSentiment},
        finemcp.NamedHandler{Name: "keywords",  Handler: extractKeywords},
        finemcp.NamedHandler{Name: "summary",   Handler: summarize},
    ),
    finemcp.WithDescription("Run analysis in parallel"),
)
```

All handlers receive the same input and run concurrently. Results are merged into a JSON object keyed by name:

```json
{
  "sentiment": { "output": "positive", "error": "" },
  "keywords":  { "output": "[\"go\", \"mcp\"]", "error": "" },
  "summary":   { "output": "A Go framework for MCP", "error": "" }
}
```

## FanOutFanIn

Like `Parallel`, but with a custom merge function for full control over result aggregation:

```go
merge := func(ctx context.Context, results map[string]finemcp.ParallelResult) ([]byte, error) {
    // Custom merge logic
    var combined []string
    for name, r := range results {
        if r.Error == "" {
            combined = append(combined, fmt.Sprintf("%s: %s", name, r.Output))
        }
    }
    return []byte(strings.Join(combined, "\n")), nil
}

tool, _ := finemcp.NewTool("fan-out",
    finemcp.FanOutFanIn(merge,
        finemcp.NamedHandler{Name: "a", Handler: handlerA},
        finemcp.NamedHandler{Name: "b", Handler: handlerB},
    ),
)
```

## Types

### NamedHandler

```go
type NamedHandler struct {
    Name    string
    Handler ToolHandler
}
```

### ParallelResult

```go
type ParallelResult struct {
    Output json.RawMessage
    Error  string
}
```

### MergeFunc

```go
type MergeFunc func(ctx context.Context, results map[string]ParallelResult) ([]byte, error)
```

## When to Use

| Pattern | Use Case |
|---------|----------|
| **Pipeline** | Sequential data transformation (validate → process → format) |
| **Parallel** | Independent analysis tasks on the same input |
| **FanOutFanIn** | Parallel with custom result merging logic |
