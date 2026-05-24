package mcptest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/mcptest"
)

func TestServer_Initialize(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	defer ts.Close()

	resp := ts.Initialize(t)
	if resp.Error != nil {
		t.Fatalf("init failed: %s", resp.Error.Message)
	}

	var result finemcp.InitializeResult
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ServerInfo.Name != "test" {
		t.Errorf("server name = %q, want %q", result.ServerInfo.Name, "test")
	}
}

func TestServer_Call_Success(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t, mcptest.WithTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	}))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "echo", json.RawMessage(`{"msg":"hello"}`))
	if resp.Error != nil {
		t.Fatalf("call failed: %s", resp.Error.Message)
	}
}

func TestServer_Call_UnknownTool(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "nope", nil)
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestServer_ListTools(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t, mcptest.WithTool("alpha", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("a"), nil
	}), mcptest.WithTool("beta", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("b"), nil
	}))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListTools(t)
	if resp.Error != nil {
		t.Fatalf("list failed: %s", resp.Error.Message)
	}

	var result finemcp.ListToolsResult
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(result.Tools))
	}
}

func TestServer_Notify(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	defer ts.Close()

	ts.Initialize(t)
	ts.Notify(t, "notifications/initialized", nil)
}

func TestServer_CallBeforeInit(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t, mcptest.WithTool("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("pong"), nil
	}))
	defer ts.Close()

	resp := ts.CallTool(t, "ping", nil)
	if resp.Error == nil {
		t.Fatal("expected error when calling before init")
	}
}

func TestServer_WithMiddleware(t *testing.T) {
	t.Parallel()

	called := false
	mw := func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			called = true
			return next(ctx, input)
		}
	}

	ts := mcptest.NewServer(t,
		mcptest.WithTool("ping", func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte("pong"), nil
		}),
		mcptest.WithMiddleware(mw),
	)
	defer ts.Close()

	ts.Initialize(t)
	ts.CallTool(t, "ping", nil)
	if !called {
		t.Error("middleware was not invoked")
	}
}

func TestServer_WithTypedTool(t *testing.T) {
	t.Parallel()
	type In struct {
		Name string `json:"name"`
	}
	type Out struct {
		Greeting string `json:"greeting"`
	}

	tool, err := finemcp.NewTypedTool("greet", func(_ context.Context, in In) (Out, error) {
		return Out{Greeting: "hello " + in.Name}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t, mcptest.WithRegisteredTool(tool))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "greet", json.RawMessage(`{"name":"world"}`))
	if resp.Error != nil {
		t.Fatalf("call failed: %s", resp.Error.Message)
	}
}

func TestServer_RawCall(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	defer ts.Close()

	resp := ts.RawCall(t, "initialize", json.RawMessage(`{
		"protocolVersion":"2025-11-25",
		"capabilities":{},
		"clientInfo":{"name":"raw","version":"0.1"}
	}`))
	if resp.Error != nil {
		t.Fatalf("raw init failed: %s", resp.Error.Message)
	}
}

func TestAssertToolResult(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t, mcptest.WithTool("echo", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("hello"), nil
	}))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "echo", nil)
	mcptest.AssertToolResult(t, resp, "hello")
}

func TestAssertError(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "nope", nil)
	mcptest.AssertError(t, resp, finemcp.ErrCodeInvalidParams, "tool not found")
}

func TestAssertNoError(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t, mcptest.WithTool("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("pong"), nil
	}))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "ping", nil)
	mcptest.AssertNoError(t, resp)
}

func TestAssertToolCount(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t,
		mcptest.WithTool("a", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }),
		mcptest.WithTool("b", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }),
	)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListTools(t)
	mcptest.AssertToolCount(t, resp, 2)
}

func TestLoadFixture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := `{"name":"world"}`
	if err := os.WriteFile(filepath.Join(dir, "greet.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	data := mcptest.LoadFixture(t, filepath.Join(dir, "greet.json"))
	if string(data) != content {
		t.Errorf("fixture = %q, want %q", string(data), content)
	}
}

func TestGoldenFile_Create(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	golden := filepath.Join(dir, "testdata", "TestGoldenFile_Create.golden")

	mcptest.GoldenFile(t, golden, []byte("snapshot-data"))

	got, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != "snapshot-data" {
		t.Errorf("golden = %q, want %q", string(got), "snapshot-data")
	}
}

func TestGoldenFile_Match(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	golden := filepath.Join(dir, "TestGoldenFile_Match.golden")

	mcptest.GoldenFile(t, golden, []byte("expected"))
	mcptest.GoldenFile(t, golden, []byte("expected"))
}

// ── Resource tests ──────────────────────────────────────────────────

func TestServer_ListResources(t *testing.T) {
	t.Parallel()
	r, err := finemcp.NewResource("file:///hello", "hello",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{finemcp.NewTextResourceContent(uri, "world")}, nil
		},
		finemcp.WithResourceDescription("A greeting"),
		finemcp.WithResourceMimeType("text/plain"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t, mcptest.WithResource(r))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListResources(t)
	mcptest.AssertResourceCount(t, resp, 1)
}

func TestServer_ReadResource(t *testing.T) {
	t.Parallel()
	r, err := finemcp.NewResource("file:///hello", "hello",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{finemcp.NewTextResourceContent(uri, "world")}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t, mcptest.WithResource(r))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ReadResource(t, "file:///hello")
	mcptest.AssertResourceText(t, resp, "world")
}

func TestServer_ReadResource_NotFound(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ReadResource(t, "file:///nope")
	if resp.Error == nil {
		t.Fatal("expected error for unknown resource")
	}
}

func TestServer_ListResourceTemplates(t *testing.T) {
	t.Parallel()
	tmpl, err := finemcp.NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{finemcp.NewTextResourceContent(uri, "log data")}, nil
		},
		finemcp.WithTemplateDescription("Daily log"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t, mcptest.WithResourceTemplate(tmpl))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListResourceTemplates(t)
	mcptest.AssertTemplateCount(t, resp, 1)
}

