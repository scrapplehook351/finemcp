// Example: Cost Tracking Middleware
//
// Tracks per-tool cost/usage metrics using a custom collector.
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
//	# Call the "premium-tool" tool (cost tracked)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "premium-tool" }
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
	s := finemcp.NewServer("costtracking", "1.0.0")
	s.Use(middleware.CostTracking(
		middleware.WithCostCollector(middleware.CostCollectorFunc(
			func(ctx context.Context, record middleware.CostRecord) {
				fmt.Printf("Cost: tool=%s duration=%v\n", record.ToolName, record.Duration)
			},
		)),
	))

	tool, err := finemcp.NewTool("premium-tool",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("premium result"), nil
		},
		finemcp.WithDescription("A premium tool that incurs cost"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting costtracking server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
