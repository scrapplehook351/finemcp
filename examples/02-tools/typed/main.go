// Example: Typed Tools
//
// Demonstrates NewTypedTool with generics, struct tags, and complex input types.
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
//	# Call the "greet" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "greet",
//	    "arguments": { "name": "Alice" }
//	  }
//	}'
//
//	# Call the "search" tool with all parameters
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "search",
//	    "arguments": { "query": "finemcp", "tags": ["go", "mcp"], "limit": 5, "verbose": true }
//	  }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("typed-tools", "1.0.0")

	greetTool, err := finemcp.NewTypedTool("greet",
		func(ctx context.Context, in struct {
			Name string `json:"name" description:"Name to greet"`
		}) (string, error) {
			return fmt.Sprintf("Hello, %s!", in.Name), nil
		},
		finemcp.WithDescription("Greet someone by name"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(greetTool)

	searchTool, err := finemcp.NewTypedTool("search",
		func(ctx context.Context, in struct {
			Query   string   `json:"query" description:"Search query"`
			Tags    []string `json:"tags" description:"Filter tags"`
			Limit   *int     `json:"limit" description:"Max results (optional)"`
			Verbose bool     `json:"verbose" description:"Include details"`
		}) (string, error) {
			limit := 10
			if in.Limit != nil {
				limit = *in.Limit
			}
			return fmt.Sprintf("Searching %q tags=%v limit=%d verbose=%v",
				in.Query, strings.Join(in.Tags, ","), limit, in.Verbose), nil
		},
		finemcp.WithDescription("Search with complex parameters"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(searchTool)

	fmt.Println("Starting typed-tools server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
