// Example: Progress Reporting
//
// Demonstrates how tools report incremental progress to the client.
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
//	# Call the "process-data" tool (reports progress 0-100)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "process-data" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("progress", "1.0.0")

	tool, err := finemcp.NewTool("process-data",
		func(ctx context.Context, input []byte) ([]byte, error) {
			total := 100.0
			for i := 0; i <= int(total); i += 10 {
				finemcp.ReportProgress(ctx, float64(i), total)
				time.Sleep(100 * time.Millisecond)
			}
			return []byte("Processing complete!"), nil
		},
		finemcp.WithDescription("Process data with progress updates"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting progress server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
