// Example: Embedding MCP in an Existing HTTP Server
//
// Shows how to mount MCP handlers alongside existing routes.
//
// Possible requests (curl):
//
//	# Check the health endpoint (standard HTTP)
//	curl http://localhost:8080/health
//
//	# Visit the root page
//	curl http://localhost:8080/
//
//	# Initialize the MCP session (note: endpoint is /mcp)
//	curl -X POST http://localhost:8080/mcp -H "Content-Type: application/json" -d '{
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
//	curl -X POST http://localhost:8080/mcp -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "tools/list"
//	}'
//
//	# Call the "status" tool
//	curl -X POST http://localhost:8080/mcp -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "tools/call",
//	  "params": { "name": "status" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("embedded", "1.0.0")

	tool, err := finemcp.NewTool("status",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("All systems operational"), nil
		},
		finemcp.WithDescription("Check system status"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	mux := http.NewServeMux()

	// Mount MCP handler at /mcp
	mux.Handle("/mcp", transport.Handler(s))

	// Mount other application routes
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Welcome to the app! MCP available at /mcp")
	})

	fmt.Println("Starting embedded server on :8080")
	fmt.Println("  MCP endpoint: http://localhost:8080/mcp")
	fmt.Println("  Health check: http://localhost:8080/health")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
