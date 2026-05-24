// Example: Simulation Middleware
//
// Runs tools in simulation mode for dry-run and testing scenarios.
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
//	# Call the "deploy" tool (runs in simulation/dry-run mode)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "deploy" }
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
	s := finemcp.NewServer("simulation", "1.0.0")
	s.Use(middleware.Simulation())

	tool, err := finemcp.NewTool("deploy",
		func(ctx context.Context, input []byte) ([]byte, error) {
			if finemcp.IsSimulatedFromCtx(ctx) {
				return []byte("[SIMULATED] Would deploy to production"), nil
			}
			return []byte("Deployed to production"), nil
		},
		finemcp.WithDescription("Deploy to production (supports simulation)"),
		finemcp.WithDestructive(),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting simulation server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
