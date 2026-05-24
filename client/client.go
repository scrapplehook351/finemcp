// Package client provides a transport-agnostic MCP client SDK.
//
// A Client connects to an MCP server, performs the initialize handshake,
// and exposes typed helpers for every MCP method (tools, resources, prompts,
// tasks, completions, ping, logging). It also handles server-initiated
// requests (sampling, elicitation) and notifications (progress, log messages,
// list-changed events) via user-provided callbacks.
//
// Usage:
//
//	tr := stdio.New(stdio.Config{Command: "my-mcp-server"})
//	c, err := client.New(tr, client.Options{
//	    ClientInfo: finemcp.ProcessInfo{Name: "my-app", Version: "1.0"},
//	})
//	if err != nil { ... }
//	defer c.Close()
//
//	init, err := c.Initialize(ctx)
//	tools, err := c.ListTools(ctx, finemcp.ListParams{})
//	result, err := c.CallTool(ctx, finemcp.CallToolParams{Name: "echo", Arguments: ...})
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/finemcp/finemcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Errors returned by Client methods.
var (
	ErrNotInitialized  = errors.New("client: not initialized; call Initialize first")
	ErrAlreadyInit     = errors.New("client: already initialized")
	ErrClosed          = errors.New("client: closed")
	ErrResponseError   = errors.New("client: server returned error")
	ErrProtocolVersion = errors.New("client: no compatible protocol version")
)

// ResponseError wraps a JSON-RPC error received from the server.
type ResponseError struct {
	Code    int
	Message string
	Data    any
}

// Error implements the error interface, returning a formatted message with the
// server error code and message.
func (e *ResponseError) Error() string {
	return fmt.Sprintf("server error %d: %s", e.Code, e.Message)
}

// Unwrap returns [ErrResponseError], allowing errors.Is checks.
func (e *ResponseError) Unwrap() error { return ErrResponseError }

// Options configures a Client.
type Options struct {
	// ClientInfo identifies this client to the server.
	ClientInfo finemcp.ProcessInfo

	// Capabilities declares this client's capabilities to the server.
	Capabilities finemcp.ClientCaps

	// ProtocolVersion is the protocol version to request. When empty,
	// defaults to finemcp.ProtocolVersion (latest).
	ProtocolVersion string

	// SamplingHandler handles server-initiated sampling/createMessage requests.
	// When nil, sampling requests are rejected with "not supported".
	SamplingHandler func(ctx context.Context, params finemcp.CreateMessageParams) (*finemcp.CreateMessageResult, error)

	// ElicitationHandler handles server-initiated elicitation/create requests.
	// When nil, elicitation requests are rejected with "not supported".
	ElicitationHandler func(ctx context.Context, params finemcp.ElicitationParams) (*finemcp.ElicitationResult, error)

	// OnProgress is called when the server sends a notifications/progress notification.
	OnProgress func(finemcp.ProgressParams)

	// OnLogMessage is called when the server sends a notifications/message notification.
	OnLogMessage func(finemcp.LogMessageParams)

	// OnToolsListChanged is called when tools/list_changed is received.
	OnToolsListChanged func()

	// OnResourcesListChanged is called when resources/list_changed is received.
	OnResourcesListChanged func()

	// OnPromptsListChanged is called when prompts/list_changed is received.
	OnPromptsListChanged func()

	// OnRootsListChanged is called when roots/list_changed is received.
	OnRootsListChanged func()

	// OnResourceUpdated is called when resources/updated is received.
	OnResourceUpdated func(uri string)

	// OnNotification is a catch-all handler for any notification not handled
	// by a specific callback above.
	OnNotification func(method string, params json.RawMessage)

	// MaxConcurrentServerRequests limits the number of server-initiated
	// requests (sampling, elicitation) processed concurrently. Zero or
	// negative values default to 10.
	MaxConcurrentServerRequests int

	// Auth provides authentication configuration for all transport requests.
	// Transports that support authentication will apply these credentials.
	Auth *AuthConfig

	// Reconnect configures automatic reconnection behavior.
	// Only applicable to transports that support reconnection (e.g., WebSocket, Streamable HTTP).
	Reconnect *ReconnectConfig

	// Logger enables structured logging of requests and responses for debugging.
	// When nil, no logging is performed.
	// Uses slog.Debug level for all protocol messages.
	Logger *slog.Logger

	// TracerProvider creates tracers for distributed tracing of MCP operations.
	// When nil, no tracing is performed (zero overhead).
	//
	// Spans are created for:
	//   - Each MCP RPC call (Initialize, CallTool, ListTools, etc.)
	//   - Transport Send/Receive operations (nested spans)
	//   - Reconnection attempts (events on parent span)
	//
	// Context propagation follows OpenTelemetry standards, allowing
	// traces to span multiple services.
	//
	// Example using OTLP exporter:
	//
	//   import "go.opentelemetry.io/otel/sdk/trace"
	//   import "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	//
	//   exporter, _ := otlptracegrpc.New(ctx)
	//   provider := trace.NewTracerProvider(trace.WithBatcher(exporter))
	//   opts := client.Options{
	//       TracerProvider: provider,
	//   }
	//
	// Optional. Default: nil (no tracing)
	TracerProvider trace.TracerProvider

	// MeterProvider creates meters for recording MCP client metrics.
	// When nil, no metrics are recorded (zero overhead).
	//
	// Metrics tracked:
	//   - mcp.client.request.duration (histogram, seconds)
	//   - mcp.client.request.count (counter)
	//   - mcp.client.active_connections (gauge)
	//   - mcp.client.reconnect.count (counter)
	//   - mcp.client.auth.failures (counter)
	//   - mcp.client.pagination.pages (counter)
	//
	// Example using Prometheus exporter:
	//
	//   import "go.opentelemetry.io/otel/sdk/metric"
	//   import "go.opentelemetry.io/otel/exporters/prometheus"
	//
	//   exporter, _ := prometheus.New()
	//   provider := metric.NewMeterProvider(metric.WithReader(exporter))
	//   opts := client.Options{
	//       MeterProvider: provider,
	//   }
	//
	// Optional. Default: nil (no metrics)
	MeterProvider metric.MeterProvider

	// RequestCoalescing enables request deduplication for identical
	// concurrent requests. When enabled, multiple goroutines calling
	// the same eligible read-style MCP method with identical parameters
	// will share a single underlying request.
	//
	// Benefits:
	//   - Reduces server load (100 requests → 1 request)
	//   - Lowers latency for duplicate requests
	//   - Saves bandwidth
	//
	// Overhead: < 1ms per request when enabled, zero when disabled.
	//
	// Thread-safe: Safe for concurrent use.
	//
	// Eligible methods include read-style helpers such as ListTools,
	// ListResources, ReadResource, ListPrompts, GetPrompt, and ListRoots.
	// Mutating or side-effectful operations such as CallTool are never
	// coalesced.
	//
	// Example: 100 concurrent ListTools() calls with same params will
	// only send 1 network request, and all 100 callers receive the
	// same result.
	//
	// Optional. Default: false (disabled)
	RequestCoalescing bool

	// CoalescingWindow is the maximum time to wait for additional
	// identical requests before sending the coalesced request.
	//
	// Longer windows allow more coalescing but add latency.
	// Shorter windows reduce latency but may miss coalescing opportunities.
	//
	// Only applies when RequestCoalescing is true.
	//
	// Optional. Default: 10ms
	// Recommended range: 5ms - 50ms
	CoalescingWindow time.Duration

	// PropagateTraceContext controls whether W3C Trace Context
	// (traceparent / tracestate) is injected into every outbound JSON-RPC
	// request via params._meta and extracted from inbound server-initiated
	// requests.
	//
	// When nil (the default), propagation is automatically enabled whenever
	// TracerProvider is set.  Set to a pointer-to-false to explicitly opt out
	// even when a TracerProvider is configured.
	//
	// Example — disable propagation while keeping metrics:
	//
	//   b := false
	//   opts := client.Options{
	//       TracerProvider:       myTracerProvider,
	//       PropagateTraceContext: &b,
	//   }
	//
	// Optional. Default: nil (enabled when TracerProvider != nil)
	PropagateTraceContext *bool

	// MaxRetries is the maximum number of additional attempts made after the
	// first request fails with a retryable error.  A value of 0 disables retry
	// (the default).  Retry uses exponential back-off with full jitter,
	// starting at 100 ms and capped at 30 s.
	//
	// Retryable conditions: ResponseError codes listed in RetryableErrors and
	// ErrClosed (connection lost while waiting for the response).
	//
	// Requests that go through the coalescing layer (see RequestCoalescing)
	// are not individually retried; the coalescer handles its own error
	// propagation.
	//
	// Optional. Default: 0 (disabled)
	MaxRetries int

	// RetryableErrors is the list of JSON-RPC error codes that are considered
	// transient and safe to retry.  When nil or empty the default set is used:
	// [408, 429, 500, 502, 503, 504] (Request Timeout, Too Many Requests,
	// and the common server/gateway error codes).
	//
	// Only meaningful when MaxRetries > 0.
	//
	// Optional. Default: DefaultRetryableErrors
	RetryableErrors []int

	// EnableIdempotency injects a unique X-Idempotency-Key value into
	// params._meta.idempotencyKey for every outbound request.  The same key
	// is reused across all retry attempts so that servers can detect and
	// deduplicate requests that arrive more than once because a response was
	// lost in transit (e.g. a charge_card tool call that timed out).
	//
	// Only meaningful when MaxRetries > 0.  The key is a 32-char lowercase
	// hex string derived from 16 cryptographically random bytes.
	//
	// Optional. Default: false
	EnableIdempotency bool
}

