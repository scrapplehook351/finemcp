// Example: Stdio Transport
//
// Demonstrates running an MCP server over standard input/output,
// commonly used for subprocess-based tool hosting.
//
// Possible requests (pipe JSON-RPC via stdin):
//
//	# Initialize the MCP session
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"cli","version":"1.0.0"},"capabilities":{}}}' | go run .
//
//	# List available tools
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"cli","version":"1.0.0"},"capabilities":{}}}\n{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | go run .
//
//	# Call the "echo" tool
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"cli","version":"1.0.0"},"capabilities":{}}}\n{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hello"}}}' | go run .
package main

import (
	"context"
	"log"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

func main() {
	s := finemcp.NewServer("stdio-server", "1.0.0")

	tool, err := finemcp.NewTool("echo",
		func(ctx context.Context, input []byte) ([]byte, error) {
			return input, nil
		},
		finemcp.WithDescription("Echo the input back"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)

	if err := transport.ServeStdio(context.Background(), s); err != nil {
		log.Fatal(err)
	}
}
