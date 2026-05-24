package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// boolPtr returns a pointer to the given bool (test helper).
func boolPtr(b bool) *bool { return &b }

// newTestTracerProvider returns a TracerProvider backed by an in-memory
// exporter so tests can inspect recorded spans.
func newTestTracerProvider() (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(rec),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	return tp, rec
}

// startSpan starts a sampled span using tp and returns the enriched ctx and
// the span (caller must call span.End()).
func startSpan(t *testing.T, tp *sdktrace.TracerProvider) (context.Context, trace.Span) {
	t.Helper()
	ctx, span := tp.Tracer("test").Start(context.Background(), "test-op")
	return ctx, span
}

// --------------------------------------------------------------------------
// injectTraceContext
// --------------------------------------------------------------------------

func TestInjectTraceContext_ValidSpan(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	meta := make(map[string]any)
	injectTraceContext(ctx, meta)

	tp_, ok := meta["traceparent"].(string)
	if !ok || tp_ == "" {
		t.Fatal("expected traceparent to be injected")
	}
	// Validate W3C format: "00-<32hex>-<16hex>-<02hex>"
	parts := strings.Split(tp_, "-")
	if len(parts) != 4 {
		t.Fatalf("traceparent has %d parts, want 4: %q", len(parts), tp_)
	}
	if parts[0] != "00" {
		t.Errorf("traceparent version = %q, want 00", parts[0])
	}
	if len(parts[1]) != 32 {
		t.Errorf("traceID length = %d, want 32", len(parts[1]))
	}
	if len(parts[2]) != 16 {
		t.Errorf("spanID length = %d, want 16", len(parts[2]))
	}
	if len(parts[3]) != 2 {
		t.Errorf("traceFlags length = %d, want 2", len(parts[3]))
	}
}

func TestInjectTraceContext_NoSpanInContext(t *testing.T) {
	t.Parallel()
	meta := make(map[string]any)
	injectTraceContext(context.Background(), meta)

	if _, ok := meta["traceparent"]; ok {
		t.Error("traceparent must not be injected when context carries no span")
	}
}

func TestInjectTraceContext_PreservesExistingMeta(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	meta := map[string]any{
		"progressToken": "tok-42",
		"custom":        true,
	}
	injectTraceContext(ctx, meta)

	if meta["progressToken"] != "tok-42" {
		t.Error("progressToken must be preserved after injection")
	}
	if meta["custom"] != true {
		t.Error("custom field must be preserved after injection")
	}
	if _, ok := meta["traceparent"]; !ok {
		t.Error("traceparent must be present after injection")
	}
}

func TestInjectTraceContext_TraceStateInjected(t *testing.T) {
	t.Parallel()
	// Build a SpanContext with a non-empty TraceState.
	ts, err := trace.ParseTraceState("vendor=abc123")
	if err != nil {
		t.Fatalf("parse tracestate: %v", err)
	}
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		TraceState: ts,
		Remote:     false,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	meta := make(map[string]any)
	injectTraceContext(ctx, meta)

	if _, ok := meta["tracestate"]; !ok {
		t.Error("tracestate must be injected when span carries non-empty TraceState")
	}
}

func TestInjectTraceContext_EmptyTraceStateNotInjected(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	meta := make(map[string]any)
	injectTraceContext(ctx, meta)

	// Default SDK spans have no tracestate.
	if _, ok := meta["tracestate"]; ok {
		t.Error("tracestate must not be injected when span has no vendor tracestate")
	}
}

// --------------------------------------------------------------------------
// extractTraceContext
// --------------------------------------------------------------------------

func TestExtractTraceContext_ValidTraceparent(t *testing.T) {
	t.Parallel()
	meta := map[string]any{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	ctx := extractTraceContext(context.Background(), meta)
	sc := trace.SpanContextFromContext(ctx)

	if !sc.IsValid() {
		t.Fatal("expected a valid remote span context in ctx")
	}
	if !sc.IsRemote() {
		t.Error("span context should be marked as Remote")
	}
	if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("traceID = %s", sc.TraceID())
	}
	if sc.SpanID().String() != "00f067aa0ba902b7" {
		t.Errorf("spanID = %s", sc.SpanID())
	}
	if !sc.IsSampled() {
		t.Error("trace flags should indicate sampled")
	}
}

func TestExtractTraceContext_NilMeta(t *testing.T) {
	t.Parallel()
	ctx := extractTraceContext(context.Background(), nil)
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		t.Error("ctx must not carry a span context when meta is nil")
	}
}