// Client is an MCP protocol client. It manages the JSON-RPC session,
// request correlation, and typed MCP method helpers.
type Client struct {
	transport Transport
	opts      Options

	// Request correlation.
	mu      sync.Mutex
	pending map[string]chan *finemcp.JSONRPCResponse
	idSeq   uint64
	closed  bool

	// Per-call streaming subscriptions: progressToken → internalCh.
	// Protected by streamMu.
	streamMu   sync.RWMutex
	streamSubs map[string]chan<- finemcp.Content
	streamSeq  atomic.Uint64

	// Session state (Bug 2 fix: protected by sessionMu).
	initialized   atomic.Bool
	sessionMu     sync.RWMutex // Protects serverInfo, serverCaps, negotiatedVer, instructions
	serverInfo    finemcp.ProcessInfo
	serverCaps    finemcp.ServerCapabilities
	negotiatedVer string
	instructions  string

	// Read loop lifecycle.
	readCtx     context.Context
	readCancel  context.CancelFunc
	readDone    chan struct{}
	readStarted atomic.Bool

	// Server-initiated request concurrency limiter.
	serverReqSem chan struct{}

	// Observability provides OpenTelemetry instrumentation.
	// Nil when both TracerProvider and MeterProvider are nil.
	observability *observability

	// Request coalescing (nil when disabled).
	coalescer *coalescer

	// Request retry (nil when disabled).
	retrier *retrier
}

// New creates a new Client using the given transport and options.
// The transport is not started until Connect or Initialize is called.
func New(tr Transport, opts Options) (*Client, error) {
	if tr == nil {
		return nil, errors.New("client: transport must not be nil")
	}
	if opts.ClientInfo.Name == "" {
		opts.ClientInfo.Name = "finemcp-client"
	}
	if opts.ClientInfo.Version == "" {
		opts.ClientInfo.Version = "0.0.0"
	}
	if opts.ProtocolVersion == "" {
		opts.ProtocolVersion = finemcp.ProtocolVersion
	}

	// Auto-declare capabilities based on provided handlers.
	if opts.SamplingHandler != nil && opts.Capabilities.Sampling == nil {
		opts.Capabilities.Sampling = &finemcp.SamplingCapability{}
	}
	if opts.ElicitationHandler != nil && opts.Capabilities.Elicitation == nil {
		opts.Capabilities.Elicitation = &finemcp.ElicitationCapability{}
	}

	ctx, cancel := context.WithCancel(context.Background())

	maxConc := opts.MaxConcurrentServerRequests
	if maxConc <= 0 {
		maxConc = 10
	}

	// Initialize observability
	obs, err := newObservability(opts.TracerProvider, opts.MeterProvider)
	if err != nil {
		cancel() // Prevent context leak
		return nil, fmt.Errorf("client: failed to initialize observability: %w", err)
	}

	// Initialize request coalescing (if enabled)
	var coal *coalescer
	if opts.RequestCoalescing {
		coal = newCoalescer(opts.CoalescingWindow)
	}

	// Initialize request retrier (if enabled)
	var ret *retrier
	if opts.MaxRetries > 0 {
		codes := opts.RetryableErrors
		if len(codes) == 0 {
			codes = DefaultRetryableErrors
		}
		ret = newRetrier(opts.MaxRetries, codes)
	}

	c := &Client{
		transport:     tr,
		opts:          opts,
		pending:       make(map[string]chan *finemcp.JSONRPCResponse),
		streamSubs:    make(map[string]chan<- finemcp.Content),
		readCtx:       ctx,
		readCancel:    cancel,
		readDone:      make(chan struct{}),
		serverReqSem:  make(chan struct{}, maxConc),
		observability: obs,
		coalescer:     coal,
		retrier:       ret,
	}

	// Note: Active connection tracking moved to Initialize() to avoid
	// metric leak if Initialize() fails (HIGH-2 security fix)

	return c, nil
}

// shouldPropagateTrace reports whether W3C trace context should be injected
// into outbound requests (and extracted from inbound server-initiated
// requests).
//
// Propagation is enabled when:
//  1. TracerProvider is configured (not nil), AND
//  2. PropagateTraceContext is nil (the default) OR points to true.
func (c *Client) shouldPropagateTrace() bool {
	if c.opts.TracerProvider == nil {
		return false
	}
	// nil pointer → default to enabled when TracerProvider is set.
	if c.opts.PropagateTraceContext == nil {
		return true
	}
	return *c.opts.PropagateTraceContext
}

