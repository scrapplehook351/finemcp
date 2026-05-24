// Example: Elicitation (Server-Initiated User Prompts)
//
// Demonstrates the elicitation API to prompt client-side users for input.
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
//	    "capabilities": { "elicitation": {} }
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
//	# Call the "delete-file" tool (triggers elicitation for confirmation)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "delete-file",
//	    "arguments": { "path": "/tmp/test.txt" }
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
	s := finemcp.NewServer("elicitation", "1.0.0")

	tool, err := finemcp.NewTool("delete-file",
		func(ctx context.Context, input []byte) ([]byte, error) {
			var req struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			result, err := s.ElicitUser(ctx, finemcp.ElicitationParams{
				Prompt:  fmt.Sprintf("Are you sure you want to delete %q? Type 'yes' to confirm.", req.Path),
				Type:    "text",
				Default: "no",
			})
			if err != nil {
				return nil, fmt.Errorf("elicitation failed: %w", err)
			}
			if result.Cancelled || result.Value != "yes" {
				return []byte("Deletion cancelled."), nil
			}
			return []byte(fmt.Sprintf("Deleted %s", req.Path)), nil
		},
		finemcp.WithDescription("Delete a file with user confirmation"),
		finemcp.WithDestructive(),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "File path to delete"},
			},
			"required": []string{"path"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting elicitation server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
