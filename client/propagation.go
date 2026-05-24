package client

// W3C Trace Context propagation for MCP over JSON-RPC.
//
// This file implements RFC 7230-compatible W3C Trace Context injection and
// extraction via the JSON-RPC _meta field, enabling end-to-end distributed
// tracing across MCP clients and servers.
//
// Spec: https://www.w3.org/TR/trace-context/
// MCP: trace context is carried in params._meta.traceparent / params._meta.tracestate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// traceparentVersion is the only supported W3C traceparent version.
const traceparentVersion = "00"

// injectTraceContext writes W3C traceparent (and tracestate when non-empty)
// into meta in-place. It is a no-op when ctx carries no valid span.
func injectTraceContext(ctx context.Context, meta map[string]any) {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return
	}

	// traceparent format: "00-<32hexTraceID>-<16hexSpanID>-<02hexFlags>"
	meta["traceparent"] = fmt.Sprintf("%s-%s-%s-%02x",
		traceparentVersion,
		sc.TraceID(),
		sc.SpanID(),
		byte(sc.TraceFlags()),
	)

	// Inject tracestate only when it carries vendor-specific data.
	if ts := sc.TraceState().String(); ts != "" {
		meta["tracestate"] = ts
	}
}

// extractTraceContext reads _meta.traceparent / _meta.tracestate from meta and
// returns a new context that carries the decoded remote span context.
// If meta is nil, traceparent is absent, or the value is malformed, the
// original context is returned unchanged (no panic).
func extractTraceContext(ctx context.Context, meta map[string]any) context.Context {
	if meta == nil {
		return ctx
	}

	tpVal, ok := meta["traceparent"]
	if !ok {
		return ctx
	}
	tp, ok := tpVal.(string)
	if !ok {
		return ctx
	}

	// Parse W3C traceparent: <version>-<traceID>-<spanID>-<flags>
	parts := strings.Split(tp, "-")
	if len(parts) != 4 || parts[0] != traceparentVersion {
		return ctx
	}

	traceID, err := trace.TraceIDFromHex(parts[1])
	if err != nil {
		return ctx
	}
	spanID, err := trace.SpanIDFromHex(parts[2])
	if err != nil {
		return ctx
	}
	flagsInt, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil {
		return ctx
	}

	cfg := trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.TraceFlags(flagsInt),
		Remote:     true,
	}

	// Optionally decode tracestate.
	if tsVal, ok := meta["tracestate"]; ok {
		if tsStr, ok := tsVal.(string); ok {
			if ts, err := trace.ParseTraceState(tsStr); err == nil {
				cfg.TraceState = ts
			}
		}
	}

	rsc := trace.NewSpanContext(cfg)
	if !rsc.IsValid() {
		return ctx
	}
	return trace.ContextWithRemoteSpanContext(ctx, rsc)
}

// marshalWithMeta marshals params to JSON and, when inject is true and ctx
// carries a valid OTel span, injects W3C trace context into params._meta.
//
// Existing _meta fields are preserved; only traceparent (and tracestate when
// non-empty) are added or overwritten.
//
// If params is nil or marshals to JSON null, a fresh object carrying only
// _meta is returned.  If params is not a JSON object (e.g. an array), the
// raw marshaled bytes are returned without modification.
func marshalWithMeta(ctx context.Context, params any, inject bool) (json.RawMessage, error) {
	// Fast path: nothing to inject.
	if !inject {
		return json.Marshal(params)
	}

	// Check span validity before paying the unmarshal cost.
	sc := trace.SpanFromContext(ctx).SpanContext()
	if !sc.IsValid() {
		return json.Marshal(params)
	}

	// Marshal once to determine whether we have a JSON object.
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("propagation: marshal params: %w", err)
	}

	// Unmarshal into a generic map so we can inject _meta.
	// If params was nil / null, start from an empty map.
	var m map[string]any
	if params == nil || string(raw) == "null" {
		m = make(map[string]any)
	} else {
		if err := json.Unmarshal(raw, &m); err != nil {
			// Not a JSON object (array, string, …) — return as-is.
			return raw, nil
		}
	}

	// Retrieve or create _meta.
	meta, _ := m["_meta"].(map[string]any)
	if meta == nil {
		meta = make(map[string]any)
	}

	injectTraceContext(ctx, meta)
	m["_meta"] = meta

	enriched, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("propagation: re-marshal params with trace context: %w", err)
	}
	return enriched, nil
}