// Initialize starts the transport, launches the read loop, performs the
// MCP initialize handshake, and sends the notifications/initialized notification.
func (c *Client) Initialize(ctx context.Context) (*finemcp.InitializeResult, error) {
	if c.initialized.Load() {
		return nil, ErrAlreadyInit
	}

	method := "initialize"

	// Start RPC span
	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	// Start the transport.
	if err := c.transport.Start(ctx); err != nil {
		c.observability.setSpanError(ctx, err)
		c.observability.recordRPCMetrics(ctx, method, time.Since(startTime), err)
		return nil, fmt.Errorf("client: transport start: %w", err)
	}

	// Start the reconnect loop (which wraps the read loop).
	c.readStarted.Store(true)
	go c.reconnectLoop()

	// Send initialize request.
	params := finemcp.InitializeParams{
		ProtocolVersion: c.opts.ProtocolVersion,
		Capabilities:    c.opts.Capabilities,
		ClientInfo:      c.opts.ClientInfo,
	}

	var result finemcp.InitializeResult
	if err := c.call(ctx, finemcp.MethodInitialize, params, &result); err != nil {
		_ = c.transport.Close()
		c.observability.setSpanError(ctx, err)
		c.observability.recordRPCMetrics(ctx, method, time.Since(startTime), err)
		return nil, fmt.Errorf("client: initialize: %w", err)
	}

	// Bug 2 fix: Protect session state writes with sessionMu
	c.sessionMu.Lock()
	c.serverInfo = result.ServerInfo
	c.serverCaps = result.Capabilities
	c.negotiatedVer = result.ProtocolVersion
	c.instructions = result.Instructions
	c.sessionMu.Unlock()
	c.initialized.Store(true)

	// Track active connection ONLY after successful initialization
	// (HIGH-2 security fix: prevents counter leak if Initialize() fails)
	if c.observability != nil {
		c.observability.trackActiveConnection(ctx, 1)
	}

	// Send notifications/initialized.
	if err := c.notify(ctx, finemcp.MethodInitialized, nil); err != nil {
		// Non-fatal: server may not require it.
		_ = err
	}

	c.observability.setSpanOK(ctx)
	c.observability.recordRPCMetrics(ctx, method, time.Since(startTime), nil)

	return &result, nil
}

// ServerInfo returns the server's process info from the initialize handshake.
func (c *Client) ServerInfo() finemcp.ProcessInfo {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.serverInfo
}

// ServerCapabilities returns the server's declared capabilities.
func (c *Client) ServerCapabilities() finemcp.ServerCapabilities {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.serverCaps
}

// NegotiatedVersion returns the protocol version agreed during initialization.
func (c *Client) NegotiatedVersion() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.negotiatedVer
}

// Instructions returns any instructions the server provided during initialization.
func (c *Client) Instructions() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.instructions
}

// ── MCP Method Helpers ──────────────────────────────────────────────

// Ping sends a ping request and waits for the server's response.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.requireInit(); err != nil {
		return err
	}

	method := "ping"

	// Start RPC span
	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result struct{}
	err := c.call(ctx, finemcp.MethodPing, nil, &result)

	// Record metrics
	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return err
	}

	c.observability.setSpanOK(ctx)
	return nil
}

