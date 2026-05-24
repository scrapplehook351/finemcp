package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// NotificationHandlerFunc type is expected to exist.
// These tests define the contract before implementation.

func TestOnNotification_RegisterAndDispatch(t *testing.T) {
	s := NewServer("test", "1.0")

	var mu sync.Mutex
	var received []json.RawMessage

	s.OnNotification("custom/event", func(ctx context.Context, params json.RawMessage) {
		mu.Lock()
		received = append(received, params)
		mu.Unlock()
	})

	// Initialize the server (notifications work before and after init).
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", resp.Error.Message)
	}

	// Send the custom notification (no id = notification).
	notif := []byte(`{"jsonrpc":"2.0","method":"custom/event","params":{"key":"value"}}`)
	resp, err = s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if resp != nil {
		t.Fatal("notification should return nil response")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 call, got %d", len(received))
	}

	var parsed map[string]string
	if err := json.Unmarshal(received[0], &parsed); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if parsed["key"] != "value" {
		t.Errorf("expected key=value, got %v", parsed)
	}
}

func TestOnNotification_NilParams(t *testing.T) {
	s := NewServer("test", "1.0")

	called := make(chan json.RawMessage, 1)
	s.OnNotification("custom/ping", func(ctx context.Context, params json.RawMessage) {
		called <- params
	})

	// Send notification with no params.
	notif := []byte(`{"jsonrpc":"2.0","method":"custom/ping"}`)
	resp, err := s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("notification should return nil response")
	}

	select {
	case params := <-called:
		if params != nil {
			t.Errorf("expected nil params, got %s", string(params))
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not called")
	}
}

func TestOnNotification_MultipleHandlersSameMethod(t *testing.T) {
	s := NewServer("test", "1.0")

	var mu sync.Mutex
	var calls []int

	s.OnNotification("custom/multi", func(ctx context.Context, params json.RawMessage) {
		mu.Lock()
		calls = append(calls, 1)
		mu.Unlock()
	})
	s.OnNotification("custom/multi", func(ctx context.Context, params json.RawMessage) {
		mu.Lock()
		calls = append(calls, 2)
		mu.Unlock()
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/multi"}`)
	_, _ = s.HandleMessage(context.Background(), notif)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 handler calls, got %d", len(calls))
	}
}

func TestOnNotification_DoesNotOverrideBuiltins(t *testing.T) {
	s := NewServer("test", "1.0")

	called := false
	// Register a handler for the built-in "notifications/initialized".
	// The built-in behavior should still run, AND the custom handler should fire.
	s.OnNotification("notifications/initialized", func(ctx context.Context, params json.RawMessage) {
		called = true
	})

	// Initialize first.
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatal(resp.Error.Message)
	}

	// Send notifications/initialized.
	notif := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	resp, err = s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("notification should return nil response")
	}

	if !called {
		t.Error("custom handler for built-in notification was not called")
	}
}

func TestOnNotification_UnregisteredMethodStillIgnored(t *testing.T) {
	s := NewServer("test", "1.0")

	// No handler registered for "unknown/thing".
	// Should still silently return nil (unchanged behavior).
	notif := []byte(`{"jsonrpc":"2.0","method":"unknown/thing"}`)
	resp, err := s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("unknown notification should still return nil response")
	}
}

func TestOnNotification_PanicInHandlerDoesNotCrashServer(t *testing.T) {
	s := initServer(t)

	s.OnNotification("custom/panic", func(ctx context.Context, params json.RawMessage) {
		panic("handler exploded")
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/panic"}`)
	// Should not panic — the server should recover.
	resp, err := s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("notification should return nil response even after panic")
	}
}

func TestOnNotification_ConcurrentRegistrationAndDispatch(t *testing.T) {
	s := initServer(t)

	var wg sync.WaitGroup
	// Register handlers concurrently.
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.OnNotification("custom/concurrent", func(ctx context.Context, params json.RawMessage) {})
		}(i)
	}
	// Dispatch concurrently.
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			notif := []byte(`{"jsonrpc":"2.0","method":"custom/concurrent"}`)
			_, _ = s.HandleMessage(context.Background(), notif)
		}()
	}
	wg.Wait()
}

func TestOnNotification_NilHandlerPanics(t *testing.T) {
	s := NewServer("test", "1.0")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil handler")
		}
	}()
	s.OnNotification("custom/test", nil)
}

func TestOnNotification_EmptyMethodPanics(t *testing.T) {
	s := NewServer("test", "1.0")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty method")
		}
	}()
	s.OnNotification("", func(ctx context.Context, params json.RawMessage) {})
}

