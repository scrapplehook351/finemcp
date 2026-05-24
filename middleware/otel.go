package middleware

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/finemcp/finemcp"
)

const (
	// instrumentationName is the OpenTelemetry instrumentation scope name.
	instrumentationName = "github.com/finemcp/finemcp/middleware"
)

// OTelOption configures the OpenTelemetry middleware.
type OTelOption func(*otelConfig)

type otelConfig struct {
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	enableTracing  bool
	enableMetrics  bool
}

// WithTracerProvider sets a custom TracerProvider.
// If not set, the global provider from [otel.GetTracerProvider] is used.
func WithTracerProvider(tp trace.TracerProvider) OTelOption {
	return func(c *otelConfig) { c.tracerProvider = tp }
}

// WithMeterProvider sets a custom MeterProvider.
// If not set, the global provider from [otel.GetMeterProvider] is used.
func WithMeterProvider(mp metric.MeterProvider) OTelOption {
	return func(c *otelConfig) { c.meterProvider = mp }
}

// WithTracing enables or disables trace span creation (default: true).
func WithTracing(enabled bool) OTelOption {
	return func(c *otelConfig) { c.enableTracing = enabled }
}

// WithMetrics enables or disables metric recording (default: true).
func WithMetrics(enabled bool) OTelOption {
	return func(c *otelConfig) { c.enableMetrics = enabled }
}

// OTel returns a middleware that instruments tool calls with OpenTelemetry
// tracing and metrics.
//
// Tracing: creates a span for each tool call with attributes for tool name,
// request ID, and error status.
//
// Metrics: records a histogram of tool call durations and a counter of
// tool calls, both tagged with tool name and error status.
//
// Usage:
//
//	server.Use(middleware.OTel())                     // uses global providers
//	server.Use(middleware.OTel(                       // explicit providers
//	    middleware.WithTracerProvider(tp),
//	    middleware.WithMeterProvider(mp),
//	))
func OTel(opts ...OTelOption) finemcp.Middleware {
	cfg := otelConfig{
		enableTracing: true,
		enableMetrics: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.tracerProvider == nil {
		cfg.tracerProvider = otel.GetTracerProvider()
	}
	if cfg.meterProvider == nil {
		cfg.meterProvider = otel.GetMeterProvider()
	}

	// Lazily initialize tracer and meter only when the corresponding
	// features are enabled, to avoid unnecessary setup and registration
	// when tracing or metrics are disabled.
	var tracer trace.Tracer
	if cfg.enableTracing {
		tracer = cfg.tracerProvider.Tracer(instrumentationName)
	}

	var (
		callCounter  metric.Int64Counter
		callDuration metric.Float64Histogram
	)
	if cfg.enableMetrics {
		meter := cfg.meterProvider.Meter(instrumentationName)

		// Pre-create metric instruments. If creation fails the OTel API
		// returns a no-op instrument, so subsequent Add / Record calls
		// silently do nothing rather than surfacing an error later.
		callCounter, _ = meter.Int64Counter(
			"mcp.tool.calls",
			metric.WithDescription("Number of tool calls"),
			metric.WithUnit("{call}"),
		)
		callDuration, _ = meter.Float64Histogram(
			"mcp.tool.duration",
			metric.WithDescription("Duration of tool calls in milliseconds"),
			metric.WithUnit("ms"),
		)
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			toolName := finemcp.ToolName(ctx)
			requestID := finemcp.RequestID(ctx)

			// ── Tracing ─────────────────────────────────────────
			if cfg.enableTracing {
				spanAttrs := []attribute.KeyValue{
					attribute.String("mcp.tool.name", toolName),
				}
				if requestID != nil {
					spanAttrs = append(spanAttrs, attribute.String("mcp.request.id", formatRequestID(requestID)))
				}

				var span trace.Span
				ctx, span = tracer.Start(ctx, "mcp.tool/"+toolName,
					trace.WithAttributes(spanAttrs...),
					trace.WithSpanKind(trace.SpanKindServer),
				)
				defer span.End()
			}

			start := time.Now()

			// Capture panics so the span and metrics reflect the failure
			// before the panic propagates up the stack.
			var (
				out      []byte
				err      error
				panicked = true
			)
			defer func() {
				elapsed := float64(time.Since(start)) / float64(time.Millisecond)

				if panicked {
					r := recover()
					if cfg.enableTracing {
						span := trace.SpanFromContext(ctx)
						span.SetStatus(codes.Error, fmt.Sprintf("panic: %v", r))
						span.RecordError(fmt.Errorf("panic: %v", r))
					}
					if cfg.enableMetrics {
						metricAttrs := metric.WithAttributes(
							attribute.String("mcp.tool.name", toolName),
							attribute.Bool("error", true),
						)
						callCounter.Add(ctx, 1, metricAttrs)
						callDuration.Record(ctx, elapsed, metricAttrs)
					}
					// Re-panic with the original value; if r is nil (e.g.
					// runtime.Goexit), panic(nil) lets the runtime unwind
					// naturally without changing semantics.
					panic(r)
				}

				// ── Record span status ──────────────────────────────
				if cfg.enableTracing {
					span := trace.SpanFromContext(ctx)
					if err != nil {
						span.SetStatus(codes.Error, err.Error())
						span.RecordError(err)
					}
					// On success we leave the status Unset per OTel conventions.
				}

				// ── Metrics ─────────────────────────────────────────
				if cfg.enableMetrics {
					metricAttrs := metric.WithAttributes(
						attribute.String("mcp.tool.name", toolName),
						attribute.Bool("error", err != nil),
					)
					callCounter.Add(ctx, 1, metricAttrs)
					callDuration.Record(ctx, elapsed, metricAttrs)
				}
			}()

			out, err = next(ctx, input)
			panicked = false

			return out, err
		}
	}
}

// formatRequestID converts a JSON-RPC request ID (string or number) to string.
func formatRequestID(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
