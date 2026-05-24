// Example: Streaming Tool Responses
//
// Demonstrates how a tool can stream multiple content fragments back
// to the client before returning the final result.
//
// Possible requests (curl):
//
//	# Initialize the MCP session
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 1,
//	  "method": "initialize",
//	  "params": {
//	    "protocolVersion": "2025-03-26",
//	    "clientInfo": { "name": "curl-client", "version": "1.0.0" },
//	    "capabilities": {}
//	  }
//	}'
//
//	# List available tools
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "tools/list"
//	}'
//
//	# Call the "long-task" tool (streams progress steps before final result)
//	# Streamable HTTP sends notifications via a separate GET SSE connection.
//	# You need TWO terminals:
//
//	# Terminal 1: Open GET SSE stream to receive streaming notifications
//	curl -N http://localhost:8080 \
//	  -H "Accept: text/event-stream" \
//	  -H "Mcp-Session-Id: <session-id>"
//
//	# Terminal 2: Send the tool call via POST (returns only the final result)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
//	  -H "Mcp-Session-Id: <session-id>" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "long-task" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("streaming", "1.0.0",
		finemcp.WithStreamBufferSize(64),
	)

	streamTool, err := finemcp.NewTool("long-task",
		func(ctx context.Context, input []byte) ([]byte, error) {
			stream := finemcp.StreamFromCtx(ctx)
			if stream != nil {
				for i := 1; i <= 5; i++ {
					text := fmt.Sprintf("Processing step %d/5...", i)
					if err := stream.SendText(text); err != nil {
						return nil, err
					}
					time.Sleep(200 * time.Millisecond)
				}
				if err := stream.Send(finemcp.TextContent{Text: "Final structured content"}); err != nil {
					return nil, err
				}
			}
			return []byte("Task completed successfully!"), nil
		},
		finemcp.WithDescription("A long-running task that streams progress"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(streamTool)

	fmt.Println("Starting streaming server on :8080")
	log.Fatal(transport.StartStreamable(context.Background(), s, ":8080"))
}