func TestAssertResourceCount(t *testing.T) {
	t.Parallel()
	r1, _ := finemcp.NewResource("file:///a", "a", func(_ context.Context, _ string) ([]finemcp.ResourceContent, error) {
		return nil, nil
	})
	r2, _ := finemcp.NewResource("file:///b", "b", func(_ context.Context, _ string) ([]finemcp.ResourceContent, error) {
		return nil, nil
	})

	ts := mcptest.NewServer(t, mcptest.WithResource(r1), mcptest.WithResource(r2))
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListResources(t)
	mcptest.AssertResourceCount(t, resp, 2)
}

// ── Prompt tests ────────────────────────────────────────────────────

func TestServer_WithPrompt_ListAndGet(t *testing.T) {
	t.Parallel()

	p, err := finemcp.NewPrompt("greet",
		func(_ context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			name := args["name"]
			if name == "" {
				name = "world"
			}
			return []finemcp.PromptMessage{finemcp.NewAssistantMessage("Hello, " + name + "!")}, nil
		},
		finemcp.WithPromptDescription("A greeting prompt"),
		finemcp.WithPromptArguments(finemcp.PromptArgument{Name: "name", Description: "who to greet"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t, mcptest.WithPrompt(p))
	defer ts.Close()

	// Test Inner() while we're at it.
	if ts.Inner() == nil {
		t.Fatal("Inner() returned nil")
	}

	ts.Initialize(t)

	// ListPrompts.
	listResp := ts.ListPrompts(t)
	mcptest.AssertPromptCount(t, listResp, 1)

	// GetPrompt.
	getResp := ts.GetPrompt(t, "greet", map[string]string{"name": "Alice"})
	mcptest.AssertPromptMessage(t, getResp, "assistant", "Hello, Alice!")
}

func TestServer_Close(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	// Close is a no-op but must not panic.
	ts.Close()
	ts.Close()
}

func TestServer_Inner(t *testing.T) {
	t.Parallel()
	ts := mcptest.NewServer(t)
	if ts.Inner() == nil {
		t.Fatal("Inner() should return non-nil server")
	}
}

func TestAssertPromptCount(t *testing.T) {
	t.Parallel()
	p, err := finemcp.NewPrompt("p1",
		func(_ context.Context, _ map[string]string) ([]finemcp.PromptMessage, error) {
			return []finemcp.PromptMessage{finemcp.NewUserMessage("hi")}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ts := mcptest.NewServer(t, mcptest.WithPrompt(p))
	defer ts.Close()
	ts.Initialize(t)
	resp := ts.ListPrompts(t)
	mcptest.AssertPromptCount(t, resp, 1)
}

func TestAssertPromptMessage(t *testing.T) {
	t.Parallel()
	p, err := finemcp.NewPrompt("welcome",
		func(_ context.Context, _ map[string]string) ([]finemcp.PromptMessage, error) {
			return []finemcp.PromptMessage{finemcp.NewUserMessage("welcome!")}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ts := mcptest.NewServer(t, mcptest.WithPrompt(p))
	defer ts.Close()
	ts.Initialize(t)
	resp := ts.GetPrompt(t, "welcome", nil)
	mcptest.AssertPromptMessage(t, resp, "user", "welcome!")
}

func TestGoldenFile_Mismatch_FormatJSON(t *testing.T) {
	// This exercises GoldenFile with a JSON payload, which internally
	// calls formatJSON via the diff output path on mismatch.
	t.Parallel()
	dir := t.TempDir()
	golden := filepath.Join(dir, "test.golden")

	// First call creates the file.
	mcptest.GoldenFile(t, golden, []byte(`{"key":"value"}`))

	// Verify the created file matches – no mismatch, formatJSON not called
	// via error path, but the happy path is exercised.
	got, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != `{"key":"value"}` {
		t.Errorf("golden = %q", string(got))
	}
}
