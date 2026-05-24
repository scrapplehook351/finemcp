package middleware_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

// passHandler is a tool handler that always succeeds.
func passHandler(_ context.Context, input []byte) ([]byte, error) {
	return []byte("ok"), nil
}

// initAndCall creates a server, registers the tool with Validation middleware,
// initializes, and calls the tool with the given arguments JSON.
func initAndCall(t *testing.T, tool *finemcp.Tool, argsJSON string) *finemcp.JSONRPCResponse {
	t.Helper()
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Validation())
	if err := s.RegisterTool(tool); err != nil {
		t.Fatal(err)
	}

	initReq := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	resp, _ := s.HandleMessage(context.Background(), []byte(initReq))
	if resp.Error != nil {
		t.Fatalf("init failed: %s", resp.Error.Message)
	}

	callReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool.Name},
	}
	if argsJSON != "" {
		var args json.RawMessage
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			t.Fatalf("bad test args JSON: %v", err)
		}
		callReq["params"] = map[string]any{"name": tool.Name, "arguments": args}
	}

	raw, _ := json.Marshal(callReq)
	resp, _ = s.HandleMessage(context.Background(), raw)
	return resp
}

func TestValidation_ValidInput_Passes(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
	}
	tool, _ := finemcp.NewTypedTool("greet", func(_ context.Context, in In) (string, error) {
		return "hi " + in.Name, nil
	})
	resp := initAndCall(t, tool, `{"name": "Alice"}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestValidation_MissingRequiredField(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"name": "Alice"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	result := string(raw)
	if !strings.Contains(result, "missing required") {
		t.Errorf("expected 'missing required' in result, got: %s", result)
	}
	if !strings.Contains(result, "age") {
		t.Errorf("expected 'age' in error message, got: %s", result)
	}
}

func TestValidation_WrongType_String(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"name": 42}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected string") {
		t.Errorf("expected type error for 'name', got: %s", raw)
	}
}

func TestValidation_WrongType_Integer(t *testing.T) {
	t.Parallel()
	type In struct {
		Count int `json:"count"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"count": "five"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected integer") {
		t.Errorf("expected type error for 'count', got: %s", raw)
	}
}

func TestValidation_WrongType_Number(t *testing.T) {
	t.Parallel()
	type In struct {
		Value float64 `json:"value"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"value": true}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected number") {
		t.Errorf("expected type error for 'value', got: %s", raw)
	}
}

func TestValidation_WrongType_Boolean(t *testing.T) {
	t.Parallel()
	type In struct {
		Active bool `json:"active"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"active": "yes"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected boolean") {
		t.Errorf("expected type error for 'active', got: %s", raw)
	}
}

func TestValidation_WrongType_Object(t *testing.T) {
	t.Parallel()
	type Addr struct {
		City string `json:"city"`
	}
	type In struct {
		Addr Addr `json:"addr"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"addr": "not-an-object"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected object") {
		t.Errorf("expected type error for 'addr', got: %s", raw)
	}
}

func TestValidation_WrongType_Array(t *testing.T) {
	t.Parallel()
	type In struct {
		Tags []string `json:"tags"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"tags": "not-array"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected array") {
		t.Errorf("expected type error for 'tags', got: %s", raw)
	}
}

func TestValidation_NestedObject_Invalid(t *testing.T) {
	t.Parallel()
	type Addr struct {
		City string `json:"city"`
		Zip  int    `json:"zip"`
	}
	type In struct {
		Name string `json:"name"`
		Addr Addr   `json:"addr"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"name": "Alice", "addr": {"city": "NYC", "zip": "bad"}}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "addr.zip") {
		t.Errorf("expected nested path 'addr.zip' in error, got: %s", raw)
	}
}

func TestValidation_Integer_AcceptsWholeFloat(t *testing.T) {
	t.Parallel()
	// Use a raw tool to isolate the validation middleware behaviour.
	// Go's json.Unmarshal rejects 42.0→int, but validation should accept it.
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"count": map[string]any{"type": "integer"},
			},
			"required": []string{"count"},
		}),
	)
	resp := initAndCall(t, tool, `{"count": 42.0}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), "isError") && strings.Contains(string(raw), "true") {
		t.Errorf("42.0 should be accepted as integer, got: %s", raw)
	}
}

