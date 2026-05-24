// Example: Raw Tool Handler
//
// Demonstrates raw ToolHandler with manual JSON parsing and WithInputSchema.
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
//	# Call the "add" tool with two numbers
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "add",
//	    "arguments": { "a": 10, "b": 25 }
//	  }
//	}'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("raw-tools", "1.0.0")

	tool, err := finemcp.NewTool("add",
		func(ctx context.Context, input []byte) ([]byte, error) {
			var params struct {
				A float64 `json:"a"`
				B float64 `json:"b"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			return []byte(fmt.Sprintf("%.2f", params.A+params.B)), nil
		},
		finemcp.WithDescription("Add two numbers"),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "number", "description": "First operand"},
				"b": map[string]any{"type": "number", "description": "Second operand"},
			},
			"required": []string{"a", "b"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting raw-tools server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
