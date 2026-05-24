// Example: Prompts
//
// Demonstrates the prompt system with reusable prompt templates.
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
//	# List available prompts
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "prompts/list"
//	}'
//
//	# Get the "greet" prompt with arguments
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "prompts/get",
//	  "params": {
//	    "name": "greet",
//	    "arguments": { "name": "Alice" }
//	  }
//	}'
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("prompts", "1.0.0")

	greetPrompt, err := finemcp.NewPrompt(
		"greet",
		func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			name := args["name"]
			if name == "" {
				name = "World"
			}
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage(fmt.Sprintf("Please greet %s warmly.", name)),
				finemcp.NewAssistantMessage(fmt.Sprintf("Hello, %s! Welcome!", name)),
			}, nil
		},
		finemcp.WithPromptDescription("Generate a friendly greeting"),
		finemcp.WithPromptArguments(
			finemcp.PromptArgument{
				Name:        "name",
				Description: "Name of the person to greet",
				Required:    true,
			},
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterPrompt(greetPrompt)

	for _, p := range s.ListPrompts() {
		fmt.Printf("Prompt: %s - %s\n", p.Name, p.Description)
	}

	fmt.Println("Starting prompts server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
