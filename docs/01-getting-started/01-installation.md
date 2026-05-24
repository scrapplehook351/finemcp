---
url: "/docs/getting-started/installation/"
title: "Installation"
description: "Install the finemcp Go module"
weight: 1
---

## Requirements

- **Go 1.25** or later

## Install

```bash
go get github.com/finemcp/finemcp@latest
```

This installs the core library. The transport and middleware sub-packages are included:

```go
import (
    "github.com/finemcp/finemcp"            // Core: Server, Tool, Resource, Prompt
    "github.com/finemcp/finemcp/transport"   // HTTP, SSE, Streamable, WebSocket, Stdio
    "github.com/finemcp/finemcp/middleware"   // Auth, RBAC, cache, rate limit, etc.
    "github.com/finemcp/finemcp/mcptest"     // In-process testing
)
```

## Verify Installation

Create a minimal server to verify everything works:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/finemcp/finemcp"
    "github.com/finemcp/finemcp/transport"
)

func main() {
    s := finemcp.NewServer("verify", "1.0.0")

    tool, err := finemcp.NewTool("ping",
        func(ctx context.Context, input []byte) ([]byte, error) {
            return []byte("pong"), nil
        },
        finemcp.WithDescription("Health check"),
    )
    if err != nil {
        log.Fatal(err)
    }
    s.RegisterTool(tool)

    fmt.Println("Server running on :8080")
    log.Fatal(transport.StartHTTP(s, ":8080"))
}
```

```bash
go run main.go
```

Test with curl:

```bash
curl -s -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1.0.0"},"capabilities":{}}}' | jq
```