func TestExtractTraceContext_MissingTraceparent(t *testing.T) {
	t.Parallel()
	meta := map[string]any{"other": "value"}
	ctx := extractTraceContext(context.Background(), meta)
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		t.Error("ctx must not carry a span context when traceparent is absent")
	}
}

func TestExtractTraceContext_MalformedTraceparent(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"not-a-traceparent",
		"00-short-spanid-01",
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", // bad version
		"00-ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ-00f067aa0ba902b7-01", // bad hex traceID
		"00-4bf92f3577b34da6a3ce929d0e0e4736-ZZZZZZZZZZZZZZZZ-01", // bad hex spanID
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-ZZ", // bad hex flags
	}
	for _, tp := range cases {
		meta := map[string]any{"traceparent": tp}
		ctx := extractTraceContext(context.Background(), meta)
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			t.Errorf("ctx must not carry span context for malformed traceparent %q", tp)
		}
	}
}

func TestExtractTraceContext_NonStringTraceparent(t *testing.T) {
	t.Parallel()
	meta := map[string]any{"traceparent": 12345}
	ctx := extractTraceContext(context.Background(), meta)
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		t.Error("ctx must not carry span context when traceparent is not a string")
	}
}

func TestExtractTraceContext_WithTraceState(t *testing.T) {
	t.Parallel()
	meta := map[string]any{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"tracestate":  "vendor=abc",
	}
	ctx := extractTraceContext(context.Background(), meta)
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		t.Fatal("expected a valid span context")
	}
	if sc.TraceState().String() != "vendor=abc" {
		t.Errorf("tracestate = %q, want %q", sc.TraceState().String(), "vendor=abc")
	}
}

// --------------------------------------------------------------------------
// marshalWithMeta
// --------------------------------------------------------------------------

