// Example: Rate Limit Middleware
//
// Limits tool invocation rate per session or globally.
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
//	# Call the "limited" tool (rate-limited to 10 req/s with burst of 20)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "limited" }
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
	s := finemcp.NewServer("ratelimit", "1.0.0")
	s.Use(middleware.RateLimit(10, middleware.WithBurst(20)))

	tool, err := finemcp.NewTool("limited",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("ok"), nil
		},
		finemcp.WithDescription("Rate-limited to 10 req/s with burst of 20"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting ratelimit server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
