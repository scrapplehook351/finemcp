// Example: Sandbox Middleware
//
// Restricts tool execution with timeout and output size limits.
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
//	# Call the "sandboxed-op" tool (5s timeout, 1MB output limit)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "sandboxed-op" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("sandbox", "1.0.0")
	s.Use(middleware.Sandbox(
		middleware.WithTimeout(5*time.Second),
		middleware.WithMaxOutputSize(1024*1024),
	))

	tool, err := finemcp.NewTool("sandboxed-op",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("sandboxed result"), nil
		},
		finemcp.WithDescription("A sandboxed operation with timeout and output limits"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting sandbox server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
