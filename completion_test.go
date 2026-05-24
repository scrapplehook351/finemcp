package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ── Unit tests for CompleteHandler / types ───────────────────────────

func TestCompletionResult_Defaults(t *testing.T) {
	t.Parallel()
	r := CompletionResult{}
	if r.HasMore {
		t.Error("HasMore should default to false")
	}
	if r.Total != 0 {
		t.Error("Total should default to 0")
	}
	if r.Values != nil {
		t.Error("Values should default to nil")
	}
}

func TestCompleteResult_JSON(t *testing.T) {
	t.Parallel()
	cr := CompleteResult{
		Completion: CompletionResult{
			Values:  []string{"python", "pytorch"},
			Total:   5,
			HasMore: true,
		},
	}
	data, err := json.Marshal(cr)
	if err != nil {
		t.Fatal(err)
	}

	var decoded CompleteResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Completion.Values) != 2 {
		t.Errorf("expected 2 values, got %d", len(decoded.Completion.Values))
	}
	if decoded.Completion.Values[0] != "python" {
		t.Errorf("first value: got %q, want %q", decoded.Completion.Values[0], "python")
	}
	if decoded.Completion.Total != 5 {
		t.Errorf("total: got %d, want 5", decoded.Completion.Total)
	}
	if !decoded.Completion.HasMore {
		t.Error("hasMore should be true")
	}
}

func TestRefTypeConstants(t *testing.T) {
	t.Parallel()
	if RefTypePrompt != "ref/prompt" {
		t.Errorf("RefTypePrompt = %q", RefTypePrompt)
	}
	if RefTypeResource != "ref/resource" {
		t.Errorf("RefTypeResource = %q", RefTypeResource)
	}
}

// ── Dispatch integration tests ──────────────────────────────────────

// langCompleter returns completions for a "language" argument matching
// the given prefix.
func langCompleter(_ context.Context, req CompleteRequest) (*CompletionResult, error) {
	all := []string{"python", "javascript", "java", "go", "rust", "ruby"}
	var matches []string
	for _, lang := range all {
		if strings.HasPrefix(lang, req.Argument.Value) {
			matches = append(matches, lang)
		}
	}
	return &CompletionResult{
		Values: matches,
		Total:  len(matches),
	}, nil
}

// serverWithPromptCompleter creates a test server with a prompt that has
// a completer attached.
func serverWithPromptCompleter(t *testing.T) *Server {
	t.Helper()
	s := initServer(t)

	p, err := NewPrompt("code_review", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return []PromptMessage{NewUserMessage("review")}, nil
	},
		WithPromptDescription("Code review prompt"),
		WithPromptArguments(
			PromptArgument{Name: "language", Description: "Programming language", Required: true},
		),
		WithCompleter(langCompleter),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterPrompt(p); err != nil {
		t.Fatal(err)
	}
	return s
}

