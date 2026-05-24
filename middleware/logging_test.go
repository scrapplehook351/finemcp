package middleware_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

// spyLogger captures log calls for assertions.
type spyLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

type logEntry struct {
	level         string
	msg           string
	keysAndValues []any
}

func (l *spyLogger) Info(msg string, keysAndValues ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{"info", msg, keysAndValues})
}

func (l *spyLogger) Error(msg string, keysAndValues ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{"error", msg, keysAndValues})
}

func (l *spyLogger) last() logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[len(l.entries)-1]
}

func (l *spyLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// kvMap converts keysAndValues to a map for easier assertions.
func kvMap(kvs []any) map[string]any {
	m := make(map[string]any)
	for i := 0; i+1 < len(kvs); i += 2 {
		k, _ := kvs[i].(string)
		m[k] = kvs[i+1]
	}
	return m
}

func TestLogging_SuccessfulCall(t *testing.T) {
	logger := &spyLogger{}
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Logging(logger))

	tool, _ := finemcp.NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(tool)

	// Use dispatch so context gets populated with tool name.
	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hi"},
	})
	s.HandleMessage(context.Background(), callMsg)

	if logger.count() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logger.count())
	}

	entry := logger.last()
	if entry.level != "info" {
		t.Errorf("level = %q, want %q", entry.level, "info")
	}
	if entry.msg != "tool call completed" {
		t.Errorf("msg = %q, want %q", entry.msg, "tool call completed")
	}

	kv := kvMap(entry.keysAndValues)
	if kv["tool"] != "echo" {
		t.Errorf("tool = %v, want %q", kv["tool"], "echo")
	}
	if _, ok := kv["duration"]; !ok {
		t.Error("missing duration field")
	}
}

func TestLogging_FailedCall(t *testing.T) {
	logger := &spyLogger{}
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Logging(logger))

	tool, _ := finemcp.NewTool("fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{"name": "fail"})
	s.HandleMessage(context.Background(), callMsg)

	entry := logger.last()
	if entry.level != "error" {
		t.Errorf("level = %q, want %q", entry.level, "error")
	}
	if entry.msg != "tool call failed" {
		t.Errorf("msg = %q, want %q", entry.msg, "tool call failed")
	}

	kv := kvMap(entry.keysAndValues)
	errVal, _ := kv["error"].(string)
	if !strings.Contains(errVal, "boom") {
		t.Errorf("error = %q, should contain %q", errVal, "boom")
	}
}

func TestLogging_IncludesRequestID(t *testing.T) {
	logger := &spyLogger{}
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Logging(logger))

	tool, _ := finemcp.NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq("req-42", "tools/call", map[string]any{"name": "noop"})
	s.HandleMessage(context.Background(), callMsg)

	kv := kvMap(logger.last().keysAndValues)
	if kv["requestID"] != "req-42" {
		t.Errorf("requestID = %v, want %q", kv["requestID"], "req-42")
	}
}

func TestLogging_IncludesDuration(t *testing.T) {
	logger := &spyLogger{}
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Logging(logger))

	tool, _ := finemcp.NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{"name": "noop"})
	s.HandleMessage(context.Background(), callMsg)

	kv := kvMap(logger.last().keysAndValues)
	dur, ok := kv["duration"].(string)
	if !ok || dur == "" {
		t.Errorf("duration = %v, want non-empty string", kv["duration"])
	}
}

func TestLogging_NoRequestIDWhenAbsent(t *testing.T) {
	logger := &spyLogger{}
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Logging(logger))

	tool, _ := finemcp.NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	// Call directly via CallTool (no dispatch, so no request ID in context).
	s.CallTool(context.Background(), "noop", nil)

	kv := kvMap(logger.last().keysAndValues)
	if _, ok := kv["requestID"]; ok {
		t.Error("requestID should be absent when not in context")
	}
}

func TestLogging_NopLogger(t *testing.T) {
	// Just verify NopLogger doesn't panic.
	middleware.NopLogger.Info("test", "key", "value")
	middleware.NopLogger.Error("test", "key", "value")
}

func TestLogging_MultipleCallsLogSeparately(t *testing.T) {
	logger := &spyLogger{}
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.Logging(logger))

	tool, _ := finemcp.NewTool("counter", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "counter", nil)
	s.CallTool(context.Background(), "counter", nil)
	s.CallTool(context.Background(), "counter", nil)

	if logger.count() != 3 {
		t.Errorf("expected 3 log entries, got %d", logger.count())
	}
}
