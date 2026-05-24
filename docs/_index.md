---
title: "finemcp"
description: "A batteries-included Go framework for building Model Context Protocol (MCP) servers"
---

# finemcp

**finemcp** is a batteries-included Go framework for building [Model Context Protocol](https://modelcontextprotocol.io) (MCP) servers. It provides a clean, idiomatic API for creating tools, resources, prompts, and more — with built-in support for streaming, middleware, multi-tenancy, and multiple transports.

## Features

- **Tools** — Raw handlers, typed handlers with auto-generated JSON Schema, annotations, session-scoped tools
- **Resources** — Static resources, URI templates (RFC 6570), subscriptions with change notifications
- **Prompts** — Reusable prompt templates with arguments and auto-completion
- **Streaming** — Real-time streaming via `ToolStream` over Streamable HTTP
- **Transports** — HTTP, Streamable HTTP, SSE, WebSocket, and Stdio
- **Middleware** — 16 composable middleware: auth, RBAC, rate limiting, caching, circuit breaker, and more
- **Sampling & Elicitation** — Server-initiated LLM requests and user prompts
- **Testing** — In-process testing with `mcptest` — no network required
- **Composition** — Pipeline and Parallel handler composition

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/finemcp/finemcp"
    "github.com/finemcp/finemcp/transport"
)

func main() {
    s := finemcp.NewServer("my-server", "1.0.0")

    tool, _ := finemcp.NewTool("hello",
        func(ctx context.Context, input []byte) ([]byte, error) {
            return []byte("Hello, World!"), nil
        },
        finemcp.WithDescription("Say hello"),
    )
    s.RegisterTool(tool)

    log.Fatal(transport.StartHTTP(s, ":8080"))
}
```

```bash
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'
```

## Documentation

{{< cards >}}
  {{< card link="getting-started" title="Getting Started" subtitle="Installation, quickstart, and your first MCP server" >}}
  {{< card link="concepts" title="Concepts" subtitle="Server, tools, resources, prompts, and transports" >}}
  {{< card link="features" title="Features" subtitle="Streaming, sampling, progress, logging, and more" >}}
  {{< card link="middleware" title="Middleware" subtitle="Auth, RBAC, caching, rate limiting, and 12 more" >}}
  {{< card link="testing" title="Testing" subtitle="In-process testing with mcptest" >}}
  {{< card link="deployment" title="Deployment" subtitle="Embedding MCP in existing applications" >}}
{{< /cards >}}

## Protocol Version

finemcp implements MCP protocol version **2025-11-25** with backwards compatibility for `2025-03-26` and `2024-11-05`.
