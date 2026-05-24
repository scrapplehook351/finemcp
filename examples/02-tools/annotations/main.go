// Example: Tool Annotations
//
// Demonstrates all tool annotation types.
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
//	# Call the "read-data" tool (read-only)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "read-data" }
//	}'
//
//	# Call the "delete-all" tool (destructive)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "tools/call",
//	  "params": { "name": "delete-all" }
//	}'
//
//	# Call the "upsert" tool (idempotent)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 5,
//	  "method": "tools/call",
//	  "params": { "name": "upsert" }
//	}'
//
//	# Call the "web-search" tool (open world)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 6,
//	  "method": "tools/call",
//	  "params": { "name": "web-search" }
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
	s := finemcp.NewServer("annotations", "1.0.0")

	readTool, err := finemcp.NewTool("read-data",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("data"), nil
		},
		finemcp.WithDescription("Read-only data access"),
		finemcp.WithReadOnly(),
		finemcp.WithTitle("Data Reader"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(readTool)

	deleteTool, err := finemcp.NewTool("delete-all",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("deleted"), nil
		},
		finemcp.WithDescription("Destructive delete operation"),
		finemcp.WithDestructive(),
		finemcp.WithRoles("admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(deleteTool)

	upsertTool, err := finemcp.NewTool("upsert",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("upserted"), nil
		},
		finemcp.WithDescription("Idempotent upsert"),
		finemcp.WithIdempotent(),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(upsertTool)

	webTool, err := finemcp.NewTool("web-search",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("results"), nil
		},
		finemcp.WithDescription("Search the open web"),
		finemcp.WithOpenWorld(),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(webTool)

	fmt.Println("Starting annotations server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
