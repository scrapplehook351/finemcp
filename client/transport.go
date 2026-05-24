package client

import "context"

// Transport is the interface that client transports must implement.
// A transport manages the underlying connection to an MCP server and
// provides the ability to send and receive raw JSON-RPC messages.
//
// Implementations must be safe for concurrent use: Send may be called from
// multiple goroutines while Receive blocks in the read loop.
type Transport interface {
	// Start establishes the connection to the MCP server. It must be called
	// before Send or Receive. The context controls connection establishment
	// only; cancelling it after Start returns does not close the transport.
	Start(ctx context.Context) error

	// Send writes a raw JSON-RPC message to the server. It must be safe
	// for concurrent use.
	Send(ctx context.Context, data []byte) error

	// Receive blocks until a complete JSON-RPC message is available from the
	// server, or an error occurs (including io.EOF for clean shutdown).
	// The returned byte slice is only valid until the next Receive call.
	Receive(ctx context.Context) ([]byte, error)

	// Close shuts down the transport and releases all resources.
	// After Close returns, Send and Receive must return errors.
	Close() error
}