// serverWithTemplateCompleter creates a test server with a resource template
// that has a completer attached.
func serverWithTemplateCompleter(t *testing.T) *Server {
	t.Helper()
	s := initServer(t)

	tmpl, err := NewResourceTemplate("file:///logs/{date}.log", "log-file",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "log content")}, nil
		},
		WithTemplateDescription("Log files by date"),
		WithTemplateCompleter(func(_ context.Context, req CompleteRequest) (*CompletionResult, error) {
			// Suggest recent dates starting with the prefix.
			dates := []string{"2026-03-01", "2026-03-02", "2026-03-03"}
			var matches []string
			for _, d := range dates {
				if strings.HasPrefix(d, req.Argument.Value) {
					matches = append(matches, d)
				}
			}
			return &CompletionResult{Values: matches, Total: len(matches)}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResourceTemplate(tmpl); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHandleMessage_CompletionComplete_PromptSuccess(t *testing.T) {
	t.Parallel()
	s := serverWithPromptCompleter(t)

	data := jsonrpcReq(10, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "code_review",
		},
		"argument": map[string]any{
			"name":  "language",
			"value": "py",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Completion.Values) != 1 || result.Completion.Values[0] != "python" {
		t.Errorf("values: got %v, want [python]", result.Completion.Values)
	}
	if result.Completion.Total != 1 {
		t.Errorf("total: got %d, want 1", result.Completion.Total)
	}
}

func TestHandleMessage_CompletionComplete_PromptMultipleMatches(t *testing.T) {
	t.Parallel()
	s := serverWithPromptCompleter(t)

	data := jsonrpcReq(11, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "code_review",
		},
		"argument": map[string]any{
			"name":  "language",
			"value": "ja",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// "ja" matches "javascript" and "java"
	if len(result.Completion.Values) != 2 {
		t.Errorf("expected 2 values, got %d: %v", len(result.Completion.Values), result.Completion.Values)
	}
	if result.Completion.Total != 2 {
		t.Errorf("total: got %d, want 2", result.Completion.Total)
	}
}

func TestHandleMessage_CompletionComplete_PromptEmptyPrefix(t *testing.T) {
	t.Parallel()
	s := serverWithPromptCompleter(t)

	data := jsonrpcReq(12, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "code_review",
		},
		"argument": map[string]any{
			"name":  "language",
			"value": "",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// Empty prefix should match all 6 languages.
	if len(result.Completion.Values) != 6 {
		t.Errorf("expected 6 values, got %d", len(result.Completion.Values))
	}
}

func TestHandleMessage_CompletionComplete_PromptNoMatch(t *testing.T) {
	t.Parallel()
	s := serverWithPromptCompleter(t)

	data := jsonrpcReq(13, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "code_review",
		},
		"argument": map[string]any{
			"name":  "language",
			"value": "zzz",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Completion.Values) != 0 {
		t.Errorf("expected 0 values, got %d", len(result.Completion.Values))
	}
}

func TestHandleMessage_CompletionComplete_ResourceTemplateSuccess(t *testing.T) {
	t.Parallel()
	s := serverWithTemplateCompleter(t)

	data := jsonrpcReq(20, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/resource",
			"uri":  "file:///logs/{date}.log",
		},
		"argument": map[string]any{
			"name":  "date",
			"value": "2026-03",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Completion.Values) != 3 {
		t.Errorf("expected 3 values, got %d", len(result.Completion.Values))
	}
}

func TestHandleMessage_CompletionComplete_ResourceTemplateViaName(t *testing.T) {
	t.Parallel()
	s := serverWithTemplateCompleter(t)

	// Some clients send the template URI in "name" instead of "uri".
	data := jsonrpcReq(21, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/resource",
			"name": "file:///logs/{date}.log",
		},
		"argument": map[string]any{
			"name":  "date",
			"value": "2026-03-0",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// "2026-03-0" matches "2026-03-01", "2026-03-02", "2026-03-03"
	if len(result.Completion.Values) != 3 {
		t.Errorf("expected 3 values, got %d", len(result.Completion.Values))
	}
}

// ── Error cases ─────────────────────────────────────────────────────

func TestHandleMessage_CompletionComplete_MissingParams(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(30, "completion/complete", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing params")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("code: got %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_CompletionComplete_InvalidParams(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(31, "completion/complete", "not-an-object")
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
}

func TestHandleMessage_CompletionComplete_MissingArgumentName(t *testing.T) {
	t.Parallel()
	s := serverWithPromptCompleter(t)

	data := jsonrpcReq(32, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "code_review",
		},
		"argument": map[string]any{
			"name":  "",
			"value": "py",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for empty argument name")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("code: got %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_CompletionComplete_UnsupportedRefType(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(33, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/unknown",
			"name": "something",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for unsupported ref type")
	}
	if !strings.Contains(resp.Error.Message, "unsupported ref type") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleMessage_CompletionComplete_PromptNotFound(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(34, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "nonexistent",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent prompt")
	}
	if !strings.Contains(resp.Error.Message, "prompt not found") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleMessage_CompletionComplete_ResourceTemplateNotFound(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(35, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/resource",
			"uri":  "file:///nonexistent/{id}",
		},
		"argument": map[string]any{
			"name":  "id",
			"value": "123",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent template")
	}
	if !strings.Contains(resp.Error.Message, "resource template not found") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleMessage_CompletionComplete_PromptMissingRefName(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(36, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing ref name")
	}
	if !strings.Contains(resp.Error.Message, "ref.name is required") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleMessage_CompletionComplete_ResourceMissingRefURI(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	data := jsonrpcReq(37, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/resource",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing ref uri")
	}
	if !strings.Contains(resp.Error.Message, "ref.uri") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

// ── No completer registered ─────────────────────────────────────────

func TestHandleMessage_CompletionComplete_NoCompleterRegistered(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	// Register a prompt without a completer.
	p, err := NewPrompt("basic_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return []PromptMessage{NewUserMessage("hi")}, nil
	},
		WithPromptArguments(PromptArgument{Name: "name", Required: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterPrompt(p); err != nil {
		t.Fatal(err)
	}

	data := jsonrpcReq(40, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "basic_prompt",
		},
		"argument": map[string]any{
			"name":  "name",
			"value": "test",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// No completer → empty values.
	if len(result.Completion.Values) != 0 {
		t.Errorf("expected 0 values, got %d", len(result.Completion.Values))
	}
}

// ── Handler error propagation ───────────────────────────────────────

func TestHandleMessage_CompletionComplete_HandlerError(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	p, _ := NewPrompt("error_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return nil, context.DeadlineExceeded
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(50, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "error_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error from handler failure")
	}
	if resp.Error.Code != ErrCodeInternalError {
		t.Errorf("code: got %d, want %d", resp.Error.Code, ErrCodeInternalError)
	}
}

// ── Handler returns nil result ──────────────────────────────────────

func TestHandleMessage_CompletionComplete_NilResult(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	p, _ := NewPrompt("nil_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return nil, nil
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(51, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "nil_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	// nil result → empty values array.
	if result.Completion.Values == nil {
		t.Error("values should be non-nil empty slice, not nil")
	}
	if len(result.Completion.Values) != 0 {
		t.Errorf("expected 0 values, got %d", len(result.Completion.Values))
	}
}

// ── Capability advertising ──────────────────────────────────────────

func TestHandleMessage_Initialize_CompletionCapability(t *testing.T) {
	t.Parallel()

	// Server with a completer → should advertise completions.
	s := NewServer("test", "1.0")
	p, _ := NewPrompt("p", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(langCompleter),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Capabilities.Completions == nil {
		t.Error("expected completions capability to be advertised")
	}
}

func TestHandleMessage_Initialize_NoCompletionCapability(t *testing.T) {
	t.Parallel()

	// Server without any completers → should NOT advertise completions.
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Capabilities.Completions != nil {
		t.Error("completions capability should NOT be advertised when no completers are registered")
	}
}

func TestHandleMessage_Initialize_TemplateCompletionCapability(t *testing.T) {
	t.Parallel()

	// Server with a template completer → should advertise completions.
	s := NewServer("test", "1.0")
	tmpl, _ := NewResourceTemplate("db:///{table}", "db-table",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return nil, nil
		},
		WithTemplateCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return &CompletionResult{Values: []string{"users"}}, nil
		}),
	)
	_ = s.RegisterResourceTemplate(tmpl)

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	_ = json.Unmarshal(raw, &result)
	if result.Capabilities.Completions == nil {
		t.Error("expected completions capability for template completer")
	}
}

// ── Before initialization ───────────────────────────────────────────

func TestHandleMessage_CompletionComplete_BeforeInit(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "some_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "val",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error before initialization")
	}
	if !strings.Contains(resp.Error.Message, "not initialized") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

// ── HasMore / Total fields ──────────────────────────────────────────

func TestHandleMessage_CompletionComplete_HasMoreAndTotal(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	p, _ := NewPrompt("paginated", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return &CompletionResult{
				Values:  []string{"a", "b", "c"},
				Total:   100,
				HasMore: true,
			}, nil
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(60, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "paginated",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "",
		},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(raw, &result)

	if len(result.Completion.Values) != 3 {
		t.Errorf("values: got %d, want 3", len(result.Completion.Values))
	}
	if result.Completion.Total != 100 {
		t.Errorf("total: got %d, want 100", result.Completion.Total)
	}
	if !result.Completion.HasMore {
		t.Error("hasMore should be true")
	}
}

// ── Context cancellation ────────────────────────────────────────────

func TestHandleMessage_CompletionComplete_ContextCancellation(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	p, _ := NewPrompt("slow_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(ctx context.Context, _ CompleteRequest) (*CompletionResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	)
	_ = s.RegisterPrompt(p)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	data := jsonrpcReq(70, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "slow_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "",
		},
	})

	resp, err := s.HandleMessage(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(resp.Error.Message, "context canceled") {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

// ── Concurrent completion requests ──────────────────────────────────

func TestHandleMessage_CompletionComplete_Concurrent(t *testing.T) {
	t.Parallel()
	s := serverWithPromptCompleter(t)

	const n = 50
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			data := jsonrpcReq(id+100, "completion/complete", map[string]any{
				"ref": map[string]any{
					"type": "ref/prompt",
					"name": "code_review",
				},
				"argument": map[string]any{
					"name":  "language",
					"value": "go",
				},
			})

			resp, err := s.HandleMessage(context.Background(), data)
			if err != nil {
				errs <- err
				return
			}
			if resp.Error != nil {
				errs <- fmt.Errorf("response error: %s", resp.Error.Message)
				return
			}

			var result CompleteResult
			raw, _ := json.Marshal(resp.Result)
			if err := json.Unmarshal(raw, &result); err != nil {
				errs <- err
				return
			}
			if len(result.Completion.Values) != 1 || result.Completion.Values[0] != "go" {
				errs <- fmt.Errorf("unexpected values: %v", result.Completion.Values)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// ── Context value propagation ───────────────────────────────────────

func TestHandleMessage_CompletionComplete_RequestIDInContext(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	var capturedRequestID any

	p, _ := NewPrompt("ctx_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(ctx context.Context, _ CompleteRequest) (*CompletionResult, error) {
			capturedRequestID = RequestID(ctx)
			return &CompletionResult{Values: []string{"ok"}}, nil
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(42, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "ctx_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "",
		},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// The request ID should have been propagated via context.
	if capturedRequestID == nil {
		t.Fatal("expected request ID in context, got nil")
	}
}

// ── Server-side truncation (100-value cap) ──────────────────────────

func TestHandleMessage_CompletionComplete_TruncatesOver100(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	// Handler returns 200 values with an explicit Total.
	allValues := make([]string, 200)
	for i := range allValues {
		allValues[i] = fmt.Sprintf("item_%03d", i)
	}

	p, _ := NewPrompt("large_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return &CompletionResult{
				Values:  allValues,
				Total:   200,
				HasMore: false,
			}, nil
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(80, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "large_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "",
		},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(raw, &result)

	// Server must truncate to 100 items.
	if len(result.Completion.Values) != 100 {
		t.Errorf("expected 100 values after truncation, got %d", len(result.Completion.Values))
	}
	// Handler-supplied Total is preserved.
	if result.Completion.Total != 200 {
		t.Errorf("total: got %d, want 200 (handler-supplied)", result.Completion.Total)
	}
	// HasMore must be set to true by the server.
	if !result.Completion.HasMore {
		t.Error("hasMore should be true after truncation")
	}
	// First and last truncated items should be correct.
	if result.Completion.Values[0] != "item_000" {
		t.Errorf("first value: got %q, want item_000", result.Completion.Values[0])
	}
	if result.Completion.Values[99] != "item_099" {
		t.Errorf("last value: got %q, want item_099", result.Completion.Values[99])
	}
}

func TestHandleMessage_CompletionComplete_TruncatesWithoutExplicitTotal(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	// Handler returns 150 values but does NOT set Total.
	allValues := make([]string, 150)
	for i := range allValues {
		allValues[i] = fmt.Sprintf("v%d", i)
	}

	p, _ := NewPrompt("no_total_prompt", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return &CompletionResult{Values: allValues}, nil
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(81, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "no_total_prompt",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "",
		},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(raw, &result)

	if len(result.Completion.Values) != 100 {
		t.Errorf("expected 100 values, got %d", len(result.Completion.Values))
	}
	// When handler omits Total, server infers it from the original length.
	if result.Completion.Total != 150 {
		t.Errorf("total: got %d, want 150 (inferred)", result.Completion.Total)
	}
	if !result.Completion.HasMore {
		t.Error("hasMore should be true after truncation")
	}
}

func TestHandleMessage_CompletionComplete_Exactly100NotTruncated(t *testing.T) {
	t.Parallel()
	s := initServer(t)

	// Handler returns exactly 100 values — should NOT be truncated.
	allValues := make([]string, 100)
	for i := range allValues {
		allValues[i] = fmt.Sprintf("item_%03d", i)
	}

	p, _ := NewPrompt("exact100", func(_ context.Context, _ map[string]string) ([]PromptMessage, error) {
		return nil, nil
	},
		WithCompleter(func(_ context.Context, _ CompleteRequest) (*CompletionResult, error) {
			return &CompletionResult{Values: allValues}, nil
		}),
	)
	_ = s.RegisterPrompt(p)

	data := jsonrpcReq(82, "completion/complete", map[string]any{
		"ref": map[string]any{
			"type": "ref/prompt",
			"name": "exact100",
		},
		"argument": map[string]any{
			"name":  "arg1",
			"value": "",
		},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result CompleteResult
	raw, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(raw, &result)

	if len(result.Completion.Values) != 100 {
		t.Errorf("expected 100 values, got %d", len(result.Completion.Values))
	}
	// No truncation occurred, so HasMore should remain at handler default (false).
	if result.Completion.HasMore {
		t.Error("hasMore should be false when values <= 100")
	}
}

func TestMaxCompletionValuesConstant(t *testing.T) {
	t.Parallel()
	if maxCompletionValues != 100 {
		t.Errorf("maxCompletionValues = %d, want 100", maxCompletionValues)
	}
}