func TestValidation_Integer_RejectsNonWholeFloat(t *testing.T) {
	t.Parallel()
	type In struct {
		Count int `json:"count"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"count": 42.5}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "expected integer") {
		t.Errorf("42.5 should fail integer check, got: %s", raw)
	}
}

func TestValidation_Number_AcceptsInteger(t *testing.T) {
	t.Parallel()
	type In struct {
		Value float64 `json:"value"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "ok", nil
	})
	resp := initAndCall(t, tool, `{"value": 42}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), "isError") && strings.Contains(string(raw), "true") {
		t.Errorf("integer should be accepted as number, got: %s", raw)
	}
}

func TestValidation_OptionalField_CanBeOmitted(t *testing.T) {
	t.Parallel()
	type In struct {
		Name  string `json:"name"`
		Label string `json:"label,omitempty"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "ok", nil
	})
	resp := initAndCall(t, tool, `{"name": "Alice"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), "isError") && strings.Contains(string(raw), "true") {
		t.Errorf("optional field omitted should pass, got: %s", raw)
	}
}

func TestValidation_SkipValidation_Bypasses(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("raw", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []string{"name"},
		}),
		finemcp.WithValidation(false),
	)
	resp := initAndCall(t, tool, `{"name": 999}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), "expected string") {
		t.Errorf("validation should be skipped but got validation error: %s", raw)
	}
}

func TestValidation_NoSchema_Passes(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("raw", passHandler)
	resp := initAndCall(t, tool, `{"anything": "goes"}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), "isError") && strings.Contains(string(raw), "true") {
		t.Errorf("no schema should mean no validation, got: %s", raw)
	}
}

func TestValidation_EmptyInput_WithRequired(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "missing required") {
		t.Errorf("expected 'missing required' error, got: %s", raw)
	}
}

func TestValidation_NullInput_WithRequired(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, "")
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "missing required") || !strings.Contains(string(raw), "name") {
		t.Errorf("expected 'missing required: name' error, got: %s", raw)
	}
}

func TestValidation_ArrayItems_WrongType(t *testing.T) {
	t.Parallel()
	type In struct {
		Tags []string `json:"tags"`
	}
	tool, _ := finemcp.NewTypedTool("test", func(_ context.Context, in In) (string, error) {
		return "", nil
	})
	resp := initAndCall(t, tool, `{"tags": ["ok", 42]}`)
	if resp.Error != nil {
		t.Fatalf("protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "tags[1]") {
		t.Errorf("expected 'tags[1]' in error path, got: %s", raw)
	}
}

// ── enum ────────────────────────────────────────────────────────────

func TestValidation_Enum_StringAllowed(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"color": map[string]any{"type": "string", "enum": []any{"red", "green", "blue"}},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"color": "red"}`)
	assertNoValidationError(t, resp)
}

func TestValidation_Enum_StringDenied(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"color": map[string]any{"type": "string", "enum": []any{"red", "green", "blue"}},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"color": "yellow"}`)
	assertValidationContains(t, resp, "must be one of")
}

func TestValidation_Enum_NumberAllowed(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"level": map[string]any{"type": "integer", "enum": []any{float64(1), float64(2), float64(3)}},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"level": 2}`)
	assertNoValidationError(t, resp)
}

func TestValidation_Enum_NumberDenied(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"level": map[string]any{"type": "integer", "enum": []any{float64(1), float64(2), float64(3)}},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"level": 9}`)
	assertValidationContains(t, resp, "must be one of")
}

// ── pattern ─────────────────────────────────────────────────────────

func TestValidation_Pattern_Matches(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"email": map[string]any{"type": "string", "pattern": `^[a-z]+@[a-z]+\.[a-z]+$`},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"email": "a@b.c"}`)
	assertNoValidationError(t, resp)
}

func TestValidation_Pattern_NoMatch(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"email": map[string]any{"type": "string", "pattern": `^[a-z]+@[a-z]+\.[a-z]+$`},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"email": "not-an-email"}`)
	assertValidationContains(t, resp, "does not match pattern")
}

func TestValidation_Pattern_InvalidRegex(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"val": map[string]any{"type": "string", "pattern": `[invalid`},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"val": "anything"}`)
	assertValidationContains(t, resp, "invalid pattern")
}

// ── minLength / maxLength ───────────────────────────────────────────

func TestValidation_MinLength_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "minLength": float64(3)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"name": "abc"}`)
	assertNoValidationError(t, resp)
}

func TestValidation_MinLength_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "minLength": float64(3)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"name": "ab"}`)
	assertValidationContains(t, resp, "less than minimum 3")
}

