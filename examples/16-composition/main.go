// Example: Tool Composition
//
// Demonstrates Pipeline and Parallel composition of tool handlers.
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
//	# Call the "pipeline" tool (validate > process > enrich)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "pipeline" }
//	}'
//
//	# Call the "parallel-checks" tool (runs check-a and check-b concurrently)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "tools/call",
//	  "params": { "name": "parallel-checks" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func validate(ctx context.Context, input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	return input, nil
}

func process(ctx context.Context, input []byte) ([]byte, error) {
	return []byte(fmt.Sprintf("Processed: %s", string(input))), nil
}

func enrich(ctx context.Context, input []byte) ([]byte, error) {
	return []byte(fmt.Sprintf("Enriched: %s", string(input))), nil
}

func main() {
	s := finemcp.NewServer("composition", "1.0.0")

	// Pipeline: validate > process > enrich
	pipelined := finemcp.Pipeline(validate, process, enrich)
	pipeTool, err := finemcp.NewTool("pipeline",
		pipelined,
		finemcp.WithDescription("Validate, process, then enrich data in sequence"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(pipeTool)

	// Parallel: run multiple handlers concurrently
	parallel := finemcp.Parallel(
		finemcp.NamedHandler{Name: "check-a", Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("Check A passed"), nil
		}},
		finemcp.NamedHandler{Name: "check-b", Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("Check B passed"), nil
		}},
	)
	parTool, err := finemcp.NewTool("parallel-checks",
		parallel,
		finemcp.WithDescription("Run multiple checks in parallel"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(parTool)

	fmt.Println("Starting composition server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
