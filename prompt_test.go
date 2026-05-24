package finemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// ── NewPrompt ───────────────────────────────────────────────────────

func TestNewPrompt(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return []PromptMessage{NewUserMessage("hello")}, nil
	}

	p, err := NewPrompt("greet", handler, WithPromptDescription("A greeting prompt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "greet" {
		t.Errorf("name = %q, want %q", p.Name, "greet")
	}
	if p.Description != "A greeting prompt" {
		t.Errorf("description = %q, want %q", p.Description, "A greeting prompt")
	}
}

func TestNewPrompt_EmptyName(t *testing.T) {
	t.Parallel()
	_, err := NewPrompt("", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	})
	if !errors.Is(err, errPromptNameEmpty) {
		t.Errorf("got %v, want errPromptNameEmpty", err)
	}
}

func TestNewPrompt_NilHandler(t *testing.T) {
	t.Parallel()
	_, err := NewPrompt("test", nil)
	if !errors.Is(err, errPromptHandlerNil) {
		t.Errorf("got %v, want errPromptHandlerNil", err)
	}
}

func TestNewPrompt_WithArguments(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	}

	p, err := NewPrompt("test", handler, WithPromptArguments(
		PromptArgument{Name: "name", Description: "User name", Required: true},
		PromptArgument{Name: "tone", Description: "Greeting tone"},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.Arguments) != 2 {
		t.Fatalf("expected 2 arguments, got %d", len(p.Arguments))
	}
	if p.Arguments[0].Name != "name" || !p.Arguments[0].Required {
		t.Errorf("first arg: %+v", p.Arguments[0])
	}
}

// ── PromptMessage ───────────────────────────────────────────────────

func TestNewUserMessage(t *testing.T) {
	t.Parallel()
	m := NewUserMessage("hello")
	if m.Role != "user" {
		t.Errorf("role = %q, want %q", m.Role, "user")
	}
	tc, ok := m.Content.(TextContent)
	if !ok {
		t.Fatalf("content type = %T, want TextContent", m.Content)
	}
	if tc.Text != "hello" {
		t.Errorf("text = %q, want %q", tc.Text, "hello")
	}
}

func TestNewAssistantMessage(t *testing.T) {
	t.Parallel()
	m := NewAssistantMessage("hi")
	if m.Role != "assistant" {
		t.Errorf("role = %q, want %q", m.Role, "assistant")
	}
}

func TestPromptMessage_MarshalJSON(t *testing.T) {
	t.Parallel()
	m := NewUserMessage("hello")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check role
	var role string
	if err := json.Unmarshal(raw["role"], &role); err != nil {
		t.Fatalf("unmarshal role: %v", err)
	}
	if role != "user" {
		t.Errorf("role = %q, want %q", role, "user")
	}

	// Check content has type and text
	var content map[string]string
	if err := json.Unmarshal(raw["content"], &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if content["type"] != "text" {
		t.Errorf("content.type = %q, want %q", content["type"], "text")
	}
	if content["text"] != "hello" {
		t.Errorf("content.text = %q, want %q", content["text"], "hello")
	}
}

// ── Server prompt registration ──────────────────────────────────────

func TestRegisterPrompt(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	p, _ := NewPrompt("greet", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	})
	if err := s.RegisterPrompt(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompts := s.ListPrompts()
	if len(prompts) != 1 {
		t.Fatalf("prompt count = %d, want 1", len(prompts))
	}
}

func TestRegisterPrompt_Nil(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	if err := s.RegisterPrompt(nil); !errors.Is(err, errPromptNil) {
		t.Errorf("got %v, want errPromptNil", err)
	}
}

func TestRegisterPrompt_Duplicate(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	p, _ := NewPrompt("dup", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	})
	s.RegisterPrompt(p)
	if err := s.RegisterPrompt(p); !errors.Is(err, errPromptAlreadyExists) {
		t.Errorf("got %v, want errPromptAlreadyExists", err)
	}
}

func TestRegisterPrompt_EmptyName(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	if err := s.RegisterPrompt(&Prompt{}); !errors.Is(err, errPromptNameEmpty) {
		t.Errorf("got %v, want errPromptNameEmpty", err)
	}
}

func TestRegisterPrompt_NilHandler(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	if err := s.RegisterPrompt(&Prompt{Name: "test"}); !errors.Is(err, errPromptHandlerNil) {
		t.Errorf("got %v, want errPromptHandlerNil", err)
	}
}

