package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// ── extractMeta unit tests ──────────────────────────────────────────

func TestExtractMeta_Present(t *testing.T) {
	raw := json.RawMessage(`{"name":"tool","_meta":{"progressToken":"tok-1","custom":"value"}}`)
	meta := extractMeta(raw)
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta["progressToken"] != "tok-1" {
		t.Errorf("progressToken = %v, want tok-1", meta["progressToken"])
	}
	if meta["custom"] != "value" {
		t.Errorf("custom = %v, want value", meta["custom"])
	}
}

func TestExtractMeta_Absent(t *testing.T) {
	raw := json.RawMessage(`{"name":"tool"}`)
	meta := extractMeta(raw)
	if meta != nil {
		t.Errorf("expected nil meta, got %v", meta)
	}
}

func TestExtractMeta_NilParams(t *testing.T) {
	meta := extractMeta(nil)
	if meta != nil {
		t.Errorf("expected nil meta, got %v", meta)
	}
}

func TestExtractMeta_MalformedJSON(t *testing.T) {
	raw := json.RawMessage(`{bad json}`)
	meta := extractMeta(raw)
	if meta != nil {
		t.Errorf("expected nil meta for malformed JSON, got %v", meta)
	}
}

func TestExtractMeta_EmptyObject(t *testing.T) {
	raw := json.RawMessage(`{"_meta":{}}`)
	meta := extractMeta(raw)
	if meta == nil {
		t.Fatal("expected non-nil (but empty) meta")
	}
	if len(meta) != 0 {
		t.Errorf("expected empty meta, got %v", meta)
	}
}

// ── progressTokenFromMeta unit tests ────────────────────────────────

func TestProgressTokenFromMeta_Present(t *testing.T) {
	meta := map[string]any{"progressToken": "my-token"}
	token := progressTokenFromMeta(meta)
	if token != "my-token" {
		t.Errorf("token = %v, want my-token", token)
	}
}

func TestProgressTokenFromMeta_Absent(t *testing.T) {
	meta := map[string]any{"other": "value"}
	token := progressTokenFromMeta(meta)
	if token != nil {
		t.Errorf("expected nil token, got %v", token)
	}
}

func TestProgressTokenFromMeta_NilMeta(t *testing.T) {
	token := progressTokenFromMeta(nil)
	if token != nil {
		t.Errorf("expected nil token, got %v", token)
	}
}

func TestProgressTokenFromMeta_NumericToken(t *testing.T) {
	meta := map[string]any{"progressToken": float64(42)}
	token := progressTokenFromMeta(meta)
	if token != float64(42) {
		t.Errorf("token = %v, want 42", token)
	}
}

// ── MetaFromCtx / WithMeta context tests ────────────────────────────

func TestMetaFromCtx_Set(t *testing.T) {
	ctx := context.Background()
	meta := map[string]any{"key": "val"}
	ctx = WithMeta(ctx, meta)

	got := MetaFromCtx(ctx)
	if got == nil {
		t.Fatal("expected non-nil meta from context")
	}
	if got["key"] != "val" {
		t.Errorf("meta[key] = %v, want val", got["key"])
	}
}

func TestMetaFromCtx_NotSet(t *testing.T) {
	ctx := context.Background()
	got := MetaFromCtx(ctx)
	if got != nil {
		t.Errorf("expected nil meta, got %v", got)
	}
}

// ── SetResponseMeta / responseMetaHolder tests ──────────────────────

func TestSetResponseMeta_Basic(t *testing.T) {
	ctx := withResponseMetaHolder(context.Background())

	SetResponseMeta(ctx, "timing", "42ms")
	SetResponseMeta(ctx, "version", 2)

	rm := responseMetaFromHolder(ctx)
	if rm == nil {
		t.Fatal("expected non-nil response meta")
	}
	if rm["timing"] != "42ms" {
		t.Errorf("timing = %v, want 42ms", rm["timing"])
	}
	if rm["version"] != 2 {
		t.Errorf("version = %v, want 2", rm["version"])
	}
}

