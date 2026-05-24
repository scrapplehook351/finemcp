// Example: Static Resources
//
// Demonstrates registering static resources with fixed URIs.
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
//	# Read the application settings resource
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 3,
//	  "method": "resources/read",
//	  "params": { "uri": "config://app/settings" }
//	}'
//
//	# Read the logo image resource (returns base64-encoded blob)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 4,
//	  "method": "resources/read",
//	  "params": { "uri": "file://logo.png" }
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
	s := finemcp.NewServer("static-resources", "1.0.0")

	textRes, err := finemcp.NewResource(
		"config://app/settings",
		"Application Settings",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, `{"theme": "dark", "lang": "en"}`),
			}, nil
		},
		finemcp.WithResourceDescription("Current application settings"),
		finemcp.WithResourceMimeType("application/json"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResource(textRes)

	blobRes, err := finemcp.NewResource(
		"file://logo.png",
		"Logo Image",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewBlobResourceContent(uri, []byte{0x89, 0x50, 0x4E, 0x47}),
			}, nil
		},
		finemcp.WithResourceDescription("Application logo"),
		finemcp.WithResourceMimeType("image/png"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResource(blobRes)

	for _, r := range s.ListResources() {
		fmt.Printf("Resource: %s (%s)\n", r.Name, r.URI)
	}

	fmt.Println("Starting static-resources server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
