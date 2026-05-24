// Example: Streamable HTTP Transport
//
// Demonstrates the modern Streamable HTTP transport (MCP 2025-03-26+).
//
// Possible requests (curl):
//
//	# Initialize the MCP session (save the Mcp-Session-Id header from response)
//	curl -v -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
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
//	# List available tools (include session ID from initialize response)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
//	  -H "Mcp-Session-Id: <session-id>" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "tools/list"
//	}'
//
//	# Call the "slow-task" tool (streams progress via SSE, then final result)
//	curl -N -X POST http://localhost:8080 -H "Content-Type: application/json" \
//	  -H "Mcp-Session-Id: <session-id>" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "slow-task" }
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
	s := finemcp.NewServer("streamable-server", "1.0.0")

	tool, err := finemcp.NewTool("slow-task",
		func(ctx context.Context, input []byte) ([]byte, error) {
			stream := finemcp.StreamFromCtx(ctx)
			if stream != nil {
				for i := 1; i <= 3; i++ {
					_ = stream.SendText(fmt.Sprintf("Step %d done", i))
					time.Sleep(200 * time.Millisecond)
				}
			}
			return []byte("All steps completed"), nil
		},
		finemcp.WithDescription("A slow task that streams progress via Streamable HTTP"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	ctx := context.Background()
	fmt.Println("Starting Streamable HTTP transport on :8080")
	log.Fatal(transport.StartStreamable(ctx, s, ":8080"))
}
