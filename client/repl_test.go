package client

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
)

type fakeREPLClient struct {
	initialized bool
	tools       []finemcp.ToolInfo
}

func (f *fakeREPLClient) Initialize(context.Context) (*finemcp.InitializeResult, error) {
	f.initialized = true
	return &finemcp.InitializeResult{ServerInfo: finemcp.ProcessInfo{Name: "mock", Version: "1.0"}}, nil
}

func (f *fakeREPLClient) Ping(context.Context) error { return nil }

func (f *fakeREPLClient) ListTools(context.Context, finemcp.ListParams) (*finemcp.ListToolsResult, error) {
	if !f.initialized {
		return nil, ErrNotInitialized
	}
	return &finemcp.ListToolsResult{Tools: f.tools}, nil
}

func (f *fakeREPLClient) CallTool(_ context.Context, params finemcp.CallToolParams) (*finemcp.CallToolResult, error) {
	if params.Name == "boom" {
		return nil, errors.New("boom")
	}
	return &finemcp.CallToolResult{Content: []finemcp.Content{finemcp.TextContent{Text: params.Name}}}, nil
}

func (f *fakeREPLClient) ListResources(context.Context, finemcp.ListParams) (*finemcp.ListResourcesResult, error) {
	return &finemcp.ListResourcesResult{}, nil
}

func (f *fakeREPLClient) ReadResource(_ context.Context, params finemcp.ReadResourceParams) (*finemcp.ReadResourceResult, error) {
	return &finemcp.ReadResourceResult{Contents: []finemcp.ResourceContent{finemcp.NewTextResourceContent(params.URI, "ok")}}, nil
}

func (f *fakeREPLClient) ListPrompts(context.Context, finemcp.ListParams) (*finemcp.ListPromptsResult, error) {
	return &finemcp.ListPromptsResult{Prompts: []finemcp.PromptInfo{{Name: "p1"}}}, nil
}

func (f *fakeREPLClient) GetPrompt(_ context.Context, params finemcp.GetPromptParams) (*finemcp.GetPromptResult, error) {
	return &finemcp.GetPromptResult{Description: params.Name}, nil
}

func (f *fakeREPLClient) Close() error { return nil }

func TestSplitWord(t *testing.T) {
	w, rest := splitWord("call-tool echo {\"a\":1}")
	if w != "call-tool" || rest != "echo {\"a\":1}" {
		t.Fatalf("unexpected split: %q / %q", w, rest)
	}
}

func TestREPL_Run_HappyPath(t *testing.T) {
	in := strings.NewReader("help\ninitialize\nlist-tools\ncall-tool echo {\"x\":1}\nexit\n")
	var out bytes.Buffer
	c := &fakeREPLClient{tools: []finemcp.ToolInfo{{Name: "echo", Description: "Echo"}}}
	r := &REPL{client: c, in: in, out: &out, prompt: "finemcp> "}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Commands:") {
		t.Fatalf("expected help output, got: %s", s)
	}
	if !strings.Contains(s, "initialized: mock 1.0") {
		t.Fatalf("expected initialize output, got: %s", s)
	}
	if !strings.Contains(s, "1. echo - Echo") {
		t.Fatalf("expected list-tools output, got: %s", s)
	}
	if !strings.Contains(s, "\"text\": \"echo\"") {
		t.Fatalf("expected call-tool json output, got: %s", s)
	}
}

func TestREPL_Run_InvalidJSONArgs(t *testing.T) {
	in := strings.NewReader("call-tool echo {not-json}\nexit\n")
	var out bytes.Buffer
	r := &REPL{client: &fakeREPLClient{}, in: in, out: &out, prompt: "finemcp> "}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "invalid JSON arguments") {
		t.Fatalf("expected json validation error in output, got: %s", out.String())
	}
}

func TestREPL_Run_UnknownCommand(t *testing.T) {
	in := strings.NewReader("nope\nexit\n")
	var out bytes.Buffer
	r := &REPL{client: &fakeREPLClient{}, in: in, out: &out, prompt: "finemcp> "}
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got: %s", out.String())
	}
}
