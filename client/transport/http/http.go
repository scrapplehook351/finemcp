// Package http provides a simple HTTP request-response transport for MCP.
//
// Unlike the streamable transport, this transport does not use server-sent
// events. Each request is a standalone POST that returns a single JSON-RPC
// response. This is suitable for stateless MCP servers or serverless deployments.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/finemcp/finemcp/client"
)

// Config configures the HTTP request-response client transport.
type Config struct {
	// BaseURL is the base URL of the MCP server (e.g., "https://api.example.com").
	BaseURL string

	// Endpoint is the path to POST JSON-RPC requests to. Defaults to "/mcp".
	Endpoint string

	// Headers are extra HTTP headers to include on every request.
	Headers map[string]string

	// Timeout is the request timeout. Defaults to 30 seconds.
	Timeout time.Duration

	// HTTPClient is the HTTP client to use. If nil, a default client is created.
	HTTPClient *http.Client

	// Auth provides authentication configuration. If set, auth headers
	// will be applied to every request via AuthConfig.ApplyToRequest().
	Auth *client.AuthConfig
}

// Transport implements client.Transport for simple HTTP request-response MCP servers.
type Transport struct {
	cfg    Config
	client *http.Client

	mu     sync.Mutex
	inbox  chan []byte
	closed bool
}

// New creates a new HTTP request-response client transport.
func New(cfg Config) *Transport {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "/mcp"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}

	return &Transport{
		cfg:    cfg,
		client: client,
		inbox:  make(chan []byte, 64),
	}
}

// Start validates the configuration. Connection is established lazily on first request.
func (t *Transport) Start(ctx context.Context) error {
	if t.cfg.BaseURL == "" {
		return errors.New("http transport: BaseURL is required")
	}
	return nil
}

// Send posts a JSON-RPC message to the server and enqueues the response.
func (t *Transport) Send(ctx context.Context, data []byte) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("http transport: closed")
	}
	t.mu.Unlock()

	url := t.cfg.BaseURL + t.cfg.Endpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("http transport: new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}

	// Apply authentication if configured
	if t.cfg.Auth != nil {
		if err := t.cfg.Auth.ApplyToRequest(ctx, req); err != nil {
			return fmt.Errorf("http transport: auth: %w", err)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("http transport: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle authentication errors
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if t.cfg.Auth != nil {
			authErr := fmt.Errorf("http transport: auth failed: status %d", resp.StatusCode)
			if shouldRetry, retryErr := t.cfg.Auth.HandleAuthError(authErr); shouldRetry {
				// OnAuthError returned nil, indicating credentials were refreshed
				// For now, return the error; retry logic can be added later
				return authErr
			} else if retryErr != nil {
				return retryErr
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http transport: status %d: %s", resp.StatusCode, string(body))
	}

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("http transport: read response: %w", err)
	}

	// Validate it's valid JSON-RPC
	var msg map[string]any
	if err := json.Unmarshal(respData, &msg); err != nil {
		return fmt.Errorf("http transport: invalid JSON response: %w", err)
	}

	// Enqueue the response for Receive()
	t.mu.Lock()
	if !t.closed {
		select {
		case t.inbox <- respData:
		default:
			// Inbox full - this shouldn't happen with proper client usage
			t.mu.Unlock()
			return errors.New("http transport: inbox full")
		}
	}
	t.mu.Unlock()

	return nil
}

// Receive blocks until a response is available from a previous Send.
func (t *Transport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case data := <-t.inbox:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close shuts down the transport and releases resources.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}

	t.closed = true
	close(t.inbox)
	return nil
}