func TestGetPrompt(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	p, _ := NewPrompt("greet", func(_ context.Context, args map[string]string) ([]PromptMessage, error) {
		return []PromptMessage{NewUserMessage("Hello, " + args["name"] + "!")}, nil
	}, WithPromptDescription("Greets someone"))
	s.RegisterPrompt(p)

	result, err := s.GetPrompt(context.Background(), "greet", map[string]string{"name": "World"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Description != "Greets someone" {
		t.Errorf("description = %q, want %q", result.Description, "Greets someone")
	}
	if len(result.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(result.Messages))
	}
	tc, ok := result.Messages[0].Content.(TextContent)
	if !ok {
		t.Fatalf("content type = %T, want TextContent", result.Messages[0].Content)
	}
	if tc.Text != "Hello, World!" {
		t.Errorf("text = %q, want %q", tc.Text, "Hello, World!")
	}
}

func TestGetPrompt_NotFound(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_, err := s.GetPrompt(context.Background(), "nonexistent", nil)
	if !errors.Is(err, errPromptNotFound) {
		t.Errorf("got %v, want errPromptNotFound", err)
	}
}

func TestGetPrompt_NilMessagesNormalized(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	p, _ := NewPrompt("empty", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil // handler returns nil slice
	})
	s.RegisterPrompt(p)

	result, err := s.GetPrompt(context.Background(), "empty", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Messages == nil {
		t.Fatal("Messages should be non-nil empty slice, got nil")
	}
	if len(result.Messages) != 0 {
		t.Errorf("Messages length = %d, want 0", len(result.Messages))
	}

	// Verify JSON marshals as [] not null.
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"messages":[]`)) {
		t.Errorf("expected messages:[], got %s", data)
	}
}

func TestGetPrompt_TooManyMessages(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	// Register a prompt handler that returns more than maxPromptMessages (1000).
	p, _ := NewPrompt("overflow", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		messages := make([]PromptMessage, 1001)
		for i := range messages {
			messages[i] = NewUserMessage("text")
		}
		return messages, nil
	})
	s.RegisterPrompt(p)

	_, err := s.GetPrompt(context.Background(), "overflow", nil)
	if err == nil {
		t.Fatal("expected error for too many messages")
	}
	if got := err.Error(); got != "prompt returned too many messages (max 1000)" {
		t.Errorf("error = %q, want %q", got, "prompt returned too many messages (max 1000)")
	}
}

// ── HandleMessage dispatch ──────────────────────────────────────────

func TestHandleMessage_PromptsList(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	p, _ := NewPrompt("greet", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	}, WithPromptDescription("A greeting"), WithPromptArguments(
		PromptArgument{Name: "name", Required: true},
	))
	s.RegisterPrompt(p)
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "prompts/list", nil)
	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result ListPromptsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(result.Prompts) != 1 {
		t.Fatalf("prompt count = %d, want 1", len(result.Prompts))
	}
	if result.Prompts[0].Name != "greet" {
		t.Errorf("name = %q, want %q", result.Prompts[0].Name, "greet")
	}
	if result.Prompts[0].Description != "A greeting" {
		t.Errorf("description = %q, want %q", result.Prompts[0].Description, "A greeting")
	}
	if len(result.Prompts[0].Arguments) != 1 {
		t.Fatalf("argument count = %d, want 1", len(result.Prompts[0].Arguments))
	}
}

func TestHandleMessage_PromptsGet(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	p, _ := NewPrompt("greet", func(_ context.Context, args map[string]string) ([]PromptMessage, error) {
		return []PromptMessage{NewUserMessage("Hello, " + args["name"] + "!")}, nil
	})
	s.RegisterPrompt(p)
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "prompts/get", map[string]any{
		"name":      "greet",
		"arguments": map[string]string{"name": "World"},
	})
	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result struct {
		Messages []struct {
			Role    string `json:"role"`
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(result.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Content.Text != "Hello, World!" {
		t.Errorf("text = %q, want %q", result.Messages[0].Content.Text, "Hello, World!")
	}
}

func TestHandleMessage_PromptsGet_NotFound(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "prompts/get", map[string]any{"name": "nonexistent"})
	resp, _ := s.HandleMessage(context.Background(), msg)

	if resp.Error == nil {
		t.Fatal("expected error for nonexistent prompt")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_PromptsGet_EmptyName(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "prompts/get", map[string]any{"name": ""})
	resp, _ := s.HandleMessage(context.Background(), msg)

	if resp.Error == nil {
		t.Fatal("expected error for empty prompt name")
	}
}

func TestHandleMessage_PromptsGet_MissingParams(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "prompts/get", nil)
	resp, _ := s.HandleMessage(context.Background(), msg)

	if resp.Error == nil {
		t.Fatal("expected error for missing params")
	}
}

func TestHandleMessage_PromptsGet_HandlerError(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	p, _ := NewPrompt("bad", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, errors.New("handler exploded")
	})
	s.RegisterPrompt(p)
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "prompts/get", map[string]any{"name": "bad"})
	resp, _ := s.HandleMessage(context.Background(), msg)

	if resp.Error == nil {
		t.Fatal("expected error from handler")
	}
	if resp.Error.Code != ErrCodeInternalError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrCodeInternalError)
	}
}

func TestHandleMessage_PromptsBeforeInit(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	// Not initialized

	msg := jsonrpcReq(1, "prompts/list", nil)
	resp, _ := s.HandleMessage(context.Background(), msg)

	if resp.Error == nil {
		t.Fatal("expected error before init")
	}
}

func TestHandleMessage_Initialize_AdvertisesPrompts(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	msg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if result.Capabilities.Prompts == nil {
		t.Fatal("expected prompts capability to be advertised")
	}
}
