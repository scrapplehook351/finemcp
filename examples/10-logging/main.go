// Example: Server Logging
//
// Demonstrates the server logging API with structured log messages.
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
//	# Call the "do-work" tool (emits debug, info, warning, error logs)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "do-work" }
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
	s := finemcp.NewServer("logging", "1.0.0")

	s.SetLogHandler(func(ctx context.Context, level finemcp.LogLevel) error {
		if level == finemcp.LogLevelDebug {
			return fmt.Errorf("debug suppressed")
		}
		return nil
	})

	tool, err := finemcp.NewTool("do-work",
		func(ctx context.Context, input []byte) ([]byte, error) {
			_ = s.SendLogMessage(ctx, finemcp.LogLevelDebug, "worker",
				map[string]any{"msg": "starting work", "step": 0})
			_ = s.SendLogMessage(ctx, finemcp.LogLevelInfo, "worker",
				map[string]any{"msg": "processing", "step": 1})
			_ = s.SendLogMessage(ctx, finemcp.LogLevelWarning, "worker",
				map[string]any{"msg": "slow query detected", "ms": 450})
			_ = s.SendLogMessage(ctx, finemcp.LogLevelError, "worker",
				map[string]any{"msg": "retrying failed operation", "attempt": 2})
			return []byte("Work done. Check logs."), nil
		},
		finemcp.WithDescription("Perform work and emit log messages"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting logging server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
