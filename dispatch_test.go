package finemcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// helper: builds a valid JSON-RPC request string.
func jsonrpcReq(id any, method string, params any) []byte {
	m := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	data, _ := json.Marshal(m)
	return data
}

// helper: creates a server and runs the initialize handshake.
func initServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatalf("initialize: unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize: unexpected response error: %s", resp.Error.Message)
	}

	return s
}

// --- Parse / validation errors ---

func TestHandleMessage_InvalidJSON(t *testing.T) {
	s := NewServer("test", "1.0")

	resp, err := s.HandleMessage(context.Background(), []byte("{bad"))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil || resp.Error.Code != ErrCodeParseError {
		t.Errorf("expected parse error, got %+v", resp)
	}
}

func TestHandleMessage_WrongVersion(t *testing.T) {
	s := NewServer("test", "1.0")

	data := []byte(`{"jsonrpc":"1.0","id":1,"method":"ping"}`)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidRequest {
		t.Errorf("expected invalid request, got %+v", resp)
	}
}

func TestHandleMessage_EmptyMethod(t *testing.T) {
	s := NewServer("test", "1.0")

	data := []byte(`{"jsonrpc":"2.0","id":1,"method":""}`)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidRequest {
		t.Errorf("expected invalid request, got %+v", resp)
	}
}

// --- Initialize handshake ---

func TestHandleMessage_Initialize(t *testing.T) {
	s := NewServer("finemcp", "0.1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Verify result structure.
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, ProtocolVersion)
	}
	if result.ServerInfo.Name != "finemcp" {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, "finemcp")
	}
	if result.ServerInfo.Version != "0.1.0" {
		t.Errorf("serverInfo.version = %q, want %q", result.ServerInfo.Version, "0.1.0")
	}
	if result.Capabilities.Tools == nil {
		t.Error("capabilities.tools should not be nil")
	}
	// Without a notification sender in ctx, listChanged must be false.
	if result.Capabilities.Tools.ListChanged {
		t.Error("tools.listChanged should be false when no notification channel is available")
	}
	if result.Capabilities.Resources.ListChanged {
		t.Error("resources.listChanged should be false when no notification channel is available")
	}
	if result.Capabilities.Prompts.ListChanged {
		t.Error("prompts.listChanged should be false when no notification channel is available")
	}
}

func TestHandleMessage_Initialize_WithNotifChan(t *testing.T) {
	s := NewServer("finemcp", "0.1.0", WithResourceSubscriptions())

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	// Both a NotificationSender and a SubscriberID are required for push capabilities.
	ctx := WithNotificationSender(context.Background(), func(_ *JSONRPCNotification) {})
	ctx = WithSubscriberID(ctx, "client-1")
	resp, err := s.HandleMessage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// With a notification sender, push capabilities must be advertised.
	if !result.Capabilities.Tools.ListChanged {
		t.Error("tools.listChanged should be true when notification channel is available")
	}
	if !result.Capabilities.Resources.ListChanged {
		t.Error("resources.listChanged should be true when notification channel is available")
	}
	if !result.Capabilities.Prompts.ListChanged {
		t.Error("prompts.listChanged should be true when notification channel is available")
	}
	if !result.Capabilities.Resources.Subscribe {
		t.Error("resources.subscribe should be true when subscriptions are enabled and channel is available")
	}
}

func TestHandleMessage_Initialize_SenderWithoutSubscriberID(t *testing.T) {
	// A NotificationSender alone is not sufficient; a stable SubscriberID is also
	// required before the server advertises push capabilities.
	s := NewServer("finemcp", "0.1.0", WithResourceSubscriptions())

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	// Inject only a sender, no subscriber ID.
	ctx := WithNotificationSender(context.Background(), func(_ *JSONRPCNotification) {})
	resp, err := s.HandleMessage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.Capabilities.Tools.ListChanged {
		t.Error("tools.listChanged should be false when no subscriber ID")
	}
	if result.Capabilities.Resources.ListChanged {
		t.Error("resources.listChanged should be false when no subscriber ID")
	}
	if result.Capabilities.Resources.Subscribe {
		t.Error("resources.subscribe should be false when no subscriber ID")
	}
}

func TestHandleMessage_InitializeTwice(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(2, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for double initialize")
	}
	if resp.Error.Code != ErrCodeInvalidRequest {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrCodeInvalidRequest)
	}
}

func TestHandleMessage_InitializeInvalidParams(t *testing.T) {
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", "not-an-object")
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("expected invalid params, got %+v", resp)
	}
}

// --- Pre-initialize gate ---

