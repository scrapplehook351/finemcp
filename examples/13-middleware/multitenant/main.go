// Example: Multi-Tenant Middleware
//
// Demonstrates tenant isolation with per-tenant tool access control.
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
//	# Call the "shared-tool" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "shared-tool" }
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
	s := finemcp.NewServer("multitenant", "1.0.0")

	extractor := middleware.TenantFromAuthMeta("tenant_id")
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(t *finemcp.Tool) bool {
				return true // acme can use all tools
			},
		},
		"globex": {
			ToolFilter: func(t *finemcp.Tool) bool {
				return t.Name == "shared-tool" // globex can only use shared-tool
			},
		},
	})

	resolver := middleware.NewTenantResolver(extractor, store)
	s.SetTenantResolver(resolver)

	tool, err := finemcp.NewTool("shared-tool",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("shared result"), nil
		},
		finemcp.WithDescription("Available to all tenants"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting multitenant server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
