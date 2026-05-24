# 15 — Content Types

Demonstrates all MCP content types: **text**, **image**, and **embedded resource**.

## How It Works

MCP tool responses can contain different content types. The framework handles serialization based on what the handler returns.

## Examples

### Text Content

Raw tool handlers return `[]byte` which is wrapped as `TextContent`:

```go
tool, _ := finemcp.NewTool("text-example",
    func(ctx context.Context, input []byte) ([]byte, error) {
        return []byte("Hello, this is plain text content"), nil
    },
    finemcp.WithDescription("Returns text content"),
)
```

### Image Content

Typed tools can return image data with base64 encoding:

```go
tool, _ := finemcp.NewTypedTool("image-example",
    func(ctx context.Context, input struct{}) (string, error) {
        return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", nil
    },
    finemcp.WithDescription("Returns an image"),
)
```

### Embedded Resource Content

Tools can embed resource content within their response:

```go
tool, _ := finemcp.NewTool("embedded-example",
    func(ctx context.Context, input []byte) ([]byte, error) {
        return []byte(`{"uri": "file:///data.json", "text": "{\"key\": \"value\"}"}`), nil
    },
    finemcp.WithDescription("Returns embedded resource content"),
)
```

## Testing with curl

```bash
go run ./15-content-types

curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Text content
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "tools/call",
  "params": { "name": "text-example" }
}'

# Image content
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "image-example" }
}'

# Embedded resource
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 4, "method": "tools/call",
  "params": { "name": "embedded-example" }
}'
```
