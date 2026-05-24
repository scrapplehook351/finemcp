// Package transport provides MCP transport implementations for finemcp.
//
// A transport handles the wire protocol — reading incoming JSON-RPC 2.0
// requests and writing back responses — while delegating all message
// processing to a [github.com/finemcp/finemcp.Server]. Transports are
// intentionally thin: they own I/O, not business logic.
//
// # Available Transports
//
// Stdio (CLI mode):
//
// The [StdioTransport] reads newline-delimited JSON-RPC messages from an
// [io.Reader] and writes one-line JSON-RPC responses to an [io.Writer].
// This is the standard transport for MCP servers invoked by editors, CLI
// tools, and other local clients.
//
//	s := finemcp.NewServer("myapp", "1.0")
//	s.RegisterTool(myTool)
//	if err := transport.ServeStdio(ctx, s); err != nil {
//	    log.Fatal(err)
//	}
//
// For testing or embedding, use [ServeWithIO] to supply custom readers
// and writers, or construct a [StdioTransport] directly via
// [NewStdioTransport] for full control over the run loop.
//
// HTTP (network mode):
//
// [Handler] returns an [http.Handler] that accepts JSON-RPC 2.0 messages
// via HTTP POST. Embed it in any router or use [StartHTTP] for standalone
// mode:
//
//	// Embedded in an existing mux:
//	mux.Handle("/mcp", transport.Handler(s))
//
//	// Standalone:
//	log.Fatal(transport.StartHTTP(s, ":8080"))
//
// Only POST is accepted (per the MCP spec); other methods receive 405.
// Notifications return 204 No Content.
//
// SSE (Server-Sent Events):
//
// [SSEHandler] returns an [http.Handler] implementing the MCP SSE transport.
// It exposes two endpoints: GET /sse for the event stream and POST /message
// for sending JSON-RPC requests. Responses are delivered asynchronously on
// the SSE stream.
//
//	// Standalone:
//	log.Fatal(transport.StartSSE(s, ":8080"))
//
//	// Embedded with custom paths:
//	mux.Handle("/", transport.SSEHandler(s,
//	    transport.WithSSEPath("/sse"),
//	    transport.WithMessagePath("/message"),
//	))
//
// The SSE transport manages per-client sessions. When a client connects
// via GET /sse, it receives an "endpoint" event containing the URL for
// POSTing messages. JSON-RPC responses are pushed as "message" events.
// Sessions are cleaned up automatically when the client disconnects.
//
// # Signal Handling & Shutdown
//
// [ServeStdio] installs signal handlers for SIGINT and SIGTERM and
// triggers [finemcp.Server.Shutdown] automatically, draining in-flight
// requests before returning. When using [Handler] directly, call
// Shutdown on the server from your own lifecycle management.
//
// # Choosing a Transport
//
//   - Use Stdio when the MCP server is launched as a child process by an
//     editor or CLI tool (the most common MCP deployment model).
//   - Use HTTP when clients connect over the network or when you need to
//     integrate MCP into an existing HTTP service.
//   - Use SSE when clients need a persistent event stream, such as web
//     browsers or MCP clients that expect the SSE transport (e.g. Claude Desktop).
package transport
