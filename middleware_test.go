package finemcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestMiddleware_SingleWraps(t *testing.T) {
	s := NewServer("test", "1.0")

	var order []string
	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			order = append(order, "before")
			out, err := next(ctx, input)
			order = append(order, "after")
			return out, err
		}
	})

	tool, _ := NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		order = append(order, "handler")
		return input, nil
	})
	s.RegisterTool(tool)

	_, err := s.CallTool(context.Background(), "echo", []byte("hi"))
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(order, " → ")
	want := "before → handler → after"
	if got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestMiddleware_ChainOrder(t *testing.T) {
	s := NewServer("test", "1.0")

	var order []string

	makeMiddleware := func(name string) Middleware {
		return func(next ToolHandler) ToolHandler {
			return func(ctx context.Context, input []byte) ([]byte, error) {
				order = append(order, name+":in")
				out, err := next(ctx, input)
				order = append(order, name+":out")
				return out, err
			}
		}
	}

	s.Use(makeMiddleware("A"))
	s.Use(makeMiddleware("B"))
	s.Use(makeMiddleware("C"))

	tool, _ := NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		order = append(order, "handler")
		return nil, nil
	})
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "noop", nil)

	got := strings.Join(order, " → ")
	want := "A:in → B:in → C:in → handler → C:out → B:out → A:out"
	if got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestMiddleware_NoMiddleware(t *testing.T) {
	s := NewServer("test", "1.0")

	tool, _ := NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "echo", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected success")
	}
}

func TestMiddleware_CanShortCircuit(t *testing.T) {
	s := NewServer("test", "1.0")

	handlerCalled := false

	s.Use(func(_ ToolHandler) ToolHandler {
		return func(_ context.Context, _ []byte) ([]byte, error) {
			// Never calls next — short-circuits.
			return nil, fmt.Errorf("blocked")
		}
	})

	tool, _ := NewTool("secret", func(_ context.Context, _ []byte) ([]byte, error) {
		handlerCalled = true
		return []byte("secret data"), nil
	})
	s.RegisterTool(tool)

	result, _ := s.CallTool(context.Background(), "secret", nil)

	if handlerCalled {
		t.Error("handler should not be called when middleware short-circuits")
	}
	if !result.IsError {
		t.Error("expected error result from short-circuit")
	}
}

func TestMiddleware_CanModifyInput(t *testing.T) {
	s := NewServer("test", "1.0")

	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, _ []byte) ([]byte, error) {
			// Override input regardless of what was passed.
			return next(ctx, []byte("injected"))
		}
	})

	var received []byte
	tool, _ := NewTool("spy", func(_ context.Context, input []byte) ([]byte, error) {
		received = input
		return input, nil
	})
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "spy", []byte("original"))

	if string(received) != "injected" {
		t.Errorf("received = %q, want %q", received, "injected")
	}
}

func TestMiddleware_CanModifyOutput(t *testing.T) {
	s := NewServer("test", "1.0")

	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			out, err := next(ctx, input)
			if err != nil {
				return out, err
			}
			// Transform output.
			return []byte(strings.ToUpper(string(out))), nil
		}
	})

	tool, _ := NewTool("greet", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("hello"), nil
	})
	s.RegisterTool(tool)

	result, _ := s.CallTool(context.Background(), "greet", nil)

	// Result is built from the middleware-modified output.
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	tc, ok := result.Content[0].(TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if tc.Text != "HELLO" {
		t.Errorf("text = %q, want %q", tc.Text, "HELLO")
	}
}

func TestMiddleware_ErrorPropagation(t *testing.T) {
	s := NewServer("test", "1.0")

	var middlewareSawError bool

	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			out, err := next(ctx, input)
			if err != nil {
				middlewareSawError = true
			}
			return out, err
		}
	})

	tool, _ := NewTool("fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	})
	s.RegisterTool(tool)

	result, _ := s.CallTool(context.Background(), "fail", nil)

	if !middlewareSawError {
		t.Error("middleware should see the handler error")
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestMiddleware_UseMultipleCalls(t *testing.T) {
	s := NewServer("test", "1.0")

	var order []string

	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			order = append(order, "first")
			return next(ctx, input)
		}
	})

	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			order = append(order, "second")
			return next(ctx, input)
		}
	})

	tool, _ := NewTool("test", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "test", nil)

	got := strings.Join(order, ",")
	if got != "first,second" {
		t.Errorf("order = %q, want %q", got, "first,second")
	}
}

func TestMiddleware_AppliesPerCall(t *testing.T) {
	s := NewServer("test", "1.0")

	callCount := 0
	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			callCount++
			return next(ctx, input)
		}
	})

	tool, _ := NewTool("counter", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "counter", nil)
	s.CallTool(context.Background(), "counter", nil)
	s.CallTool(context.Background(), "counter", nil)

	if callCount != 3 {
		t.Errorf("middleware called %d times, want 3", callCount)
	}
}
