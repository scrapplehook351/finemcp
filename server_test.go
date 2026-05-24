package finemcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestNewServer_Construction(t *testing.T) {
	t.Parallel()

	s := NewServer("test-server", "1.0.0")

	if s.Name() != "test-server" {
		t.Errorf("expected name %q, got %q", "test-server", s.Name())
	}
	if s.Version() != "1.0.0" {
		t.Errorf("expected version %q, got %q", "1.0.0", s.Version())
	}
	if s.tools == nil {
		t.Error("expected tools map to be initialized")
	}
}

func TestRegisterTool_Success(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	tool, err := NewTool("ping", stubHandler)
	if err != nil {
		t.Fatalf("unexpected error creating tool: %v", err)
	}

	if err := s.RegisterTool(tool); err != nil {
		t.Fatalf("unexpected error registering tool: %v", err)
	}

	tools := s.ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "ping" {
		t.Errorf("expected tool name %q, got %q", "ping", tools[0].Name)
	}
}

func TestRegisterTool_NilTool(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")

	err := s.RegisterTool(nil)
	if err != errToolNil {
		t.Errorf("expected %q, got %v", errToolNil, err)
	}
}

func TestRegisterTool_DuplicateName(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	tool1, _ := NewTool("ping", stubHandler)
	tool2, _ := NewTool("ping", stubHandler)

	if err := s.RegisterTool(tool1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err := s.RegisterTool(tool2)
	if err != errToolAlreadyExists {
		t.Errorf("expected %q, got %v", errToolAlreadyExists, err)
	}
}

func TestRegisterTool_NilHandler(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	tool := &Tool{Name: "no-handler"} // Handler is nil

	err := s.RegisterTool(tool)
	if err != errToolHandlerNil {
		t.Errorf("expected %q, got %v", errToolHandlerNil, err)
	}

	tools := s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestRegisterTool_InvalidName(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	tool := &Tool{Name: "bad name!", Handler: stubHandler}

	err := s.RegisterTool(tool)
	if !errors.Is(err, errToolNameChars) {
		t.Errorf("expected errToolNameChars, got %v", err)
	}

	tools := s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestRegisterTool_MultipleTools(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	t1, _ := NewTool("ping", stubHandler)
	t2, _ := NewTool("pong", stubHandler)
	t3, _ := NewTool("echo", stubHandler)

	for _, tool := range []*Tool{t1, t2, t3} {
		if err := s.RegisterTool(tool); err != nil {
			t.Fatalf("unexpected error registering %q: %v", tool.Name, err)
		}
	}

	tools := s.ListTools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
}

func TestRegisterTool_SendsNotification(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var received *JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		received = n
	}

	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	tool, _ := NewTool("test-tool", stubHandler)
	if err := s.RegisterTool(tool); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	if received == nil {
		t.Fatal("expected notification, got nil")
	}
	if received.Method != methodToolsListChanged {
		t.Errorf("method = %q, want %q", received.Method, methodToolsListChanged)
	}
}

func TestRemoveTool_Success(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	tool, _ := NewTool("ping", stubHandler)
	if err := s.RegisterTool(tool); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	if err := s.RemoveTool("ping"); err != nil {
		t.Fatalf("unexpected error removing tool: %v", err)
	}

	tools := s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools after removal, got %d", len(tools))
	}
}

func TestRemoveTool_NotFound(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")

	err := s.RemoveTool("nonexistent")
	if err != errToolNotFound {
		t.Errorf("expected %q, got %v", errToolNotFound, err)
	}
}

func TestRemoveTool_EmptyName(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")

	err := s.RemoveTool("")
	if err != errToolNotFound {
		t.Errorf("expected %q, got %v", errToolNotFound, err)
	}
}

func TestRemoveTool_InvalidatesCache(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	t1, _ := NewTool("alpha", stubHandler)
	t2, _ := NewTool("beta", stubHandler)
	t3, _ := NewTool("gamma", stubHandler)

	_ = s.RegisterTool(t1)
	_ = s.RegisterTool(t2)
	_ = s.RegisterTool(t3)

	// First call to ListTools builds and caches the sorted list.
	tools := s.ListTools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	// Remove the middle tool.
	if err := s.RemoveTool("beta"); err != nil {
		t.Fatalf("RemoveTool: %v", err)
	}

	// ListTools should rebuild the cache and return the correct list.
	tools = s.ListTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools after removal, got %d", len(tools))
	}
	if tools[0].Name != "alpha" || tools[1].Name != "gamma" {
		t.Errorf("expected [alpha, gamma], got [%s, %s]", tools[0].Name, tools[1].Name)
	}
}