func TestHandleMessage_MethodBeforeInit(t *testing.T) {
	s := NewServer("test", "1.0")

	for _, method := range []string{"tools/list", "tools/call", "ping"} {
		data := jsonrpcReq(1, method, nil)
		resp, err := s.HandleMessage(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Error == nil {
			t.Errorf("%s: expected error before initialize", method)
			continue
		}
		if resp.Error.Code != ErrCodeInvalidRequest {
			t.Errorf("%s: code = %d, want %d", method, resp.Error.Code, ErrCodeInvalidRequest)
		}
	}
}

// --- Notifications ---

func TestHandleMessage_InitializedNotification(t *testing.T) {
	s := initServer(t)

	// notifications/initialized has no id field.
	data := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Error("notification should return nil response")
	}
}

func TestHandleMessage_UnknownNotification(t *testing.T) {
	s := NewServer("test", "1.0")

	data := []byte(`{"jsonrpc":"2.0","method":"unknown/notification"}`)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Error("unknown notification should return nil response")
	}
}

// --- Ping ---

func TestHandleMessage_Ping(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(42, "ping", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	// JSON numbers are decoded as json.Number to preserve precision.
	if resp.ID != json.Number("42") {
		t.Errorf("id = %v (%T), want 42", resp.ID, resp.ID)
	}
}

// --- Unknown method ---

func TestHandleMessage_UnknownMethod(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(1, "some/unknown/method", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("expected method not found, got %+v", resp)
	}
}

// --- resources/subscribe & resources/unsubscribe ---

func TestHandleMessage_ResourcesSubscribe_Disabled(t *testing.T) {
	s := initServer(t) // Default server disables subscriptions.
	data := jsonrpcReq(1, "resources/subscribe", map[string]any{"uri": "file:///test"})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("expected MethodNotFound when disabled, got: %+v", resp)
	}
}

func TestHandleMessage_ResourcesSubscribe_NoNotifChan(t *testing.T) {
	// Subscriptions are enabled, but the transport provides no notification sender.
	s := NewServer("test", "1.0", WithResourceSubscriptions())
	s.initialized.Store(true)

	data := jsonrpcReq(1, "resources/subscribe", map[string]any{"uri": "file:///test"})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidRequest {
		t.Errorf("expected InvalidRequest when no notification channel, got: %+v", resp)
	}
}

func TestHandleMessage_ResourcesSubscribe_Success(t *testing.T) {
	s := NewServer("test", "1.0", WithResourceSubscriptions())
	s.initialized.Store(true)

	data := jsonrpcReq(1, "resources/subscribe", map[string]any{"uri": "file:///test"})

	// Inject sender and subscriber ID so the subscription is actually tracked.
	ctx := WithSubscriberID(context.Background(), "client-1")
	ctx = WithNotificationSender(ctx, func(_ *JSONRPCNotification) {})

	resp, err := s.HandleMessage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	if resp.Result == nil {
		t.Error("expected empty result object, got nil")
	}
}

func TestHandleMessage_ResourcesUnsubscribe_Success(t *testing.T) {
	s := NewServer("test", "1.0", WithResourceSubscriptions())
	s.initialized.Store(true)

	data := jsonrpcReq(1, "resources/unsubscribe", map[string]any{"uri": "file:///test"})

	ctx := WithSubscriberID(context.Background(), "client-1")
	resp, err := s.HandleMessage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error.Message)
	}
	if resp.Result == nil {
		t.Error("expected empty result object, got nil")
	}
}

func TestHandleMessage_ResourcesUnsubscribe_NoSubscriberID(t *testing.T) {
	s := NewServer("test", "1.0", WithResourceSubscriptions())
	s.initialized.Store(true)

	data := jsonrpcReq(1, "resources/unsubscribe", map[string]any{"uri": "file:///test"})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidRequest {
		t.Errorf("expected InvalidRequest when no subscriber ID, got: %+v", resp)
	}
}

// --- tools/list ---

func TestHandleMessage_ToolsList_Empty(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(1, "tools/list", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
}

func TestHandleMessage_ToolsList_WithTools(t *testing.T) {
	s := initServer(t)

	echo, _ := NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	}, WithDescription("echoes input"))

	if err := s.RegisterTool(echo); err != nil {
		t.Fatal(err)
	}

	data := jsonrpcReq(1, "tools/list", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "echo" {
		t.Errorf("name = %q, want %q", result.Tools[0].Name, "echo")
	}
	if result.Tools[0].Description != "echoes input" {
		t.Errorf("description = %q, want %q", result.Tools[0].Description, "echoes input")
	}
}

