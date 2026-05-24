package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// StdioTransport implements the MCP stdio transport.
// It reads newline-delimited JSON-RPC messages from an io.Reader
// and writes JSON-RPC responses (one per line) to an io.Writer.
// This is the standard transport for CLI-based MCP servers.
type StdioTransport struct {
	server *finemcp.Server
	reader io.Reader
	writer io.Writer

	mu sync.Mutex // serializes writes
}

// NewStdioTransport creates a stdio transport wired to the given server.
// Use os.Stdin / os.Stdout for a real MCP server, or bytes.Buffer / io.Pipe for testing.
func NewStdioTransport(server *finemcp.Server, reader io.Reader, writer io.Writer) *StdioTransport {
	return &StdioTransport{
		server: server,
		reader: reader,
		writer: writer,
	}
}

// Run reads messages from the reader until EOF or the context is cancelled.
// Each message is processed sequentially (MCP stdio is single-threaded).
// Returns nil on clean EOF, or the first read/write error encountered.
func (t *StdioTransport) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(t.reader)

	// MCP messages can be large (e.g. tool results with embedded data).
	// Default bufio.Scanner buffer is 64KB; bump to 1MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// A unique ID is generated rather than using a fixed constant such as
	// "stdio" so that multiple StdioTransport instances wired to the same
	// Server (e.g. in tests) do not silently overwrite each other's sender
	// registration in the Server's senders map.
	clientID, err := generateSessionID()
	if err != nil {
		return fmt.Errorf("generate stdio client ID: %w", err)
	}
	sender := func(n *finemcp.JSONRPCNotification) {
		_ = t.writeNotification(n)
	}

	// Register this connection for broadcast/subscription notifications.
	if err := t.server.AddSender(clientID, sender); err != nil {
		return fmt.Errorf("register stdio sender: %w", err)
	}

	// Set up pending-request tracking for server-to-client requests
	// (e.g. sampling/createMessage). The writeFn appends a newline
	// (stdio framing) and writes atomically under the same mutex.
	pr := finemcp.NewPendingRequests(func(data []byte) error {
		t.mu.Lock()
		defer t.mu.Unlock()
		data = append(data, '\n')
		_, err := t.writer.Write(data)
		return err
	})
	defer pr.CloseAll()

	defer func() {
		t.server.RemoveSender(clientID)
		t.server.UnsubscribeAll(clientID)
		t.server.RemoveSessionTools(clientID)
	}()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue // skip blank lines
		}

		// Check if this is a client response to a server-initiated request.
		if finemcp.IsResponse(line) {
			pr.Deliver(line)
			continue
		}

		// Inject a notification sender so tool handlers can emit progress
		// via finemcp.ReportProgress. Notifications are written directly
		// to the writer; errors are silently dropped to keep the loop running.
		msgCtx := finemcp.WithNotificationSender(ctx, sender)
		msgCtx = finemcp.WithSubscriberID(msgCtx, clientID)
		msgCtx = finemcp.WithRequestSender(msgCtx, pr.Send)

		resp, err := t.server.HandleMessage(msgCtx, line)
		if err != nil {
			// Truly unrecoverable — stop the loop.
			return fmt.Errorf("handle message: %w", err)
		}

		// Notifications produce no response.
		if resp == nil {
			continue
		}

		if err := t.writeResponse(resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read: %w", err)
	}

	// Clean EOF.
	return nil
}

// writeResponse serializes a JSON-RPC response as a single line.
func (t *StdioTransport) writeResponse(resp *finemcp.JSONRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Write message + newline atomically.
	data = append(data, '\n')
	_, err = t.writer.Write(data)
	return err
}

// writeNotification serializes a JSON-RPC notification as a single line.
// It is safe for concurrent use and shares the same mutex as writeResponse.
func (t *StdioTransport) writeNotification(n *finemcp.JSONRPCNotification) error {
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	data = append(data, '\n')
	_, err = t.writer.Write(data)
	return err
}

// ServeStdio starts the server with the stdio transport, listening on os.Stdin
// and writing to os.Stdout. It blocks until one of:
//   - EOF on stdin (client disconnected)
//   - An OS signal is received (SIGINT or SIGTERM)
//   - The provided context is cancelled
//
// On shutdown, it waits for any in-flight request to complete before returning.
//
// Usage:
//
//	s := finemcp.NewServer("myapp", "1.0")
//	s.RegisterTool(myTool)
//	if err := transport.ServeStdio(ctx, s); err != nil {
//	    log.Fatal(err)
//	}
func ServeStdio(ctx context.Context, s *finemcp.Server) error {
	ctx, stop := setupSignals(ctx)
	defer stop()

	return ServeWithIO(ctx, s, os.Stdin, os.Stdout)
}

// ServeWithIO is like ServeStdio but accepts custom reader/writer and does not
// install signal handlers. Useful for testing or non-stdio transports.
// On return, it waits for any in-flight requests to drain.
func ServeWithIO(ctx context.Context, s *finemcp.Server, reader io.Reader, writer io.Writer) error {
	t := NewStdioTransport(s, reader, writer)

	err := t.Run(ctx)

	// Wait for any in-flight requests to drain via Shutdown.
	// Use a generous timeout — inflight requests should finish quickly.
	drainCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.Shutdown(drainCtx)

	return err
}
