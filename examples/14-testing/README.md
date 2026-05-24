# 14 — Testing

Demonstrates in-process MCP server testing using the `mcptest` package — no HTTP, no network.

## How It Works

The `mcptest` package creates a fully wired MCP server in memory. You register tools, resources, prompts, and middleware, then call methods directly:

```go
func TestBasicTool(t *testing.T) {
    ts := mcptest.NewServer(t,
        mcptest.WithTool("greet", func(ctx context.Context, input []byte) ([]byte, error) {
            return []byte("Hello!"), nil
        }, finemcp.WithDescription("Say hello")),
    )
    ts.Initialize(t)
    result := ts.CallTool(t, "greet", nil)
    mcptest.AssertNoError(t, result)
    mcptest.AssertToolResult(t, result, "Hello!")
}
```

## Test Helpers

| Function | Description |
|----------|-------------|
| `mcptest.NewServer(t, opts...)` | Create a test server with tools/resources/prompts |
| `ts.Initialize(t)` | Initialize the MCP session |
| `ts.CallTool(t, name, args)` | Call a tool by name |
| `ts.ListResources(t)` | List registered resources |
| `ts.ListPrompts(t)` | List registered prompts |
| `mcptest.AssertNoError(t, result)` | Assert no error in tool result |
| `mcptest.AssertToolResult(t, result, expected)` | Assert tool returned expected text |

## Server Options

| Option | Description |
|--------|-------------|
| `mcptest.WithTool(name, handler, opts...)` | Register a raw tool |
| `mcptest.WithRegisteredTool(tool)` | Register a pre-built tool |
| `mcptest.WithMiddleware(m)` | Apply middleware |
| `mcptest.WithResource(resource)` | Register a resource |
| `mcptest.WithPrompt(prompt)` | Register a prompt |

## Examples

### Testing with Middleware

```go
func TestWithMiddleware(t *testing.T) {
    ts := mcptest.NewServer(t,
        mcptest.WithTool("safe-tool", handler),
        mcptest.WithMiddleware(middleware.Recovery()),
        mcptest.WithMiddleware(middleware.Logging(logger)),
    )
    ts.Initialize(t)
    result := ts.CallTool(t, "safe-tool", nil)
    mcptest.AssertNoError(t, result)
}
```

### Testing Typed Tools

```go
func TestTypedTool(t *testing.T) {
    tool, _ := finemcp.NewTypedTool("add",
        func(ctx context.Context, input struct{ A, B int }) (string, error) {
            return fmt.Sprintf("%d", input.A+input.B), nil
        },
    )
    ts := mcptest.NewServer(t, mcptest.WithRegisteredTool(tool))
    ts.Initialize(t)
    result := ts.CallTool(t, "add", map[string]any{"A": 2, "B": 3})
    mcptest.AssertToolResult(t, result, "5")
}
```

## Running Tests

```bash
go test ./14-testing/
```