func TestMarshalWithMeta_InjectEnabled_ValidSpan(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	params := map[string]any{"name": "echo"}
	raw, err := marshalWithMeta(ctx, params, true)
	if err != nil {
		t.Fatalf("marshalWithMeta: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	meta, ok := out["_meta"].(map[string]any)
	if !ok {
		t.Fatal("expected _meta in output")
	}
	tp_, ok := meta["traceparent"].(string)
	if !ok || tp_ == "" {
		t.Error("expected traceparent in _meta")
	}
}

func TestMarshalWithMeta_InjectDisabled(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	params := map[string]any{"name": "echo"}
	raw, err := marshalWithMeta(ctx, params, false)
	if err != nil {
		t.Fatalf("marshalWithMeta: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := out["_meta"]; ok {
		t.Error("_meta must not be injected when inject=false")
	}
}

func TestMarshalWithMeta_NoSpanInContext(t *testing.T) {
	t.Parallel()
	params := map[string]any{"name": "echo"}
	raw, err := marshalWithMeta(context.Background(), params, true)
	if err != nil {
		t.Fatalf("marshalWithMeta: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := out["_meta"]; ok {
		t.Error("_meta must not be injected when context has no span")
	}
}

func TestMarshalWithMeta_NilParams(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	raw, err := marshalWithMeta(ctx, nil, true)
	if err != nil {
		t.Fatalf("marshalWithMeta(nil): %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	meta, ok := out["_meta"].(map[string]any)
	if !ok {
		t.Fatal("expected _meta even for nil params")
	}
	if _, ok := meta["traceparent"]; !ok {
		t.Error("expected traceparent in _meta for nil params")
	}
}

func TestMarshalWithMeta_ExistingMetaPreserved(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	params := map[string]any{
		"name": "echo",
		"_meta": map[string]any{
			"progressToken": "tok-99",
		},
	}
	raw, err := marshalWithMeta(ctx, params, true)
	if err != nil {
		t.Fatalf("marshalWithMeta: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	meta, ok := out["_meta"].(map[string]any)
	if !ok {
		t.Fatal("expected _meta in output")
	}
	if meta["progressToken"] != "tok-99" {
		t.Errorf("progressToken = %v, want tok-99", meta["progressToken"])
	}
	if _, ok := meta["traceparent"]; !ok {
		t.Error("expected traceparent alongside preserved progressToken")
	}
}

func TestMarshalWithMeta_NonObjectParams(t *testing.T) {
	t.Parallel()
	// JSON arrays and other non-object types should pass through unmodified.
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	params := []string{"a", "b", "c"}
	raw, err := marshalWithMeta(ctx, params, true)
	if err != nil {
		t.Fatalf("marshalWithMeta: %v", err)
	}

	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("expected array result: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("expected 3 elements, got %d", len(out))
	}
}

// --------------------------------------------------------------------------
// Round-trip: inject → extract
// --------------------------------------------------------------------------

func TestRoundTrip_InjectExtract(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	sc := span.SpanContext()

	// Inject into a meta map.
	meta := make(map[string]any)
	injectTraceContext(ctx, meta)

	// Extract back.
	outCtx := extractTraceContext(context.Background(), meta)
	outSC := trace.SpanContextFromContext(outCtx)

	if !outSC.IsValid() {
		t.Fatal("extracted span context must be valid")
	}
	if outSC.TraceID() != sc.TraceID() {
		t.Errorf("traceID mismatch: got %s, want %s", outSC.TraceID(), sc.TraceID())
	}
	if outSC.SpanID() != sc.SpanID() {
		t.Errorf("spanID mismatch: got %s, want %s", outSC.SpanID(), sc.SpanID())
	}
	if outSC.TraceFlags() != sc.TraceFlags() {
		t.Errorf("traceFlags mismatch: got %v, want %v", outSC.TraceFlags(), sc.TraceFlags())
	}
	if !outSC.IsRemote() {
		t.Error("extracted span context must be Remote")
	}
}

// --------------------------------------------------------------------------
// shouldPropagateTrace / Client.Options.PropagateTraceContext
// --------------------------------------------------------------------------

func TestShouldPropagateTrace_NoTracerProvider(t *testing.T) {
	t.Parallel()
	c := &Client{opts: Options{}}
	if c.shouldPropagateTrace() {
		t.Error("shouldPropagateTrace must return false when TracerProvider is nil")
	}
}

func TestShouldPropagateTrace_TracerProviderSet_DefaultEnabled(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	c := &Client{opts: Options{TracerProvider: tp}}
	if !c.shouldPropagateTrace() {
		t.Error("shouldPropagateTrace must return true by default when TracerProvider is set")
	}
}

func TestShouldPropagateTrace_ExplicitlyDisabled(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	c := &Client{opts: Options{
		TracerProvider:        tp,
		PropagateTraceContext: boolPtr(false),
	}}
	if c.shouldPropagateTrace() {
		t.Error("shouldPropagateTrace must return false when PropagateTraceContext is &false")
	}
}

func TestShouldPropagateTrace_ExplicitlyEnabled(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	c := &Client{opts: Options{
		TracerProvider:        tp,
		PropagateTraceContext: boolPtr(true),
	}}
	if !c.shouldPropagateTrace() {
		t.Error("shouldPropagateTrace must return true when PropagateTraceContext is &true")
	}
}

// --------------------------------------------------------------------------
// Race-condition safety
// --------------------------------------------------------------------------

func TestInjectExtract_Race(t *testing.T) {
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			meta := make(map[string]any)
			injectTraceContext(ctx, meta)
			_ = extractTraceContext(context.Background(), meta)
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestMarshalWithMeta_Race(t *testing.T) {
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			params := map[string]any{"n": n}
			_, _ = marshalWithMeta(ctx, params, true)
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

// --------------------------------------------------------------------------
// Struct-typed params (ensures struct fields survive injection)
// --------------------------------------------------------------------------

func TestMarshalWithMeta_StructParams(t *testing.T) {
	t.Parallel()
	type myParams struct {
		Name string         `json:"name"`
		Meta map[string]any `json:"_meta,omitempty"`
	}

	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	p := myParams{Name: "tool-x"}
	raw, err := marshalWithMeta(ctx, p, true)
	if err != nil {
		t.Fatalf("marshalWithMeta: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["name"] != "tool-x" {
		t.Errorf("name = %v, want tool-x", out["name"])
	}
	meta, _ := out["_meta"].(map[string]any)
	if meta == nil {
		t.Fatal("_meta missing in output")
	}
	if _, ok := meta["traceparent"]; !ok {
		t.Error("traceparent missing in _meta")
	}
}

// --------------------------------------------------------------------------
// Traceparent content matches span
// --------------------------------------------------------------------------

func TestInjectTraceContext_ContentMatchesSpan(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTracerProvider()
	ctx, span := startSpan(t, tp)
	defer span.End()

	sc := span.SpanContext()
	meta := make(map[string]any)
	injectTraceContext(ctx, meta)

	tpStr, _ := meta["traceparent"].(string)
	wantTP := fmt.Sprintf("00-%s-%s-%02x", sc.TraceID(), sc.SpanID(), byte(sc.TraceFlags()))
	if tpStr != wantTP {
		t.Errorf("traceparent = %q, want %q", tpStr, wantTP)
	}
}
