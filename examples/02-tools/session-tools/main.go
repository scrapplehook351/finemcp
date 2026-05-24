// Example: Session-Scoped Tools
//
// Demonstrates per-session tool overlays that shadow global tools.
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
//	# Call the "greet" tool (global version)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
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
	s := finemcp.NewServer("session-tools", "1.0.0",
		finemcp.WithMaxSessionTools(10),
	)

	globalTool, err := finemcp.NewTypedTool("greet",
		func(ctx context.Context, in struct {
			Name string `json:"name" description:"Name to greet"`
		}) (string, error) {
			return fmt.Sprintf("Hello, %s! (global)", in.Name), nil
		},
		finemcp.WithDescription("Global greeting tool"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(globalTool)

	s.OnSessionToolShadow(func(sessionID, toolName string) {
		fmt.Printf("Session %s shadowed global tool %q\n", sessionID, toolName)
	})

	sessionTool, err := finemcp.NewTypedTool("greet",
		func(ctx context.Context, in struct {
			Name string `json:"name" description:"Name to greet"`
		}) (string, error) {
			return fmt.Sprintf("Hi %s! (session-specific)", in.Name), nil
		},
		finemcp.WithDescription("Session-specific greeting"),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err := s.AddSessionTool(context.Background(), "demo-session", sessionTool); err != nil {
		log.Fatal(err)
	}

	tools := s.SessionTools("demo-session")
	for _, t := range tools {
		fmt.Printf("Session tool: %s - %s\n", t.Name, t.Description)
	}

	if err := s.RemoveSessionTool("demo-session", "greet"); err != nil {
		log.Fatal(err)
	}
	s.RemoveSessionTools("demo-session")

	fmt.Println("Starting session-tools server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
