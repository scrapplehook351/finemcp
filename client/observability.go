// Package client provides OpenTelemetry observability integration.
package client

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	// instrumentationName is the name used for the tracer and meter.
	// Follows OpenTelemetry convention: library.name
	instrumentationName = "github.com/finemcp/finemcp/client"

	// instrumentationVersion should match the client SDK version.
	instrumentationVersion = "0.1.0"
)

// panicError captures a panic from user-provided observability providers.
// panicError wraps a panic value recovered during an observability operation.
type panicError struct {
	method string
	panic  interface{}
}

// Error implements the error interface for panicError.
func (e *panicError) Error() string {
	return fmt.Sprintf("observability: panic in %s: %v", e.method, e.panic)
}

// observability encapsulates all OpenTelemetry instrumentation for the client.
// When providers are nil, all methods become no-ops with zero overhead.
type observability struct {
	tracer      trace.Tracer
	meter       metric.Meter
	instruments *observabilityInstruments
}

// observabilityInstruments holds all metric instruments.
// Instruments are created once during client initialization and reused.
type observabilityInstruments struct {
	// requestDuration measures the total duration of MCP RPC calls.
	// Unit: seconds (float64)
	// Attributes: method, status, error_code (optional)
	requestDuration metric.Float64Histogram

	// requestCount counts the number of MCP RPC calls.
	// Unit: requests (int64)
	// Attributes: method, status, error_code (optional)
	requestCount metric.Int64Counter

	// activeConnections tracks the number of active MCP connections.
	// Unit: connections (int64)
	// Attributes: none
	activeConnections metric.Int64UpDownCounter

	// reconnectCount counts the number of reconnection attempts.
	// Unit: attempts (int64)
	// Attributes: success (true/false)
	reconnectCount metric.Int64Counter

	// authFailureCount counts authentication/authorization failures.
	// Unit: failures (int64)
	// Attributes: auth_type (oauth2, api_key, custom)
	authFailureCount metric.Int64Counter

	// paginationPages counts pages fetched during paginated operations.
	// Unit: pages (int64)
	// Attributes: method (e.g., tools.list, resources.list)
	paginationPages metric.Int64Counter
}

// newObservability creates observability instrumentation from providers.
// Returns nil if both providers are nil (no observability).
// Recovers from panics in user-provided providers to prevent client crashes.
func newObservability(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (obs *observability, err error) {
	// Panic recovery: user-provided providers must not crash the client
	defer func() {
		if r := recover(); r != nil {
			obs = nil
			err = &panicError{
				method: "newObservability",
				panic:  r,
			}
		}
	}()

	// Fast path: both disabled
	if tracerProvider == nil && meterProvider == nil {
		return nil, nil
	}

	o := &observability{}

	// Initialize tracer (can panic from user provider)
	if tracerProvider != nil {
		o.tracer = tracerProvider.Tracer(
			instrumentationName,
			trace.WithInstrumentationVersion(instrumentationVersion),
		)
	} else {
		// Use no-op tracer if only metrics are enabled
		o.tracer = otel.Tracer(instrumentationName)
	}

	// Initialize meter and instruments (can panic from user provider)
	if meterProvider != nil {
		o.meter = meterProvider.Meter(
			instrumentationName,
			metric.WithInstrumentationVersion(instrumentationVersion),
		)

		var createErr error
		o.instruments, createErr = createInstruments(o.meter)
		if createErr != nil {
			return nil, createErr
		}
	}

	return o, nil
}

// createInstruments creates all metric instruments.
func createInstruments(meter metric.Meter) (*observabilityInstruments, error) {
	inst := &observabilityInstruments{}
	var err error

	// Request duration histogram
	inst.requestDuration, err = meter.Float64Histogram(
		"mcp.client.request.duration",
		metric.WithDescription("Duration of MCP RPC requests"),
		metric.WithUnit("s"), // seconds
		metric.WithExplicitBucketBoundaries(
			// Buckets optimized for RPC latencies: 1ms to 60s
			0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0,
		),
	)
	if err != nil {
		return nil, err
	}

	// Request count
	inst.requestCount, err = meter.Int64Counter(
		"mcp.client.request.count",
		metric.WithDescription("Total number of MCP RPC requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	// Active connections gauge
	inst.activeConnections, err = meter.Int64UpDownCounter(
		"mcp.client.active_connections",
		metric.WithDescription("Number of active MCP client connections"),
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, err
	}

	// Reconnect count
	inst.reconnectCount, err = meter.Int64Counter(
		"mcp.client.reconnect.count",
		metric.WithDescription("Number of reconnection attempts"),
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		return nil, err
	}

	// Auth failure count
	inst.authFailureCount, err = meter.Int64Counter(
		"mcp.client.auth.failures",
		metric.WithDescription("Number of authentication/authorization failures"),
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, err
	}

	// Pagination pages
	inst.paginationPages, err = meter.Int64Counter(
		"mcp.client.pagination.pages",
		metric.WithDescription("Number of pages fetched in paginated operations"),
		metric.WithUnit("{page}"),
	)
	if err != nil {
		return nil, err
	}

	return inst, nil
}

// startRPCSpan starts a new span for an MCP RPC call.
// Returns the updated context and the span. Caller must call span.End().
//
// If tracing is disabled (o == nil), returns ctx and a no-op span.
func (o *observability) startRPCSpan(ctx context.Context, method string) (context.Context, trace.Span) {
	if o == nil || o.tracer == nil {
		return ctx, trace.SpanFromContext(ctx) // no-op span
	}

	// Span name follows OpenTelemetry semantic convention: <system>.<operation>
	// Example: "mcp.tools.call", "mcp.initialize"
	spanName := "mcp." + method

	ctx, span := o.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("rpc.system", "jsonrpc"),
			attribute.String("rpc.service", "mcp"),
			attribute.String("rpc.method", method),
		),
	)

	return ctx, span
}

// startTransportSpan starts a nested span for transport operations (send/receive).
func (o *observability) startTransportSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	if o == nil || o.tracer == nil {
		return ctx, trace.SpanFromContext(ctx) // no-op span
	}

	spanName := "mcp.transport." + operation // "send" or "receive"

	ctx, span := o.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("mcp.transport.operation", operation),
		),
	)

	return ctx, span
}