func TestRemoveTool_SendsNotification(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	tool, _ := NewTool("test-tool", stubHandler)
	_ = s.RegisterTool(tool)

	var received *JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		received = n
	}

	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	if err := s.RemoveTool("test-tool"); err != nil {
		t.Fatalf("RemoveTool: %v", err)
	}

	if received == nil {
		t.Fatal("expected notification, got nil")
	}
	if received.Method != methodToolsListChanged {
		t.Errorf("method = %q, want %q", received.Method, methodToolsListChanged)
	}
}

func TestHotReload_AddRemoveSequence(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var notifications []*JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		notifications = append(notifications, n)
	}

	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	// Add a tool and verify it's callable.
	tool, _ := NewTool("dynamic-tool", func(_ context.Context, input []byte) ([]byte, error) {
		return []byte("response"), nil
	})
	if err := s.RegisterTool(tool); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	// Verify notification was sent.
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification after add, got %d", len(notifications))
	}
	if notifications[0].Method != methodToolsListChanged {
		t.Errorf("method = %q, want %q", notifications[0].Method, methodToolsListChanged)
	}

	// Verify tool is listed.
	tools := s.ListTools()
	if len(tools) != 1 || tools[0].Name != "dynamic-tool" {
		t.Fatalf("expected [dynamic-tool], got %v", tools)
	}

	// Verify tool is callable.
	result, err := s.CallTool(context.Background(), "dynamic-tool", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	// Remove the tool.
	if err := s.RemoveTool("dynamic-tool"); err != nil {
		t.Fatalf("RemoveTool: %v", err)
	}

	// Verify second notification was sent.
	if len(notifications) != 2 {
		t.Fatalf("expected 2 notifications after remove, got %d", len(notifications))
	}
	if notifications[1].Method != methodToolsListChanged {
		t.Errorf("method = %q, want %q", notifications[1].Method, methodToolsListChanged)
	}

	// Verify tool is no longer listed.
	tools = s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools after removal, got %d", len(tools))
	}

	// Verify tool is no longer callable.
	_, err = s.CallTool(context.Background(), "dynamic-tool", nil)
	if err != errToolNotFound {
		t.Errorf("expected %q, got %v", errToolNotFound, err)
	}
}

func TestHotReload_Concurrent(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("tool-%d", i)
		tool, _ := NewTool(name, stubHandler)
		wg.Add(3)
		go func(t *Tool) { defer wg.Done(); _ = s.RegisterTool(t) }(tool)
		go func(n string) { defer wg.Done(); _ = s.RemoveTool(n) }(name)
		go func() { defer wg.Done(); _ = s.ListTools() }()
	}
	wg.Wait()

	// Every surviving tool must map to one of the 50 known names
	// and must not have a nil Handler (would indicate map corruption).
	validNames := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		validNames[fmt.Sprintf("tool-%d", i)] = true
	}
	for _, tool := range s.ListTools() {
		if !validNames[tool.Name] {
			t.Errorf("unexpected tool name in registry: %q", tool.Name)
		}
		if tool.Handler == nil {
			t.Errorf("tool %q has nil Handler after concurrent operations", tool.Name)
		}
	}
}

func TestRegisterTools_Batch_SingleNotification(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var notifications []*JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		notifications = append(notifications, n)
	}

	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	t1, _ := NewTool("alpha", stubHandler)
	t2, _ := NewTool("beta", stubHandler)
	t3, _ := NewTool("gamma", stubHandler)
	t4, _ := NewTool("delta", stubHandler)
	t5, _ := NewTool("epsilon", stubHandler)

	if err := s.RegisterTools(t1, t2, t3, t4, t5); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	// Exactly 1 notification despite 5 tools registered.
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
	if notifications[0].Method != methodToolsListChanged {
		t.Errorf("method = %q, want %q", notifications[0].Method, methodToolsListChanged)
	}

	// All 5 tools are listed.
	tools := s.ListTools()
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}
}

