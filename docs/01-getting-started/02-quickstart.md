---
url: "/docs/getting-started/quickstart/"
title: "Quickstart"
description: "Build a complete MCP server with tools, resources, and prompts"
weight: 2
---

This guide walks through building a complete MCP server with tools, resources, and prompts.

## Create the Server

Every MCP server starts with `finemcp.NewServer`:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/finemcp/finemcp"
    "github.com/finemcp/finemcp/transport"
)

func main() {
    s := finemcp.NewServer("quickstart", "1.0.0")
```

## Add a Tool

Tools are functions that clients can invoke. Use `NewTool` for raw handlers:

```go
    greet, _ := finemcp.NewTool("greet",
        func(ctx context.Context, input []byte) ([]byte, error) {
            var req struct {
                Name string `json:"name"`
            }
            if err := json.Unmarshal(input, &req); err != nil {
                return nil, err
            }
            return []byte(fmt.Sprintf("Hello, %s!", req.Name)), nil
        },
        finemcp.WithDescription("Greet someone by name"),
        finemcp.WithInputSchema(map[string]any{
            "type": "object",
            "properties": map[string]any{
                "name": map[string]any{"type": "string", "description": "Name to greet"},
            },
            "required": []string{"name"},
        }),
    )
    s.RegisterTool(greet)
```

Or use `NewTypedTool` for automatic schema generation:

```go
    type AddInput struct {
        A int `json:"a" description:"First number"`
        B int `json:"b" description:"Second number"`
    }

    add, _ := finemcp.NewTypedTool("add",
        func(ctx context.Context, input AddInput) (string, error) {
            return fmt.Sprintf("%d", input.A+input.B), nil
        },
        finemcp.WithDescription("Add two numbers"),
    )
    s.RegisterTool(add)
```

## Add a Resource

Resources expose data that clients can read:

```go
    res, _ := finemcp.NewResource(
        "config://app/settings",
        "App Settings",
        func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
            return []finemcp.ResourceContent{
                finemcp.NewTextResourceContent(uri, `{"theme": "dark", "lang": "en"}`),
            }, nil
        },
        finemcp.WithResourceDescription("Application configuration"),
        finemcp.WithResourceMimeType("application/json"),
    )
    s.RegisterResource(res)
```

## Add a Prompt

Prompts are reusable message templates:

```go
    prompt, _ := finemcp.NewPrompt("review",
        func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
            return []finemcp.PromptMessage{
                finemcp.NewUserMessage(fmt.Sprintf("Review this code:\n%s", args["code"])),
            }, nil
        },
        finemcp.WithPromptDescription("Code review prompt"),
        finemcp.WithPromptArguments(finemcp.PromptArgument{
            Name: "code", Description: "Code to review", Required: true,
        }),
    )
    s.RegisterPrompt(prompt)
```

## Start the Server

```go
    fmt.Println("Server running on :8080")
    log.Fatal(transport.StartHTTP(s, ":8080"))
}
```

## Test It

```bash
go run main.go
```

Initialize the session:

```bash
curl -s -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0", "id": 1, "method": "initialize",
    "params": {
      "protocolVersion": "2025-03-26",
      "clientInfo": { "name": "curl", "version": "1.0.0" },
      "capabilities": {}
    }
  }' | jq
```

Call a tool:

```bash
curl -s -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0", "id": 2, "method": "tools/call",
    "params": { "name": "greet", "arguments": { "name": "Alice" } }
  }' | jq
```

Read a resource:

```bash
curl -s -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0", "id": 3, "method": "resources/read",
    "params": { "uri": "config://app/settings" }
  }' | jq
```

Get a prompt:

```bash
curl -s -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0", "id": 4, "method": "prompts/get",
    "params": { "name": "review", "arguments": { "code": "fmt.Println(42)" } }
  }' | jq
```

## Next Steps

- [Tools]({{< relref "/concepts/tools" >}}) — Raw, typed, annotations, session-scoped
- [Resources]({{< relref "/concepts/resources" >}}) — Templates, subscriptions
- [Transports]({{< relref "/concepts/transports" >}}) — HTTP, Streamable, SSE, WebSocket, Stdio
- [Middleware]({{< relref "/middleware" >}}) — Auth, caching, rate limiting, and more