func TestHandleMessage_ToolsList_DefaultInputSchema(t *testing.T) {
	s := initServer(t)

	noop, _ := NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(noop)

	data := jsonrpcReq(1, "tools/list", nil)
	resp, _ := s.HandleMessage(context.Background(), data)

	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	json.Unmarshal(raw, &result)

	schemaBytes, _ := json.Marshal(result.Tools[0].InputSchema)
	if string(schemaBytes) != `{"type":"object"}` {
		t.Errorf("default inputSchema = %s, want {\"type\":\"object\"}", schemaBytes)
	}
}

func TestHandleMessage_ToolsList_WithAnnotations(t *testing.T) {
	s := initServer(t)

	getter, _ := NewTool("get_data", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, WithReadOnly(), WithIdempotent(), WithTitle("Get Data"))
	s.RegisterTool(getter)

	deleter, _ := NewTool("delete_item", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, WithDestructive())
	s.RegisterTool(deleter)

	plain, _ := NewTool("plain_tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(plain)

	data := jsonrpcReq(1, "tools/list", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// Tools are sorted by name: delete_item, get_data, plain_tool.
	if len(result.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(result.Tools))
	}

	// delete_item — destructive only.
	di := result.Tools[0]
	if di.Name != "delete_item" {
		t.Fatalf("expected delete_item first, got %q", di.Name)
	}
	if di.Annotations == nil {
		t.Fatal("delete_item: annotations should not be nil")
	}
	if di.Annotations.DestructiveHint == nil || !*di.Annotations.DestructiveHint {
		t.Error("delete_item: DestructiveHint should be true")
	}
	if di.Annotations.ReadOnlyHint != nil {
		t.Error("delete_item: ReadOnlyHint should be nil")
	}

	// get_data — readOnly + idempotent + title.
	gd := result.Tools[1]
	if gd.Name != "get_data" {
		t.Fatalf("expected get_data second, got %q", gd.Name)
	}
	if gd.Annotations == nil {
		t.Fatal("get_data: annotations should not be nil")
	}
	if gd.Annotations.ReadOnlyHint == nil || !*gd.Annotations.ReadOnlyHint {
		t.Error("get_data: ReadOnlyHint should be true")
	}
	if gd.Annotations.IdempotentHint == nil || !*gd.Annotations.IdempotentHint {
		t.Error("get_data: IdempotentHint should be true")
	}
	if gd.Annotations.Title != "Get Data" {
		t.Errorf("get_data: Title = %q, want %q", gd.Annotations.Title, "Get Data")
	}

	// plain_tool — no annotations.
	pt := result.Tools[2]
	if pt.Name != "plain_tool" {
		t.Fatalf("expected plain_tool third, got %q", pt.Name)
	}
	if pt.Annotations != nil {
		t.Errorf("plain_tool: annotations should be nil, got %+v", pt.Annotations)
	}
}

func TestHandleMessage_ToolsList_AnnotationsRoundTrip(t *testing.T) {
	s := initServer(t)

	// Explicit false should survive JSON round-trip (not be omitted).
	tool, _ := NewTool("explicit_false", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, WithAnnotations(ToolAnnotations{
		ReadOnlyHint:    BoolPtr(false),
		DestructiveHint: BoolPtr(true),
	}))
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/list", nil)
	resp, _ := s.HandleMessage(context.Background(), data)

	raw, _ := json.Marshal(resp.Result)

	// Check raw JSON contains explicit false.
	js := string(raw)
	if !strings.Contains(js, `"readOnlyHint":false`) {
		t.Errorf("explicit false should survive round-trip: %s", js)
	}
	if !strings.Contains(js, `"destructiveHint":true`) {
		t.Errorf("destructiveHint should be present: %s", js)
	}
}

// --- tools/call ---

func TestHandleMessage_ToolsCall_Success(t *testing.T) {
	s := initServer(t)

	echo, _ := NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(echo)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hello"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Check via raw JSON since Content is a sealed interface.
	raw, _ := json.Marshal(resp.Result)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["isError"]; ok {
		t.Error("expected no isError field for success")
	}
	if string(result["content"]) == "[]" || string(result["content"]) == "null" {
		t.Fatal("expected non-empty content")
	}
}

func TestHandleMessage_ToolsCall_NotFound(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name": "nonexistent",
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing tool")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_ToolsCall_MissingParams(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(1, "tools/call", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("expected invalid params, got %+v", resp)
	}
}

func TestHandleMessage_ToolsCall_EmptyName(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name": "",
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("expected invalid params for empty name, got %+v", resp)
	}
}