func TestOnNotification_CancelledContextSkipsHandlers(t *testing.T) {
	s := initServer(t)

	called := false
	s.OnNotification("custom/skip", func(ctx context.Context, params json.RawMessage) {
		called = true
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before dispatch

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/skip"}`)
	_, _ = s.HandleMessage(ctx, notif)

	if called {
		t.Error("handler should not be called when context is already cancelled")
	}
}

func TestOnNotification_PanicDoesNotPreventSubsequentHandlers(t *testing.T) {
	s := initServer(t)

	secondCalled := false

	s.OnNotification("custom/panic-chain", func(ctx context.Context, params json.RawMessage) {
		panic("first handler explodes")
	})
	s.OnNotification("custom/panic-chain", func(ctx context.Context, params json.RawMessage) {
		secondCalled = true
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/panic-chain"}`)
	resp, err := s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("notification should return nil response")
	}
	if !secondCalled {
		t.Error("panic in first handler should not prevent second handler from running")
	}
}

func TestOnNotification_HandlerCanRegisterNewHandler(t *testing.T) {
	s := initServer(t)

	innerCalled := false
	s.OnNotification("custom/self-register", func(ctx context.Context, params json.RawMessage) {
		// Register another handler from within a handler — must not deadlock.
		s.OnNotification("custom/inner", func(ctx context.Context, params json.RawMessage) {
			innerCalled = true
		})
	})

	done := make(chan struct{})
	go func() {
		notif := []byte(`{"jsonrpc":"2.0","method":"custom/self-register"}`)
		_, _ = s.HandleMessage(context.Background(), notif)
		close(done)
	}()

	select {
	case <-done:
		// No deadlock.
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: handler calling OnNotification blocked")
	}

	// Verify the inner handler was registered and works.
	notif2 := []byte(`{"jsonrpc":"2.0","method":"custom/inner"}`)
	_, _ = s.HandleMessage(context.Background(), notif2)
	if !innerCalled {
		t.Error("inner handler registered from within a handler was not called")
	}
}

func TestOnNotification_MethodLimitExceeded(t *testing.T) {
	s := NewServer("test", "1.0", WithMaxNotificationMethods(3))

	s.OnNotification("m1", func(context.Context, json.RawMessage) {})
	s.OnNotification("m2", func(context.Context, json.RawMessage) {})
	s.OnNotification("m3", func(context.Context, json.RawMessage) {})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when exceeding method limit")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "method limit exceeded") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	s.OnNotification("m4-overflow", func(context.Context, json.RawMessage) {})
}

func TestOnNotification_HandlerLimitPerMethodExceeded(t *testing.T) {
	s := NewServer("test", "1.0", WithMaxHandlersPerNotification(2))

	s.OnNotification("spam", func(context.Context, json.RawMessage) {})
	s.OnNotification("spam", func(context.Context, json.RawMessage) {})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when exceeding per-method handler limit")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "handler limit") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	s.OnNotification("spam", func(context.Context, json.RawMessage) {})
}

func TestOnNotification_SameMethodDoesNotCountAsNewMethod(t *testing.T) {
	s := NewServer("test", "1.0",
		WithMaxNotificationMethods(1),
		WithMaxHandlersPerNotification(10),
	)

	// First handler for "m1" — uses the only method slot.
	s.OnNotification("m1", func(context.Context, json.RawMessage) {})
	// Second handler for "m1" — same method, should NOT count as a new method.
	s.OnNotification("m1", func(context.Context, json.RawMessage) {})
}

func TestOnNotification_MethodNameTooLong(t *testing.T) {
	s := NewServer("test", "1.0")

	longName := strings.Repeat("x", maxNotifMethodNameLength+1)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for oversized method name")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "too long") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	s.OnNotification(longName, func(context.Context, json.RawMessage) {})
}

func TestOnNotification_MethodNameAtMaxLength(t *testing.T) {
	s := NewServer("test", "1.0")

	// Exactly at the limit — should not panic.
	exactName := strings.Repeat("x", maxNotifMethodNameLength)
	s.OnNotification(exactName, func(context.Context, json.RawMessage) {})
}

func TestRemoveNotificationHandlers(t *testing.T) {
	s := initServer(t)

	called := false
	s.OnNotification("custom/removable", func(ctx context.Context, params json.RawMessage) {
		called = true
	})

	n := s.RemoveNotificationHandlers("custom/removable")
	if n != 1 {
		t.Fatalf("expected 1 removed, got %d", n)
	}

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/removable"}`)
	_, _ = s.HandleMessage(context.Background(), notif)

	if called {
		t.Error("handler should not be called after removal")
	}
}

func TestRemoveNotificationHandlers_NonExistent(t *testing.T) {
	s := NewServer("test", "1.0")

	n := s.RemoveNotificationHandlers("nope")
	if n != 0 {
		t.Fatalf("expected 0 removed for non-existent method, got %d", n)
	}
}

func TestRemoveNotificationHandlers_FreesMethodSlot(t *testing.T) {
	s := NewServer("test", "1.0", WithMaxNotificationMethods(1))

	s.OnNotification("m1", func(context.Context, json.RawMessage) {})
	s.RemoveNotificationHandlers("m1")
	// Should now be able to register a new distinct method.
	s.OnNotification("m2", func(context.Context, json.RawMessage) {})
}

func TestNotificationStats(t *testing.T) {
	s := NewServer("test", "1.0")

	s.OnNotification("a", func(context.Context, json.RawMessage) {})
	s.OnNotification("a", func(context.Context, json.RawMessage) {})
	s.OnNotification("b", func(context.Context, json.RawMessage) {})

	stats := s.NotificationStats()
	if stats["a"] != 2 {
		t.Errorf("expected 2 handlers for 'a', got %d", stats["a"])
	}
	if stats["b"] != 1 {
		t.Errorf("expected 1 handler for 'b', got %d", stats["b"])
	}
	if len(stats) != 2 {
		t.Errorf("expected 2 methods in stats, got %d", len(stats))
	}

	// Mutating the returned map should not affect internal state.
	stats["a"] = 999
	stats2 := s.NotificationStats()
	if stats2["a"] != 2 {
		t.Error("mutating returned stats should not affect internal state")
	}
}

func TestNotificationStats_Empty(t *testing.T) {
	s := NewServer("test", "1.0")

	stats := s.NotificationStats()
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %v", stats)
	}
}

func TestWithNotificationPanicHandler(t *testing.T) {
	var mu sync.Mutex
	var panicMethod string
	var panicValue any

	s := NewServer("test", "1.0", WithNotificationPanicHandler(func(method string, recovered any) {
		mu.Lock()
		panicMethod = method
		panicValue = recovered
		mu.Unlock()
	}))

	// Initialize server.
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatal(resp.Error.Message)
	}

	s.OnNotification("custom/boom", func(ctx context.Context, params json.RawMessage) {
		panic("kaboom")
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/boom"}`)
	_, _ = s.HandleMessage(context.Background(), notif)

	mu.Lock()
	defer mu.Unlock()
	if panicMethod != "custom/boom" {
		t.Errorf("expected method 'custom/boom', got %q", panicMethod)
	}
	if panicValue != "kaboom" {
		t.Errorf("expected panic value 'kaboom', got %v", panicValue)
	}
}

func TestWithNotificationPanicHandler_NilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil panic handler")
		}
	}()
	NewServer("test", "1.0", WithNotificationPanicHandler(nil))
}

