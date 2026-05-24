// Example: Prompt Completion
//
// Demonstrates auto-completion for prompt arguments.
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
//	# Get the "translate" prompt
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "prompts/get",
//	  "params": {
//	    "name": "translate",
//	    "arguments": { "language": "Go" }
//	  }
//	}'
//
//	# Auto-complete the language argument (e.g. prefix "Ty" matches "TypeScript")
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "completion/complete",
//	  "params": {
//	    "ref": { "type": "ref/prompt", "name": "translate" },
//	    "argument": { "name": "language", "value": "Ty" }
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

var languages = []string{
	"Go", "Python", "JavaScript", "TypeScript", "Rust", "Java", "C++",
}

func main() {
	s := finemcp.NewServer("prompt-completion", "1.0.0")

	prompt, err := finemcp.NewPrompt(
		"translate",
		func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage(fmt.Sprintf("Translate this code to %s.", args["language"])),
			}, nil
		},
		finemcp.WithPromptDescription("Translate code to another language"),
		finemcp.WithPromptArguments(
			finemcp.PromptArgument{
				Name:        "language",
				Description: "Target programming language",
				Required:    true,
			},
		),
		finemcp.WithCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := req.Argument.Value
			var matches []string
			for _, lang := range languages {
				if strings.HasPrefix(strings.ToLower(lang), strings.ToLower(prefix)) {
					matches = append(matches, lang)
				}
			}
			return &finemcp.CompletionResult{
				Values:  matches,
				HasMore: false,
			}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterPrompt(prompt)

	fmt.Println("Starting prompt-completion server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