// recordRPCMetrics records request duration and count metrics.
// Recovers from panics to prevent observability failures from crashing the client.
func (o *observability) recordRPCMetrics(ctx context.Context, method string, duration time.Duration, err error) {
	if o == nil || o.instruments == nil {
		return // metrics disabled
	}

	// Panic recovery: metric recording must not crash the client
	defer func() {
		if r := recover(); r != nil {
			// Observability failure is non-fatal
			// Could log here if logger is available
			_ = r
		}
	}()

	status := "success"
	attrs := []attribute.KeyValue{
		attribute.String("method", method),
	}

	if err != nil {
		status = "error"
		// Record error code if it's a known MCP error
		if respErr, ok := err.(*ResponseError); ok {
			attrs = append(attrs, attribute.Int("error_code", respErr.Code))
		}
	}

	attrs = append(attrs, attribute.String("status", status))

	// Record duration (convert to seconds for standard metric unit)
	o.instruments.requestDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))

	// Record count
	o.instruments.requestCount.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// recordReconnect records a reconnection attempt metric.
// Recovers from panics to prevent observability failures from crashing the client.
func (o *observability) recordReconnect(ctx context.Context, success bool) {
	if o == nil || o.instruments == nil {
		return
	}

	// Panic recovery: metric recording must not crash the client
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()

	o.instruments.reconnectCount.Add(ctx, 1,
		metric.WithAttributes(attribute.Bool("success", success)),
	)
}

// recordAuthFailure records an authentication failure metric.
// Recovers from panics to prevent observability failures from crashing the client.
//
//nolint:unused // Public API for auth implementations
func (o *observability) recordAuthFailure(ctx context.Context, authType string) {
	if o == nil || o.instruments == nil {
		return
	}

	// Panic recovery: metric recording must not crash the client
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()

	o.instruments.authFailureCount.Add(ctx, 1,
		metric.WithAttributes(attribute.String("auth_type", authType)),
	)
}

// recordPaginationPage records a pagination page fetch metric.
// Recovers from panics to prevent observability failures from crashing the client.
//
//nolint:unused // Public API for pagination implementations
func (o *observability) recordPaginationPage(ctx context.Context, method string) {
	if o == nil || o.instruments == nil {
		return
	}

	// Panic recovery: metric recording must not crash the client
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()

	o.instruments.paginationPages.Add(ctx, 1,
		metric.WithAttributes(attribute.String("method", method)),
	)
}

// addSpanEvent adds an event to the current span.
// No-op if tracing is disabled.
func (o *observability) addSpanEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	if o == nil || o.tracer == nil {
		return
	}

	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// setSpanError marks the current span as failed with an error.
func (o *observability) setSpanError(ctx context.Context, err error) {
	if o == nil || o.tracer == nil || err == nil {
		return
	}

	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// setSpanOK marks the current span as successful.
func (o *observability) setSpanOK(ctx context.Context) {
	if o == nil || o.tracer == nil {
		return
	}

	span := trace.SpanFromContext(ctx)
	span.SetStatus(codes.Ok, "")
}

// trackActiveConnection increments/decrements the active connections gauge.
// Recovers from panics to prevent observability failures from crashing the client.
func (o *observability) trackActiveConnection(ctx context.Context, delta int64) {
	if o == nil || o.instruments == nil {
		return
	}

	// Panic recovery: metric recording must not crash the client
	defer func() {
		if r := recover(); r != nil {
			// Observability failure is non-fatal
			_ = r
		}
	}()

	o.instruments.activeConnections.Add(ctx, delta)
}