func TestWithNotificationPanicHandler_NotCalledOnSuccess(t *testing.T) {
	called := false
	s := initServer(t)
	// Manually set the field since initServer doesn't use the option.
	s.notifPanicHandler = func(string, any) { called = true }

	s.OnNotification("custom/ok", func(ctx context.Context, params json.RawMessage) {
		// no panic
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/ok"}`)
	_, _ = s.HandleMessage(context.Background(), notif)

	if called {
		t.Error("panic handler should not be called when handler succeeds")
	}
}

func TestWithNotificationPanicHandler_PanicHandlerItselfPanics(t *testing.T) {
	s := NewServer("test", "1.0", WithNotificationPanicHandler(func(method string, recovered any) {
		panic("panic handler is buggy")
	}))

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatal(resp.Error.Message)
	}

	secondCalled := false
	s.OnNotification("custom/boom", func(ctx context.Context, params json.RawMessage) {
		panic("handler panic")
	})
	s.OnNotification("custom/boom", func(ctx context.Context, params json.RawMessage) {
		secondCalled = true
	})

	// Must not crash even though the panic handler itself panics.
	notif := []byte(`{"jsonrpc":"2.0","method":"custom/boom"}`)
	resp, err = s.HandleMessage(context.Background(), notif)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatal("notification should return nil response")
	}
	if !secondCalled {
		t.Error("buggy panic handler should not prevent subsequent notification handlers")
	}
}

