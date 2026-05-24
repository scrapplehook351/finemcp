---
url: "/docs/concepts/transports/"
title: "Transports"
description: "HTTP, Streamable HTTP, SSE, WebSocket, and Stdio transport options"
weight: 5
---

finemcp supports five transport mechanisms. Each has different capabilities for sessions, streaming, and bidirectional communication.

## Transport Comparison

| Transport | Function | Sessions | Streaming | Bidirectional | Best For |
|-----------|----------|----------|-----------|---------------|----------|
| **HTTP** | `StartHTTP` | No | No | No | Simple deployments |
| **Streamable HTTP** | `StartStreamable` | Yes | Yes (SSE) | Yes | Modern MCP (recommended) |
| **SSE** | `StartSSE` | Yes | Yes | Yes | Legacy streaming |
| **WebSocket** | `StartWebSocket` | Yes | Yes | Yes | Full-duplex real-time |
| **Stdio** | `ServeStdio` | No | Via stdout | Via stdin/stdout | CLI tools, subprocesses |

## HTTP

The simplest transport — stateless JSON-RPC over HTTP POST:

```go
import "github.com/finemcp/finemcp/transport"

log.Fatal(transport.StartHTTP(s, ":8080"))
```

No sessions, no streaming. Each request/response is independent. Good for getting started and simple use cases.

```bash
curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}'
```

## Streamable HTTP

The modern MCP transport (protocol version 2025-03-26+). Supports sessions via `Mcp-Session-Id` header and streaming via SSE:

```go
ctx := context.Background()
log.Fatal(transport.StartStreamable(ctx, s, ":8080"))
```

### Session Management

The `initialize` response includes an `Mcp-Session-Id` header. All subsequent requests must include this header:

```bash
# Initialize (save the session ID from response headers)
curl -v -X POST http://localhost:8080 -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}'

# Use session ID for subsequent requests
curl -X POST http://localhost:8080 \
  -H "Mcp-Session-Id: <session-id>" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

### Server-Push Notifications

Clients open a GET SSE connection to receive server-initiated notifications (progress, logging, resource updates):

```bash
# Terminal 1: SSE listener
curl -N -H "Mcp-Session-Id: <session-id>" -H "Accept: text/event-stream" http://localhost:8080

# Terminal 2: Call a tool (notifications arrive on Terminal 1)
curl -X POST http://localhost:8080 -H "Mcp-Session-Id: <session-id>" ...
```

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithStreamableKeepAlive(d)` | — | SSE keep-alive interval |
| `WithStreamableMaxBody(n)` | — | Max request body size |
| `WithStreamableSessionTimeout(d)` | — | Session inactivity timeout |
| `WithStreamableGETBufferSize(n)` | — | SSE event buffer size |
| `WithStreamableOriginValidator(fn)` | — | Custom origin validation |

### Handler

For embedding in an existing HTTP server:

```go
handler := transport.StreamableHandler(s, opts...)
mux.Handle("/mcp", handler)
```

## SSE (Legacy)

The legacy Server-Sent Events transport. Clients connect to `/sse` to receive events and send messages to a dynamic `/message` endpoint:

```go
log.Fatal(transport.StartSSE(s, ":8080"))
```

```bash
# Terminal 1: Connect SSE (returns message URL)
curl -N http://localhost:8080/sse

# Terminal 2: Send to message URL
curl -X POST "http://localhost:8080/message?sessionId=..." \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}'
```

### Options

| Option | Description |
|--------|-------------|
| `WithSSEPath(path)` | Custom SSE endpoint path |
| `WithMessagePath(path)` | Custom message endpoint path |
| `WithKeepAlive(d)` | Keep-alive ping interval |
| `WithMaxBodySize(n)` | Max request body size |

### Handler

```go
handler := transport.SSEHandler(s, opts...)
```

## WebSocket

Full-duplex bidirectional communication:

```go
log.Fatal(transport.StartWebSocket(s, ":8080"))
```

```bash
websocat ws://localhost:8080
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}
```

### Options

| Option | Description |
|--------|-------------|
| `WithWebSocketPath(path)` | Custom WebSocket endpoint path |
| `WithWebSocketMaxMessageSize(n)` | Max message size |
| `WithWebSocketCheckOrigin(fn)` | Custom origin check function |

### Handler

```go
handler := transport.WebSocketHandler(s, opts...)
```

## Stdio

Runs over standard input/output — ideal for CLI tools and subprocess-based hosting:

```go
if err := transport.ServeStdio(context.Background(), s); err != nil {
    log.Fatal(err)
}
```

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}' | ./my-mcp-tool
```

### Custom I/O

For testing or custom piping:

```go
transport.ServeWithIO(ctx, s, customReader, customWriter)

// Or with full control:
t := transport.NewStdioTransport(s, reader, writer)
t.Run(ctx)
```

## Embedding in Existing HTTP Servers

All HTTP-based transports provide handler constructors for use with custom `http.ServeMux`:

```go
mux := http.NewServeMux()
mux.Handle("/mcp", transport.Handler(s))             // Basic HTTP
mux.Handle("/mcp", transport.StreamableHandler(s))    // Streamable
mux.Handle("/mcp", transport.SSEHandler(s))           // SSE
mux.Handle("/mcp", transport.WebSocketHandler(s))     // WebSocket
mux.HandleFunc("/health", healthHandler)
http.ListenAndServe(":8080", mux)
```

See [Embedding]({{< relref "/deployment/embedding" >}}) for a full example.

## Interactive API Docs

finemcp includes a built-in docs UI handler:

```go
mux.Handle("/docs", transport.DocsHandler(s,
    transport.WithDocsTitle("My MCP Server"),
    transport.WithDocsBaseURL("http://localhost:8080"),
))
```

| Option | Description |
|--------|-------------|
| `WithDocsTitle(t)` | Page title |
| `WithDocsBaseURL(u)` | Server base URL for interactive testing |
| `WithToolFilter(fn)` | Filter which tools appear in docs |
| `WithCORS(origin)` | Enable CORS for docs UI |
| `WithExecuteRateLimit(n)` | Rate limit for interactive execution |

## Choosing a Transport

- **Starting out?** Use `StartHTTP` — simplest, no sessions.
- **Production?** Use `StartStreamable` — the modern MCP standard with sessions and streaming.
- **Full-duplex?** Use `StartWebSocket` for real-time bidirectional communication.
- **CLI tool?** Use `ServeStdio` for subprocess-based hosting.
- **Legacy clients?** Use `StartSSE` for backwards compatibility.