func TestRegisterTools_NilTool(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	t1, _ := NewTool("a", stubHandler)

	err := s.RegisterTools(t1, nil)
	if err != errToolNil {
		t.Errorf("expected %q, got %v", errToolNil, err)
	}

	// No tools should be registered (all-or-nothing).
	tools := s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools after failed batch, got %d", len(tools))
	}
}

func TestRegisterTools_Duplicate(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	t1, _ := NewTool("existing", stubHandler)
	_ = s.RegisterTool(t1)

	t2, _ := NewTool("new-tool", stubHandler)
	t3, _ := NewTool("existing", stubHandler) // duplicate

	err := s.RegisterTools(t2, t3)
	if err != errToolAlreadyExists {
		t.Errorf("expected %q, got %v", errToolAlreadyExists, err)
	}

	// Only the original tool should be registered (all-or-nothing).
	tools := s.ListTools()
	if len(tools) != 1 || tools[0].Name != "existing" {
		t.Errorf("expected [existing], got %v", tools)
	}
}

func TestRegisterTools_Duplicate_FirstElement(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	t1, _ := NewTool("existing", stubHandler)
	_ = s.RegisterTool(t1)

	// Duplicate is the first element in the batch.
	t2, _ := NewTool("existing", stubHandler)
	t3, _ := NewTool("brand-new", stubHandler)

	err := s.RegisterTools(t2, t3)
	if err != errToolAlreadyExists {
		t.Errorf("expected %q, got %v", errToolAlreadyExists, err)
	}

	tools := s.ListTools()
	if len(tools) != 1 || tools[0].Name != "existing" {
		t.Errorf("expected [existing], got %v", tools)
	}
}

func TestRegisterTools_InvalidName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		make func() []*Tool
	}{
		{"invalid first", func() []*Tool {
			g, _ := NewTool("valid-tool", stubHandler)
			return []*Tool{{Name: "bad name!", Handler: stubHandler}, g}
		}},
		{"invalid second", func() []*Tool {
			g, _ := NewTool("valid-tool", stubHandler)
			return []*Tool{g, {Name: "bad name!", Handler: stubHandler}}
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewServer("test", "1.0")
			if err := s.RegisterTools(tc.make()...); !errors.Is(err, errToolNameChars) {
				t.Errorf("expected errToolNameChars, got %v", err)
			}
			if tools := s.ListTools(); len(tools) != 0 {
				t.Errorf("expected 0 tools after invalid batch, got %d", len(tools))
			}
		})
	}
}

func TestRegisterTools_NilHandler(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		make func() []*Tool
	}{
		{"nil handler first", func() []*Tool {
			g, _ := NewTool("valid-tool", stubHandler)
			return []*Tool{{Name: "no-handler"}, g}
		}},
		{"nil handler second", func() []*Tool {
			g, _ := NewTool("valid-tool", stubHandler)
			return []*Tool{g, {Name: "no-handler"}}
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewServer("test", "1.0")
			if err := s.RegisterTools(tc.make()...); err != errToolHandlerNil {
				t.Errorf("expected %q, got %v", errToolHandlerNil, err)
			}
			if tools := s.ListTools(); len(tools) != 0 {
				t.Errorf("expected 0 tools, got %d", len(tools))
			}
		})
	}
}

func TestRegisterTools_IntraBatchDuplicate(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	t1, _ := NewTool("same-name", stubHandler)
	t2, _ := NewTool("same-name", stubHandler)

	err := s.RegisterTools(t1, t2)
	if err != errToolAlreadyExists {
		t.Errorf("expected %q, got %v", errToolAlreadyExists, err)
	}

	tools := s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools after intra-batch duplicate, got %d", len(tools))
	}
}

