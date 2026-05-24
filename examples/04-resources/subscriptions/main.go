// Example: Resource Subscriptions
//
// Demonstrates subscribing to resource changes and receiving notifications.
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
//	# List available resources
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "resources/list"
//	}'
//
//	# Read the counter resource
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "resources/read",
//	  "params": { "uri": "metrics://counter" }
//	}'
//
//	# Subscribe to counter changes
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "resources/subscribe",
//	  "params": { "uri": "metrics://counter" }
//	}'
//
//	# Call the "bump" tool to increment the counter (triggers notification)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 5,
//	  "method": "tools/call",
//	  "params": { "name": "bump" }
//	}'
package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("resource-subscriptions", "1.0.0",
		finemcp.WithResourceSubscriptions(),
	)

	var counter atomic.Int64

	res, err := finemcp.NewResource(
		"metrics://counter",
		"Live Counter",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			val := counter.Add(1)
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, fmt.Sprintf(`{"counter": %d}`, val)),
			}, nil
		},
		finemcp.WithResourceDescription("A counter that increments on each read"),
		finemcp.WithResourceMimeType("application/json"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResource(res)

	bumpTool, err := finemcp.NewTool("bump",
		func(ctx context.Context, input []byte) ([]byte, error) {
			counter.Add(1)
			s.NotifyResourceUpdated("metrics://counter")
			return []byte("Counter bumped"), nil
		},
		finemcp.WithDescription("Increment the counter and notify subscribers"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(bumpTool)

	fmt.Println("Starting resource-subscriptions server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
