# 02 — Tools

Demonstrates all the ways to create and configure tools in FineMCP.

## How It Works

Tools are the primary way an MCP server exposes functionality to clients. A tool has:

- **Name** — unique identifier the client uses to call it
- **Handler** — function that executes when the tool is called
- **Description** — human-readable explanation for the client/LLM
- **Input Schema** — JSON Schema describing expected arguments (auto-generated for typed tools)
- **Annotations** — metadata like `readOnly`, `destructive`, `idempotent`, `openWorld`, `roles`

```
Client sends:  tools/call { name: "add", arguments: { a: 10, b: 20 } }
Server runs:   handler(ctx, arguments) → result
Server returns: { content: [{ type: "text", text: "30" }] }
```

## Examples

### raw/

Manual JSON parsing with explicit `WithInputSchema`:

```go
tool, _ := finemcp.NewTool("add",
    func(ctx context.Context, input []byte) ([]byte, error) {
        var params struct {
            A float64 `json:"a"`
            B float64 `json:"b"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, fmt.Errorf("invalid input: %w", err)
        }
        return []byte(fmt.Sprintf("%.2f", params.A+params.B)), nil
    },
    finemcp.WithDescription("Add two numbers"),
    finemcp.WithInputSchema(map[string]any{
        "type": "object",
        "properties": map[string]any{
            "a": map[string]any{"type": "number", "description": "First operand"},
            "b": map[string]any{"type": "number", "description": "Second operand"},
        },
        "required": []string{"a", "b"},
    }),
)
```

**Run:** `go run ./02-tools/raw`

### typed/

Uses Go generics to auto-generate the JSON Schema from struct tags:

```go
greetTool, _ := finemcp.NewTypedTool("greet",
    func(ctx context.Context, in struct {
        Name string `json:"name" description:"Name to greet"`
    }) (string, error) {
        return fmt.Sprintf("Hello, %s!", in.Name), nil
    },
    finemcp.WithDescription("Greet someone by name"),
)
```

Complex input types with optional fields:

```go
searchTool, _ := finemcp.NewTypedTool("search",
    func(ctx context.Context, in struct {
        Query   string   `json:"query" description:"Search query"`
        Tags    []string `json:"tags" description:"Filter tags"`
        Limit   *int     `json:"limit" description:"Max results (optional)"`
        Verbose bool     `json:"verbose" description:"Include details"`
    }) (string, error) {
        // Limit is nil when not provided by the client
        limit := 10
        if in.Limit != nil {
            limit = *in.Limit
        }
        return fmt.Sprintf("Searching %q limit=%d", in.Query, limit), nil
    },
)
```

**Run:** `go run ./02-tools/typed`

### annotations/

Demonstrates tool annotations that describe behavior:

```go
// Read-only tool — safe to call without side effects
finemcp.WithReadOnly()

// Destructive tool — may delete or modify data irreversibly
finemcp.WithDestructive()

// Idempotent tool — calling multiple times has the same effect
finemcp.WithIdempotent()

// Open-world tool — accesses external/untrusted data (e.g. web)
finemcp.WithOpenWorld()

// Role-restricted tool — requires specific roles
finemcp.WithRoles("admin")

// Human-readable title (separate from the tool name)
finemcp.WithTitle("Data Reader")
```

**Run:** `go run ./02-tools/annotations`

### session-tools/

Per-session tool overlays that shadow global tools:

```go
// Register a global tool
s.RegisterTool(globalTool)  // greet → "Hello, Alice! (global)"

// Add a session-specific override
s.AddSessionTool(ctx, "session-123", sessionTool)  // greet → "Hi Alice! (session)"

// Session "session-123" sees the override; other sessions see the global version

// Callback when shadowing occurs
s.OnSessionToolShadow(func(sessionID, toolName string) {
    fmt.Printf("Session %s shadowed %q\n", sessionID, toolName)
})

// Remove session tools
s.RemoveSessionTool("session-123", "greet")  // restore global for this tool
s.RemoveSessionTools("session-123")          // remove all session overrides
```

**Run:** `go run ./02-tools/session-tools`

## Tool Creation Comparison

| Approach | Schema | Parsing | Best for |
|----------|--------|---------|----------|
| `NewTool` (raw) | Manual `WithInputSchema` | Manual `json.Unmarshal` | Full control, dynamic schemas |
| `NewTypedTool` (typed) | Auto-generated from struct tags | Automatic | Most use cases, type safety |

## Testing with curl

```bash
go run ./02-tools/typed

# Call the greet tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "tools/call",
  "params": { "name": "greet", "arguments": { "name": "Alice" } }
}'
# → {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"Hello, Alice!"}]}}

# Call the search tool with complex arguments
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "search", "arguments": { "query": "finemcp", "tags": ["go", "mcp"], "limit": 5, "verbose": true } }
}'
```
