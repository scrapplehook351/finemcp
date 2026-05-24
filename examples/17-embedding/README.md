# 17 — Embedding

Demonstrates how to **embed an MCP server** into an existing HTTP application alongside regular HTTP routes.

## How It Works

Instead of using `transport.StartHTTP(s, addr)` which takes over the entire port, you can get a standard `http.Handler` and mount it on any path:

```go
s := finemcp.NewServer("embedded", "1.0.0")
// ... register tools ...

mux := http.NewServeMux()
mux.Handle("/mcp", transport.Handler(s))    // MCP endpoint
mux.HandleFunc("/health", healthHandler)     // Custom route
mux.HandleFunc("/", homeHandler)             // Custom route

http.ListenAndServe(":8080", mux)
```

This lets you:
- Add MCP to an existing web service
- Serve MCP at a sub-path (`/mcp`, `/api/mcp`, etc.)
- Mix MCP with REST APIs, health checks, metrics, etc.

## Key API

```go
transport.Handler(s) http.Handler
```

Returns a standard `http.Handler` that handles MCP JSON-RPC requests. Works with any Go HTTP router or framework.

## Example

```go
func main() {
    s := finemcp.NewServer("embedded", "1.0.0")

    tool, _ := finemcp.NewTool("ping",
        func(ctx context.Context, input []byte) ([]byte, error) {
            return []byte("pong"), nil
        },
        finemcp.WithDescription("Responds with pong"),
    )
    s.RegisterTool(tool)

    mux := http.NewServeMux()
    mux.Handle("/mcp", transport.Handler(s))
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("ok"))
    })

    fmt.Println("Server on :8080 — MCP at /mcp, health at /health")
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

## Testing with curl

```bash
go run ./17-embedding

# Health check (regular HTTP)
curl http://localhost:8080/health
# → ok

# MCP requests go to /mcp
curl -X POST http://localhost:8080/mcp -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

curl -X POST http://localhost:8080/mcp -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "tools/call",
  "params": { "name": "ping" }
}'
```
