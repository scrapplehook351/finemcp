// Example: Circuit Breaker Middleware
//
// Opens the circuit after repeated failures and fast-fails subsequent calls.
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
//	# Call the "external-call" tool (circuit breaker protected)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "external-call" }
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
	s := finemcp.NewServer("circuitbreaker", "1.0.0")
	s.Use(middleware.CircuitBreaker(
		middleware.WithFailureThreshold(5),
		middleware.WithSuccessThreshold(3),
	))

	tool, err := finemcp.NewTool("external-call",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("external service response"), nil
		},
		finemcp.WithDescription("Calls an external service with circuit breaker protection"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting circuitbreaker server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
