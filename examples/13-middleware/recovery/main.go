// Example: Recovery Middleware
//
// Catches panics in tool handlers and converts them to error responses.
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
//	# Call the "panic-test" tool (panics but recovered by middleware)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "panic-test" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("recovery", "1.0.0")
	s.Use(middleware.Recovery())

	tool, err := finemcp.NewTool("panic-test",
		func(ctx context.Context, input []byte) ([]byte, error) {
			panic("something went wrong!")
		},
		finemcp.WithDescription("A tool that panics (recovered by middleware)"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting recovery server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
