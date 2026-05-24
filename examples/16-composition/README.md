# 16 — Composition

Demonstrates **Pipeline** and **Parallel** composition patterns for combining multiple tool handlers.

## How It Works

Instead of writing monolithic tool handlers, you can compose smaller functions:

- **Pipeline** — chains handlers sequentially (output of one feeds into the next)
- **Parallel** — runs handlers concurrently and merges results

## Pipeline

Sequential handler chain where each step transforms the data:

```go
validate := func(ctx context.Context, input []byte) ([]byte, error) {
    // validate and pass through
    return input, nil
}
process := func(ctx context.Context, input []byte) ([]byte, error) {
    // transform data
    return processed, nil
}
enrich := func(ctx context.Context, input []byte) ([]byte, error) {
    // add metadata
    return enriched, nil
}

tool, _ := finemcp.NewTool("pipeline-tool",
    finemcp.Pipeline(validate, process, enrich),
    finemcp.WithDescription("Validate → Process → Enrich"),
)
```

Execution flow:
```
input → validate → process → enrich → output
```

## Parallel

Concurrent fan-out where multiple handlers run simultaneously:

```go
tool, _ := finemcp.NewTool("parallel-tool",
    finemcp.Parallel(
        finemcp.NamedHandler{Name: "sentiment", Handler: analyzeSentiment},
        finemcp.NamedHandler{Name: "keywords", Handler: extractKeywords},
        finemcp.NamedHandler{Name: "summary", Handler: summarize},
    ),
    finemcp.WithDescription("Run sentiment, keywords, and summary in parallel"),
)
```

Each handler receives the same input. Results are merged into a single response with named sections.

## Testing with curl

```bash
go run ./16-composition

curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Pipeline tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "tools/call",
  "params": { "name": "pipeline-tool", "arguments": { "text": "hello world" } }
}'

# Parallel tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "parallel-tool", "arguments": { "text": "hello world" } }
}'
```
