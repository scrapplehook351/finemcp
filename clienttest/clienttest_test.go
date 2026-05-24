package clienttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

func newClient(t *testing.T, m *MockServer) *client.Client {
	t.Helper()
	c, err := client.New(m.AsTransport(), client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestMockServer_InitializeAndListTools(t *testing.T) {
	m := NewInitializedMockServer()
	m.QueueToolsList([]finemcp.ToolInfo{
		{Name: "echo", InputSchema: map[string]any{"type": "object"}},
	})

	c := newClient(t, m)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	out, err := c.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools response: %+v", out.Tools)
	}

	m.AssertRequestCount(t, 2)
	m.AssertMethodCalled(t, finemcp.MethodInitialize)
	m.AssertMethodCalled(t, finemcp.MethodToolsList)
}

func TestMockServer_CallToolError(t *testing.T) {
	m := NewInitializedMockServer()
	m.QueueError(finemcp.MethodToolsCall, finemcp.ErrCodeInvalidParams, "invalid params")

	c := newClient(t, m)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	_, err := c.CallTool(ctx, finemcp.CallToolParams{Name: "echo"})
	if err == nil {
		t.Fatal("expected call tool error")
	}

	var re *client.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected ResponseError, got %T (%v)", err, err)
	}
	if re.Code != finemcp.ErrCodeInvalidParams {
		t.Fatalf("unexpected error code: got=%d want=%d", re.Code, finemcp.ErrCodeInvalidParams)
	}

	last := m.LastRequest()
	if last == nil || last.Method != finemcp.MethodToolsCall {
		t.Fatalf("unexpected last request: %#v", last)
	}
}

func TestMockServer_SendNotification(t *testing.T) {
	progressCh := make(chan finemcp.ProgressParams, 1)
	m := NewInitializedMockServer()

	c, err := client.New(m.AsTransport(), client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		OnProgress: func(p finemcp.ProgressParams) {
			progressCh <- p
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	err = m.SendNotification(finemcp.MethodProgress, finemcp.ProgressParams{
		Progress:      42,
		ProgressToken: "tok-1",
	})
	if err != nil {
		t.Fatalf("send notification: %v", err)
	}

	select {
	case p := <-progressCh:
		if p.Progress != 42 {
			t.Fatalf("unexpected progress: %+v", p)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected progress callback")
	}
}

func TestMockServer_Reset(t *testing.T) {
	m := NewInitializedMockServer()
	m.QueueToolsList([]finemcp.ToolInfo{{Name: "echo", InputSchema: map[string]any{"type": "object"}}})

	c := newClient(t, m)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close first client: %v", err)
	}

	m.AssertRequestCount(t, 2)
	m.Reset()
	m.AssertRequestCount(t, 0)

	m.QueueResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		ServerInfo:      finemcp.ProcessInfo{Name: "mock-server", Version: "1.0.0"},
	})
	m.QueueToolsList([]finemcp.ToolInfo{{Name: "echo-2", InputSchema: map[string]any{"type": "object"}}})

	c2 := newClient(t, m)
	defer c2.Close()

	if _, err := c2.Initialize(ctx); err != nil {
		t.Fatalf("re-initialize after reset: %v", err)
	}
	out, err := c2.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("list tools after reset: %v", err)
	}
	if out.Tools[0].Name != "echo-2" {
		t.Fatalf("unexpected tool name after reset: %q", out.Tools[0].Name)
	}
}

func TestMockServer_QueueResourcesList(t *testing.T) {
	m := NewInitializedMockServer()
	m.QueueResourcesList([]finemcp.ResourceInfo{
		{URI: "file:///a.txt", Name: "a"},
		{URI: "file:///b.txt", Name: "b"},
	})

	c := newClient(t, m)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	out, err := c.ListResources(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(out.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(out.Resources))
	}
	if out.Resources[0].URI != "file:///a.txt" {
		t.Fatalf("unexpected resource URI: %q", out.Resources[0].URI)
	}
	m.AssertMethodCalled(t, finemcp.MethodResourcesList)
}

func TestMockServer_QueuePromptsList(t *testing.T) {
	m := NewInitializedMockServer()
	m.QueuePromptsList([]finemcp.PromptInfo{
		{Name: "greeting", Description: "A greeting"},
	})

	c := newClient(t, m)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	out, err := c.ListPrompts(ctx, finemcp.ListParams{})
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(out.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(out.Prompts))
	}
	if out.Prompts[0].Name != "greeting" {
		t.Fatalf("unexpected prompt name: %q", out.Prompts[0].Name)
	}
	m.AssertMethodCalled(t, finemcp.MethodPromptsList)
}

func TestMockServer_QueueToolCallText(t *testing.T) {
	m := NewInitializedMockServer()
	m.QueueToolCallText("hello from mock")

	c := newClient(t, m)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := c.CallTool(ctx, finemcp.CallToolParams{Name: "echo"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in tool result")
	}
	tc, ok := result.Content[0].(finemcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if tc.Text != "hello from mock" {
		t.Fatalf("unexpected text: %q", tc.Text)
	}
}
