// Example: Authentication Middleware
//
// Demonstrates HTTP-level authentication with bearer tokens and API keys.
//
// Possible requests (curl):
//
//	# Initialize with bearer token
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
//	  -H "Authorization: Bearer my-secret-token" -d '{
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
//	# Initialize with API key
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
//	  -H "X-API-Key: my-api-key" -d '{
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
//	# Call the "protected" tool (with auth header)
//	curl -X POST http://localhost:8080 -H "Content-Type: application/json" \
//	  -H "Authorization: Bearer my-secret-token" -d '{
//	  "jsonrpc": "2.0",
//	  "id": 2,
//	  "method": "tools/call",
//	  "params": { "name": "protected" }
//	}'
//
//	# Unauthenticated request (should fail with 401)
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
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("auth", "1.0.0")
	s.SetAuthChecker(middleware.RequireAuth())

	tool, err := finemcp.NewTool("protected",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("secret data"), nil
		},
		finemcp.WithDescription("Requires authentication"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	// Wrap the HTTP handler with token verification
	verifier := middleware.ChainVerifiers(
		middleware.StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
			"my-secret-token": {Subject: "admin", Roles: []string{"admin"}},
		}),
		middleware.StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
			"my-api-key": {Subject: "service", Roles: []string{"service"}},
		}),
	)

	handler := transport.Handler(s)
	protected := middleware.HTTPAuth(verifier, handler)

	fmt.Println("Starting auth server on :8080")
	log.Fatal(http.ListenAndServe(":8080", protected))
}
