// Example: SSE (Server-Sent Events) Transport
//
// Demonstrates the legacy SSE transport for streaming events.
//
// Possible requests (curl):
//
//	# Connect to the SSE endpoint to receive events
//	curl -N http://localhost:8080/sse
//
//	# Once connected, use the message endpoint from the SSE stream to send JSON-RPC.
//	# The SSE stream will return a message_url, e.g. http://localhost:8080/message?sessionId=...
//
//	# Initialize the MCP session (replace <message_url> with the URL from SSE)
//	curl -X POST "<message_url>" -H "Content-Type: application/json" -d '{
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
//	# Call the "clock" tool
//	curl -X POST "<message_url>" -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "tools/call",
//	  "params": { "name": "clock" }
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
	s := finemcp.NewServer("sse-server", "1.0.0")

	tool, err := finemcp.NewTool("clock",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(time.Now().Format(time.RFC3339)), nil
		},
		finemcp.WithDescription("Returns the current server time"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting SSE transport on :8080")
	log.Fatal(transport.StartSSE(s, ":8080"))
}