func TestHandleMessage_ToolsCall_HandlerError(t *testing.T) {
	s := initServer(t)

	failing, _ := NewTool("fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, context.DeadlineExceeded
	})
	s.RegisterTool(failing)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name": "fail",
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatal("handler errors should not be protocol errors")
	}

	// Check isError via raw JSON.
	raw, _ := json.Marshal(resp.Result)
	var result map[string]json.RawMessage
	json.Unmarshal(raw, &result)

	if string(result["isError"]) != "true" {
		t.Errorf("expected isError=true, got %s", result["isError"])
	}
}

func TestHandleMessage_ToolsCall_NoArguments(t *testing.T) {
	s := initServer(t)

	noop, _ := NewTool("noop", func(_ context.Context, input []byte) ([]byte, error) {
		if len(input) != 0 {
			t.Errorf("expected empty input, got %s", input)
		}
		return []byte("ok"), nil
	})
	s.RegisterTool(noop)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name": "noop",
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

// --- ID propagation ---

func TestHandleMessage_StringID(t *testing.T) {
	s := initServer(t)

	data := jsonrpcReq("req-abc", "ping", nil)
	resp, _ := s.HandleMessage(context.Background(), data)

	if resp.ID != "req-abc" {
		t.Errorf("id = %v, want %q", resp.ID, "req-abc")
	}
}

func TestHandleMessage_ToolsCall_InvalidParamsJSON(t *testing.T) {
	s := initServer(t)

	// Params is present but not a valid CallToolParams object.
	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"not-an-object"}`)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("expected invalid params, got %+v", resp)
	}
}

// --- Cancellation ---

func TestHandleMessage_Cancellation(t *testing.T) {
	s := initServer(t)

	started := make(chan struct{})
	blocker, _ := NewTool("blocker", func(ctx context.Context, _ []byte) ([]byte, error) {
		close(started) // signal that the handler is running
		<-ctx.Done()   // block until cancelled
		return []byte(ctx.Err().Error()), nil
	})
	s.RegisterTool(blocker)

	reqData := jsonrpcReq(123, "tools/call", map[string]any{
		"name": "blocker",
	})

	// Use a timeout context so the goroutine is guaranteed to exit
	// even if the cancellation notification fails to unblock it.
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reqCancel()

	doneCh := make(chan struct{})
	go func() {
		_, err := s.HandleMessage(reqCtx, reqData)
		if err != nil {
			t.Errorf("unexpected error from blocking call: %v", err)
		}
		close(doneCh)
	}()

	// Wait for the handler to start, which guarantees the request is tracked.
	<-started

	cancelData := []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":123}}`)
	resp, err := s.HandleMessage(context.Background(), cancelData)
	if err != nil {
		t.Fatalf("unexpected error from cancel: %v", err)
	}
	if resp != nil {
		t.Fatalf("notifications should not return a response, got %+v", resp)
	}

	select {
	case <-doneCh:
		// success: the tool returned gracefully after cancellation.
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for tool to return after cancellation")
	}
}

// ── Tool composition integration tests ──────────────────────────────

func TestHandleMessage_ToolsCall_PipelineComposed(t *testing.T) {
	s := initServer(t)

	// Register a pipeline tool: upper → reverse.
	tool, _ := NewTool("upper_reverse",
		Pipeline(
			func(_ context.Context, in []byte) ([]byte, error) {
				return []byte(strings.ToUpper(string(in))), nil
			},
			func(_ context.Context, in []byte) ([]byte, error) {
				b := make([]byte, len(in))
				for i, c := range in {
					b[len(in)-1-i] = c
				}
				return b, nil
			},
		),
		WithDescription("Upper then reverse"),
	)
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/call", map[string]any{
		"name":      "upper_reverse",
		"arguments": json.RawMessage(`{}`),
	})

	// We need raw input, so let's call with the tool's raw handler via dispatch.
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// The tool handler receives the arguments JSON (empty object {}).
	// Pipeline: upper("{}") → "{}" → reverse → "}{".
	raw, _ := json.Marshal(resp.Result)
	js := string(raw)
	// Verify it's a successful result (contains content, no isError).
	if strings.Contains(js, `"isError":true`) {
		t.Errorf("expected success, got error result: %s", js)
	}
	if !strings.Contains(js, `"content"`) {
		t.Errorf("expected content in result: %s", js)
	}
}

