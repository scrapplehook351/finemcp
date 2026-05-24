---
url: "/docs/testing/"
title: "Testing"
description: "In-process MCP server testing with the mcptest package"
weight: 5
---

The `mcptest` package provides in-process testing of MCP servers without any network or HTTP setup.

## Quick Example

```go
package myserver_test

import (
    "context"
    "testing"

    "github.com/finemcp/finemcp/mcptest"
)

func TestPingTool(t *testing.T) {
    ts := mcptest.NewServer(t,
        mcptest.WithTool("ping", func(ctx context.Context, input []byte) ([]byte, error) {
            return []byte("pong"), nil
        }),
    )

    ts.Initialize(t)
    result := ts.CallTool(t, "ping", nil)

    mcptest.AssertNoError(t, result)
    mcptest.AssertToolResult(t, result, "pong")
}
```

## Creating a Test Server

```go
ts := mcptest.NewServer(t, opts...)
```

### Server Options

| Option | Description |
|--------|-------------|
| `WithTool(name, handler)` | Register a raw tool handler |
| `WithRegisteredTool(tool)` | Register a pre-built `*Tool` |
| `WithMiddleware(mw)` | Apply middleware |
| `WithResource(r)` | Register a resource |
| `WithResourceTemplate(t)` | Register a resource template |
| `WithPrompt(p)` | Register a prompt |

## Server Methods

| Method | Description |
|--------|-------------|
| `ts.Initialize(t)` | Initialize the MCP session |
| `ts.CallTool(t, name, args)` | Call a tool |
| `ts.ListTools(t)` | List all tools |
| `ts.ListResources(t)` | List all resources |
| `ts.ReadResource(t, uri)` | Read a resource |
| `ts.ListResourceTemplates(t)` | List resource templates |
| `ts.ListPrompts(t)` | List all prompts |
| `ts.GetPrompt(t, name, args)` | Get a prompt |
| `ts.Notify(t, method, params)` | Send a notification |
| `ts.RawCall(t, method, params)` | Send a raw JSON-RPC request |
| `ts.Inner()` | Access the underlying `*finemcp.Server` |
| `ts.Close()` | Clean up |

## Assertions

| Function | Description |
|----------|-------------|
| `AssertNoError(t, resp)` | Assert no JSON-RPC error |
| `AssertError(t, resp, code, msgSubstr)` | Assert specific error |
| `AssertToolResult(t, resp, expected)` | Assert tool returned expected text |
| `AssertToolCount(t, resp, n)` | Assert number of tools in list |
| `AssertResourceCount(t, resp, n)` | Assert number of resources |
| `AssertResourceText(t, resp, expected)` | Assert resource text content |
| `AssertPromptCount(t, resp, n)` | Assert number of prompts |
| `AssertPromptMessage(t, resp, role, text)` | Assert prompt message content |
| `AssertTemplateCount(t, resp, n)` | Assert number of templates |

## Testing Typed Tools

```go
func TestCalculator(t *testing.T) {
    type CalcInput struct {
        A int `json:"a"`
        B int `json:"b"`
    }

    tool, _ := finemcp.NewTypedTool("add",
        func(ctx context.Context, input CalcInput) (string, error) {
            return fmt.Sprintf("%d", input.A+input.B), nil
        },
    )

    ts := mcptest.NewServer(t, mcptest.WithRegisteredTool(tool))
    ts.Initialize(t)

    result := ts.CallTool(t, "add", map[string]any{"a": 2, "b": 3})
    mcptest.AssertToolResult(t, result, "5")
}
```

## Testing with Middleware

```go
func TestWithRecovery(t *testing.T) {
    ts := mcptest.NewServer(t,
        mcptest.WithTool("panic-tool", func(ctx context.Context, input []byte) ([]byte, error) {
            panic("oops")
        }),
        mcptest.WithMiddleware(middleware.Recovery()),
    )
    ts.Initialize(t)

    result := ts.CallTool(t, "panic-tool", nil)
    // Should get an error response, not a panic
    mcptest.AssertError(t, result, -32603, "")
}
```

## Testing Resources

```go
func TestResource(t *testing.T) {
    res, _ := finemcp.NewResource("config://app", "Config",
        func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
            return []finemcp.ResourceContent{
                finemcp.NewTextResourceContent(uri, `{"key": "value"}`),
            }, nil
        },
    )

    ts := mcptest.NewServer(t, mcptest.WithResource(res))
    ts.Initialize(t)

    listResp := ts.ListResources(t)
    mcptest.AssertResourceCount(t, listResp, 1)

    readResp := ts.ReadResource(t, "config://app")
    mcptest.AssertResourceText(t, readResp, `{"key": "value"}`)
}
```

## Testing Prompts

```go
func TestPrompt(t *testing.T) {
    prompt, _ := finemcp.NewPrompt("greet",
        func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
            return []finemcp.PromptMessage{
                finemcp.NewUserMessage(fmt.Sprintf("Hello, %s!", args["name"])),
            }, nil
        },
        finemcp.WithPromptArguments(finemcp.PromptArgument{Name: "name", Required: true}),
    )

    ts := mcptest.NewServer(t, mcptest.WithPrompt(prompt))
    ts.Initialize(t)

    resp := ts.GetPrompt(t, "greet", map[string]string{"name": "Alice"})
    mcptest.AssertPromptMessage(t, resp, "user", "Hello, Alice!")
}
```

## Fixtures

Load test data from files:

```go
data := mcptest.LoadFixture(t, "testdata/input.json")
```

## Running Tests

```bash
go test ./...
```
