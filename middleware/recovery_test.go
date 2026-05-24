package middleware_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

func TestRecovery_CatchesPanic(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("boom", func(_ context.Context, _ []byte) ([]byte, error) {
		panic("something broke")
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "boom", nil)
	if err != nil {
		t.Fatalf("CallTool should not return error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result from panic")
	}

	tc, ok := result.Content[0].(finemcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if !strings.Contains(tc.Text, "something broke") {
		t.Errorf("text = %q, should contain panic message", tc.Text)
	}
}

func TestRecovery_CatchesNilPanic(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("nilpanic", func(_ context.Context, _ []byte) ([]byte, error) {
		var p *int
		panic(p)
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "nilpanic", nil)
	if err != nil {
		t.Fatalf("CallTool should not return error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

func TestRecovery_CatchesIntPanic(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("intpanic", func(_ context.Context, _ []byte) ([]byte, error) {
		panic(42)
	})
	s.RegisterTool(tool)

	result, _ := s.CallTool(context.Background(), "intpanic", nil)
	if !result.IsError {
		t.Fatal("expected error result")
	}

	tc := result.Content[0].(finemcp.TextContent)
	if !strings.Contains(tc.Text, "42") {
		t.Errorf("text = %q, should mention panic value", tc.Text)
	}
}

func TestRecovery_PassesThroughNormalHandler(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("ok", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "ok", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected success")
	}

	tc := result.Content[0].(finemcp.TextContent)
	if tc.Text != "hello" {
		t.Errorf("text = %q, want %q", tc.Text, "hello")
	}
}

func TestRecovery_PassesThroughHandlerError(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, context.DeadlineExceeded
	})
	s.RegisterTool(tool)

	result, _ := s.CallTool(context.Background(), "fail", nil)
	if !result.IsError {
		t.Error("expected error result")
	}

	tc := result.Content[0].(finemcp.TextContent)
	if !strings.Contains(tc.Text, "deadline") {
		t.Errorf("text = %q, should contain original error", tc.Text)
	}
}

func TestRecoveryWithHandler_CustomHandler(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	var capturedCtx context.Context
	var capturedVal any

	s.Use(middleware.RecoveryWithHandler(func(ctx context.Context, val any) string {
		capturedCtx = ctx
		capturedVal = val
		return "custom: caught it"
	}))

	tool, _ := finemcp.NewTool("boom", func(_ context.Context, _ []byte) ([]byte, error) {
		panic("oops")
	})
	s.RegisterTool(tool)

	result, _ := s.CallTool(context.Background(), "boom", nil)
	if !result.IsError {
		t.Fatal("expected error result")
	}

	tc := result.Content[0].(finemcp.TextContent)
	if tc.Text != "custom: caught it" {
		t.Errorf("text = %q, want %q", tc.Text, "custom: caught it")
	}

	if capturedCtx == nil {
		t.Error("PanicHandler should receive context")
	}
	if capturedVal != "oops" {
		t.Errorf("PanicHandler val = %v, want %q", capturedVal, "oops")
	}
}

func TestRecovery_ChainWithOtherMiddleware(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	var outerSawReturn bool

	s.Use(func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			out, err := next(ctx, input)
			if err != nil {
				outerSawReturn = true
			}
			return out, err
		}
	})

	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("boom", func(_ context.Context, _ []byte) ([]byte, error) {
		panic("crash")
	})
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "boom", nil)

	if !outerSawReturn {
		t.Error("outer middleware should see error from recovery, not a panic")
	}
}

func TestRecovery_EndToEnd_ViaDispatch(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Recovery())

	tool, _ := finemcp.NewTool("boom", func(_ context.Context, _ []byte) ([]byte, error) {
		panic("dispatch panic")
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{"name": "boom"})
	resp, err := s.HandleMessage(context.Background(), callMsg)
	if err != nil {
		t.Fatalf("dispatch should not error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("should be tool error, not protocol error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "dispatch panic") {
		t.Errorf("result = %s, should mention panic", raw)
	}
	if !strings.Contains(string(raw), `"isError":true`) {
		t.Error("expected isError:true in result")
	}
}

// jsonrpcReq builds a valid JSON-RPC request byte slice.
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