func TestSetResponseMeta_NoHolder(t *testing.T) {
	// Should be a no-op, not panic.
	ctx := context.Background()
	SetResponseMeta(ctx, "key", "value")

	rm := responseMetaFromHolder(ctx)
	if rm != nil {
		t.Errorf("expected nil response meta, got %v", rm)
	}
}

func TestSetResponseMeta_NotSet(t *testing.T) {
	ctx := withResponseMetaHolder(context.Background())
	rm := responseMetaFromHolder(ctx)
	if rm != nil {
		t.Errorf("expected nil when no meta set, got %v", rm)
	}
}

// ── _meta on request params integration tests ───────────────────────

func TestHandleMessage_ToolsCall_MetaInContext(t *testing.T) {
	s := initServer(t)

	var capturedMeta map[string]any
	tool, _ := NewTool("meta_reader", func(ctx context.Context, _ []byte) ([]byte, error) {
		capturedMeta = MetaFromCtx(ctx)
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "meta_reader",
		"arguments": map[string]any{},
		"_meta":     map[string]any{"custom": "header-value", "traceId": "abc123"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	if capturedMeta == nil {
		t.Fatal("expected tool handler to see _meta in context")
	}
	if capturedMeta["custom"] != "header-value" {
		t.Errorf("custom = %v, want header-value", capturedMeta["custom"])
	}
	if capturedMeta["traceId"] != "abc123" {
		t.Errorf("traceId = %v, want abc123", capturedMeta["traceId"])
	}
}

func TestHandleMessage_ToolsCall_NoMeta(t *testing.T) {
	s := initServer(t)

	var capturedMeta map[string]any
	tool, _ := NewTool("no_meta", func(ctx context.Context, _ []byte) ([]byte, error) {
		capturedMeta = MetaFromCtx(ctx)
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "no_meta",
		"arguments": map[string]any{},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	if capturedMeta != nil {
		t.Errorf("expected nil meta when _meta not sent, got %v", capturedMeta)
	}
}

// ── _meta.progressToken integration test ────────────────────────────

func TestHandleMessage_ToolsCall_ProgressToken(t *testing.T) {
	s := initServer(t)

	var capturedTokens []any
	tool, _ := NewTool("progress_tool", func(ctx context.Context, _ []byte) ([]byte, error) {
		ReportProgress(ctx, 1, 10)
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	// Provide a notification sender that captures progress notifications.
	sender := func(n *JSONRPCNotification) {
		if n.Method == methodProgress {
			if pp, ok := n.Params.(ProgressParams); ok {
				capturedTokens = append(capturedTokens, pp.ProgressToken)
			}
		}
	}

	ctx := WithNotificationSender(context.Background(), sender)

	// Send with _meta.progressToken
	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "progress_tool",
		"arguments": map[string]any{},
		"_meta":     map[string]any{"progressToken": "my-progress-tok"},
	})

	resp, err := s.HandleMessage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	if len(capturedTokens) != 1 {
		t.Fatalf("expected 1 progress notification, got %d", len(capturedTokens))
	}
	if capturedTokens[0] != "my-progress-tok" {
		t.Errorf("progressToken = %v, want my-progress-tok", capturedTokens[0])
	}
}

func TestHandleMessage_ToolsCall_ProgressToken_FallbackToRequestID(t *testing.T) {
	s := initServer(t)

	var capturedTokens []any
	tool, _ := NewTool("progress_fallback", func(ctx context.Context, _ []byte) ([]byte, error) {
		ReportProgress(ctx, 5, 10)
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	sender := func(n *JSONRPCNotification) {
		if n.Method == methodProgress {
			if pp, ok := n.Params.(ProgressParams); ok {
				capturedTokens = append(capturedTokens, pp.ProgressToken)
			}
		}
	}

	ctx := WithNotificationSender(context.Background(), sender)

	// No _meta.progressToken — should fall back to request ID.
	data := jsonrpcReq(42, "tools/call", map[string]any{
		"name":      "progress_fallback",
		"arguments": map[string]any{},
	})

	resp, _ := s.HandleMessage(ctx, data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	if len(capturedTokens) != 1 {
		t.Fatalf("expected 1 progress notification, got %d", len(capturedTokens))
	}
	// The request ID is json.Number "42" after our custom unmarshaling.
	if capturedTokens[0] == nil {
		t.Fatal("expected non-nil progress token")
	}
	// Verify the token matches the request ID (json.Number string representation).
	if fmt.Sprint(capturedTokens[0]) != "42" {
		t.Errorf("expected progress token to be request ID 42, got %v", capturedTokens[0])
	}
}

// ── SetResponseMeta integration tests (tools/call) ──────────────────

func TestHandleMessage_ToolsCall_ResponseMeta(t *testing.T) {
	s := initServer(t)

	tool, _ := NewTool("meta_writer", func(ctx context.Context, _ []byte) ([]byte, error) {
		SetResponseMeta(ctx, "processingTimeMs", float64(42))
		SetResponseMeta(ctx, "cached", false)
		return []byte("done"), nil
	})
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "meta_writer",
		"arguments": map[string]any{},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Marshal and re-parse the result to inspect _meta.
	raw, _ := json.Marshal(resp.Result)
	var result CallToolResult
	json.Unmarshal(raw, &result)

	if result.Meta == nil {
		t.Fatal("expected response _meta")
	}
	if result.Meta["processingTimeMs"] != float64(42) {
		t.Errorf("processingTimeMs = %v, want 42", result.Meta["processingTimeMs"])
	}
	if result.Meta["cached"] != false {
		t.Errorf("cached = %v, want false", result.Meta["cached"])
	}
}

func TestHandleMessage_ToolsCall_NoResponseMeta(t *testing.T) {
	s := initServer(t)

	tool, _ := NewTool("no_resp_meta", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("plain"), nil
	})
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "no_resp_meta",
		"arguments": map[string]any{},
	})

	resp, _ := s.HandleMessage(context.Background(), data)

	raw, _ := json.Marshal(resp.Result)
	var result CallToolResult
	json.Unmarshal(raw, &result)

	if result.Meta != nil {
		t.Errorf("expected nil _meta when handler doesn't set it, got %v", result.Meta)
	}
}

// ── SetResponseMeta integration tests (resources/read) ──────────────

func TestHandleMessage_ResourcesRead_ResponseMeta(t *testing.T) {
	s := initServer(t)

	r, _ := NewResource("file:///test", "test",
		func(ctx context.Context, _ string) ([]ResourceContent, error) {
			SetResponseMeta(ctx, "format", "text")
			return []ResourceContent{NewTextResourceContent("file:///test", "hello")}, nil
		})
	s.RegisterResource(r)

	data := jsonrpcReq(1, "resources/read", map[string]any{
		"uri": "file:///test",
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ReadResourceResult
	json.Unmarshal(raw, &result)

	if result.Meta == nil {
		t.Fatal("expected response _meta on resource read")
	}
	if result.Meta["format"] != "text" {
		t.Errorf("format = %v, want text", result.Meta["format"])
	}
}

// ── SetResponseMeta integration tests (prompts/get) ─────────────────

func TestHandleMessage_PromptsGet_ResponseMeta(t *testing.T) {
	s := initServer(t)

	p, _ := NewPrompt("test_prompt",
		func(ctx context.Context, _ map[string]string) ([]PromptMessage, error) {
			SetResponseMeta(ctx, "model", "gpt-4")
			return []PromptMessage{NewUserMessage("Hello")}, nil
		})
	s.RegisterPrompt(p)

	data := jsonrpcReq(1, "prompts/get", map[string]any{
		"name": "test_prompt",
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result GetPromptResult
	json.Unmarshal(raw, &result)

	if result.Meta == nil {
		t.Fatal("expected response _meta on prompt get")
	}
	if result.Meta["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", result.Meta["model"])
	}
}

// ── _meta JSON wire format tests ────────────────────────────────────

func TestCallToolResult_MetaMarshalJSON(t *testing.T) {
	result := &CallToolResult{
		Content: []Content{TextContent{Text: "ok"}},
		Meta:    map[string]any{"timing": "fast"},
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	// Verify _meta appears in the JSON output.
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; !ok {
		t.Fatal("_meta missing from JSON output")
	}

	var meta map[string]any
	json.Unmarshal(m["_meta"], &meta)
	if meta["timing"] != "fast" {
		t.Errorf("timing = %v, want fast", meta["timing"])
	}
}

func TestCallToolResult_NoMetaMarshalJSON(t *testing.T) {
	result := &CallToolResult{
		Content: []Content{TextContent{Text: "ok"}},
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	// Verify _meta is omitted when nil.
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; ok {
		t.Fatal("_meta should be omitted when nil")
	}
}

func TestCallToolParams_MetaUnmarshalJSON(t *testing.T) {
	raw := []byte(`{"name":"tool","arguments":{},"_meta":{"progressToken":"tok"}}`)
	var params CallToolParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	if params.Meta == nil {
		t.Fatal("expected non-nil Meta on params")
	}
	if params.Meta["progressToken"] != "tok" {
		t.Errorf("progressToken = %v, want tok", params.Meta["progressToken"])
	}
}

// ── _meta on list results (wire format only — these are set by server) ──

func TestListToolsResult_MetaMarshalJSON(t *testing.T) {
	result := ListToolsResult{
		Tools: []ToolInfo{{Name: "t1", InputSchema: map[string]string{"type": "object"}}},
		Meta:  map[string]any{"page": 1},
	}

	raw, _ := json.Marshal(result)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; !ok {
		t.Fatal("_meta missing from ListToolsResult JSON")
	}
}

func TestListResourcesResult_MetaMarshalJSON(t *testing.T) {
	result := ListResourcesResult{
		Resources: []ResourceInfo{{URI: "file:///x", Name: "x"}},
		Meta:      map[string]any{"total": 100},
	}

	raw, _ := json.Marshal(result)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; !ok {
		t.Fatal("_meta missing from ListResourcesResult JSON")
	}
}

func TestGetPromptResult_MetaMarshalJSON(t *testing.T) {
	result := GetPromptResult{
		Messages: []PromptMessage{NewUserMessage("hi")},
		Meta:     map[string]any{"source": "cache"},
	}

	raw, _ := json.Marshal(result)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; !ok {
		t.Fatal("_meta missing from GetPromptResult JSON")
	}
}

func TestReadResourceResult_MetaMarshalJSON(t *testing.T) {
	result := ReadResourceResult{
		Contents: []ResourceContent{NewTextResourceContent("file:///x", "hi")},
		Meta:     map[string]any{"etag": "v1"},
	}

	raw, _ := json.Marshal(result)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; !ok {
		t.Fatal("_meta missing from ReadResourceResult JSON")
	}
}

func TestListResourceTemplatesResult_MetaMarshalJSON(t *testing.T) {
	result := ListResourceTemplatesResult{
		ResourceTemplates: []ResourceTemplateInfo{{URITemplate: "file:///{path}", Name: "files"}},
		Meta:              map[string]any{"count": 5},
	}

	raw, _ := json.Marshal(result)
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)

	if _, ok := m["_meta"]; !ok {
		t.Fatal("_meta missing from ListResourceTemplatesResult JSON")
	}
}

// ── _meta propagation to initialize ─────────────────────────────────

func TestHandleMessage_Initialize_MetaInContext(t *testing.T) {
	// Verify that _meta is parsed even during the initialize handshake.
	// We can't inspect the context directly, but we can verify the response
	// doesn't break when _meta is included in the initialize params.
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
		"_meta":           map[string]any{"clientTraceId": "trace-abc"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}
