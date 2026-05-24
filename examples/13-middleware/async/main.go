// Example: Async Middleware
//
// Converts long-running tools into async tasks with background execution.
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
//	# Call the "background-job" tool (returns immediately, runs in background)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "background-job" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	store := finemcp.NewTaskStore()

	s := finemcp.NewServer("async", "1.0.0",
		finemcp.WithTaskStore(store),
	)

	asyncMW, waiter := middleware.Async()
	s.Use(asyncMW)
	_ = waiter // waiter can be used to await task completion

	tool, err := finemcp.NewTool("background-job",
		func(ctx context.Context, input []byte) ([]byte, error) {
			time.Sleep(2 * time.Second)
			return []byte("job completed"), nil
		},
		finemcp.WithDescription("A long-running job executed asynchronously"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting async server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
