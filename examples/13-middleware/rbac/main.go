// Example: RBAC Middleware
//
// Demonstrates role-based access control for tools.
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
//	# Call the "public-tool" (no role required)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "public-tool" }
//	}'
//
//	# Call the "admin-tool" (requires admin role)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "tools/call",
//	  "params": { "name": "admin-tool" }
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
	s := finemcp.NewServer("rbac", "1.0.0")
	s.Use(middleware.RBAC())

	adminTool, err := finemcp.NewTool("admin-tool",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("admin action done"), nil
		},
		finemcp.WithDescription("Only admins can use this"),
		finemcp.WithRoles("admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(adminTool)

	pubTool, err := finemcp.NewTool("public-tool",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("public action done"), nil
		},
		finemcp.WithDescription("Anyone can use this (no roles)"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(pubTool)

	fmt.Println("Starting rbac server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
