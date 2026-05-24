// Example: Testing with mcptest
//
// Demonstrates using the mcptest package for in-process server testing.
package testing_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/mcptest"
	"github.com/finemcp/finemcp/middleware"
)

func TestBasicTool(t *testing.T) {
	ts := mcptest.NewServer(t,
		mcptest.WithTool("greet", func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("hello"), nil
		}),
	)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "greet", nil)
	mcptest.AssertNoError(t, resp)
	mcptest.AssertToolResult(t, resp, "hello")
}

func TestTypedTool(t *testing.T) {
	tool, err := finemcp.NewTypedTool("add",
		func(ctx context.Context, in struct {
			A int `json:"a"`
			B int `json:"b"`
		}) (int, error) {
			return in.A + in.B, nil
		},
		finemcp.WithDescription("Add numbers"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t,
		mcptest.WithRegisteredTool(tool),
	)
	defer ts.Close()

	ts.Initialize(t)
	args, _ := json.Marshal(map[string]any{"a": 2, "b": 3})
	resp := ts.CallTool(t, "add", args)
	mcptest.AssertNoError(t, resp)
}

func TestWithMiddleware(t *testing.T) {
	ts := mcptest.NewServer(t,
		mcptest.WithTool("work", func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("done"), nil
		}),
		mcptest.WithMiddleware(middleware.Recovery()),
		mcptest.WithMiddleware(middleware.Logging(middleware.NopLogger)),
	)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.CallTool(t, "work", nil)
	mcptest.AssertNoError(t, resp)
}

func TestWithResource(t *testing.T) {
	res, err := finemcp.NewResource(
		"test://data",
		"Test Data",
		func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, "test data"),
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t,
		mcptest.WithResource(res),
	)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListResources(t)
	mcptest.AssertNoError(t, resp)
}

func TestWithPrompt(t *testing.T) {
	p, err := finemcp.NewPrompt("greet",
		func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage("Hello!"),
			}, nil
		},
		finemcp.WithPromptDescription("Greeting prompt"),
	)
	if err != nil {
		t.Fatal(err)
	}

	ts := mcptest.NewServer(t,
		mcptest.WithPrompt(p),
	)
	defer ts.Close()

	ts.Initialize(t)
	resp := ts.ListPrompts(t)
	mcptest.AssertNoError(t, resp)
}
