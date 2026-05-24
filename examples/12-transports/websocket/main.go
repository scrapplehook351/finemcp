// Example: WebSocket Transport
//
// Demonstrates the WebSocket transport for bidirectional communication.
//
// Possible requests (websocat or wscat):
//
//	# Connect and send JSON-RPC via WebSocket (using websocat)
//	websocat ws://localhost:8080
//
//	# Then type JSON-RPC messages interactively:
//	{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"ws-client","version":"1.0.0"},"capabilities":{}}}
//	{"jsonrpc":"2.0","id":2,"method":"tools/list"}
//	{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}
//
//	# Or pipe a single message:
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"ws-client","version":"1.0.0"},"capabilities":{}}}' | websocat ws://localhost:8080
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("websocket-server", "1.0.0")

	tool, err := finemcp.NewTool("echo",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return append([]byte("Echo: "), input...), nil
		},
		finemcp.WithDescription("Echo back the input over WebSocket"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	fmt.Println("Starting WebSocket transport on :8080")
	log.Fatal(transport.StartWebSocket(s, ":8080"))
}
