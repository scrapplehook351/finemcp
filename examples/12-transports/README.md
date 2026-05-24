# 12 — Transports

Demonstrates the five transport mechanisms available in finemcp for client-server communication.

## Transport Comparison

| Transport | Function | Sessions | Streaming | Bidirectional | Use Case |
|-----------|----------|----------|-----------|---------------|----------|
| **HTTP** | `transport.StartHTTP(s, addr)` | No | No | No | Simple REST-like deployments |
| **Streamable HTTP** | `transport.StartStreamable(ctx, s, addr)` | Yes (`Mcp-Session-Id`) | Yes (SSE) | Yes (GET SSE) | Modern MCP standard (2025-03-26+) |
| **SSE** | `transport.StartSSE(s, addr)` | Yes (URL-based) | Yes | Yes | Legacy streaming |
| **WebSocket** | `transport.StartWebSocket(s, addr)` | Yes | Yes | Yes | Full-duplex real-time |
| **Stdio** | `transport.ServeStdio(ctx, s)` | No | Via stdout | Via stdin/stdout | CLI tools, subprocesses |

## Examples

### http/

Simplest transport — stateless HTTP POST with JSON-RPC request/response:

```go
transport.StartHTTP(s, ":8080")
```
```bash
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'
```

### streamable/

Modern transport with session management and SSE streaming:

```go
transport.StartStreamable(context.Background(), s, ":8080")
```
```bash
# Initialize (save Mcp-Session-Id from response headers)
curl -v -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Subsequent requests require the session header
curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: <session-id>" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "tools/list"
}'
```

### sse/

Legacy SSE transport — client connects to `/sse` for events, sends to `/message`:

```go
transport.StartSSE(s, ":8080")
```
```bash
# Terminal 1: Connect to SSE stream (returns message URL)
curl -N http://localhost:8080/sse

# Terminal 2: Send requests to the message URL from SSE stream
curl -X POST "<message_url>" -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'
```

### websocket/

Full-duplex WebSocket transport:

```go
transport.StartWebSocket(s, ":8080")
```
```bash
# Using websocat
websocat ws://localhost:8080

# Type JSON-RPC messages interactively:
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"ws-client","version":"1.0.0"},"capabilities":{}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
```

### stdio/

Subprocess transport over stdin/stdout:

```go
transport.ServeStdio(context.Background(), s)
```
```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"cli","version":"1.0.0"},"capabilities":{}}}' | go run ./12-transports/stdio
```

## Choosing a Transport

- **Starting out?** Use `StartHTTP` — simplest, no sessions, easy to curl.
- **Need streaming?** Use `StartStreamable` — the modern MCP standard.
- **Full-duplex?** Use `StartWebSocket` for real-time bidirectional communication.
- **CLI tool?** Use `ServeStdio` for subprocess-based hosting.
- **Legacy clients?** Use `StartSSE` for backwards compatibility.