func TestValidation_MaxLength_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{"type": "string", "maxLength": float64(5)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"code": "abc"}`)
	assertNoValidationError(t, resp)
}

func TestValidation_MaxLength_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{"type": "string", "maxLength": float64(3)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"code": "abcdef"}`)
	assertValidationContains(t, resp, "exceeds maximum 3")
}

func TestValidation_MinLength_Unicode(t *testing.T) {
	t.Parallel()
	// "日本語" is 3 runes but 9 bytes — should count as 3.
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "minLength": float64(3)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"text": "日本語"}`)
	assertNoValidationError(t, resp)
}

// ── minimum / maximum / exclusiveMinimum / exclusiveMaximum ─────────

func TestValidation_Minimum_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"age": map[string]any{"type": "integer", "minimum": float64(0)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"age": 0}`)
	assertNoValidationError(t, resp)
}

func TestValidation_Minimum_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"age": map[string]any{"type": "integer", "minimum": float64(0)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"age": -1}`)
	assertValidationContains(t, resp, "less than minimum")
}

func TestValidation_Maximum_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"score": map[string]any{"type": "number", "maximum": float64(100)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"score": 100}`)
	assertNoValidationError(t, resp)
}

func TestValidation_Maximum_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"score": map[string]any{"type": "number", "maximum": float64(100)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"score": 100.1}`)
	assertValidationContains(t, resp, "exceeds maximum")
}

func TestValidation_ExclusiveMinimum_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rate": map[string]any{"type": "number", "exclusiveMinimum": float64(0)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"rate": 0.001}`)
	assertNoValidationError(t, resp)
}

func TestValidation_ExclusiveMinimum_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rate": map[string]any{"type": "number", "exclusiveMinimum": float64(0)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"rate": 0}`)
	assertValidationContains(t, resp, "must be greater than")
}

func TestValidation_ExclusiveMaximum_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"temp": map[string]any{"type": "number", "exclusiveMaximum": float64(100)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"temp": 99.9}`)
	assertNoValidationError(t, resp)
}

func TestValidation_ExclusiveMaximum_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"temp": map[string]any{"type": "number", "exclusiveMaximum": float64(100)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"temp": 100}`)
	assertValidationContains(t, resp, "must be less than")
}

// ── minItems / maxItems / uniqueItems ───────────────────────────────

func TestValidation_MinItems_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": float64(1)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"tags": ["a"]}`)
	assertNoValidationError(t, resp)
}

func TestValidation_MinItems_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": float64(2)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"tags": ["a"]}`)
	assertValidationContains(t, resp, "less than minItems")
}

func TestValidation_MaxItems_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": float64(3)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"tags": ["a","b","c"]}`)
	assertNoValidationError(t, resp)
}

func TestValidation_MaxItems_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": float64(2)},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"tags": ["a","b","c"]}`)
	assertValidationContains(t, resp, "exceeds maxItems")
}

func TestValidation_UniqueItems_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "uniqueItems": true},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"ids": [1, 2, 3]}`)
	assertNoValidationError(t, resp)
}

func TestValidation_UniqueItems_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "uniqueItems": true},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"ids": [1, 2, 1]}`)
	assertValidationContains(t, resp, "duplicate")
}

// ── additionalProperties ────────────────────────────────────────────

func TestValidation_AdditionalProperties_False_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		}),
	)
	resp := initAndCall(t, tool, `{"name": "Alice"}`)
	assertNoValidationError(t, resp)
}

func TestValidation_AdditionalProperties_False_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		}),
	)
	resp := initAndCall(t, tool, `{"name": "Alice", "extra": 42}`)
	assertValidationContains(t, resp, "additional property not allowed")
}

func TestValidation_AdditionalProperties_Schema_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"additionalProperties": map[string]any{"type": "integer"},
		}),
	)
	resp := initAndCall(t, tool, `{"name": "Alice", "count": 5}`)
	assertNoValidationError(t, resp)
}

func TestValidation_AdditionalProperties_Schema_Fail(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"additionalProperties": map[string]any{"type": "integer"},
		}),
	)
	resp := initAndCall(t, tool, `{"name": "Alice", "extra": "string-value"}`)
	assertValidationContains(t, resp, "expected integer")
}

// ── $ref / $defs ────────────────────────────────────────────────────

