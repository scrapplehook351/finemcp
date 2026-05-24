# 03 — Streaming

Demonstrates how a tool can stream multiple content fragments back to the client before returning the final result.

## How It Works

MCP supports **streaming tool responses** — a tool can send incremental results while it's still running, then return a final result when done. This is useful for:

- Long-running operations with progress updates
- Search results arriving incrementally
- Data processing pipelines with intermediate output

The tool accesses the stream via `finemcp.StreamFromCtx(ctx)`:

```go
tool, _ := finemcp.NewTool("long-task",
    func(ctx context.Context, input []byte) ([]byte, error) {
        stream := finemcp.StreamFromCtx(ctx)
        if stream != nil {
            // Send intermediate results
            for i := 1; i <= 5; i++ {
                stream.SendText(fmt.Sprintf("Step %d/5 done", i))
                time.Sleep(200 * time.Millisecond)
            }
            // Send structured content
            stream.Send(finemcp.TextContent{Text: "Final structured content"})
        }
        // Return the final result
        return []byte("Task completed!"), nil
    },
)
```

### Stream API

| Method | Description |
|--------|-------------|
| `finemcp.StreamFromCtx(ctx)` | Get the `*ToolStream` from context (nil if transport doesn't support streaming) |
| `stream.SendText(text)` | Send a text fragment as a notification |
| `stream.Send(content)` | Send structured content (e.g. `TextContent`, `ImageContent`) |

### Important: Transport Requirements

**Not all transports support streaming.** The stream is only available when the transport sets a `NotificationSender` in the context:

| Transport | `StreamFromCtx` | Streaming works? |
|-----------|-----------------|------------------|
| `StartHTTP` | Returns `nil` | No — request/response only |
| `StartStreamable` | Returns `*ToolStream` | Yes — via GET SSE connection |
| `StartSSE` | Returns `*ToolStream` | Yes — via SSE events |
| `StartWebSocket` | Returns `*ToolStream` | Yes — via WebSocket messages |
| `ServeStdio` | Returns `*ToolStream` | Yes — via stdout notifications |

This example uses `StartStreamable` because it's the modern recommended transport for streaming.

### Server Option

```go
finemcp.WithStreamBufferSize(64)  // Channel buffer size for queued stream chunks
```

## Architecture: Streamable HTTP

With `StartStreamable`, notifications (streaming chunks) are delivered via a **separate GET SSE connection**, not in the POST response:

```
Terminal 1 (GET SSE — receives notifications):
  curl -N http://localhost:8080 -H "Accept: text/event-stream" -H "Mcp-Session-Id: <id>"
  ← data: {"jsonrpc":"2.0","method":"notifications/message","params":{"content":{"type":"text","text":"Step 1/5 done"}}}
  ← data: {"jsonrpc":"2.0","method":"notifications/message","params":{"content":{"type":"text","text":"Step 2/5 done"}}}
  ← ...

Terminal 2 (POST — sends request, gets final result):
  curl -X POST http://localhost:8080 -H "Mcp-Session-Id: <id>" -d '{...tools/call...}'
  ← {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"Task completed!"}]}}
```

In real MCP clients (Claude Desktop, Cursor, etc.), both connections are managed internally — you never see this split.

## Testing with curl

```bash
# Start the server
go run ./03-streaming

# Step 1: Initialize and note the Mcp-Session-Id from response headers
curl -v -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-03-26",
    "clientInfo": { "name": "curl-client", "version": "1.0.0" },
    "capabilities": {}
  }
}'

# Step 2 (Terminal 1): Open SSE stream to receive notifications
curl -N http://localhost:8080 \
  -H "Accept: text/event-stream" \
  -H "Mcp-Session-Id: <session-id>"

# Step 3 (Terminal 2): Call the tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: <session-id>" -d '{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": { "name": "long-task" }
}'
```

Terminal 1 will show the 5 progress steps in real-time. Terminal 2 will show only the final result.

## More Examples

### Streaming with error handling

```go
stream := finemcp.StreamFromCtx(ctx)
if stream != nil {
    for i, item := range items {
        if err := stream.SendText(fmt.Sprintf("Processing %d/%d: %s", i+1, len(items), item)); err != nil {
            return nil, fmt.Errorf("stream interrupted: %w", err)
        }
    }
}
```

### Graceful fallback for non-streaming transports

```go
stream := finemcp.StreamFromCtx(ctx)
if stream != nil {
    stream.SendText("Starting...")
}
result := doWork()
if stream != nil {
    stream.SendText("Done!")
}
return []byte(result), nil
// Works with all transports — streaming or not
```
