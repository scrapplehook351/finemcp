// Example: Resource Templates
//
// Demonstrates resource templates with URI variables (RFC 6570).
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
//	# List resource templates
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "resources/templates/list"
//	}'
//
//	# Read a user profile by ID
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "resources/read",
//	  "params": { "uri": "users://42" }
//	}'
//
//	# Read a project file by path
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "resources/read",
//	  "params": { "uri": "files://README.md" }
//	}'
//
//	# Auto-complete file path
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 5,
//	  "method": "completion/complete",
//	  "params": {
//	    "ref": { "type": "ref/resource", "uri": "files://{path}" },
//	    "argument": { "name": "path", "value": "R" }
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
	s := finemcp.NewServer("resource-templates", "1.0.0")

	userTmpl, err := finemcp.NewResourceTemplate(
		"users://{id}",
		"User Profile",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri,
					fmt.Sprintf(`{"id": "%s", "name": "User"}`, uri)),
			}, nil
		},
		finemcp.WithTemplateDescription("Fetch a user profile by ID"),
		finemcp.WithTemplateMimeType("application/json"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResourceTemplate(userTmpl)

	fileTmpl, err := finemcp.NewResourceTemplate(
		"files://{path}",
		"Project File",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, "file contents here..."),
			}, nil
		},
		finemcp.WithTemplateDescription("Read a project file by path"),
		finemcp.WithTemplateMimeType("text/plain"),
		finemcp.WithTemplateCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			return &finemcp.CompletionResult{
				Values: []string{"README.md", "go.mod", "main.go"},
			}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResourceTemplate(fileTmpl)

	for _, t := range s.ListResourceTemplates() {
		fmt.Printf("Template: %s (%s)\n", t.Name, t.URITemplate)
	}

	fmt.Println("Starting resource-templates server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