func TestRegisterTools_Empty(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var notifications []*JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		notifications = append(notifications, n)
	}
	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	err := s.RegisterTools()
	if err != nil {
		t.Errorf("expected nil error for empty RegisterTools, got %v", err)
	}

	if len(notifications) != 0 {
		t.Errorf("expected 0 notifications for empty RegisterTools, got %d", len(notifications))
	}

	tools := s.ListTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestListTools_Empty(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	tools := s.ListTools()

	if tools == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestListTools_DoesNotLeakInternalState(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")
	tool, _ := NewTool("ping", stubHandler)
	_ = s.RegisterTool(tool)

	tools := s.ListTools()
	tools[0] = nil // mutate the returned slice

	// Internal state should be unaffected
	fresh := s.ListTools()
	if fresh[0] == nil {
		t.Error("mutating ListTools result affected internal state")
	}
}

func TestCallTool_Success(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, input []byte) ([]byte, error) {
		return []byte("pong"), nil
	}

	s := NewServer("test", "1.0.0")
	tool, _ := NewTool("ping", handler)
	_ = s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}

	tc, ok := result.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if tc.Text != "pong" {
		t.Errorf("expected text %q, got %q", "pong", tc.Text)
	}
}

func TestCallTool_NotFound(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")

	result, err := s.CallTool(context.Background(), "nonexistent", nil)
	if err != errToolNotFound {
		t.Errorf("expected %q, got %v", errToolNotFound, err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

func TestCallTool_HandlerError(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("disk full")
	}

	s := NewServer("test", "1.0.0")
	tool, _ := NewTool("write_file", handler)
	_ = s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "write_file", []byte(`{}`))
	if err != nil {
		t.Fatalf("handler errors should not be protocol errors, got: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true for handler error")
	}

	tc, ok := result.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	if tc.Text != "disk full" {
		t.Errorf("expected error text %q, got %q", "disk full", tc.Text)
	}
}

func TestCallTool_PassesInput(t *testing.T) {
	t.Parallel()

	var received []byte
	handler := func(_ context.Context, input []byte) ([]byte, error) {
		received = input
		return []byte("ok"), nil
	}

	s := NewServer("test", "1.0.0")
	tool, _ := NewTool("echo", handler)
	_ = s.RegisterTool(tool)

	input := []byte(`{"message":"hello"}`)
	_, err := s.CallTool(context.Background(), "echo", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(received) != string(input) {
		t.Errorf("expected input %q, got %q", input, received)
	}
}

func TestCallTool_PassesContext(t *testing.T) {
	t.Parallel()

	type ctxKey string
	var got string
	handler := func(ctx context.Context, _ []byte) ([]byte, error) {
		got = ctx.Value(ctxKey("user")).(string)
		return nil, nil
	}

	s := NewServer("test", "1.0.0")
	tool, _ := NewTool("whoami", handler)
	_ = s.RegisterTool(tool)

	ctx := context.WithValue(context.Background(), ctxKey("user"), "alice")
	_, err := s.CallTool(ctx, "whoami", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "alice" {
		t.Errorf("expected context value %q, got %q", "alice", got)
	}
}

func TestNewServer_WithOption(t *testing.T) {
	t.Parallel()

	called := false
	opt := func(s *Server) {
		called = true
	}

	s := NewServer("test", "1.0", opt)
	if !called {
		t.Error("ServerOption was not applied")
	}
	if s.Name() != "test" {
		t.Errorf("name = %q, want %q", s.Name(), "test")
	}
}

func TestNewServer_MultipleOptions(t *testing.T) {
	t.Parallel()

	var order []int
	opt1 := func(_ *Server) { order = append(order, 1) }
	opt2 := func(_ *Server) { order = append(order, 2) }

	NewServer("test", "1.0", opt1, opt2)

	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("options applied in wrong order: %v", order)
	}
}

// ── Broadcasters & Subscriptions Tests ──────────────────────────────

func TestServer_Subscribe_And_NotifyUpdated(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0", WithResourceSubscriptions())

	// Create a sender that records the notification it receives.
	var received *JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		received = n
	}

	// Subscribing to a resource.
	err := s.Subscribe("client-1", "file:///etc/hosts", sender)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Triggering update for a different resource should not call our sender.
	s.NotifyResourceUpdated("file:///etc/passwd")
	if received != nil {
		t.Errorf("expected no notification, got %v", received)
	}

	// Triggering update for the subscribed resource.
	s.NotifyResourceUpdated("file:///etc/hosts")
	if received == nil {
		t.Fatal("expected notification, got nil")
	}

	if received.Method != methodResourcesUpdated {
		t.Errorf("method = %q, want %q", received.Method, methodResourcesUpdated)
	}
	params, ok := received.Params.(ResourceUpdatedParams)
	if !ok {
		t.Fatalf("unexpected params type: %T", received.Params)
	}
	if params.URI != "file:///etc/hosts" {
		t.Errorf("uri = %q, want %q", params.URI, "file:///etc/hosts")
	}
}

func TestServer_UnsubscribeAll(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0", WithResourceSubscriptions())

	count := 0
	sender := func(_ *JSONRPCNotification) {
		count++
	}

	if err := s.Subscribe("client-1", "file:///a", sender); err != nil {
		t.Fatalf("Subscribe client-1 a: %v", err)
	}
	if err := s.Subscribe("client-1", "file:///b", sender); err != nil {
		t.Fatalf("Subscribe client-1 b: %v", err)
	}
	if err := s.Subscribe("client-2", "file:///a", sender); err != nil {
		t.Fatalf("Subscribe client-2 a: %v", err)
	}

	// Disconnect client-1.
	s.UnsubscribeAll("client-1")

	// Trigger updates.
	s.NotifyResourceUpdated("file:///a")
	s.NotifyResourceUpdated("file:///b")

	// Only client-2 should receive the update for file:///a.
	if count != 1 {
		t.Errorf("expected 1 notification triggered, got %d", count)
	}
}

func TestServer_NotifyToolsListChanged(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")

	var received *JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		received = n
	}

	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	s.NotifyToolsListChanged()

	if received == nil {
		t.Fatal("expected notification, got nil")
	}
	if received.Method != methodToolsListChanged {
		t.Errorf("method = %q, want %q", received.Method, methodToolsListChanged)
	}

	s.RemoveSender("client-1")
	received = nil
	s.NotifyToolsListChanged()
	if received != nil {
		t.Errorf("expected no notification after removal, got %v", received)
	}
}