// ListTools sends a tools/list request.
func (c *Client) ListTools(ctx context.Context, params finemcp.ListParams) (*finemcp.ListToolsResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "tools.list"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.ListToolsResult
	err := c.call(ctx, finemcp.MethodToolsList, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// CallTool sends a tools/call request.
func (c *Client) CallTool(ctx context.Context, params finemcp.CallToolParams) (*finemcp.CallToolResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "tools.call"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	// Add request parameters as span attributes
	span.SetAttributes(
		attribute.String("mcp.tool.name", params.Name),
	)

	startTime := time.Now()

	var result finemcp.CallToolResult
	err := c.call(ctx, finemcp.MethodToolsCall, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// CallToolStreaming sends a tools/call request and streams content blocks as
// they arrive in progress notifications, rather than waiting for the full
// response. It returns two channels:
//
//   - contentCh: receives [finemcp.Content] blocks in arrival order. The
//     channel is closed once all content has been delivered (including any
//     remaining content in the final [finemcp.CallToolResult]).
//   - errCh: receives a single error value and is then closed. A nil receive
//     indicates success; a non-nil receive indicates the call failed.
//
// Streaming relies on the server embedding content blocks inside
// notifications/progress payloads (the [finemcp.ProgressParams.Content]
// extension field). Servers that do not support streaming simply return all
// content in the final result, so CallToolStreaming always works — callers
// need not know whether the server streams.
//
// The global [Options.OnProgress] callback still fires for every progress
// notification, even those that carry content blocks.
//
// Usage:
//
//	content, errs := c.CallToolStreaming(ctx, finemcp.CallToolParams{Name: "report"})
//	for block := range content {
//	    process(block)
//	}
//	if err := <-errs; err != nil {
//	    log.Fatal(err)
//	}
func (c *Client) CallToolStreaming(ctx context.Context, params finemcp.CallToolParams) (<-chan finemcp.Content, <-chan error) {
	contentCh, _, errCh := c.callToolStreamingInternal(ctx, params)
	return contentCh, errCh
}

// CallToolStreamingWithResult is like [Client.CallToolStreaming] but also
// delivers the final [finemcp.CallToolResult] (including the IsError flag)
// via a dedicated channel. The resultCh receives exactly one value on success
// and is closed without a value on error.
//
// Usage:
//
//	content, result, errs := c.CallToolStreamingWithResult(ctx, params)
//	for block := range content {
//	    process(block)
//	}
//	if err := <-errs; err != nil {
//	    log.Fatal(err)
//	}
//	if r := <-result; r != nil && r.IsError {
//	    log.Println("tool reported an error")
//	}
func (c *Client) CallToolStreamingWithResult(ctx context.Context, params finemcp.CallToolParams) (<-chan finemcp.Content, <-chan *finemcp.CallToolResult, <-chan error) {
	return c.callToolStreamingInternal(ctx, params)
}

// callToolStreamingInternal is the shared implementation behind
// CallToolStreaming and CallToolStreamingWithResult.
func (c *Client) callToolStreamingInternal(ctx context.Context, params finemcp.CallToolParams) (<-chan finemcp.Content, <-chan *finemcp.CallToolResult, <-chan error) {
	contentCh := make(chan finemcp.Content, 16)
	resultCh := make(chan *finemcp.CallToolResult, 1)
	errCh := make(chan error, 1)

	if err := c.requireInit(); err != nil {
		errCh <- err
		close(errCh)
		close(contentCh)
		close(resultCh)
		return contentCh, resultCh, errCh
	}

	// Generate a unique progress token for this streaming call.
	seq := c.streamSeq.Add(1)
	token := fmt.Sprintf("stream-%d", seq)

	// Inject the progress token so the server can correlate notifications.
	if params.Meta == nil {
		params.Meta = make(map[string]any)
	}
	params.Meta["progressToken"] = token

	// internalCh is the bridge between the notification handler (which runs on
	// the read-loop goroutine) and the worker goroutine below. It is buffered
	// to avoid blocking the read loop under normal backpressure.
	internalCh := make(chan finemcp.Content, 256)

	// Register subscription so the notification handler can deliver content.
	c.streamMu.Lock()
	c.streamSubs[token] = internalCh
	c.streamMu.Unlock()

	// Worker goroutine: forwards content from internalCh to the caller's
	// contentCh. It exits when internalCh is closed by the RPC goroutine.
	go func() {
		defer close(contentCh)
		for {
			select {
			case block, ok := <-internalCh:
				if !ok {
					return
				}
				select {
				case contentCh <- block:
				case <-ctx.Done():
					// Drain internalCh so the RPC goroutine is never blocked on
					// sending to a subscriber that is no longer being read.
					for range internalCh {
					}
					return
				}
			case <-ctx.Done():
				for range internalCh {
				}
				return
			}
		}
	}()

	// RPC goroutine: performs the actual tools/call, then flushes any remaining
	// content from the final result into internalCh before closing it.
	go func() {
		defer func() {
			// Deregister subscription before closing internalCh so the
			// notification handler never writes to a closed channel.
			c.streamMu.Lock()
			delete(c.streamSubs, token)
			c.streamMu.Unlock()

			close(internalCh)
			close(errCh)
		}()

		var result finemcp.CallToolResult
		if err := c.call(ctx, finemcp.MethodToolsCall, params, &result); err != nil {
			errCh <- err
			return
		}

		// Deliver remaining content from the final result.
		for _, block := range result.Content {
			select {
			case internalCh <- block:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}

		// Signal successful completion with the final result.
		resultCh <- &result
		close(resultCh)
	}()

	return contentCh, resultCh, errCh
}

// ListResources sends a resources/list request.
func (c *Client) ListResources(ctx context.Context, params finemcp.ListParams) (*finemcp.ListResourcesResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "resources.list"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.ListResourcesResult
	err := c.call(ctx, finemcp.MethodResourcesList, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// ReadResource sends a resources/read request.
func (c *Client) ReadResource(ctx context.Context, params finemcp.ReadResourceParams) (*finemcp.ReadResourceResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "resources.read"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	// Add request parameters as span attributes
	if c.observability != nil && c.observability.tracer != nil {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(
			attribute.String("mcp.resource.uri", params.URI),
		)
	}

	startTime := time.Now()

	var result finemcp.ReadResourceResult
	err := c.call(ctx, finemcp.MethodResourcesRead, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// ListResourceTemplates sends a resources/templates/list request.
func (c *Client) ListResourceTemplates(ctx context.Context, params finemcp.ListParams) (*finemcp.ListResourceTemplatesResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "resources.templates.list"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.ListResourceTemplatesResult
	err := c.call(ctx, finemcp.MethodResourcesTemplatesList, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// SubscribeResource sends a resources/subscribe request.
func (c *Client) SubscribeResource(ctx context.Context, params finemcp.SubscribeParams) error {
	if err := c.requireInit(); err != nil {
		return err
	}

	method := "resources.subscribe"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result struct{}
	err := c.call(ctx, finemcp.MethodResourcesSubscribe, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return err
	}

	c.observability.setSpanOK(ctx)
	return nil
}

// UnsubscribeResource sends a resources/unsubscribe request.
func (c *Client) UnsubscribeResource(ctx context.Context, params finemcp.SubscribeParams) error {
	if err := c.requireInit(); err != nil {
		return err
	}

	method := "resources.unsubscribe"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result struct{}
	err := c.call(ctx, finemcp.MethodResourcesUnsubscribe, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return err
	}

	c.observability.setSpanOK(ctx)
	return nil
}

// ListPrompts sends a prompts/list request.
func (c *Client) ListPrompts(ctx context.Context, params finemcp.ListParams) (*finemcp.ListPromptsResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "prompts.list"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.ListPromptsResult
	err := c.call(ctx, finemcp.MethodPromptsList, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// GetPrompt sends a prompts/get request.
func (c *Client) GetPrompt(ctx context.Context, params finemcp.GetPromptParams) (*finemcp.GetPromptResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "prompts.get"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	// Add request parameters as span attributes
	if c.observability != nil && c.observability.tracer != nil {
		span := trace.SpanFromContext(ctx)
		span.SetAttributes(
			attribute.String("mcp.prompt.name", params.Name),
		)
	}

	startTime := time.Now()

	var result finemcp.GetPromptResult
	err := c.call(ctx, finemcp.MethodPromptsGet, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// ListRoots sends a roots/list request.
func (c *Client) ListRoots(ctx context.Context, params finemcp.ListParams) (*finemcp.ListRootsResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "roots.list"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.ListRootsResult
	err := c.call(ctx, finemcp.MethodRootsList, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// Complete sends a completion/complete request.
func (c *Client) Complete(ctx context.Context, params finemcp.CompleteParams) (*finemcp.CompleteResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "completion.complete"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.CompleteResult
	err := c.call(ctx, finemcp.MethodCompletionComplete, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// SetLogLevel sends a logging/setLevel request.
func (c *Client) SetLogLevel(ctx context.Context, level finemcp.LogLevel) error {
	if err := c.requireInit(); err != nil {
		return err
	}

	method := "logging.setLevel"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result struct{}
	err := c.call(ctx, finemcp.MethodLoggingSetLevel, finemcp.SetLevelParams{Level: level}, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return err
	}

	c.observability.setSpanOK(ctx)
	return nil
}

// ── Task Helpers ────────────────────────────────────────────────────

// GetTask sends a tasks/get request.
func (c *Client) GetTask(ctx context.Context, taskID string) (*finemcp.Task, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "tasks.get"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.Task
	err := c.call(ctx, finemcp.MethodTasksGet, finemcp.TaskIdParams{TaskID: taskID}, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// GetTaskResult sends a tasks/result request.
func (c *Client) GetTaskResult(ctx context.Context, taskID string) (*finemcp.CallToolResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "tasks.result"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.CallToolResult
	err := c.call(ctx, finemcp.MethodTasksResult, finemcp.TaskIdParams{TaskID: taskID}, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// CancelTask sends a tasks/cancel request.
func (c *Client) CancelTask(ctx context.Context, taskID string) (*finemcp.Task, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "tasks.cancel"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.Task
	err := c.call(ctx, finemcp.MethodTasksCancel, finemcp.TaskIdParams{TaskID: taskID}, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// ListTasks sends a tasks/list request.
func (c *Client) ListTasks(ctx context.Context, params finemcp.ListParams) (*finemcp.ListTasksResult, error) {
	if err := c.requireInit(); err != nil {
		return nil, err
	}

	method := "tasks.list"

	ctx, span := c.observability.startRPCSpan(ctx, method)
	defer span.End()

	startTime := time.Now()

	var result finemcp.ListTasksResult
	err := c.call(ctx, finemcp.MethodTasksList, params, &result)

	duration := time.Since(startTime)
	c.observability.recordRPCMetrics(ctx, method, duration, err)

	if err != nil {
		c.observability.setSpanError(ctx, err)
		return nil, err
	}

	c.observability.setSpanOK(ctx)
	return &result, nil
}

// ── Low-Level API ───────────────────────────────────────────────────

// Call sends a JSON-RPC request with the given method and params, and
// decodes the result into dest. This is the low-level escape hatch for
// methods not covered by typed helpers.
func (c *Client) Call(ctx context.Context, method string, params any, dest any) error {
	return c.call(ctx, method, params, dest)
}

// Notify sends a JSON-RPC notification (no response expected).
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	return c.notify(ctx, method, params)
}

// Close shuts down the client: cancels the read loop, closes pending
// requests, and closes the transport.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	// Close all pending request channels
	for id, ch := range c.pending {
		select {
		case ch <- nil:
		default:
			// Channel full, close it instead
			close(ch)
		}
		delete(c.pending, id)
	}
	c.mu.Unlock()

	c.readCancel()

	// Track connection closure
	if c.observability != nil {
		c.observability.trackActiveConnection(context.Background(), -1)
	}

	// Close the transport before waiting for readDone.  For transports where
	// Receive blocks without checking context (e.g., stdio Scanner), we need
	// to close the underlying connection to unblock the read loop.  Once the
	// transport is closed, Receive will return an error and readLoop will exit.
	closeErr := c.transport.Close()

	if c.readStarted.Load() {
		<-c.readDone
	}
	return closeErr
}

// ── Internal ────────────────────────────────────────────────────────

func (c *Client) requireInit() error {
	if !c.initialized.Load() {
		return ErrNotInitialized
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, params any, dest any) error {
	// Coalescing layer (if enabled, eligible methods only).
	if c.coalescer != nil && canCoalesceMethod(method) {
		return c.callCoalesced(ctx, method, params, dest)
	}

	// Retry layer (if enabled).
	if c.retrier != nil {
		return c.callWithRetry(ctx, method, params, dest)
	}

	return c.callDirect(ctx, method, params, dest)
}

func canCoalesceMethod(method string) bool {
	switch method {
	case finemcp.MethodPing,
		finemcp.MethodToolsList,
		finemcp.MethodResourcesList,
		finemcp.MethodResourcesRead,
		finemcp.MethodResourcesTemplatesList,
		finemcp.MethodPromptsList,
		finemcp.MethodPromptsGet,
		finemcp.MethodRootsList,
		finemcp.MethodTasksGet,
		finemcp.MethodTasksResult,
		finemcp.MethodTasksList:
		return true
	default:
		return false
	}
}

// callCoalesced executes a request with coalescing enabled.
func (c *Client) callCoalesced(ctx context.Context, method string, params any, dest any) error {
	// Create request key
	key, err := newRequestKey(method, params)
	if err != nil {
		return fmt.Errorf("client: create request key: %w", err)
	}

	// Execute with coalescing
	respData, err := c.coalescer.Do(ctx, key, func(parentCtx context.Context) ([]byte, error) {
		return c.executeRequest(parentCtx, method, params)
	})

	if err != nil {
		return err
	}

	// Unmarshal result into dest
	if dest != nil {
		if err := json.Unmarshal(respData, dest); err != nil {
			return fmt.Errorf("client: decode result: %w", err)
		}
	}

	return nil
}

// callDirect executes a request without coalescing (original behavior).
func (c *Client) callDirect(ctx context.Context, method string, params any, dest any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.idSeq++
	id := fmt.Sprintf("c-%d", c.idSeq)
	ch := make(chan *finemcp.JSONRPCResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	// Inject W3C trace context into params._meta before marshaling.
	// This is the single choke-point for all outbound JSON-RPC requests.
	effectiveParams := params
	if c.shouldPropagateTrace() {
		enriched, merr := marshalWithMeta(ctx, params, true)
		if merr != nil {
			return fmt.Errorf("client: inject trace context: %w", merr)
		}
		effectiveParams = enriched
	}

	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  effectiveParams,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("client: marshal request: %w", err)
	}

	// Log outgoing request
	startTime := time.Now()
	if c.opts.Logger != nil {
		c.opts.Logger.DebugContext(ctx, "mcp request",
			slog.String("method", method),
			slog.String("id", id),
			slog.Any("params", params),
		)
	}

	// Start transport send span
	sendCtx, sendSpan := c.observability.startTransportSpan(ctx, "send")
	if c.observability != nil && c.observability.tracer != nil {
		sendSpan.SetAttributes(
			attribute.Int("mcp.request.size", len(data)),
			attribute.String("mcp.request.id", id),
		)
	}

	err = c.transport.Send(sendCtx, data)

	if err != nil {
		c.observability.setSpanError(sendCtx, err)
		sendSpan.End()

		// Log send failure
		if c.opts.Logger != nil {
			c.opts.Logger.DebugContext(ctx, "mcp request failed",
				slog.String("method", method),
				slog.String("id", id),
				slog.String("error", err.Error()),
				slog.Duration("elapsed", time.Since(startTime)),
			)
		}
		return fmt.Errorf("client: send: %w", err)
	}

	c.observability.setSpanOK(sendCtx)
	sendSpan.End()

	// Bug 3 fix: Handle nil response from failPendingRequests (blocking send)
	select {
	case resp := <-ch:
		elapsed := time.Since(startTime)

		if resp == nil {
			// Connection closed during request
			if c.opts.Logger != nil {
				c.opts.Logger.DebugContext(ctx, "mcp response closed",
					slog.String("method", method),
					slog.String("id", id),
					slog.Duration("elapsed", elapsed),
				)
			}
			return ErrClosed
		}
		if resp.Error != nil {
			if c.opts.Logger != nil {
				c.opts.Logger.DebugContext(ctx, "mcp response error",
					slog.String("method", method),
					slog.String("id", id),
					slog.Int("code", resp.Error.Code),
					slog.String("message", resp.Error.Message),
					slog.Duration("elapsed", elapsed),
				)
			}
			return &ResponseError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		if c.opts.Logger != nil {
			c.opts.Logger.DebugContext(ctx, "mcp response success",
				slog.String("method", method),
				slog.String("id", id),
				slog.Any("result", resp.Result),
				slog.Duration("elapsed", elapsed),
			)
		}
		if dest != nil {
			raw, err := json.Marshal(resp.Result)
			if err != nil {
				return fmt.Errorf("client: marshal result: %w", err)
			}
			if err := json.Unmarshal(raw, dest); err != nil {
				return fmt.Errorf("client: decode result: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		elapsed := time.Since(startTime)
		if c.opts.Logger != nil {
			c.opts.Logger.DebugContext(ctx, "mcp request timeout",
				slog.String("method", method),
				slog.String("id", id),
				slog.String("error", ctx.Err().Error()),
				slog.Duration("elapsed", elapsed),
			)
		}
		return ctx.Err()
	}
}

// executeRequest performs the actual network request and returns raw JSON response.
// Used by coalescer to execute the shared request.
func (c *Client) executeRequest(ctx context.Context, method string, params any) ([]byte, error) {
	// 1. Allocate request ID
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.idSeq++
	id := fmt.Sprintf("c-%d", c.idSeq)
	ch := make(chan *finemcp.JSONRPCResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	// 2. Build JSON-RPC request
	effectiveParams := params
	if c.shouldPropagateTrace() {
		enriched, merr := marshalWithMeta(ctx, params, true)
		if merr != nil {
			return nil, fmt.Errorf("client: inject trace context: %w", merr)
		}
		effectiveParams = enriched
	}

	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  effectiveParams,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("client: marshal request: %w", err)
	}

	// Log outgoing request
	startTime := time.Now()
	if c.opts.Logger != nil {
		c.opts.Logger.DebugContext(ctx, "mcp request",
			slog.String("method", method),
			slog.String("id", id),
			slog.Any("params", params),
		)
	}

	// Start transport send span
	sendCtx, sendSpan := c.observability.startTransportSpan(ctx, "send")
	if c.observability != nil && c.observability.tracer != nil {
		sendSpan.SetAttributes(
			attribute.Int("mcp.request.size", len(data)),
			attribute.String("mcp.request.id", id),
		)
	}

	// 3. Send request
	err = c.transport.Send(sendCtx, data)

	if err != nil {
		c.observability.setSpanError(sendCtx, err)
		sendSpan.End()

		// Log send failure
		if c.opts.Logger != nil {
			c.opts.Logger.DebugContext(ctx, "mcp request failed",
				slog.String("method", method),
				slog.String("id", id),
				slog.String("error", err.Error()),
				slog.Duration("elapsed", time.Since(startTime)),
			)
		}
		return nil, fmt.Errorf("client: send: %w", err)
	}

	c.observability.setSpanOK(sendCtx)
	sendSpan.End()

	// 4. Wait for response
	select {
	case resp := <-ch:
		elapsed := time.Since(startTime)

		if resp == nil {
			// Connection closed during request
			if c.opts.Logger != nil {
				c.opts.Logger.DebugContext(ctx, "mcp response closed",
					slog.String("method", method),
					slog.String("id", id),
					slog.Duration("elapsed", elapsed),
				)
			}
			return nil, ErrClosed
		}

		if resp.Error != nil {
			if c.opts.Logger != nil {
				c.opts.Logger.DebugContext(ctx, "mcp response error",
					slog.String("method", method),
					slog.String("id", id),
					slog.Int("code", resp.Error.Code),
					slog.String("message", resp.Error.Message),
					slog.Duration("elapsed", elapsed),
				)
			}
			return nil, &ResponseError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}

		if c.opts.Logger != nil {
			c.opts.Logger.DebugContext(ctx, "mcp response success",
				slog.String("method", method),
				slog.String("id", id),
				slog.Any("result", resp.Result),
				slog.Duration("elapsed", elapsed),
			)
		}

		// 5. Marshal result to JSON for coalescing cache
		raw, err := json.Marshal(resp.Result)
		if err != nil {
			return nil, fmt.Errorf("client: marshal result: %w", err)
		}

		return raw, nil

	case <-ctx.Done():
		elapsed := time.Since(startTime)
		if c.opts.Logger != nil {
			c.opts.Logger.DebugContext(ctx, "mcp request timeout",
				slog.String("method", method),
				slog.String("id", id),
				slog.String("error", ctx.Err().Error()),
				slog.Duration("elapsed", elapsed),
			)
		}
		return nil, ctx.Err()
	}
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	msg := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("client: marshal notification: %w", err)
	}
	return c.transport.Send(ctx, data)
}

// ── Reconnection System ─────────────────────────────────────────────

// sessionState holds the state that must be preserved across reconnections.
type sessionState struct {
	serverInfo    finemcp.ProcessInfo
	serverCaps    finemcp.ServerCapabilities
	negotiatedVer string
	instructions  string
}

// captureState takes a snapshot of the current session state.
func (c *Client) captureState() *sessionState {
	// Bug 2 fix: Protect session state reads with sessionMu
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return &sessionState{
		serverInfo:    c.serverInfo,
		serverCaps:    c.serverCaps,
		negotiatedVer: c.negotiatedVer,
		instructions:  c.instructions,
	}
}

// restoreState restores the session state from a snapshot.
func (c *Client) restoreState(state *sessionState) {
	if state == nil {
		return
	}
	// Bug 2 fix: Protect session state writes with sessionMu
	c.sessionMu.Lock()
	c.serverInfo = state.serverInfo
	c.serverCaps = state.serverCaps
	c.negotiatedVer = state.negotiatedVer
	c.instructions = state.instructions
	c.sessionMu.Unlock()
}

// failPendingRequests fails all pending requests by sending nil responses.
// Bug 3 fix: Use blocking send instead of select/default to ensure all waiters are notified.
func (c *Client) failPendingRequests(err error) {
	c.mu.Lock()
	pendingChans := make([]chan *finemcp.JSONRPCResponse, 0, len(c.pending))
	for id, ch := range c.pending {
		pendingChans = append(pendingChans, ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()

	// Send nil to all channels outside the lock to avoid holding the lock during blocking sends
	for _, ch := range pendingChans {
		ch <- nil
	}
}

// getBackoffDuration calculates the backoff duration for a reconnection attempt.
func (c *Client) getBackoffDuration(attempt int) time.Duration {
	if c.opts.Reconnect == nil || c.opts.Reconnect.Strategy == nil {
		// Default: exponential backoff 1s to 60s
		return ExponentialBackoff(1*time.Second, 60*time.Second).NextBackoff(attempt)
	}
	return c.opts.Reconnect.Strategy.NextBackoff(attempt)
}

// reconnectTransport closes the current transport and re-establishes connection.
func (c *Client) reconnectTransport(ctx context.Context) error {
	// Close the old transport
	_ = c.transport.Close()

	// Re-establish connection
	if err := c.transport.Start(ctx); err != nil {
		return fmt.Errorf("transport start failed: %w", err)
	}

	return nil
}

// reinitializeSession performs the full MCP initialize handshake after reconnection.
// This function must handle receiving the response directly since readLoopCore is not running.
func (c *Client) reinitializeSession(ctx context.Context, savedState *sessionState) error {
	// Send initialize request
	params := finemcp.InitializeParams{
		ProtocolVersion: c.opts.ProtocolVersion,
		Capabilities:    c.opts.Capabilities,
		ClientInfo:      c.opts.ClientInfo,
	}

	// Build request
	c.mu.Lock()
	c.idSeq++
	id := fmt.Sprintf("c-%d", c.idSeq)
	c.mu.Unlock()

	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  finemcp.MethodInitialize,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal initialize request: %w", err)
	}

	if err := c.transport.Send(ctx, data); err != nil {
		return fmt.Errorf("send initialize request: %w", err)
	}

	// Receive response directly (readLoopCore is not running during reconnection)
	respData, err := c.transport.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receive initialize response: %w", err)
	}

	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal initialize response: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("initialize error: code=%d, message=%s", resp.Error.Code, resp.Error.Message)
	}

	var result finemcp.InitializeResult
	if resp.Result != nil {
		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
		if err := json.Unmarshal(resultBytes, &result); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}

	// Bug 2 fix: Protect session state writes with sessionMu
	c.sessionMu.Lock()
	c.serverInfo = result.ServerInfo
	c.serverCaps = result.Capabilities
	c.negotiatedVer = result.ProtocolVersion
	c.instructions = result.Instructions
	c.sessionMu.Unlock()
	c.initialized.Store(true)

	// Send notifications/initialized
	notifyReq := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  finemcp.MethodInitialized,
		Params:  nil,
	}
	notifyData, _ := json.Marshal(notifyReq)
	_ = c.transport.Send(ctx, notifyData) // Non-fatal

	return nil
}

// attemptReconnection tries to reconnect with exponential backoff.
// Returns nil on success, or error if max retries exhausted.
func (c *Client) attemptReconnection(startAttempt int, savedState *sessionState) error {
	attempt := startAttempt
	maxRetries := 0
	if c.opts.Reconnect != nil {
		maxRetries = c.opts.Reconnect.MaxRetries
	}

	for {
		// Check if we've exhausted retries (0 means infinite)
		if maxRetries > 0 && attempt >= maxRetries {
			return fmt.Errorf("max reconnection attempts (%d) exhausted", maxRetries)
		}

		// Calculate backoff and wait
		backoff := c.getBackoffDuration(attempt)
		if backoff > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-c.readCtx.Done():
				timer.Stop()
				return c.readCtx.Err()
			}
		}

		// Notify user of reconnection attempt
		if c.opts.Reconnect != nil && c.opts.Reconnect.OnReconnecting != nil {
			c.opts.Reconnect.OnReconnecting(attempt, fmt.Errorf("reconnection attempt %d", attempt))
		}

		// Record reconnection attempt event
		c.observability.addSpanEvent(c.readCtx, "reconnect.attempt",
			attribute.Int("attempt", attempt),
		)

		// Try to reconnect transport
		ctx, cancel := context.WithTimeout(c.readCtx, 30*time.Second)
		err := c.reconnectTransport(ctx)
		cancel()
		if err != nil {
			// Record failed reconnection
			c.observability.recordReconnect(context.Background(), false)
			c.observability.addSpanEvent(c.readCtx, "reconnect.transport.failure",
				attribute.Int("attempt", attempt),
				attribute.String("error", err.Error()),
			)
			attempt++
			continue
		}

		// Try to reinitialize session
		ctx, cancel = context.WithTimeout(c.readCtx, 30*time.Second)
		err = c.reinitializeSession(ctx, savedState)
		cancel()
		if err != nil {
			// Record failed reconnection
			c.observability.recordReconnect(context.Background(), false)
			c.observability.addSpanEvent(c.readCtx, "reconnect.reinitialize.failure",
				attribute.Int("attempt", attempt),
				attribute.String("error", err.Error()),
			)
			attempt++
			continue
		}

		// Success! Record successful reconnection
		c.observability.recordReconnect(context.Background(), true)
		c.observability.addSpanEvent(c.readCtx, "reconnect.success",
			attribute.Int("attempt", attempt),
		)

		// Notify user
		if c.opts.Reconnect != nil && c.opts.Reconnect.OnReconnected != nil {
			c.opts.Reconnect.OnReconnected()
		}

		return nil
	}
}

// reconnectLoop is the outer wrapper that manages reconnection logic.
func (c *Client) reconnectLoop() {
	defer close(c.readDone)

	// Check if reconnection is enabled
	reconnectEnabled := c.opts.Reconnect != nil && c.opts.Reconnect.Enabled

	attempt := 0
	for {
		// Run the core read loop
		err := c.readLoopCore()

		// Check if we should reconnect
		if err == nil {
			// Distinguish between a deliberate shutdown (Close was called / context
			// cancelled) and an unexpected EOF (server process died).  When the
			// context is still live, EOF means the remote end closed the connection
			// unexpectedly; treat it as a transport error so pending requests get
			// failed and reconnection is attempted when enabled.
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if c.readCtx.Err() != nil || closed {
				// True clean shutdown: Close was called or the read context
				// was cancelled externally.  No work needed.
				return
			}
			// Unexpected EOF — server died without us asking it to.
			err = io.EOF
		}

		if !reconnectEnabled {
			// Reconnection disabled; still fail any pending requests so
			// callers don't block indefinitely.
			c.failPendingRequests(err)
			return
		}

		if c.readCtx.Err() != nil {
			// Context cancelled, don't reconnect
			return
		}

		// Capture state before reconnection
		savedState := c.captureState()

		// Clear initialized flag
		c.initialized.Store(false)

		// Fail all pending requests
		c.failPendingRequests(err)

		// Attempt reconnection
		if reconErr := c.attemptReconnection(attempt, savedState); reconErr != nil {
			// All reconnection attempts failed
			if c.opts.Reconnect != nil && c.opts.Reconnect.OnFailed != nil {
				c.opts.Reconnect.OnFailed(reconErr)
			}
			return
		}

		// Reset attempt counter on successful reconnection
		attempt = 0
	}
}

// readLoopCore is the core read loop logic, extracted for reconnection support.
// It returns an error if the transport fails unexpectedly, or nil for clean shutdown.
func (c *Client) readLoopCore() error {
	for {
		data, err := c.transport.Receive(c.readCtx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || c.readCtx.Err() != nil {
				// Clean shutdown
				return nil
			}
			// Unexpected error; return for potential reconnection
			return err
		}

		c.handleMessage(data)
	}
}

func (c *Client) handleMessage(data []byte) {
	// Try to detect if it's a response (has id + result/error, no method).
	if finemcp.IsResponse(data) {
		c.deliverResponse(data)
		return
	}

	// Parse as a generic message to detect method.
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	if msg.Method == "" {
		// Possibly a response we failed to categorize; try delivery anyway.
		c.deliverResponse(data)
		return
	}

	// If it has an id, it's a server-initiated request.
	if msg.ID != nil {
		// Rate-limit concurrent server-initiated requests.
		select {
		case c.serverReqSem <- struct{}{}:
			go func() {
				defer func() { <-c.serverReqSem }()
				c.handleServerRequest(msg.ID, msg.Method, msg.Params)
			}()
		default:
			c.sendServerError(msg.ID, finemcp.ErrCodeInternalError, "too many concurrent requests")
		}
		return
	}

	// Otherwise it's a notification.
	c.handleNotification(msg.Method, msg.Params)
}

func (c *Client) deliverResponse(data []byte) {
	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return
	}

	key := fmt.Sprintf("%v", resp.ID)

	c.mu.Lock()
	ch, ok := c.pending[key]
	c.mu.Unlock()

	if !ok {
		return
	}

	select {
	case ch <- &resp:
	default:
	}
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	switch method {
	case finemcp.MethodProgress:
		var p finemcp.ProgressParams
		if json.Unmarshal(params, &p) == nil {
			if c.opts.OnProgress != nil {
				c.opts.OnProgress(p)
			}
			// Dispatch any embedded content blocks to a per-call streaming subscriber.
			if len(p.Content) > 0 {
				token := fmt.Sprintf("%v", p.ProgressToken)
				c.streamMu.RLock()
				subCh, ok := c.streamSubs[token]
				c.streamMu.RUnlock()
				if ok {
					for _, raw := range p.Content {
						block, err := finemcp.DecodeContent(raw)
						if err == nil {
							select {
							case subCh <- block:
							default:
								// Internal channel full; drop block to avoid
								// blocking the read loop.
							}
						}
					}
				}
			}
		}
	case finemcp.MethodLoggingMessage:
		if c.opts.OnLogMessage != nil {
			var p finemcp.LogMessageParams
			if json.Unmarshal(params, &p) == nil {
				c.opts.OnLogMessage(p)
			}
		}
	case finemcp.MethodToolsListChanged:
		if c.opts.OnToolsListChanged != nil {
			c.opts.OnToolsListChanged()
		}
	case finemcp.MethodResourcesListChanged:
		if c.opts.OnResourcesListChanged != nil {
			c.opts.OnResourcesListChanged()
		}
	case finemcp.MethodPromptsListChanged:
		if c.opts.OnPromptsListChanged != nil {
			c.opts.OnPromptsListChanged()
		}
	case finemcp.MethodRootsListChanged:
		if c.opts.OnRootsListChanged != nil {
			c.opts.OnRootsListChanged()
		}
	case finemcp.MethodResourcesUpdated:
		if c.opts.OnResourceUpdated != nil {
			var p struct {
				URI string `json:"uri"`
			}
			if json.Unmarshal(params, &p) == nil {
				c.opts.OnResourceUpdated(p.URI)
			}
		}
	default:
		if c.opts.OnNotification != nil {
			c.opts.OnNotification(method, params)
		}
	}
}

func (c *Client) handleServerRequest(rawID json.RawMessage, method string, params json.RawMessage) {
	var id any
	_ = json.Unmarshal(rawID, &id)

	startTime := time.Now()

	// Build the request-scoped context.  When trace propagation is enabled,
	// extract W3C trace context from params._meta so that handlers can create
	// child spans of the server's trace.
	reqCtx := c.readCtx
	if c.shouldPropagateTrace() {
		var metaWrapper struct {
			Meta map[string]any `json:"_meta"`
		}
		if err := json.Unmarshal(params, &metaWrapper); err == nil && metaWrapper.Meta != nil {
			reqCtx = extractTraceContext(reqCtx, metaWrapper.Meta)
		}
	}

	// Log incoming server request
	if c.opts.Logger != nil {
		c.opts.Logger.DebugContext(reqCtx, "mcp server request",
			slog.String("method", method),
			slog.Any("id", id),
			slog.Any("params", params),
		)
	}

	var result any
	var respErr *finemcp.JSONRPCError

	switch method {
	case finemcp.MethodSamplingCreateMessage:
		if c.opts.SamplingHandler != nil {
			var p finemcp.CreateMessageParams
			if err := json.Unmarshal(params, &p); err != nil {
				respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeInvalidParams, Message: "invalid sampling params"}
			} else {
				res, err := c.opts.SamplingHandler(reqCtx, p)
				if err != nil {
					respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeInternalError, Message: "sampling handler failed"}
				} else {
					result = res
				}
			}
		} else {
			respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeMethodNotFound, Message: "sampling not supported"}
		}

	case finemcp.MethodElicitationCreate:
		if c.opts.ElicitationHandler != nil {
			var p finemcp.ElicitationParams
			if err := json.Unmarshal(params, &p); err != nil {
				respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeInvalidParams, Message: "invalid elicitation params"}
			} else {
				res, err := c.opts.ElicitationHandler(reqCtx, p)
				if err != nil {
					respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeInternalError, Message: "elicitation handler failed"}
				} else {
					result = res
				}
			}
		} else {
			respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeMethodNotFound, Message: "elicitation not supported"}
		}

	case finemcp.MethodPing:
		result = struct{}{}

	default:
		respErr = &finemcp.JSONRPCError{Code: finemcp.ErrCodeMethodNotFound, Message: "unknown method"}
	}

	elapsed := time.Since(startTime)

	// Log server response
	if c.opts.Logger != nil {
		if respErr != nil {
			c.opts.Logger.DebugContext(reqCtx, "mcp server response error",
				slog.String("method", method),
				slog.Any("id", id),
				slog.Int("code", respErr.Code),
				slog.String("message", respErr.Message),
				slog.Duration("elapsed", elapsed),
			)
		} else {
			c.opts.Logger.DebugContext(reqCtx, "mcp server response success",
				slog.String("method", method),
				slog.Any("id", id),
				slog.Any("result", result),
				slog.Duration("elapsed", elapsed),
			)
		}
	}

	c.sendServerResponse(rawID, result, respErr)
}

// sendServerResponse marshals and sends a JSON-RPC response to a
// server-initiated request. If marshaling the result fails, it sends
// an error response instead. If that also fails (e.g., un-marshalable ID),
// a minimal static error response is sent.
func (c *Client) sendServerResponse(rawID json.RawMessage, result any, respErr *finemcp.JSONRPCError) {
	var id any
	_ = json.Unmarshal(rawID, &id)

	resp := &finemcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
	}
	if respErr != nil {
		resp.Error = respErr
	} else {
		resp.Result = result
	}

	data, err := json.Marshal(resp)
	if err != nil {
		// Result was un-marshalable. Try sending an error response.
		resp.Result = nil
		resp.Error = &finemcp.JSONRPCError{Code: finemcp.ErrCodeInternalError, Message: "internal error"}
		data, err = json.Marshal(resp)
		if err != nil {
			// ID itself is un-marshalable. Use a fully static fallback with null ID
			// to avoid JSON injection from malformed rawID bytes.
			data = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
		}
	}
	_ = c.transport.Send(c.readCtx, data)
}

// sendServerError is a helper to send a JSON-RPC error response for a
// server-initiated request identified by rawID.
func (c *Client) sendServerError(rawID json.RawMessage, code int, message string) {
	c.sendServerResponse(rawID, nil, &finemcp.JSONRPCError{Code: code, Message: message})
}
