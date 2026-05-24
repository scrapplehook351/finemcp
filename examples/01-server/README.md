# 01 — Server

Demonstrates how to create and configure an MCP server using `finemcp.NewServer`.

## How It Works

An MCP server is the core component that:

1. Registers **tools**, **resources**, and **prompts**
2. Handles the **JSON-RPC 2.0** protocol (initialize, tools/call, resources/read, etc.)
3. Binds to a **transport** (HTTP, SSE, WebSocket, stdio) to communicate with clients

The MCP session lifecycle:

```
Client                        Server
  |── initialize ──────────→    |   Negotiate capabilities & protocol version
  |←──── initialize response ── |
  |── initialized (notif) ──→  |   Client confirms ready
  |── tools/list ──────────→   |   Normal requests begin
  |←──── tools/list response ── |
  |── tools/call ──────────→   |
  |←──── tools/call response ── |
```

## Examples

### basic/

The simplest possible MCP server — one tool, one transport.

```go
s := finemcp.NewServer("basic-server", "1.0.0")

tool, _ := finemcp.NewTool("hello",
    func(ctx context.Context, input []byte) ([]byte, error) {
        return []byte("Hello from FineMCP!"), nil
    },
    finemcp.WithDescription("Says hello"),
)
s.RegisterTool(tool)

log.Fatal(transport.StartHTTP(s, ":8080"))
```

**Run:** `go run ./01-server/basic`

### options/

Demonstrates all `ServerOption` variants:

```go
s := finemcp.NewServer("options-demo", "1.0.0",
    finemcp.WithResourceSubscriptions(),     // Enable resource change notifications
    finemcp.WithTaskStore(store),             // Enable async task tracking
    finemcp.WithStreamBufferSize(128),        // Buffer size for streaming responses
    finemcp.WithMaxSessionTools(20),          // Max tools per session overlay
    finemcp.WithMaxSessions(100),             // Max concurrent sessions
    finemcp.WithSupportedVersions("2025-11-25", "2025-03-26"), // Protocol versions
)
```

**Run:** `go run ./01-server/options`

## Server Options Reference

| Option | Description |
|--------|-------------|
| `WithResourceSubscriptions()` | Enables `resources/subscribe` and `resources/unsubscribe` |
| `WithTaskStore(store)` | Enables async task execution with a task store |
| `WithStreamBufferSize(n)` | Sets the channel buffer size for streaming tool responses |
| `WithMaxSessionTools(n)` | Limits the number of per-session tool overlays |
| `WithMaxSessions(n)` | Limits the number of concurrent sessions |
| `WithSupportedVersions(v...)` | Declares which MCP protocol versions are supported |

## Testing with curl

```bash
# Start the server
go run ./01-server/basic

# Initialize (required first step for every session)
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-03-26",
    "clientInfo": { "name": "curl-client", "version": "1.0.0" },
    "capabilities": {}
  }
}'

# List tools
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list"
}'

# Call a tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": { "name": "hello" }
}'
```

> **Note:** `initialize` can only be called once per session. Restart the server to reset.
