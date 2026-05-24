---
url: "/docs/deployment/embedding/"
title: "Embedding"
description: "Mount an MCP server alongside existing HTTP routes in your application"
weight: 1
---

Instead of dedicating a port to MCP, you can embed the MCP handler into an existing HTTP server.

## Using transport.Handler

`transport.Handler(s)` returns a standard `http.Handler`:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "github.com/finemcp/finemcp"
    "github.com/finemcp/finemcp/transport"
)

func main() {
    s := finemcp.NewServer("embedded", "1.0.0")

    tool, _ := finemcp.NewTool("ping",
        func(ctx context.Context, input []byte) ([]byte, error) {
            return []byte("pong"), nil
        },
        finemcp.WithDescription("Health check"),
    )
    s.RegisterTool(tool)

    mux := http.NewServeMux()
    mux.Handle("/mcp", transport.Handler(s))
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("ok"))
    })
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("My Application"))
    })

    fmt.Println("Server on :8080 — MCP at /mcp, health at /health")
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

## Available Handlers

All transports provide handler constructors:

| Function | Returns |
|----------|---------|
| `transport.Handler(s)` | Basic HTTP handler |
| `transport.StreamableHandler(s, opts...)` | Streamable HTTP handler |
| `transport.SSEHandler(s, opts...)` | SSE handler |
| `transport.WebSocketHandler(s, opts...)` | WebSocket handler |
| `transport.DocsHandler(s, opts...)` | Interactive API docs UI |

## Full-Featured Example

```go
mux := http.NewServeMux()

// MCP endpoint with Streamable HTTP
mux.Handle("/mcp", transport.StreamableHandler(s))

// Interactive API docs
mux.Handle("/docs", transport.DocsHandler(s,
    transport.WithDocsTitle("My MCP Server"),
    transport.WithDocsBaseURL("http://localhost:8080"),
))

// WebSocket endpoint
mux.Handle("/ws", transport.WebSocketHandler(s))

// Regular HTTP routes
mux.HandleFunc("/health", healthHandler)
mux.HandleFunc("/api/v1/", apiHandler)

log.Fatal(http.ListenAndServe(":8080", mux))
```

## With Auth Middleware

Wrap the handler with HTTP authentication:

```go
mcpHandler := transport.Handler(s)
authedHandler := middleware.HTTPAuth(verifier, mcpHandler)

mux.Handle("/mcp", authedHandler)
```

## Testing

```bash
# Regular HTTP endpoint
curl http://localhost:8080/health

# MCP endpoint
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}'
```
