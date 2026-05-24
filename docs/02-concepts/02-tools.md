---
url: "/docs/concepts/tools/"
title: "Tools"
description: "Create and register MCP tools with raw handlers, typed handlers, annotations, and session scoping"
weight: 2
---

Tools are the primary way MCP servers expose executable functionality. When a client calls a tool, the server runs the handler and returns the result.

## Raw Tools

The simplest tool takes raw `[]byte` input and returns `[]byte`:

```go
tool, err := finemcp.NewTool("greet",
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
            "name": map[string]any{"type": "string"},
        },
        "required": []string{"name"},
    }),
)
s.RegisterTool(tool)
```

## Typed Tools

`NewTypedTool` uses Go generics to auto-generate the JSON Schema from struct tags:

```go
type CalcInput struct {
    A int    `json:"a" description:"First number"`
    B int    `json:"b" description:"Second number"`
    Op string `json:"op" description:"Operation: add, sub, mul, div"`
}

tool, err := finemcp.NewTypedTool("calc",
    func(ctx context.Context, input CalcInput) (string, error) {
        switch input.Op {
        case "add":
            return fmt.Sprintf("%d", input.A+input.B), nil
        case "sub":
            return fmt.Sprintf("%d", input.A-input.B), nil
        default:
            return "", fmt.Errorf("unknown op: %s", input.Op)
        }
    },
    finemcp.WithDescription("Basic calculator"),
)
```

The schema is derived from the `CalcInput` struct automatically — no need for `WithInputSchema`.

### Supported Struct Tags

| Tag | Purpose |
|-----|---------|
| `json:"name"` | JSON field name |
| `description:"text"` | Field description in schema |

## Tool Options

| Option | Description |
|--------|-------------|
| `WithDescription(d)` | Human-readable description |
| `WithInputSchema(s)` | JSON Schema for input validation |
| `WithTitle(t)` | Display title (distinct from name) |
| `WithRoles(r...)` | Required roles for RBAC middleware |
| `WithSimulator(fn)` | Custom handler for simulation mode |
| `WithValidation(bool)` | Enable/disable input validation for this tool |

## Annotations

Annotations provide hints about a tool's behavior:

```go
tool, _ := finemcp.NewTool("read-file", handler,
    finemcp.WithReadOnly(),       // DestructiveHint = false
    finemcp.WithIdempotent(),     // IdempotentHint = true
)

tool, _ := finemcp.NewTool("delete-db", handler,
    finemcp.WithDestructive(),    // DestructiveHint = true
)

tool, _ := finemcp.NewTool("search", handler,
    finemcp.WithOpenWorld(),      // OpenWorldHint = true
)
```

### Annotation Helpers

| Helper | Sets |
|--------|------|
| `WithReadOnly()` | `DestructiveHint = false` |
| `WithDestructive()` | `DestructiveHint = true` |
| `WithIdempotent()` | `IdempotentHint = true` |
| `WithOpenWorld()` | `OpenWorldHint = true` |
| `WithTitle(t)` | `Title` in annotations |

For full control, use `WithAnnotations`:

```go
finemcp.WithAnnotations(finemcp.ToolAnnotations{
    ReadOnlyHint:     finemcp.BoolPtr(true),
    DestructiveHint:  finemcp.BoolPtr(false),
    IdempotentHint:   finemcp.BoolPtr(true),
    OpenWorldHint:    finemcp.BoolPtr(false),
    Title:            "My Tool",
})
```

## Session-Scoped Tools

Tools can be added to specific sessions, overriding (shadowing) global tools:

```go
// Inside a tool handler or after getting a session ID
sessionTool, _ := finemcp.NewTool("user-prefs", handler)
s.AddSessionTool(ctx, sessionID, sessionTool)

// List session tools
tools := s.SessionTools(sessionID)

// Remove
s.RemoveSessionTool(sessionID, "user-prefs")

// Remove all session tools
s.RemoveSessionTools(sessionID)
```

### Shadow Callbacks

Get notified when a session tool shadows a global tool:

```go
s.OnSessionToolShadow(func(sessionID, toolName string) {
    log.Printf("Session %s shadowed global tool %s", sessionID, toolName)
})
```

### Limits

| Option | Default |
|--------|---------|
| `WithMaxSessionTools(n)` | 100 per session |
| `WithMaxSessions(n)` | 10,000 |

## Dynamic Tool Management

Tools can be added and removed at runtime:

```go
s.RegisterTool(tool)
s.RemoveTool("old-tool")

// Notify connected clients that the tool list changed
s.NotifyToolsListChanged()
```

## Tool Results

Raw handlers return `[]byte` which is automatically wrapped as text content. For richer results, tools can use the stream API or return structured content directly.

See [Content Types]({{< relref "/features/content-types" >}}) for image, audio, and embedded resource responses.
See [Streaming]({{< relref "/features/streaming" >}}) for incremental responses.
