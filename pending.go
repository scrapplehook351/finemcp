package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// PendingRequests tracks in-flight server-to-client requests, correlating
// outbound requests with their responses by JSON-RPC ID. Transports embed
// this to implement the RequestSender callback.
//
// Usage in a transport:
//  1. Create a PendingRequests via NewPendingRequests(writeFn).
//  2. Pass pr.Send as the RequestSender to WithRequestSender.
//  3. When reading messages from the client, check pr.IsResponse(data) and
//     call pr.Deliver(data) for responses; forward non-responses to HandleMessage.
//  4. Call pr.CloseAll() on transport shutdown.
type PendingRequests struct {
	writeFn func(data []byte) error // writes raw JSON to the client

	mu      sync.Mutex
	pending map[string]chan *JSONRPCResponse
	idSeq   uint64
	closed  bool
}

// NewPendingRequests creates a PendingRequests that writes outgoing requests
// via writeFn. The function must be safe for concurrent use.
func NewPendingRequests(writeFn func(data []byte) error) *PendingRequests {
	return &PendingRequests{
		writeFn: writeFn,
		pending: make(map[string]chan *JSONRPCResponse),
	}
}

// Send implements the RequestSender signature. It assigns a unique ID to
// the request, writes it to the client, and blocks until the client responds
// or the context expires.
func (pr *PendingRequests) Send(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	pr.mu.Lock()
	if pr.closed {
		pr.mu.Unlock()
		return nil, fmt.Errorf("transport closed")
	}
	pr.idSeq++
	id := fmt.Sprintf("srv-%d", pr.idSeq)
	ch := make(chan *JSONRPCResponse, 1)
	pr.pending[id] = ch
	pr.mu.Unlock()

	defer func() {
		pr.mu.Lock()
		delete(pr.pending, id)
		pr.mu.Unlock()
	}()

	// Build and write the request.
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if err := pr.writeFn(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Wait for the response or context cancellation.
	select {
	case resp := <-ch:
		if resp == nil {
			// Channel was closed by CloseAll (transport shutdown).
			return nil, fmt.Errorf("transport closed")
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// IsResponse checks whether a raw JSON message is a JSON-RPC response
// (has "id" and either "result" or "error", but no "method"). This is used
// by transports to distinguish client responses from client requests.
func IsResponse(data []byte) bool {
	var peek struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return false
	}
	// A response has an id but no method, and has result or error.
	return peek.ID != nil && peek.Method == "" && (peek.Result != nil || peek.Error != nil)
}

// Deliver routes a client response to the pending request matching its ID.
// Returns true if the response was delivered, false if no pending request
// matches (e.g., the caller timed out already or the response is unexpected).
func (pr *PendingRequests) Deliver(data []byte) bool {
	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return false
	}

	// Normalize the ID to a string key.
	key := fmt.Sprintf("%v", resp.ID)

	pr.mu.Lock()
	ch, ok := pr.pending[key]
	pr.mu.Unlock()

	if !ok {
		return false
	}

	// Non-blocking send: the channel has buffer size 1.
	select {
	case ch <- &resp:
		return true
	default:
		return false
	}
}

// CloseAll cancels all pending requests. Called on transport shutdown.
// After CloseAll, Send returns an error immediately.
func (pr *PendingRequests) CloseAll() {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.closed = true
	for id, ch := range pr.pending {
		close(ch)
		delete(pr.pending, id)
	}
}
