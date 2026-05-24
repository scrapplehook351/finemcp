// Example: Sampling (Server-Initiated LLM Requests)
//
// Demonstrates using the sampling API to send a createMessage request
// to the client for LLM inference.
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
//	    "capabilities": { "sampling": {} }
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
//	# Call the "smart-answer" tool (requires client-side sampling support)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "smart-answer",
//	    "arguments": { "question": "What is MCP?" }
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
	s := finemcp.NewServer("sampling", "1.0.0")

	tool, err := finemcp.NewTool("smart-answer",
		func(ctx context.Context, input []byte) ([]byte, error) {
			var req struct {
				Question string `json:"question"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			temp := 0.7
			result, err := s.CreateMessage(ctx, finemcp.CreateMessageParams{
				Messages: []finemcp.SamplingMessage{
					{Role: "user", Content: finemcp.TextContent{Text: req.Question}},
				},
				SystemPrompt: "You are a helpful assistant. Be concise.",
				MaxTokens:    256,
				Temperature:  &temp,
				ModelPreferences: &finemcp.ModelPreferences{
					Hints: []finemcp.ModelHint{{Name: "claude"}},
				},
				IncludeContext: "thisServer",
			})
			if err != nil {
				return nil, fmt.Errorf("sampling failed: %w", err)
			}
			return []byte(fmt.Sprintf("Model %s responded", result.Model)), nil
		},
		finemcp.WithDescription("Answer a question using client-side LLM sampling"),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question to answer"},
			},
			"required": []string{"question"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting sampling server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