func TestValidation_Ref_Defs_Pass(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"addr": map[string]any{"$ref": "#/$defs/address"},
			},
			"$defs": map[string]any{
				"address": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
						"zip":  map[string]any{"type": "string", "pattern": `^\d{5}$`},
					},
					"required": []any{"city"},
				},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"addr": {"city": "NYC", "zip": "10001"}}`)
	assertNoValidationError(t, resp)
}

func TestValidation_Ref_Defs_MissingRequired(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"addr": map[string]any{"$ref": "#/$defs/address"},
			},
			"$defs": map[string]any{
				"address": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"addr": {}}`)
	assertValidationContains(t, resp, "missing required")
}

func TestValidation_Ref_Defs_PatternViolation(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"addr": map[string]any{"$ref": "#/$defs/address"},
			},
			"$defs": map[string]any{
				"address": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
						"zip":  map[string]any{"type": "string", "pattern": `^\d{5}$`},
					},
					"required": []any{"city"},
				},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"addr": {"city": "NYC", "zip": "bad"}}`)
	assertValidationContains(t, resp, "does not match pattern")
}

func TestValidation_Ref_InArrayItems(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":  "array",
					"items": map[string]any{"$ref": "#/$defs/item"},
				},
			},
			"$defs": map[string]any{
				"item": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":  map[string]any{"type": "string"},
						"price": map[string]any{"type": "number", "minimum": float64(0)},
					},
					"required": []any{"name"},
				},
			},
		}),
	)
	resp := initAndCall(t, tool, `{"items": [{"name": "a", "price": 10}, {"name": "b", "price": -1}]}`)
	assertValidationContains(t, resp, "less than minimum")
}

func TestValidation_Ref_UnresolvableIgnored(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x": map[string]any{"$ref": "#/$defs/nope"},
			},
		}),
	)
	// Unresolvable ref should not crash; pass through without errors.
	resp := initAndCall(t, tool, `{"x": "anything"}`)
	assertNoValidationError(t, resp)
}

// ── Combined constraints ────────────────────────────────────────────

func TestValidation_Combined_StringPatternAndLength(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":      "string",
					"pattern":   `^[A-Z]+$`,
					"minLength": float64(2),
					"maxLength": float64(5),
				},
			},
		}),
	)

	// Valid: pattern OK, length OK.
	resp := initAndCall(t, tool, `{"code": "ABC"}`)
	assertNoValidationError(t, resp)

	// Too short.
	resp = initAndCall(t, tool, `{"code": "A"}`)
	assertValidationContains(t, resp, "less than minimum 2")

	// Too long.
	resp = initAndCall(t, tool, `{"code": "ABCDEF"}`)
	assertValidationContains(t, resp, "exceeds maximum 5")

	// Pattern mismatch.
	resp = initAndCall(t, tool, `{"code": "abc"}`)
	assertValidationContains(t, resp, "does not match pattern")
}

func TestValidation_Combined_IntegerRangeAndEnum(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"level": map[string]any{
					"type": "integer",
					"enum": []any{float64(1), float64(2), float64(3)},
				},
			},
		}),
	)
	// Enum wins: 5 is an integer but not in the enum.
	resp := initAndCall(t, tool, `{"level": 5}`)
	assertValidationContains(t, resp, "must be one of")
}

func TestValidation_Combined_ArrayWithAllConstraints(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string", "minLength": float64(1)},
					"minItems":    float64(1),
					"maxItems":    float64(3),
					"uniqueItems": true,
				},
			},
		}),
	)

	// Valid.
	resp := initAndCall(t, tool, `{"tags": ["a", "b"]}`)
	assertNoValidationError(t, resp)

	// Empty array (violates minItems).
	resp = initAndCall(t, tool, `{"tags": []}`)
	assertValidationContains(t, resp, "less than minItems")

	// Too many (violates maxItems).
	resp = initAndCall(t, tool, `{"tags": ["a", "b", "c", "d"]}`)
	assertValidationContains(t, resp, "exceeds maxItems")

	// Duplicates.
	resp = initAndCall(t, tool, `{"tags": ["a", "a"]}`)
	assertValidationContains(t, resp, "duplicate")

	// Item too short (violates item minLength).
	resp = initAndCall(t, tool, `{"tags": [""]}`)
	assertValidationContains(t, resp, "less than minimum 1")
}

func TestValidation_NestedRef_AdditionalProperties(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"config": map[string]any{"$ref": "#/$defs/config"},
			},
			"$defs": map[string]any{
				"config": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key": map[string]any{"type": "string"},
					},
					"additionalProperties": false,
				},
			},
		}),
	)

	resp := initAndCall(t, tool, `{"config": {"key": "val"}}`)
	assertNoValidationError(t, resp)

	resp = initAndCall(t, tool, `{"config": {"key": "val", "extra": 1}}`)
	assertValidationContains(t, resp, "additional property not allowed")
}

// ── Chained $ref (Finding 1) ───────────────────────────────────────

func TestValidation_Ref_ChainedDefs(t *testing.T) {
	t.Parallel()
	// identifier → nonEmptyString → concrete type.
	// Before fix, only one hop was followed so the second $ref was never
	// resolved, silently passing any value without type checks.
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"$ref": "#/$defs/identifier"},
			},
			"$defs": map[string]any{
				"identifier":     map[string]any{"$ref": "#/$defs/nonEmptyString"},
				"nonEmptyString": map[string]any{"type": "string", "minLength": float64(1)},
			},
		}),
	)

	// Valid — should pass.
	resp := initAndCall(t, tool, `{"id": "abc"}`)
	assertNoValidationError(t, resp)

	// Integer where string is expected — must fail.
	resp = initAndCall(t, tool, `{"id": 42}`)
	assertValidationContains(t, resp, "expected string")

	// Empty string — violates minLength.
	resp = initAndCall(t, tool, `{"id": ""}`)
	assertValidationContains(t, resp, "less than minimum 1")
}

func TestValidation_Ref_ThreeHopChain(t *testing.T) {
	t.Parallel()
	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"val": map[string]any{"$ref": "#/$defs/a"},
			},
			"$defs": map[string]any{
				"a": map[string]any{"$ref": "#/$defs/b"},
				"b": map[string]any{"$ref": "#/$defs/c"},
				"c": map[string]any{"type": "integer", "minimum": float64(0)},
			},
		}),
	)

	resp := initAndCall(t, tool, `{"val": 5}`)
	assertNoValidationError(t, resp)

	resp = initAndCall(t, tool, `{"val": -1}`)
	assertValidationContains(t, resp, "less than minimum")

	resp = initAndCall(t, tool, `{"val": "text"}`)
	assertValidationContains(t, resp, "expected integer")
}

// ── Deep nesting without $ref (Finding 2) ───────────────────────────

func TestValidation_Ref_DeepNesting(t *testing.T) {
	t.Parallel()
	// Build a schema with 20 levels of nested objects; the innermost
	// property uses a $ref. Before the fix, structural nesting consumed
	// the depth budget and the ref at the bottom was silently skipped.
	innermost := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"leaf": map[string]any{"$ref": "#/$defs/posInt"},
		},
		"required": []any{"leaf"},
	}
	current := innermost
	for i := 0; i < 20; i++ {
		current = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"child": current,
			},
			"required": []any{"child"},
		}
	}
	current["$defs"] = map[string]any{
		"posInt": map[string]any{"type": "integer", "minimum": float64(1)},
	}

	tool, _ := finemcp.NewTool("test", passHandler,
		finemcp.WithInputSchema(current),
	)

	// Build a valid deeply-nested input.
	var inner any = map[string]any{"leaf": float64(10)}
	for i := 0; i < 20; i++ {
		inner = map[string]any{"child": inner}
	}
	inputBytes, _ := json.Marshal(inner)
	resp := initAndCall(t, tool, string(inputBytes))
	assertNoValidationError(t, resp)

	// Supply a string where integer is expected at the deepest level.
	var badInner any = map[string]any{"leaf": "oops"}
	for i := 0; i < 20; i++ {
		badInner = map[string]any{"child": badInner}
	}
	badInputBytes, _ := json.Marshal(badInner)
	resp = initAndCall(t, tool, string(badInputBytes))
	assertValidationContains(t, resp, "expected integer")
}

// ── Helpers ─────────────────────────────────────────────────────────

// assertNoValidationError checks that the response has no protocol error and
// no tool-level validation error (isError: true).
func assertNoValidationError(t *testing.T, resp *finemcp.JSONRPCResponse) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	s := string(raw)
	if strings.Contains(s, `"isError":true`) || strings.Contains(s, "validation failed") {
		t.Errorf("expected no validation error, got: %s", s)
	}
}

// assertValidationContains checks that the response contains a validation
// error with the given substring.
func assertValidationContains(t *testing.T, resp *finemcp.JSONRPCResponse, substr string) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	s := string(raw)
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q in validation output, got: %s", substr, s)
	}
}
