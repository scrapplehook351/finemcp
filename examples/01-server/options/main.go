// Example: Server Options
//
// Demonstrates all ServerOption variants available when creating a server.
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
//	# Call the "ping" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "ping" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	store := finemcp.NewTaskStore()

	s := finemcp.NewServer("options-demo", "1.0.0",
		finemcp.WithResourceSubscriptions(),
		finemcp.WithTaskStore(store),
		finemcp.WithStreamBufferSize(128),
		finemcp.WithMaxSessionTools(20),
		finemcp.WithMaxSessions(100),
		finemcp.WithSupportedVersions("2025-11-25", "2025-03-26"),
	)

	tool, err := finemcp.NewTool("ping",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("pong"), nil
		},
		finemcp.WithDescription("Ping"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Server:", s.Name(), s.Version())
	fmt.Println("Starting options-demo server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