func TestRemoveNotificationHandlers_EmptyMethodReturnsZero(t *testing.T) {
	s := NewServer("test", "1.0")
	n := s.RemoveNotificationHandlers("")
	if n != 0 {
		t.Fatalf("expected 0 for empty method, got %d", n)
	}
}

func TestRemoveNotificationHandlers_OversizedMethodReturnsZero(t *testing.T) {
	s := NewServer("test", "1.0")
	long := strings.Repeat("x", maxNotifMethodNameLength+1)
	n := s.RemoveNotificationHandlers(long)
	if n != 0 {
		t.Fatalf("expected 0 for oversized method, got %d", n)
	}
}

func TestOnNotification_InvalidMethodNameChars(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"null byte", "custom/\x00event"},
		{"control char", "custom/\x01event"},
		{"space", "custom/ event"},
		{"tab", "custom/\tevent"},
		{"newline", "custom/\nevent"},
		{"unicode", "custom/événement"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer("test", "1.0")
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for method %q", tt.method)
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, "invalid notification method name") {
					t.Errorf("unexpected panic message: %v", r)
				}
			}()
			s.OnNotification(tt.method, func(context.Context, json.RawMessage) {})
		})
	}
}

func TestOnNotification_ValidMethodNameChars(t *testing.T) {
	s := NewServer("test", "1.0")
	// All allowed characters: letters, digits, underscore, hyphen, slash, dot.
	s.OnNotification("a-z/A-Z/0_9.test", func(context.Context, json.RawMessage) {})
}

func TestNotificationStats_ConcurrentWithRegistration(t *testing.T) {
	s := NewServer("test", "1.0")
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.OnNotification(fmt.Sprintf("m%d", n), func(context.Context, json.RawMessage) {})
		}(i)
	}

	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.NotificationStats()
		}()
	}

	wg.Wait()
}

func TestRemoveNotificationHandlers_ConcurrentWithDispatch(t *testing.T) {
	s := initServer(t)

	s.OnNotification("custom/racy", func(ctx context.Context, params json.RawMessage) {
		// May or may not run — both outcomes are valid.
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		notif := []byte(`{"jsonrpc":"2.0","method":"custom/racy"}`)
		_, _ = s.HandleMessage(context.Background(), notif)
	}()
	go func() {
		defer wg.Done()
		s.RemoveNotificationHandlers("custom/racy")
	}()
	wg.Wait()
}

func TestOnNotification_MaxLengthWithInvalidLastChar(t *testing.T) {
	s := NewServer("test", "1.0")
	// 511 valid chars + 1 invalid space = exactly 512 bytes.
	method := strings.Repeat("x", maxNotifMethodNameLength-1) + " "
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid char at end of max-length name")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "invalid notification method name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	s.OnNotification(method, func(context.Context, json.RawMessage) {})
}

func TestOnNotification_InvalidSlashPatterns(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"leading slash", "/custom/event"},
		{"trailing slash", "custom/event/"},
		{"consecutive slashes", "custom//event"},
		{"only slashes", "///"},
		{"single slash", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer("test", "1.0")
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for method %q", tt.method)
				}
				msg, ok := r.(string)
				if !ok || !strings.Contains(msg, "invalid notification method name") {
					t.Errorf("unexpected panic message: %v", r)
				}
			}()
			s.OnNotification(tt.method, func(context.Context, json.RawMessage) {})
		})
	}
}

func TestOnNotification_ValidSlashPatterns(t *testing.T) {
	s := NewServer("test", "1.0")
	// These should all pass validation.
	valid := []string{
		"custom/event",
		"a/b/c/d/e",
		"notifications/initialized",
		"no-slash",
		"dotted.name",
	}
	for _, m := range valid {
		s.OnNotification(m, func(context.Context, json.RawMessage) {})
	}
}

// Benchmark baselines (Apple M1, go1.23):
//
//	No handlers:     ~800 ns/op,  544 B/op, 10 allocs/op  (JSON parse dominates)
//	Single handler: ~1200 ns/op,  592 B/op, 13 allocs/op  (+48 B = slice copy)
//	100 handlers:   ~1900 ns/op, 1488 B/op, 14 allocs/op  (+896 B = larger copy)
//	Panic recovery: similar to single handler + defer overhead
//	Stats (100):    ~2200 ns/op, 3544 B/op,  4 allocs/op

func benchInitServer(b *testing.B) *Server {
	b.Helper()
	s := NewServer("bench", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "benchclient", "version": "0.1"},
	})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		b.Fatalf("initialize: %v", err)
	}
	if resp.Error != nil {
		b.Fatalf("initialize error: %s", resp.Error.Message)
	}
	return s
}