func TestServer_AddSender_DuplicateID(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0")
	noop := func(_ *JSONRPCNotification) {}

	if err := s.AddSender("conn-1", noop); err != nil {
		t.Fatalf("first AddSender: %v", err)
	}
	if err := s.AddSender("conn-1", noop); err == nil {
		t.Error("expected error for duplicate ID, got nil")
	}
}

func TestServer_Subscribe_InvalidArgs(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithResourceSubscriptions())
	noop := func(_ *JSONRPCNotification) {}

	testCases := []struct {
		name         string
		subscriberID string
		uri          string
		sender       NotificationSender
		wantErr      string
	}{
		{"NilSender", "c1", "file:///a", nil, "requires a non-nil sender"},
		{"EmptySubscriber", "", "file:///a", noop, "requires a non-empty subscriberID"},
		{"EmptyURI", "c1", "", noop, "resource URI must not be empty"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Subscribe(tc.subscriberID, tc.uri, tc.sender)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// --- Server option tests ---

func TestWithServerTitle(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithServerTitle("My Title"))
	if s.title != "My Title" {
		t.Errorf("title = %q, want %q", s.title, "My Title")
	}
}

func TestWithServerDescription(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithServerDescription("A great server"))
	if s.description != "A great server" {
		t.Errorf("description = %q, want %q", s.description, "A great server")
	}
}

func TestWithWebsiteURL(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithWebsiteURL("https://example.com"))
	if s.websiteURL != "https://example.com" {
		t.Errorf("websiteURL = %q, want %q", s.websiteURL, "https://example.com")
	}
}

