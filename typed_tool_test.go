package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// --- Struct input/output ---

type greetInput struct {
	Name string `json:"name"`
}

type greetOutput struct {
	Message string `json:"message"`
}

func TestNewTypedTool_StructInStructOut(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("greet",
		func(_ context.Context, in greetInput) (greetOutput, error) {
			return greetOutput{Message: "Hello, " + in.Name + "!"}, nil
		},
		WithDescription("Greets someone"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "greet" {
		t.Errorf("name = %q, want %q", tool.Name, "greet")
	}
	if tool.Description != "Greets someone" {
		t.Errorf("desc = %q", tool.Description)
	}

	// Execute via the raw handler.
	input, _ := json.Marshal(greetInput{Name: "World"})
	out, err := tool.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	var result greetOutput
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if result.Message != "Hello, World!" {
		t.Errorf("message = %q", result.Message)
	}
}

// --- Primitive output (string) ---

func TestNewTypedTool_StringOutput(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("echo",
		func(_ context.Context, in greetInput) (string, error) {
			return "echo: " + in.Name, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(greetInput{Name: "test"})
	out, err := tool.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	var s string
	if err := json.Unmarshal(out, &s); err != nil {
		t.Fatal(err)
	}
	if s != "echo: test" {
		t.Errorf("got %q", s)
	}
}

// --- Numeric input/output ---

type addInput struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

func TestNewTypedTool_NumericTypes(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("add",
		func(_ context.Context, in addInput) (float64, error) {
			return in.A + in.B, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(addInput{A: 3, B: 4})
	out, err := tool.Handler(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	var result float64
	json.Unmarshal(out, &result)
	if result != 7 {
		t.Errorf("result = %f, want 7", result)
	}
}

// --- Handler returns error ---

func TestNewTypedTool_HandlerError(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("fail",
		func(_ context.Context, _ greetInput) (string, error) {
			return "", errors.New("something broke")
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(greetInput{Name: "world"})
	_, err = tool.Handler(context.Background(), input)
	if err == nil || err.Error() != "something broke" {
		t.Errorf("err = %v, want 'something broke'", err)
	}
}

// --- Invalid JSON input ---

func TestNewTypedTool_InvalidInput(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("greet",
		func(_ context.Context, in greetInput) (string, error) {
			return in.Name, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Handler(context.Background(), []byte("{bad json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid input") {
		t.Errorf("err = %q, should contain 'invalid input'", err)
	}
}

// --- Nil/empty input ---

func TestNewTypedTool_NilInput(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("noop",
		func(_ context.Context, in greetInput) (string, error) {
			return "name=" + in.Name, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// nil input → zero value of In used.
	out, err := tool.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var s string
	json.Unmarshal(out, &s)
	if s != "name=" {
		t.Errorf("got %q, want 'name='", s)
	}
}

func TestNewTypedTool_EmptyInput(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("noop",
		func(_ context.Context, in greetInput) (string, error) {
			return "name=" + in.Name, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Empty slice → zero value of In used.
	out, err := tool.Handler(context.Background(), []byte{})
	if err != nil {
		t.Fatal(err)
	}

	var s string
	json.Unmarshal(out, &s)
	if s != "name=" {
		t.Errorf("got %q", s)
	}
}

// --- Nil handler ---

func TestNewTypedTool_NilHandler(t *testing.T) {
	t.Parallel()

	_, err := NewTypedTool[greetInput, string]("test", nil)
	if err == nil {
		t.Fatal("expected error for nil handler")
	}
	if !errors.Is(err, errToolHandlerNil) {
		t.Errorf("err = %v, want errToolHandlerNil", err)
	}
}

// --- Invalid tool name ---

func TestNewTypedTool_InvalidName(t *testing.T) {
	t.Parallel()

	_, err := NewTypedTool("has space",
		func(_ context.Context, _ greetInput) (string, error) {
			return "", nil
		},
	)
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !errors.Is(err, errToolNameChars) {
		t.Errorf("err = %v, want errToolNameChars", err)
	}
}

// --- Options pass through ---

func TestNewTypedTool_WithOptions(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	tool, err := NewTypedTool("greet",
		func(_ context.Context, in greetInput) (string, error) {
			return in.Name, nil
		},
		WithDescription("A greeting tool"),
		WithInputSchema(schema),
		WithRoles("admin"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if tool.Description != "A greeting tool" {
		t.Errorf("description = %q", tool.Description)
	}
	if tool.InputSchema == nil {
		t.Error("schema is nil")
	}
	if len(tool.Roles) != 1 || tool.Roles[0] != "admin" {
		t.Errorf("roles = %v", tool.Roles)
	}
}

// --- End-to-end via Server.CallTool ---

func TestNewTypedTool_EndToEnd(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	tool, err := NewTypedTool("greet",
		func(_ context.Context, in greetInput) (greetOutput, error) {
			return greetOutput{Message: "Hi, " + in.Name}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterTool(tool)

	input, _ := json.Marshal(greetInput{Name: "Alice"})
	result, err := s.CallTool(context.Background(), "greet", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("expected success")
	}

	tc, ok := result.Content[0].(TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}

	// CallTool wraps raw output as text — the JSON-encoded greetOutput.
	var out greetOutput
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("unmarshal output: %v (text was %q)", err, tc.Text)
	}
	if out.Message != "Hi, Alice" {
		t.Errorf("message = %q", out.Message)
	}
}

// --- End-to-end via JSON-RPC dispatch ---

func TestNewTypedTool_EndToEnd_Dispatch(t *testing.T) {
	s := NewServer("test", "1.0")

	tool, err := NewTypedTool("add",
		func(_ context.Context, in addInput) (float64, error) {
			return in.A + in.B, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterTool(tool)

	// Initialize.
	initMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
		},
	})
	s.HandleMessage(context.Background(), initMsg)

	// Call the typed tool.
	callMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "add",
			"arguments": map[string]any{"a": 10, "b": 20},
		},
	})
	resp, err := s.HandleMessage(context.Background(), callMsg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "30") {
		t.Errorf("result = %s, expected to contain 30", raw)
	}
}

// --- Auto-schema generation integration ---

func TestNewTypedTool_AutoSchema_Generated(t *testing.T) {
	t.Parallel()

	type In struct {
		Name string `json:"name" description:"Person to greet"`
		Age  int    `json:"age,omitempty"`
	}

	tool, err := NewTypedTool("greet",
		func(_ context.Context, in In) (string, error) {
			return "hi", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("InputSchema type = %T, want map[string]any", tool.InputSchema)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}

	props := schema["properties"].(map[string]any)
	nameProp := props["name"].(map[string]any)
	if nameProp["type"] != "string" {
		t.Errorf("name type = %v, want string", nameProp["type"])
	}
	if nameProp["description"] != "Person to greet" {
		t.Errorf("name description = %v, want 'Person to greet'", nameProp["description"])
	}

	ageProp := props["age"].(map[string]any)
	if ageProp["type"] != "integer" {
		t.Errorf("age type = %v, want integer", ageProp["type"])
	}

	// "name" should be required, "age" (omitempty) should not.
	req := schema["required"].([]string)
	found := false
	for _, r := range req {
		if r == "name" {
			found = true
		}
		if r == "age" {
			t.Error("age should not be required (omitempty)")
		}
	}
	if !found {
		t.Error("name should be required")
	}
}

func TestNewTypedTool_AutoSchema_OverriddenByWithInputSchema(t *testing.T) {
	t.Parallel()

	type In struct {
		Name string `json:"name"`
	}

	manual := map[string]any{"type": "custom"}

	tool, err := NewTypedTool("test",
		func(_ context.Context, in In) (string, error) {
			return "", nil
		},
		WithInputSchema(manual),
	)
	if err != nil {
		t.Fatal(err)
	}

	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("InputSchema type = %T, want map[string]any", tool.InputSchema)
	}
	if schema["type"] != "custom" {
		t.Errorf("manual schema should override auto: got type=%v", schema["type"])
	}
}

func TestNewTypedTool_AutoSchema_EmptyStruct(t *testing.T) {
	t.Parallel()

	tool, err := NewTypedTool("noop",
		func(_ context.Context, in struct{}) (string, error) {
			return "ok", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("InputSchema type = %T, want map[string]any", tool.InputSchema)
	}
	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}
	props := schema["properties"].(map[string]any)
	if len(props) != 0 {
		t.Errorf("expected no properties for empty struct, got %d", len(props))
	}
}

func TestNewTypedTool_AutoSchema_VisibleInToolsList(t *testing.T) {
	t.Parallel()

	type In struct {
		Query string `json:"query" description:"Search query"`
	}

	s := NewServer("test", "1.0")
	tool, _ := NewTypedTool("search",
		func(_ context.Context, in In) (string, error) {
			return "results", nil
		},
		WithDescription("Searches things"),
	)
	s.RegisterTool(tool)

	// Initialize.
	initReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
		},
	})
	s.HandleMessage(context.Background(), initReq)

	// List tools.
	listReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	resp, _ := s.HandleMessage(context.Background(), listReq)
	if resp.Error != nil {
		t.Fatalf("error: %s", resp.Error.Message)
	}

	// Verify the schema appears in the response.
	data, _ := json.Marshal(resp.Result)
	result := string(data)

	if !strings.Contains(result, `"query"`) {
		t.Errorf("tools/list should contain auto-generated schema property 'query', got: %s", result)
	}
	if !strings.Contains(result, `"Search query"`) {
		t.Errorf("tools/list should contain description 'Search query', got: %s", result)
	}
	if !strings.Contains(result, `"string"`) {
		t.Errorf("tools/list should contain type 'string', got: %s", result)
	}
}
