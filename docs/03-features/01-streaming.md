---
url: "/docs/features/streaming/"
title: "Streaming"
description: "Real-time incremental tool responses via ToolStream"
weight: 1
---

Streaming lets tools send incremental results to the client as they become available, rather than waiting for the entire computation to finish.

## How It Works

Tools access the stream via `StreamFromCtx(ctx)` and send chunks using `Send` or `SendText`:

```go
tool, _ := finemcp.NewTool("analyze",
    func(ctx context.Context, input []byte) ([]byte, error) {
        stream := finemcp.StreamFromCtx(ctx)
        if stream != nil {
            stream.SendText("Analyzing data...")
            stream.SendText("Found 42 patterns")
            stream.SendText("Generating report...")
        }
        return []byte("Analysis complete"), nil
    },
    finemcp.WithDescription("Stream analysis steps"),
)
```

The final `return` value is the tool's official result. Streamed chunks are intermediate notifications.

## ToolStream API

```go
// Get the stream from context (nil if transport doesn't support streaming)
stream := finemcp.StreamFromCtx(ctx)

// Send text content
stream.SendText("Step 1 complete")

// Send any Content type (text, image, audio, embedded resource)
stream.Send(finemcp.TextContent{Text: "hello"})
stream.Send(finemcp.NewImageContent("image/png", pngBytes))

// Get the current sequence number
seq := stream.Sequence()
```

### Methods

| Method | Description |
|--------|-------------|
| `SendText(text)` | Send a text chunk |
| `Send(content)` | Send any `Content` type |
| `Sequence()` | Current chunk sequence number |

### Errors

- `ErrStreamClosed` — stream was already closed (tool handler returned)

## Transport Requirements

Streaming requires a transport that supports server-push. Not all transports deliver streaming chunks:

| Transport | Streaming Support |
|-----------|------------------|
| `StartHTTP` | **No** — `StreamFromCtx` returns `nil` |
| `StartStreamable` | **Yes** — chunks sent via SSE |
| `StartSSE` | **Yes** — chunks sent via SSE |
| `StartWebSocket` | **Yes** — chunks sent via WebSocket frames |
| `ServeStdio` | **Yes** — chunks written to stdout |

Always check for `nil`:

```go
stream := finemcp.StreamFromCtx(ctx)
if stream != nil {
    // Streaming is available
    stream.SendText("progress update")
} else {
    // Fallback: just return the final result
}
```

## Buffer Size

Control the channel buffer size for streaming chunks:

```go
s := finemcp.NewServer("my-server", "1.0.0",
    finemcp.WithStreamBufferSize(64), // Default: 16
)
```

## Testing Streaming with curl

Streaming requires Streamable HTTP and two terminals:

```bash
# Terminal 1: Start the server
go run main.go

# Terminal 2: Initialize and get session ID
curl -v -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'
# Note the Mcp-Session-Id header in the response

# Terminal 3: Open SSE listener for notifications
curl -N -H "Mcp-Session-Id: <session-id>" -H "Accept: text/event-stream" http://localhost:8080

# Terminal 2: Call the streaming tool (notifications appear in Terminal 3)
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: <session-id>" \
  -d '{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": { "name": "analyze" }}'
```

## Streamable HTTP Architecture

```
Client                          Server
  │                               │
  │─── POST initialize ──────────>│
  │<── 200 + Mcp-Session-Id ─────│
  │                               │
  │─── GET /  (SSE listener) ────>│  ← Keeps connection open
  │                               │
  │─── POST tools/call ──────────>│
  │                               │  stream.SendText("step 1")
  │<── SSE: stream chunk 1 ──────│  ← via GET connection
  │                               │  stream.SendText("step 2")
  │<── SSE: stream chunk 2 ──────│
  │                               │  return final result
  │<── 200 final result ─────────│  ← via POST response
```
