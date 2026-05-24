// Example: Roots
//
// Demonstrates registering root URIs that define content boundaries.
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
//	# Call the "list-roots" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "list-roots" }
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
	s := finemcp.NewServer("roots", "1.0.0")

	projectRoot, err := finemcp.NewRoot("file:///workspace/project",
		finemcp.WithRootName("Project Root"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterRoot(projectRoot)

	docsRoot, err := finemcp.NewRoot("file:///workspace/docs",
		finemcp.WithRootName("Documentation"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterRoot(docsRoot)

	for _, r := range s.ListRoots() {
		fmt.Printf("Root: %s (name: %s)\n", r.URI, r.Name)
	}

	listTool, err := finemcp.NewTool("list-roots",
		func(ctx context.Context, input []byte) ([]byte, error) {
			roots := s.ListRoots()
			var lines string
			for _, r := range roots {
				lines += fmt.Sprintf("- %s (%s)\n", r.URI, r.Name)
			}
			return []byte(lines), nil
		},
		finemcp.WithDescription("List all registered root URIs"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(listTool)

	fmt.Println("Starting roots server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