func TestWithIcons(t *testing.T) {
	t.Parallel()
	icons := []Icon{
		{Src: "https://example.com/icon.png", MimeType: "image/png"},
		{Src: "https://example.com/icon.svg"},
	}
	s := NewServer("test", "1.0", WithIcons(icons...))
	if len(s.icons) != 2 {
		t.Fatalf("expected 2 icons, got %d", len(s.icons))
	}
	if s.icons[0].Src != "https://example.com/icon.png" {
		t.Errorf("icon[0].Src = %q", s.icons[0].Src)
	}
	if s.icons[1].Src != "https://example.com/icon.svg" {
		t.Errorf("icon[1].Src = %q", s.icons[1].Src)
	}
}

func TestWithIcons_DefensiveCopy(t *testing.T) {
	t.Parallel()
	icons := []Icon{{Src: "https://example.com/a.png"}}
	s := NewServer("test", "1.0", WithIcons(icons...))

	// Mutating the original slice should not affect the server.
	icons[0].Src = "https://example.com/mutated.png"
	if s.icons[0].Src == "https://example.com/mutated.png" {
		t.Error("WithIcons should make a defensive copy")
	}
}

func TestWithIcons_RejectsJavascriptScheme(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for javascript: scheme, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "disallowed icon URL scheme") {
			t.Errorf("unexpected panic value: %v", r)
		}
	}()

	NewServer("test", "1.0", WithIcons(Icon{Src: "javascript:alert(1)"}))
}

func TestWithIcons_AllowsHTTPScheme(t *testing.T) {
	t.Parallel()
	// Should not panic.
	s := NewServer("test", "1.0", WithIcons(
		Icon{Src: "https://example.com/icon.png"},
		Icon{Src: "http://example.com/icon.png"},
		Icon{Src: "data:image/png;base64,abc123"},
	))
	if len(s.icons) != 3 {
		t.Fatalf("expected 3 icons, got %d", len(s.icons))
	}
	if s.icons[0].Src != "https://example.com/icon.png" {
		t.Errorf("icon[0].Src = %q", s.icons[0].Src)
	}
}

func TestWithIcons_RejectsRelativeURL(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for relative URL, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "explicit scheme") {
			t.Errorf("unexpected panic value: %v", r)
		}
	}()
	NewServer("test", "1.0", WithIcons(Icon{Src: "/path/to/icon.png"}))
}

func TestWithIcons_RejectsDataURLWithHTMLMIME(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for data URL with text/html MIME, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "image/") {
			t.Errorf("unexpected panic value: %v", r)
		}
	}()
	NewServer("test", "1.0", WithIcons(Icon{Src: "data:text/html,<script>alert(1)</script>"}))
}

func TestWithIcons_RejectsDataURLDefaultMIME(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for data URL with default text/plain MIME, got none")
		}
	}()
	NewServer("test", "1.0", WithIcons(Icon{Src: "data:,hello"}))
}

func TestWithInstructions(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithInstructions("Use JSON output."))
	if s.instructions != "Use JSON output." {
		t.Errorf("instructions = %q, want %q", s.instructions, "Use JSON output.")
	}
}

func TestWithInstructions_PanicOnTooLong(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for oversized instructions, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "exceeds maximum") {
			t.Errorf("unexpected panic value: %v", r)
		}
	}()

	huge := strings.Repeat("x", 100*1024+1)
	NewServer("test", "1.0", WithInstructions(huge))
}

func TestWithLifespan(t *testing.T) {
	t.Parallel()
	fn := func(ctx context.Context, _ *Server) (context.Context, func(), error) {
		return ctx, nil, nil
	}
	s := NewServer("test", "1.0", WithLifespan(fn))
	if s.lifespan == nil {
		t.Error("lifespan should not be nil after WithLifespan")
	}
}

func TestSetAuthChecker(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	// SetAuthChecker should not panic with a non-nil checker.
	s.SetAuthChecker(func(_ context.Context) error {
		return nil
	})
}

func TestNotifyResourcesListChanged(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	// No senders registered; broadcast should not panic.
	s.NotifyResourcesListChanged()
}

func TestNotifyPromptsListChanged(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	// No senders registered; broadcast should not panic.
	s.NotifyPromptsListChanged()
}
