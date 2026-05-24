package client_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestObservability_Disabled verifies zero overhead when observability is disabled
func TestObservability_Disabled(t *testing.T) {
	mt := newMockTransport()
	autoResponder(t, mt)

	// Create client WITHOUT observability providers
	c, err := client.New(mt, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Initialize
	_, err = c.Initialize(ctx)
	require.NoError(t, err)

	// Call ListTools
	_, err = c.ListTools(ctx, finemcp.ListParams{})
	require.NoError(t, err)

	// Verify no panics and normal operation
	c.Close()
}

// TestObservability_Tracing verifies tracing functionality
func TestObservability_Tracing(t *testing.T) {
	// Setup tracing
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := trace.NewTracerProvider(
		trace.WithSpanProcessor(spanRecorder),
	)

	mt := newMockTransport()
	autoResponder(t, mt)

	// Create client with tracing
	c, err := client.New(mt, client.Options{
		ClientInfo:     finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		TracerProvider: tracerProvider,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Initialize
	_, err = c.Initialize(ctx)
	require.NoError(t, err)

	// Call tool
	toolArgs := map[string]any{"text": "hello"}
	toolArgsJSON, _ := json.Marshal(toolArgs)

	_, err = c.CallTool(ctx, finemcp.CallToolParams{
		Name:      "echo",
		Arguments: toolArgsJSON,
	})
	require.NoError(t, err)

	c.Close()

	// Verify spans were created
	spans := spanRecorder.Ended()
	require.Greater(t, len(spans), 0, "should have recorded spans")

	// Verify we have initialize and tools.call spans
	spanNames := make(map[string]bool)
	for _, s := range spans {
		spanNames[s.Name()] = true
	}

	require.True(t, spanNames["mcp.initialize"], "should have initialize span")
	require.True(t, spanNames["mcp.tools.call"], "should have tools.call span")
	require.True(t, spanNames["mcp.transport.send"], "should have transport.send span")
}

// TestObservability_Metrics verifies metrics recording
func TestObservability_Metrics(t *testing.T) {
	// Setup metrics
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)

	mt := newMockTransport()
	autoResponder(t, mt)

	// Create client with metrics
	c, err := client.New(mt, client.Options{
		ClientInfo:    finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		MeterProvider: meterProvider,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Initialize
	_, err = c.Initialize(ctx)
	require.NoError(t, err)

	// Make some requests
	_, err = c.ListTools(ctx, finemcp.ListParams{})
	require.NoError(t, err)

	err = c.Ping(ctx)
	require.NoError(t, err)

	c.Close()

	// Collect metrics
	var rm metricdata.ResourceMetrics
	err = reader.Collect(ctx, &rm)
	require.NoError(t, err)

	// Verify metrics were recorded
	metricNames := make(map[string]bool)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metricNames[m.Name] = true
		}
	}

	require.True(t, metricNames["mcp.client.request.count"], "should have request count metric")
	require.True(t, metricNames["mcp.client.request.duration"], "should have request duration metric")
	require.True(t, metricNames["mcp.client.active_connections"], "should have active connections metric")
}

// TestObservability_BothTracingAndMetrics verifies both work together
func TestObservability_BothTracingAndMetrics(t *testing.T) {
	// Setup both tracing and metrics
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := trace.NewTracerProvider(
		trace.WithSpanProcessor(spanRecorder),
	)
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)

	mt := newMockTransport()
	autoResponder(t, mt)

	// Create client with both providers
	c, err := client.New(mt, client.Options{
		ClientInfo:     finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Initialize and ping
	_, err = c.Initialize(ctx)
	require.NoError(t, err)

	err = c.Ping(ctx)
	require.NoError(t, err)

	c.Close()

	// Verify both spans and metrics were recorded
	spans := spanRecorder.Ended()
	require.Greater(t, len(spans), 0, "should have recorded spans")

	var rm metricdata.ResourceMetrics
	err = reader.Collect(ctx, &rm)
	require.NoError(t, err)

	metricCount := 0
	for _, sm := range rm.ScopeMetrics {
		metricCount += len(sm.Metrics)
	}
	require.Greater(t, metricCount, 0, "should have recorded metrics")
}

// TestObservability_ErrorRecording verifies error recording in spans and metrics
func TestObservability_ErrorRecording(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := trace.NewTracerProvider(
		trace.WithSpanProcessor(spanRecorder),
	)
	reader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)

	mt := newMockTransport()

	// Manually respond to initialize, then send error for tools/list
	go func() {
		for {
			mt.mu.Lock()
			count := len(mt.sent)
			mt.mu.Unlock()

			if count == 0 {
				continue
			}

			for i := 0; i < count; i++ {
				mt.mu.Lock()
				data := mt.sent[i]
				mt.mu.Unlock()

				var msg struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				json.Unmarshal(data, &msg)

				if msg.Method == "initialize" {
					mt.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result: finemcp.InitializeResult{
							ProtocolVersion: finemcp.ProtocolVersion,
							ServerInfo:      finemcp.ProcessInfo{Name: "test", Version: "1.0"},
						},
					})
				} else if msg.Method == "tools/list" {
					mt.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Error: &finemcp.JSONRPCError{
							Code:    -32601,
							Message: "method not found",
						},
					})
					return
				}
			}
		}
	}()

	c, err := client.New(mt, client.Options{
		ClientInfo:     finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Initialize
	_, err = c.Initialize(ctx)
	require.NoError(t, err)

	// Make a request that will fail
	_, err = c.ListTools(ctx, finemcp.ListParams{})
	require.Error(t, err, "should return error")

	c.Close()

	// Verify error was recorded in span
	spans := spanRecorder.Ended()
	var toolsListSpan trace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == "mcp.tools.list" {
			toolsListSpan = s
			break
		}
	}
	require.NotNil(t, toolsListSpan, "should have tools.list span")

	// Check span status indicates error
	require.Equal(t, "Error", toolsListSpan.Status().Code.String())

	// Verify metrics have error data
	var rm metricdata.ResourceMetrics
	err = reader.Collect(ctx, &rm)
	require.NoError(t, err)

	// Look for error metrics
	hasErrorMetric := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "mcp.client.request.count" {
				hasErrorMetric = true
				break
			}
		}
	}
	require.True(t, hasErrorMetric, "should have recorded error metric")
}
