// Example: Content Types
//
// Demonstrates all content types: text, image, audio, and embedded resources.
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
//	# Call the "text-example" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "text-example" }
//	}'
//
//	# Call the "image-example" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "tools/call",
//	  "params": { "name": "image-example" }
//	}'
//
//	# Call the "embedded-example" tool
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 5,
//	  "method": "tools/call",
//	  "params": { "name": "embedded-example" }
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
	s := finemcp.NewServer("content-types", "1.0.0")

	// Text result (default) — handler returns []byte, framework wraps as text
	textTool, err := finemcp.NewTool("text-example",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("Plain text content"), nil
		},
		finemcp.WithDescription("Returns text content"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(textTool)

	// Demonstrates image content type
	imageTool, err := finemcp.NewTypedTool("image-example",
		func(ctx context.Context, in struct{}) (string, error) {
			return "Image tools return results via NewImageResult in advanced patterns", nil
		},
		finemcp.WithDescription("Demonstrates image content concept"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(imageTool)

	// Demonstrates embedded resource content type
	embedTool, err := finemcp.NewTypedTool("embedded-example",
		func(ctx context.Context, in struct{}) (string, error) {
			return "Embedded resources can be returned for rich content", nil
		},
		finemcp.WithDescription("Demonstrates embedded resource concept"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(embedTool)

	fmt.Println("Starting content-types server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
