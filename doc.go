// Package finemcp is a production-ready Go framework for building
// [Model Context Protocol] (MCP) servers.
//
// FineMCP implements the full MCP specification (2025-11-25) over JSON-RPC 2.0,
// with backward compatibility for 2025-06-18, 2025-03-26, and 2024-11-05. It
// provides typed tool handlers with automatic JSON Schema generation, streaming
// tool responses, a composable middleware architecture, and a zero-network test
// harness.
//
// # Quick start
//
//	s := finemcp.NewServer("my-server", "1.0.0")
//
//	tool, _ := finemcp.NewTool("greet", func(ctx context.Context, input json.RawMessage) (*finemcp.CallToolResult, error) {
//	    return finemcp.NewTextResult("Hello!"), nil
//	}, finemcp.WithDescription("Say hello"))
//
//	s.AddTool(tool)
//	finemcp.ServeStdio(s)
//
// # Transports
//
// Four built-in transports are available in the [github.com/finemcp/finemcp/transport] package:
//   - stdio — newline-delimited JSON over stdin/stdout
//   - SSE — HTTP Server-Sent Events
//   - Streamable HTTP — MCP Streamable HTTP with session management
//   - WebSocket — full-duplex WebSocket
//
// # Middleware
//
// The [github.com/finemcp/finemcp/middleware] package provides 16 composable
// middleware components including authentication, RBAC, rate limiting, circuit
// breaking, OpenTelemetry tracing, audit logging, cost tracking, and more.
//
// # Streaming
//
// Long-running tools can stream incremental results via [ToolStream]:
//
//	stream := finemcp.StreamFromCtx(ctx)
//	if stream != nil {
//	    stream.SendText("progress update…")
//	}
//
// Streaming works over SSE and Streamable HTTP transports.
//
// # Testing
//
// The [github.com/finemcp/finemcp/mcptest] package provides an in-memory test
// server that requires no network and works with the race detector.
//
// # Content types
//
// Tool results use the sealed [Content] interface, implemented by [TextContent],
// [ImageContent], [AudioContent], and [EmbeddedResource].
//
// For the complete documentation, see https://finemcp.dev/docs
//
// [Model Context Protocol]: https://modelcontextprotocol.io
package finemcp