func BenchmarkHandleNotification_SingleHandler(b *testing.B) {
	s := benchInitServer(b)
	s.OnNotification("bench/event", func(context.Context, json.RawMessage) {})
	notif := []byte(`{"jsonrpc":"2.0","method":"bench/event","params":{"k":"v"}}`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = s.HandleMessage(context.Background(), notif)
	}
}

func BenchmarkHandleNotification_100Handlers(b *testing.B) {
	s := benchInitServer(b)
	for range 100 {
		s.OnNotification("bench/event", func(context.Context, json.RawMessage) {})
	}
	notif := []byte(`{"jsonrpc":"2.0","method":"bench/event","params":{"k":"v"}}`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = s.HandleMessage(context.Background(), notif)
	}
}

func BenchmarkHandleNotification_NoHandlers(b *testing.B) {
	s := benchInitServer(b)
	notif := []byte(`{"jsonrpc":"2.0","method":"bench/event"}`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = s.HandleMessage(context.Background(), notif)
	}
}

func BenchmarkNotificationStats(b *testing.B) {
	s := NewServer("bench", "1.0")
	for i := range 100 {
		s.OnNotification(fmt.Sprintf("m%d", i), func(context.Context, json.RawMessage) {})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = s.NotificationStats()
	}
}

func BenchmarkHandleNotification_PanicRecovery(b *testing.B) {
	s := benchInitServer(b)
	s.OnNotification("bench/panic", func(context.Context, json.RawMessage) {
		panic("benchmark panic")
	})
	notif := []byte(`{"jsonrpc":"2.0","method":"bench/panic"}`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = s.HandleMessage(context.Background(), notif)
	}
}

func TestOnNotification_MaxLengthAllDots(t *testing.T) {
	s := NewServer("test", "1.0")
	// 512 dots is pathological but valid — dots are allowed characters.
	method := strings.Repeat(".", maxNotifMethodNameLength)
	s.OnNotification(method, func(context.Context, json.RawMessage) {})
}

func TestOnNotification_ContextCancelledDuringSlowHandler(t *testing.T) {
	s := initServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	firstRan := false
	secondRan := false

	s.OnNotification("custom/slow", func(ctx context.Context, params json.RawMessage) {
		firstRan = true
		cancel() // cancel context during first handler
	})
	s.OnNotification("custom/slow", func(ctx context.Context, params json.RawMessage) {
		secondRan = true
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/slow"}`)
	_, _ = s.HandleMessage(ctx, notif)

	if !firstRan {
		t.Error("first handler should have executed")
	}
	if secondRan {
		t.Error("second handler should be skipped after context cancellation")
	}
}

func TestOnNotification_HandlerRemovesItself(t *testing.T) {
	s := initServer(t)

	done := make(chan int, 1)
	s.OnNotification("custom/self-destruct", func(ctx context.Context, params json.RawMessage) {
		n := s.RemoveNotificationHandlers("custom/self-destruct")
		done <- n
	})

	notif := []byte(`{"jsonrpc":"2.0","method":"custom/self-destruct"}`)
	go func() {
		_, _ = s.HandleMessage(context.Background(), notif)
	}()

	select {
	case n := <-done:
		if n != 1 {
			t.Errorf("expected 1 removed, got %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: handler removing itself blocked")
	}
}

func TestNotificationStats_ConcurrentWithRemove(t *testing.T) {
	s := NewServer("test", "1.0")
	for i := range 50 {
		s.OnNotification(fmt.Sprintf("m%d", i), func(context.Context, json.RawMessage) {})
	}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.RemoveNotificationHandlers(fmt.Sprintf("m%d", n))
		}(i)
	}
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.NotificationStats()
		}()
	}
	wg.Wait()
}

func ExampleServer_OnNotification() {
	server := NewServer("myserver", "1.0")

	// Register a handler for custom notifications.
	server.OnNotification("custom/event", func(ctx context.Context, params json.RawMessage) {
		var data map[string]any
		if err := json.Unmarshal(params, &data); err != nil {
			return
		}
		fmt.Printf("event: %v\n", data["type"])
	})

	// Multiple handlers for the same method run in registration order.
	server.OnNotification("custom/event", func(ctx context.Context, params json.RawMessage) {
		fmt.Println("second handler")
	})

	// Check registered handlers.
	stats := server.NotificationStats()
	fmt.Printf("handlers for custom/event: %d\n", stats["custom/event"])

	// Output:
	// handlers for custom/event: 2
}
