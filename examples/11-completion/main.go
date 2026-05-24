// Example: Auto-Completion
//
// Demonstrates the completion system for prompts and resource templates.
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
//	# Get the "weather" prompt
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "prompts/get",
//	  "params": {
//	    "name": "weather",
//	    "arguments": { "city": "Tokyo" }
//	  }
//	}'
//
//	# Auto-complete city name for the "weather" prompt
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "completion/complete",
//	  "params": {
//	    "ref": { "type": "ref/prompt", "name": "weather" },
//	    "argument": { "name": "city", "value": "Ne" }
//	  }
//	}'
//
//	# Auto-complete city name for the resource template
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 5,
//	  "method": "completion/complete",
//	  "params": {
//	    "ref": { "type": "ref/resource", "uri": "city://{name}" },
//	    "argument": { "name": "name", "value": "Pa" }
//	  }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

var cities = []string{
	"New York", "London", "Tokyo", "Paris", "Berlin",
	"Sydney", "Toronto", "Mumbai", "Beijing", "Cairo",
}

func main() {
	s := finemcp.NewServer("completion", "1.0.0")

	prompt, err := finemcp.NewPrompt(
		"weather",
		func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage(fmt.Sprintf("What's the weather like in %s?", args["city"])),
			}, nil
		},
		finemcp.WithPromptDescription("Get weather for a city"),
		finemcp.WithPromptArguments(finemcp.PromptArgument{
			Name: "city", Description: "City name", Required: true,
		}),
		finemcp.WithCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := strings.ToLower(req.Argument.Value)
			var matches []string
			for _, c := range cities {
				if strings.HasPrefix(strings.ToLower(c), prefix) {
					matches = append(matches, c)
				}
			}
			return &finemcp.CompletionResult{Values: matches, Total: len(cities)}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterPrompt(prompt)

	tmpl, err := finemcp.NewResourceTemplate(
		"city://{name}",
		"City Data",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, `{"population": 1000000}`),
			}, nil
		},
		finemcp.WithTemplateCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := strings.ToLower(req.Argument.Value)
			var matches []string
			for _, c := range cities {
				if strings.HasPrefix(strings.ToLower(c), prefix) {
					matches = append(matches, c)
				}
			}
			return &finemcp.CompletionResult{Values: matches}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResourceTemplate(tmpl)

	fmt.Println("Starting completion server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
