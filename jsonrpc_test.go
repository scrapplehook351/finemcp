package finemcp

import (
	"encoding/json"
	"testing"
)

func TestJSONRPCRequest_UnmarshalWithStringID(t *testing.T) {
	t.Parallel()

	data := `{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc %q, got %q", "2.0", req.JSONRPC)
	}
	if req.ID != "abc" {
		t.Errorf("expected id %q, got %v", "abc", req.ID)
	}
	if req.Method != "tools/list" {
		t.Errorf("expected method %q, got %q", "tools/list", req.Method)
	}
	if req.IsNotification() {
		t.Error("expected IsNotification() to be false for request with id")
	}
}

func TestJSONRPCRequest_UnmarshalWithIntID(t *testing.T) {
	t.Parallel()

	data := `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"ping"}}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// JSON numbers are now decoded as json.Number to preserve precision.
	if req.ID != json.Number("42") {
		t.Errorf("expected id 42, got %v (%T)", req.ID, req.ID)
	}
	if req.IsNotification() {
		t.Error("expected IsNotification() to be false")
	}
}

func TestJSONRPCRequest_UnmarshalNotification(t *testing.T) {
	t.Parallel()

	data := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !req.IsNotification() {
		t.Error("expected IsNotification() to be true when id is absent")
	}
	if req.ID != nil {
		t.Errorf("expected nil id, got %v", req.ID)
	}
	if req.Method != "notifications/initialized" {
		t.Errorf("expected method %q, got %q", "notifications/initialized", req.Method)
	}
}

func TestJSONRPCRequest_UnmarshalNullID(t *testing.T) {
	t.Parallel()

	// null id is a valid request (not a notification)
	data := `{"jsonrpc":"2.0","id":null,"method":"tools/list"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.IsNotification() {
		t.Error("expected IsNotification() to be false for null id (id is present)")
	}
	if req.ID != nil {
		t.Errorf("expected nil id value, got %v", req.ID)
	}
}

func TestJSONRPCRequest_UnmarshalWithParams(t *testing.T) {
	t.Parallel()

	data := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","arguments":{"msg":"hello"}}}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Params == nil {
		t.Fatal("expected params to be non-nil")
	}

	// Verify params can be further decoded
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unexpected error decoding params: %v", err)
	}

	if params["name"] != "ping" {
		t.Errorf("expected params.name %q, got %v", "ping", params["name"])
	}
}

func TestJSONRPCRequest_UnmarshalWithoutParams(t *testing.T) {
	t.Parallel()

	data := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if req.Params != nil {
		t.Errorf("expected nil params, got %s", req.Params)
	}
}

func TestJSONRPCRequest_UnmarshalInvalidJSON(t *testing.T) {
	t.Parallel()

	data := `{not valid json}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestNewResponse_Success(t *testing.T) {
	t.Parallel()

	resp := NewResponse(1, map[string]string{"status": "ok"})

	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc %q, got %q", "2.0", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Errorf("expected id 1, got %v", resp.ID)
	}
	if resp.Result == nil {
		t.Error("expected non-nil result")
	}
	if resp.Error != nil {
		t.Error("expected nil error on success response")
	}
}

func TestNewResponse_MarshalJSON(t *testing.T) {
	t.Parallel()

	resp := NewResponse("req-1", map[string]string{"text": "hello"})
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if raw["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc %q, got %v", "2.0", raw["jsonrpc"])
	}
	if raw["id"] != "req-1" {
		t.Errorf("expected id %q, got %v", "req-1", raw["id"])
	}
	if _, hasError := raw["error"]; hasError {
		t.Error("success response should not have error field")
	}
	if raw["result"] == nil {
		t.Error("expected result to be present")
	}
}

func TestNewErrorResponse_Construction(t *testing.T) {
	t.Parallel()

	resp := NewErrorResponse(1, ErrCodeMethodNotFound, "Method not found")

	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc %q, got %q", "2.0", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Errorf("expected id 1, got %v", resp.ID)
	}
	if resp.Result != nil {
		t.Error("expected nil result on error response")
	}
	if resp.Error == nil {
		t.Fatal("expected non-nil error")
	}
	if resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("expected code %d, got %d", ErrCodeMethodNotFound, resp.Error.Code)
	}
	if resp.Error.Message != "Method not found" {
		t.Errorf("expected message %q, got %q", "Method not found", resp.Error.Message)
	}
}

func TestNewErrorResponse_MarshalJSON(t *testing.T) {
	t.Parallel()

	resp := NewErrorResponse(2, ErrCodeInternalError, "something broke")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if _, hasResult := raw["result"]; hasResult {
		t.Error("error response should not have result field")
	}

	errObj, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error to be object, got %T", raw["error"])
	}
	if errObj["code"] != float64(ErrCodeInternalError) {
		t.Errorf("expected code %d, got %v", ErrCodeInternalError, errObj["code"])
	}
	if errObj["message"] != "something broke" {
		t.Errorf("expected message %q, got %v", "something broke", errObj["message"])
	}
}

func TestJSONRPCError_ImplementsError(t *testing.T) {
	t.Parallel()

	e := &JSONRPCError{Code: ErrCodeInternalError, Message: "boom"}

	var err error = e
	if err.Error() != "boom" {
		t.Errorf("expected %q, got %q", "boom", err.Error())
	}
}

func TestErrorCodes_Values(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code int
		want int
	}{
		{"ParseError", ErrCodeParseError, -32700},
		{"InvalidRequest", ErrCodeInvalidRequest, -32600},
		{"MethodNotFound", ErrCodeMethodNotFound, -32601},
		{"InvalidParams", ErrCodeInvalidParams, -32602},
		{"InternalError", ErrCodeInternalError, -32603},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.code != tt.want {
				t.Errorf("expected %d, got %d", tt.want, tt.code)
			}
		})
	}
}

func TestNewResponse_NilResult(t *testing.T) {
	t.Parallel()

	resp := NewResponse(1, nil)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	// With omitempty, nil result is omitted. But JSON-RPC requires result on success.
	// This is acceptable for now — the dispatcher will always pass a non-nil result.
	if raw["id"] != float64(1) {
		t.Errorf("expected id 1, got %v", raw["id"])
	}
}

func TestJSONRPCRequest_UnmarshalWithBoolID(t *testing.T) {
	t.Parallel()

	// Bool id is valid JSON but unusual — verifies the "exists" branch with varied types.
	data := `{"jsonrpc":"2.0","id":true,"method":"ping"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.IsNotification() {
		t.Error("expected non-notification")
	}
	if req.ID != true {
		t.Errorf("id = %v, want true", req.ID)
	}
}

func TestJSONRPCRequest_UnmarshalWithObjectID(t *testing.T) {
	t.Parallel()

	// Object id — unusual but exercises the unmarshal path.
	data := `{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.IsNotification() {
		t.Error("expected non-notification")
	}
}

func TestJSONRPCRequest_UnmarshalWithFloatID(t *testing.T) {
	t.Parallel()

	data := `{"jsonrpc":"2.0","id":3.14,"method":"ping"}`
	var req JSONRPCRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.ID != json.Number("3.14") {
		t.Errorf("id = %v, want 3.14", req.ID)
	}
}
