// Example: Logging Middleware
//
// Logs tool invocations with duration and result status.
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
//	# Call the "work" tool (logged by middleware)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "work" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

// stdLogger adapts the standard log package to the middleware.Logger interface.
type stdLogger struct{}

func (stdLogger) Info(msg string, keysAndValues ...any) {
	log.Printf("[INFO] %s %v", msg, keysAndValues)
}

func (stdLogger) Error(msg string, keysAndValues ...any) {
	log.Printf("[ERROR] %s %v", msg, keysAndValues)
}

func main() {
	s := finemcp.NewServer("logging-mw", "1.0.0")
	s.Use(middleware.Logging(stdLogger{}))

	tool, err := finemcp.NewTool("work",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("done"), nil
		},
		finemcp.WithDescription("Does some work"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting logging-mw server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
