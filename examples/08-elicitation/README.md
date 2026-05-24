# 08 — Elicitation

Demonstrates the MCP elicitation API for **server-initiated user prompts** — where the server asks the client to collect input from the user.

## How It Works

Elicitation lets a tool pause execution, ask the user a question via the client UI, and act based on the response. This is useful for confirmations, additional input, or interactive workflows.

```
Client → tools/call "delete-file" { path: "/tmp/test.txt" }
Server → elicitation/create { prompt: "Are you sure?", type: "text" }  (to client)
Client → shows UI prompt → user types "yes" → returns value
Server → proceeds with deletion → returns tool result
```

> **Note:** Requires the client to declare `"elicitation": {}` in its capabilities.

## Example

The `delete-file` tool asks for confirmation before proceeding:

```go
tool, _ := finemcp.NewTool("delete-file",
    func(ctx context.Context, input []byte) ([]byte, error) {
        var req struct {
            Path string `json:"path"`
        }
        json.Unmarshal(input, &req)

        result, err := s.ElicitUser(ctx, finemcp.ElicitationParams{
            Prompt:  fmt.Sprintf("Are you sure you want to delete %q? Type 'yes' to confirm.", req.Path),
            Type:    "text",
            Default: "no",
        })
        if err != nil {
            return nil, fmt.Errorf("elicitation failed: %w", err)
        }

        if result.Cancelled || result.Value != "yes" {
            return []byte("Deletion cancelled."), nil
        }
        return []byte(fmt.Sprintf("Deleted %s", req.Path)), nil
    },
    finemcp.WithDescription("Delete a file with user confirmation"),
    finemcp.WithDestructive(),
    finemcp.WithInputSchema(map[string]any{
        "type": "object",
        "properties": map[string]any{
            "path": map[string]any{"type": "string", "description": "File path to delete"},
        },
        "required": []string{"path"},
    }),
)
```

## ElicitationParams

| Field | Type | Description |
|-------|------|-------------|
| `Prompt` | `string` | Message shown to the user |
| `Type` | `string` | Input type (`"text"`, etc.) |
| `Default` | `string` | Default value pre-filled in the prompt |

## ElicitationResult

| Field | Type | Description |
|-------|------|-------------|
| `Cancelled` | `bool` | Whether the user dismissed the prompt |
| `Value` | `string` | The user's response |

## Testing with curl

```bash
go run ./08-elicitation

# Initialize (must declare elicitation capability)
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": {
    "protocolVersion": "2025-03-26",
    "clientInfo": { "name": "curl", "version": "1.0.0" },
    "capabilities": { "elicitation": {} }
  }
}'

# Call delete-file (triggers user prompt)
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "delete-file", "arguments": { "path": "/tmp/test.txt" } }
}'
```

> **Note:** curl cannot respond to elicitation requests. Use a proper MCP client that supports elicitation.
