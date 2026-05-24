// Example: Audit Log Middleware
//
// Logs all tool invocations for audit and compliance.
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
//	# Call the "sensitive-op" tool (audit logged)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "sensitive-op" }
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
	s := finemcp.NewServer("auditlog", "1.0.0")
	s.Use(middleware.AuditLog(
		middleware.WithAuditSink(middleware.AuditSinkFunc(
			func(ctx context.Context, entry middleware.AuditEntry) {
				fmt.Printf("[AUDIT] tool=%s duration=%v success=%v\n",
					entry.ToolName, entry.Duration, entry.Success)
			},
		)),
	))

	tool, err := finemcp.NewTool("sensitive-op",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("done"), nil
		},
		finemcp.WithDescription("A sensitive operation (audit logged)"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting auditlog server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
