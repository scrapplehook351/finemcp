// Example: Validation Middleware
//
// Validates tool inputs against JSON schemas before invoking the handler.
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
//	# Call the "add" tool with valid input
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "add",
//	    "arguments": { "a": 10, "b": 20 }
//	  }
//	}'
//
//	# Call with invalid input (missing required field, should fail validation)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "add",
//	    "arguments": { "a": 10 }
//	  }
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
	s := finemcp.NewServer("validation", "1.0.0")
	s.Use(middleware.Validation())

	tool, err := finemcp.NewTool("add",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("result"), nil
		},
		finemcp.WithDescription("Add two numbers"),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
			"required": []string{"a", "b"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting validation server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
