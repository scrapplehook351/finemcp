package middleware

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/finemcp/finemcp"
)

// ── helpers ─────────────────────────────────────────────────────────

func newTestTracer() (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	return exp, tp
}

func newTestMeter() (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return reader, mp
}

// ── Tracing tests ───────────────────────────────────────────────────

func TestOTel_CreatesSpan(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithMetrics(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	handler(ctx, []byte(`{}`))

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "mcp.tool/greet" {
		t.Errorf("span name = %q, want %q", spans[0].Name, "mcp.tool/greet")
	}
}

func TestOTel_SpanHasToolNameAttribute(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithMetrics(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "calculate")
	handler(ctx, []byte(`{}`))

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	found := false
	for _, a := range spans[0].Attributes {
		if a.Key == "mcp.tool.name" && a.Value.AsString() == "calculate" {
			found = true
			break
		}
	}
	if !found {
		t.Error("span missing mcp.tool.name attribute")
	}
}

func TestOTel_SpanHasRequestIDAttribute(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithMetrics(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	ctx = finemcp.WithRequestID(ctx, "req-42")
	handler(ctx, []byte(`{}`))

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	found := false
	for _, a := range spans[0].Attributes {
		if a.Key == "mcp.request.id" && a.Value.AsString() == "req-42" {
			found = true
			break
		}
	}
	if !found {
		t.Error("span missing mcp.request.id attribute")
	}
}

func TestOTel_SpanRecordsError(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithMetrics(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return nil, errors.New("boom")
	})

	ctx := finemcp.WithToolName(context.Background(), "failing")
	out, err := handler(ctx, []byte(`{}`))

	// Verify middleware propagates the error unchanged.
	if err == nil || err.Error() != "boom" {
		t.Errorf("expected error 'boom', got %v", err)
	}
	if out != nil {
		t.Errorf("expected nil output on error, got %q", out)
	}

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want Error", spans[0].Status.Code)
	}
	if spans[0].Status.Description != "boom" {
		t.Errorf("span status description = %q, want %q", spans[0].Status.Description, "boom")
	}
}

func TestOTel_SpanUnsetOnSuccess(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithMetrics(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	handler(ctx, []byte(`{}`))

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Status.Code != codes.Unset {
		t.Errorf("span status = %v, want Unset", spans[0].Status.Code)
	}
}

func TestOTel_PanicRecordedOnSpan(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	reader, mp := newTestMeter()
	defer mp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithMeterProvider(mp))
	handler := mw(func(_ context.Context, _ []byte) ([]byte, error) {
		panic("test panic")
	})

	ctx := finemcp.WithToolName(context.Background(), "panicky")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate, got none")
		}
		if r != "test panic" {
			t.Fatalf("recovered value = %v, want %q", r, "test panic")
		}

		tp.ForceFlush(context.Background())
		spans := exp.GetSpans()
		if len(spans) != 1 {
			t.Fatalf("span count = %d, want 1", len(spans))
		}
		if spans[0].Status.Code != codes.Error {
			t.Errorf("span status = %v, want Error", spans[0].Status.Code)
		}

		// Verify metrics were recorded even on panic.
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatalf("collect: %v", err)
		}
		found := findMetric(rm, "mcp.tool.calls")
		if found == nil {
			t.Fatal("mcp.tool.calls metric not found after panic")
		}
	}()

	handler(ctx, []byte(`{}`))
}

func TestOTel_TracingDisabled(t *testing.T) {
	t.Parallel()

	exp, tp := newTestTracer()
	defer tp.Shutdown(context.Background())

	mw := OTel(WithTracerProvider(tp), WithTracing(false), WithMetrics(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	handler(ctx, []byte(`{}`))

	tp.ForceFlush(context.Background())
	spans := exp.GetSpans()
	if len(spans) != 0 {
		t.Errorf("span count = %d, want 0 when tracing disabled", len(spans))
	}
}

// ── Metric tests ────────────────────────────────────────────────────

func TestOTel_RecordsCallCounter(t *testing.T) {
	t.Parallel()

	reader, mp := newTestMeter()
	defer mp.Shutdown(context.Background())

	mw := OTel(WithMeterProvider(mp), WithTracing(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	handler(ctx, []byte(`{}`))
	handler(ctx, []byte(`{}`))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	found := findMetric(rm, "mcp.tool.calls")
	if found == nil {
		t.Fatal("mcp.tool.calls metric not found")
	}
	sum, ok := found.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("expected Sum[int64], got %T", found.Data)
	}
	if len(sum.DataPoints) == 0 {
		t.Fatal("no data points")
	}
	if sum.DataPoints[0].Value != 2 {
		t.Errorf("call count = %d, want 2", sum.DataPoints[0].Value)
	}
}

func TestOTel_RecordsDurationHistogram(t *testing.T) {
	t.Parallel()

	reader, mp := newTestMeter()
	defer mp.Shutdown(context.Background())

	mw := OTel(WithMeterProvider(mp), WithTracing(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	handler(ctx, []byte(`{}`))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	found := findMetric(rm, "mcp.tool.duration")
	if found == nil {
		t.Fatal("mcp.tool.duration metric not found")
	}
	hist, ok := found.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("expected Histogram[float64], got %T", found.Data)
	}
	var totalCount uint64
	for _, dp := range hist.DataPoints {
		totalCount += dp.Count
	}
	if totalCount != 1 {
		t.Errorf("total histogram count = %d, want 1", totalCount)
	}
}

func TestOTel_MetricHasErrorAttribute(t *testing.T) {
	t.Parallel()

	reader, mp := newTestMeter()
	defer mp.Shutdown(context.Background())

	mw := OTel(WithMeterProvider(mp), WithTracing(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return nil, errors.New("boom")
	})

	ctx := finemcp.WithToolName(context.Background(), "failing")
	handler(ctx, []byte(`{}`))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	found := findMetric(rm, "mcp.tool.calls")
	if found == nil {
		t.Fatal("mcp.tool.calls metric not found")
	}
	sum, ok := found.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("mcp.tool.calls metric has unexpected data type %T, want metricdata.Sum[int64]", found.Data)
	}
	for _, dp := range sum.DataPoints {
		for _, a := range dp.Attributes.ToSlice() {
			if a.Key == "error" && a.Value == attribute.BoolValue(true) {
				return // found it
			}
		}
	}
	t.Error("metric missing error=true attribute on error call")
}

func TestOTel_MetricsDisabled(t *testing.T) {
	t.Parallel()

	reader, mp := newTestMeter()
	defer mp.Shutdown(context.Background())

	mw := OTel(WithMeterProvider(mp), WithMetrics(false), WithTracing(false))
	handler := mw(func(_ context.Context, in []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	ctx := finemcp.WithToolName(context.Background(), "greet")
	handler(ctx, []byte(`{}`))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	found := findMetric(rm, "mcp.tool.calls")
	if found != nil {
		t.Error("expected no mcp.tool.calls metric when metrics disabled")
	}
}

// ── formatRequestID ─────────────────────────────────────────────────

func TestFormatRequestID_String(t *testing.T) {
	if got := formatRequestID("abc"); got != "abc" {
		t.Errorf("got %q, want %q", got, "abc")
	}
}

func TestFormatRequestID_Float(t *testing.T) {
	if got := formatRequestID(float64(42)); got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

func TestFormatRequestID_Other(t *testing.T) {
	if got := formatRequestID(123); got != "123" {
		t.Errorf("got %q, want %q", got, "123")
	}
}

// ── helper ──────────────────────────────────────────────────────────

func findMetric(rm metricdata.ResourceMetrics, name string) *metricdata.Metrics {
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}