func TestHandleMessage_ToolsCall_ParallelComposed(t *testing.T) {
	s := initServer(t)

	tool, _ := NewTool("multi_check",
		Parallel(
			NamedHandler{Name: "echo", Handler: func(_ context.Context, in []byte) ([]byte, error) {
				return in, nil
			}},
			NamedHandler{Name: "len", Handler: func(_ context.Context, in []byte) ([]byte, error) {
				return []byte(strings.Repeat("x", len(in))), nil
			}},
		),
		WithDescription("Run checks in parallel"),
	)
	s.RegisterTool(tool)

	data := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "multi_check",
		"arguments": json.RawMessage(`{"msg":"hi"}`),
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	js := string(raw)
	// The result should be a successful CallToolResult whose text contains
	// the parallel JSON with both "echo" and "len" keys.
	if strings.Contains(js, `"isError":true`) {
		t.Errorf("expected success, got error result: %s", js)
	}
	if !strings.Contains(js, "echo") || !strings.Contains(js, "len") {
		t.Errorf("expected both echo and len in output, got: %s", js)
	}
}

func TestHandleMessage_ToolsList_ComposedToolVisible(t *testing.T) {
	s := initServer(t)

	tool, _ := NewTool("pipeline_tool",
		Pipeline(
			func(_ context.Context, in []byte) ([]byte, error) { return in, nil },
			func(_ context.Context, in []byte) ([]byte, error) { return in, nil },
		),
		WithDescription("A composed pipeline tool"),
		WithReadOnly(),
	)
	s.RegisterTool(tool)

	data := jsonrpcReq(1, "tools/list", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, ti := range result.Tools {
		if ti.Name == "pipeline_tool" {
			found = true
			if ti.Description != "A composed pipeline tool" {
				t.Errorf("description = %q", ti.Description)
			}
			if ti.Annotations == nil || ti.Annotations.ReadOnlyHint == nil || !*ti.Annotations.ReadOnlyHint {
				t.Error("expected ReadOnlyHint to be true")
			}
		}
	}
	if !found {
		t.Error("pipeline_tool not found in tools/list")
	}
}

// --- Extended server info in initialize response ---

func TestHandleMessage_Initialize_ExtendedServerInfo(t *testing.T) {
	s := NewServer("myserver", "2.0",
		WithServerTitle("My Server"),
		WithServerDescription("A test server"),
		WithWebsiteURL("https://myserver.dev"),
		WithIcons(
			Icon{Src: "https://myserver.dev/icon.png", MimeType: "image/png", Sizes: []string{"64x64"}},
		),
	)

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.ServerInfo.Name != "myserver" {
		t.Errorf("name = %q, want %q", result.ServerInfo.Name, "myserver")
	}
	if result.ServerInfo.Title != "My Server" {
		t.Errorf("title = %q, want %q", result.ServerInfo.Title, "My Server")
	}
	if result.ServerInfo.Description != "A test server" {
		t.Errorf("description = %q, want %q", result.ServerInfo.Description, "A test server")
	}
	if result.ServerInfo.WebsiteURL != "https://myserver.dev" {
		t.Errorf("websiteUrl = %q, want %q", result.ServerInfo.WebsiteURL, "https://myserver.dev")
	}
	if len(result.ServerInfo.Icons) != 1 {
		t.Fatalf("expected 1 icon, got %d", len(result.ServerInfo.Icons))
	}
	if result.ServerInfo.Icons[0].Src != "https://myserver.dev/icon.png" {
		t.Errorf("icon src = %q", result.ServerInfo.Icons[0].Src)
	}
	if result.ServerInfo.Icons[0].MimeType != "image/png" {
		t.Errorf("icon mimeType = %q", result.ServerInfo.Icons[0].MimeType)
	}
}

func TestHandleMessage_Initialize_Instructions(t *testing.T) {
	s := NewServer("test", "1.0",
		WithInstructions("Always use JSON output format."),
	)

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.Instructions != "Always use JSON output format." {
		t.Errorf("instructions = %q, want %q", result.Instructions, "Always use JSON output format.")
	}
}

func TestHandleMessage_Initialize_NoInstructions(t *testing.T) {
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(resp.Result)

	// instructions should be omitted when empty.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawMap["instructions"]; ok {
		t.Error("instructions should be omitted when not set")
	}
}

func TestHandleMessage_Initialize_LoggingCapability(t *testing.T) {
	s := NewServer("test", "1.0")
	s.SetLogHandler(func(_ context.Context, _ LogLevel) error {
		return nil
	})

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.Capabilities.Logging == nil {
		t.Error("logging capability should be advertised when log handler is set")
	}
}

func TestHandleMessage_Initialize_NoLoggingCapability(t *testing.T) {
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.Capabilities.Logging != nil {
		t.Error("logging capability should NOT be advertised when no log handler is set")
	}
}
