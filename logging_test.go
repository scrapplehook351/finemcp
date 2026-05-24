package finemcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func TestSendLogMessage_Success(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	var mu sync.Mutex
	var received *JSONRPCNotification
	sender := NotificationSender(func(n *JSONRPCNotification) {
		mu.Lock()
		defer mu.Unlock()
		received = n
	})

	ctx := WithNotificationSender(context.Background(), sender)

	err := s.SendLogMessage(ctx, LogLevelInfo, "test-logger", "test message")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if received == nil {
		t.Fatal("expected notification to be sent")
	}

	if received.Method != "notifications/message" {
		t.Errorf("expected method notifications/message, got %s", received.Method)
	}
}

func TestSendLogMessage_NotInitialized(t *testing.T) {
	s := NewServer("test", "1.0.0")

	sender := NotificationSender(func(n *JSONRPCNotification) {})
	ctx := WithNotificationSender(context.Background(), sender)

	err := s.SendLogMessage(ctx, LogLevelInfo, "logger", "message")
	if err != errNotInitialized {
		t.Errorf("expected errNotInitialized, got %v", err)
	}
}

func TestSendLogMessage_NoSender(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	err := s.SendLogMessage(context.Background(), LogLevelInfo, "logger", "message")
	if err != errNoNotificationSender {
		t.Errorf("expected errNoNotificationSender, got %v", err)
	}
}

func TestHandleLoggingSetLevel_Success(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	var receivedLevel LogLevel
	s.SetLogHandler(func(ctx context.Context, level LogLevel) error {
		receivedLevel = level
		return nil
	})

	ctx := context.Background()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "logging/setLevel",
		Params:  json.RawMessage(`{"level":"debug"}`),
	}

	resp, err := s.handleLoggingSetLevel(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("expected no error in response, got %v", resp.Error)
	}

	if receivedLevel != LogLevelDebug {
		t.Errorf("expected log level debug, got %s", receivedLevel)
	}
}

func TestHandleLoggingSetLevel_InvalidLevel(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	ctx := context.Background()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "logging/setLevel",
		Params:  json.RawMessage(`{"level":"invalid"}`),
	}

	resp, err := s.handleLoggingSetLevel(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response for invalid log level")
	}
}

func TestHandleLoggingSetLevel_InvalidJSON(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	ctx := context.Background()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "logging/setLevel",
		Params:  json.RawMessage(`{invalid json`),
	}

	resp, err := s.handleLoggingSetLevel(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response for invalid JSON")
	}
}

func TestHandleLoggingSetLevel_NoHandler(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	// Don't set a handler

	ctx := context.Background()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Method:  "logging/setLevel",
		Params:  json.RawMessage(`{"level":"info"}`),
	}

	resp, err := s.handleLoggingSetLevel(ctx, req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("expected success even without handler, got error: %v", resp.Error)
	}
}
