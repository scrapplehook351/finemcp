# 07 — Roots

Demonstrates registering **root URIs** that define the content boundaries visible to the server.

## How It Works

Roots are URI-based entry points that tell clients what content the server can access. They typically represent file system paths or other hierarchical namespaces.

```go
s := finemcp.NewServer("roots", "1.0.0")

projectRoot, _ := finemcp.NewRoot("file:///workspace/project",
    finemcp.WithRootName("Project Root"),
)
s.RegisterRoot(projectRoot)

docsRoot, _ := finemcp.NewRoot("file:///workspace/docs",
    finemcp.WithRootName("Documentation"),
)
s.RegisterRoot(docsRoot)
```

Roots can also be listed programmatically:

```go
for _, r := range s.ListRoots() {
    fmt.Printf("Root: %s (name: %s)\n", r.URI, r.Name)
}
```

## Example Tool

The `list-roots` tool returns all registered root URIs:

```go
tool, _ := finemcp.NewTool("list-roots",
    func(ctx context.Context, input []byte) ([]byte, error) {
        roots := s.ListRoots()
        var lines string
        for _, r := range roots {
            lines += fmt.Sprintf("- %s (%s)\n", r.URI, r.Name)
        }
        return []byte(lines), nil
    },
    finemcp.WithDescription("List all registered root URIs"),
)
```

## Root Options

| Option | Description |
|--------|-------------|
| `WithRootName(name)` | Human-readable name for the root |

## Testing with curl

```bash
go run ./07-roots

# Initialize
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# List roots via tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "list-roots" }
}'
```
