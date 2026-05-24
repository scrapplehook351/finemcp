package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── sessionToolRegistry unit tests ──────────────────────────────────

func TestSessionToolRegistry_Add_Basic(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()

	tool := &Tool{Name: "session-tool", Handler: stubHandler}
	if err := r.add("s1", tool); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := r.lookup("s1", "session-tool")
	if got == nil {
		t.Fatal("expected tool, got nil")
	}
	if got.Name != "session-tool" {
		t.Errorf("got name %q, want %q", got.Name, "session-tool")
	}
}

func TestSessionToolRegistry_Add_EmptySessionID(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	err := r.add("", &Tool{Name: "t", Handler: stubHandler})
	if err != errSessionIDEmpty {
		t.Errorf("got %v, want errSessionIDEmpty", err)
	}
}

func TestSessionToolRegistry_Add_NilTool(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	err := r.add("s1", nil)
	if err != errSessionToolNil {
		t.Errorf("got %v, want errSessionToolNil", err)
	}
}

func TestSessionToolRegistry_Add_EmptyName(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	err := r.add("s1", &Tool{Handler: stubHandler})
	if err != errSessionToolNameEmpty {
		t.Errorf("got %v, want errSessionToolNameEmpty", err)
	}
}

func TestSessionToolRegistry_Add_NilHandler(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	err := r.add("s1", &Tool{Name: "t"})
	if err != errSessionToolNoHandler {
		t.Errorf("got %v, want errSessionToolNoHandler", err)
	}
}

func TestSessionToolRegistry_Add_Duplicate(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	tool := &Tool{Name: "t", Handler: stubHandler}
	_ = r.add("s1", tool)
	err := r.add("s1", tool)
	if err != errSessionToolExists {
		t.Errorf("got %v, want errSessionToolExists", err)
	}
}

func TestSessionToolRegistry_Add_SameNameDifferentSessions(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	t1 := &Tool{Name: "shared-name", Handler: stubHandler}
	t2 := &Tool{Name: "shared-name", Handler: stubHandler}
	if err := r.add("s1", t1); err != nil {
		t.Fatalf("s1 add: %v", err)
	}
	if err := r.add("s2", t2); err != nil {
		t.Fatalf("s2 add: %v", err)
	}
}

func TestSessionToolRegistry_Remove_Basic(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	_ = r.add("s1", &Tool{Name: "t", Handler: stubHandler})

	if err := r.remove("s1", "t"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := r.lookup("s1", "t"); got != nil {
		t.Error("expected nil after remove")
	}
}

func TestSessionToolRegistry_Remove_NotFound(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	err := r.remove("s1", "nonexistent")
	if err != errSessionToolNotFound {
		t.Errorf("got %v, want errSessionToolNotFound", err)
	}
}

func TestSessionToolRegistry_Remove_EmptySessionID(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	err := r.remove("", "t")
	if err != errSessionIDEmpty {
		t.Errorf("got %v, want errSessionIDEmpty", err)
	}
}

func TestSessionToolRegistry_RemoveAll(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	_ = r.add("s1", &Tool{Name: "a", Handler: stubHandler})
	_ = r.add("s1", &Tool{Name: "b", Handler: stubHandler})
	_ = r.add("s2", &Tool{Name: "c", Handler: stubHandler})

	r.removeAll("s1")

	if got := r.lookup("s1", "a"); got != nil {
		t.Error("expected nil after removeAll for s1")
	}
	if got := r.lookup("s2", "c"); got == nil {
		t.Error("s2 tools should not be affected")
	}
}

func TestSessionToolRegistry_RemoveAll_EmptyID_NoPanic(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	r.removeAll("") // should not panic
}

func TestSessionToolRegistry_Lookup_NoSession(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	if got := r.lookup("nonexistent", "t"); got != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestSessionToolRegistry_SortedTools(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	_ = r.add("s1", &Tool{Name: "zebra", Handler: stubHandler})
	_ = r.add("s1", &Tool{Name: "alpha", Handler: stubHandler})
	_ = r.add("s1", &Tool{Name: "mid", Handler: stubHandler})

	sorted := r.sortedTools("s1")
	if len(sorted) != 3 {
		t.Fatalf("got %d tools, want 3", len(sorted))
	}
	want := []string{"alpha", "mid", "zebra"}
	for i, w := range want {
		if sorted[i].Name != w {
			t.Errorf("index %d: got %q, want %q", i, sorted[i].Name, w)
		}
	}
}

func TestSessionToolRegistry_SortedTools_NoSession(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	if got := r.sortedTools("x"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSessionToolRegistry_SortedTools_Cached(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	_ = r.add("s1", &Tool{Name: "a", Handler: stubHandler})

	s1 := r.sortedTools("s1")
	s2 := r.sortedTools("s1")
	// Both calls should return the same backing array (cache hit).
	if &s1[0] != &s2[0] {
		t.Error("expected cached slice to be reused")
	}
}

func TestSessionToolRegistry_HasSession(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	if r.hasSession("s1") {
		t.Error("expected false for nonexistent session")
	}
	_ = r.add("s1", &Tool{Name: "t", Handler: stubHandler})
	if !r.hasSession("s1") {
		t.Error("expected true after add")
	}
}

func TestSessionToolRegistry_Concurrent(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := "s1"
			name := "tool-" + string(rune('a'+i%26))
			_ = r.add(sid, &Tool{Name: name, Handler: stubHandler})
			_ = r.sortedTools(sid)
			_ = r.lookup(sid, name)
			_ = r.remove(sid, name)
		}(i)
	}
	wg.Wait()
}

// ── Server-level AddSessionTool / RemoveSessionTool ─────────────────

func TestServer_AddSessionTool(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	tool := &Tool{Name: "session-only", Handler: stubHandler}
	if err := s.AddSessionTool(context.Background(), "conn-1", tool); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := s.SessionTools("conn-1")
	if len(got) != 1 || got[0].Name != "session-only" {
		t.Errorf("got %v, want [session-only]", got)
	}
}

func TestServer_AddSessionTool_NilTool(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	err := s.AddSessionTool(context.Background(), "conn-1", nil)
	if err != errSessionToolNil {
		t.Errorf("got %v, want errSessionToolNil", err)
	}
}

func TestServer_AddSessionTool_EmptyName(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{Handler: stubHandler})
	// validateToolName catches empty names before the registry does.
	if err != errToolNameEmpty {
		t.Errorf("got %v, want errToolNameEmpty", err)
	}
}

func TestServer_AddSessionTool_Notification(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	var received *JSONRPCNotification
	_ = s.AddSender("conn-1", func(n *JSONRPCNotification) { received = n })

	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "t", Handler: stubHandler})

	if received == nil {
		t.Fatal("expected notification")
	}
	if received.Method != methodToolsListChanged {
		t.Errorf("got method %q, want %q", received.Method, methodToolsListChanged)
	}
}

func TestServer_AddSessionTool_NotificationOnlyToAffectedSession(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	var s1Received, s2Received bool
	_ = s.AddSender("conn-1", func(n *JSONRPCNotification) { s1Received = true })
	_ = s.AddSender("conn-2", func(n *JSONRPCNotification) { s2Received = true })

	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "t", Handler: stubHandler})

	if !s1Received {
		t.Error("conn-1 should have received notification")
	}
	if s2Received {
		t.Error("conn-2 should NOT have received notification")
	}
}

func TestServer_RemoveSessionTool(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "t", Handler: stubHandler})

	if err := s.RemoveSessionTool("conn-1", "t"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.SessionTools("conn-1"); got != nil {
		t.Errorf("expected nil after remove, got %v", got)
	}
}

func TestServer_RemoveSessionTool_NotFound(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	err := s.RemoveSessionTool("conn-1", "nonexistent")
	if err != errSessionToolNotFound {
		t.Errorf("got %v, want errSessionToolNotFound", err)
	}
}

func TestServer_RemoveSessionTool_Notification(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSender("conn-1", func(*JSONRPCNotification) {})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "t", Handler: stubHandler})

	var received *JSONRPCNotification
	// Re-register sender to capture the remove notification.
	s.RemoveSender("conn-1")
	_ = s.AddSender("conn-1", func(n *JSONRPCNotification) { received = n })

	_ = s.RemoveSessionTool("conn-1", "t")

	if received == nil {
		t.Fatal("expected notification on remove")
	}
	if received.Method != methodToolsListChanged {
		t.Errorf("got method %q, want %q", received.Method, methodToolsListChanged)
	}
}

func TestServer_RemoveSessionTools_Cleanup(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "a", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "b", Handler: stubHandler})

	s.RemoveSessionTools("conn-1")

	if got := s.SessionTools("conn-1"); got != nil {
		t.Errorf("expected nil after cleanup, got %v", got)
	}
}

func TestServer_SessionTools_NoSession(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	if got := s.SessionTools("nonexistent"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ── E2E: tools/list merges session + global tools ───────────────────

func initSessionTestServer(t *testing.T, s *Server) {
	t.Helper()
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
}

func ctxWithSession(sessionID string) context.Context {
	return WithSubscriberID(context.Background(), sessionID)
}

func TestE2E_ToolsList_IncludesSessionTools(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{Name: "global-tool", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "session-tool", Handler: stubHandler})

	initSessionTestServer(t, s)

	// List tools as conn-1 — should see both.
	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := s.HandleMessage(ctxWithSession("conn-1"), []byte(listMsg))
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(result.Tools))
	}
	// Should be sorted: global-tool, session-tool.
	if result.Tools[0].Name != "global-tool" || result.Tools[1].Name != "session-tool" {
		t.Errorf("got [%s, %s], want [global-tool, session-tool]",
			result.Tools[0].Name, result.Tools[1].Name)
	}
}

func TestE2E_ToolsList_SessionToolsShadowGlobal(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{
		Name: "shared-tool", Description: "global version",
		Handler: stubHandler,
	})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "shared-tool", Description: "session version",
		Handler: stubHandler,
	})

	initSessionTestServer(t, s)

	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, _ := s.HandleMessage(ctxWithSession("conn-1"), []byte(listMsg))

	raw, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &result)

	if len(result.Tools) != 1 {
		t.Fatalf("got %d tools, want 1 (shadowed)", len(result.Tools))
	}
	if result.Tools[0].Description != "session version" {
		t.Errorf("got description %q, want 'session version' (shadow)",
			result.Tools[0].Description)
	}
}

func TestE2E_ToolsList_NoSessionToolsForOtherSession(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{Name: "global-tool", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "s1-only", Handler: stubHandler})

	initSessionTestServer(t, s)

	// List tools as conn-2 — should only see the global tool.
	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, _ := s.HandleMessage(ctxWithSession("conn-2"), []byte(listMsg))

	raw, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(raw, &result)

	if len(result.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(result.Tools))
	}
	if result.Tools[0].Name != "global-tool" {
		t.Errorf("got %q, want 'global-tool'", result.Tools[0].Name)
	}
}

func TestE2E_ToolsList_NoSession_OnlyGlobal(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{Name: "global-tool", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "s1-only", Handler: stubHandler})

	initSessionTestServer(t, s)

	// No session in context — should only see global tools.
	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, _ := s.HandleMessage(context.Background(), []byte(listMsg))

	raw, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(raw, &result)

	if len(result.Tools) != 1 || result.Tools[0].Name != "global-tool" {
		t.Errorf("expected only global-tool, got %v", result.Tools)
	}
}

// ── E2E: tools/call routes to session tools ─────────────────────────

func TestE2E_ToolsCall_SessionTool(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "session-only",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`"session-response"`), nil
		},
	})

	initSessionTestServer(t, s)

	callMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"session-only","arguments":{}}}`
	resp, err := s.HandleMessage(ctxWithSession("conn-1"), []byte(callMsg))
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	if !json.Valid(raw) {
		t.Fatalf("invalid JSON: %s", raw)
	}
}

func TestE2E_ToolsCall_SessionTool_NotVisibleToOtherSession(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name:    "s1-only",
		Handler: stubHandler,
	})

	initSessionTestServer(t, s)

	// conn-2 tries to call conn-1's session tool — should get "not found".
	callMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"s1-only","arguments":{}}}`
	resp, _ := s.HandleMessage(ctxWithSession("conn-2"), []byte(callMsg))
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response for tool not visible to this session")
	}
}

func TestE2E_ToolsCall_SessionToolShadowsGlobal(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	_ = s.RegisterTool(&Tool{
		Name: "shared",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`"global"`), nil
		},
	})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "shared",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`"session"`), nil
		},
	})

	initSessionTestServer(t, s)

	callMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"shared","arguments":{}}}`

	// conn-1 should get session version.
	resp1, _ := s.HandleMessage(ctxWithSession("conn-1"), []byte(callMsg))
	raw1, _ := json.Marshal(resp1.Result)
	if !contains(string(raw1), "session") {
		t.Errorf("conn-1 should get session handler, got %s", raw1)
	}

	// conn-2 should get global version.
	resp2, _ := s.HandleMessage(ctxWithSession("conn-2"), []byte(callMsg))
	raw2, _ := json.Marshal(resp2.Result)
	if !contains(string(raw2), "global") {
		t.Errorf("conn-2 should get global handler, got %s", raw2)
	}
}

// ── E2E: cleanup removes session tools ──────────────────────────────

func TestE2E_RemoveSessionTools_CleansUpFromList(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{Name: "global", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "session-t", Handler: stubHandler})

	initSessionTestServer(t, s)

	// Before cleanup: 2 tools visible.
	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp1, _ := s.HandleMessage(ctxWithSession("conn-1"), []byte(listMsg))
	raw1, _ := json.Marshal(resp1.Result)
	var before struct{ Tools []struct{ Name string } }
	_ = json.Unmarshal(raw1, &before)
	if len(before.Tools) != 2 {
		t.Fatalf("before cleanup: got %d tools, want 2", len(before.Tools))
	}

	// Simulate disconnect.
	s.RemoveSessionTools("conn-1")

	// After cleanup: only global tool.
	resp2, _ := s.HandleMessage(ctxWithSession("conn-1"), []byte(listMsg))
	raw2, _ := json.Marshal(resp2.Result)
	var after struct{ Tools []struct{ Name string } }
	_ = json.Unmarshal(raw2, &after)
	if len(after.Tools) != 1 || after.Tools[0].Name != "global" {
		t.Errorf("after cleanup: expected [global], got %v", after.Tools)
	}
}

// ── Fix 1: Shadow callback tests ────────────────────────────────────

func TestServer_AddSessionTool_ShadowCallback(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{Name: "global-tool", Handler: stubHandler})

	var called bool
	var gotSession, gotTool string
	s.OnSessionToolShadow(func(sessionID, toolName string) {
		called = true
		gotSession = sessionID
		gotTool = toolName
	})

	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "global-tool", Handler: stubHandler,
	})

	if !called {
		t.Fatal("shadow callback should have been called")
	}
	if gotSession != "conn-1" {
		t.Errorf("session = %q, want \"conn-1\"", gotSession)
	}
	if gotTool != "global-tool" {
		t.Errorf("tool = %q, want \"global-tool\"", gotTool)
	}
}

func TestServer_AddSessionTool_ShadowCallback_NoShadow(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	var called bool
	s.OnSessionToolShadow(func(string, string) { called = true })

	// Add a session tool that does NOT shadow any global tool.
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "unique-session", Handler: stubHandler,
	})

	if called {
		t.Error("shadow callback should NOT fire when no global tool is shadowed")
	}
}

func TestServer_AddSessionTool_ShadowCallback_Disable(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.RegisterTool(&Tool{Name: "g", Handler: stubHandler})

	s.OnSessionToolShadow(func(string, string) { t.Error("should not fire") })
	s.OnSessionToolShadow(nil) // disable

	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "g", Handler: stubHandler})
	// No assertion needed — the t.Error inside the old callback catches failures.
}

// ── Fix 3: Tenant filter on add ─────────────────────────────────────

func TestServer_AddSessionTool_TenantDenied(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	// Set up a tenant resolver that denies any tool named "blocked".
	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		return &ItemFilter{
			Tool: func(tool *Tool) bool {
				return tool.Name != "blocked"
			},
		}, nil
	})

	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "blocked", Handler: stubHandler,
	})
	if err != errSessionToolDenied {
		t.Errorf("got %v, want errSessionToolDenied", err)
	}

	// Verify the tool was NOT registered.
	if got := s.SessionTools("conn-1"); got != nil {
		t.Errorf("expected nil after denied add, got %v", got)
	}
}

func TestServer_AddSessionTool_TenantAllowed(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		return &ItemFilter{
			Tool: func(tool *Tool) bool {
				return tool.Name == "allowed"
			},
		}, nil
	})

	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "allowed", Handler: stubHandler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := s.SessionTools("conn-1")
	if len(got) != 1 || got[0].Name != "allowed" {
		t.Errorf("expected [allowed], got %v", got)
	}
}

func TestServer_AddSessionTool_TenantResolverError(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		return nil, errors.New("tenant lookup failed")
	})

	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "t", Handler: stubHandler,
	})
	if err != errSessionToolDenied {
		t.Errorf("got %v, want errSessionToolDenied", err)
	}
}

func TestServer_AddSessionTool_NoTenantResolver(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	// No tenant resolver — should allow everything.
	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "t", Handler: stubHandler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Fix 5: Closed-session guard ─────────────────────────────────────

func TestSessionToolRegistry_Add_ClosedSession(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	_ = r.add("s1", &Tool{Name: "a", Handler: stubHandler})

	// Simulate disconnect cleanup.
	r.removeAll("s1")

	// Attempt to add a tool to the closed session.
	err := r.add("s1", &Tool{Name: "b", Handler: stubHandler})
	if err != errSessionClosed {
		t.Errorf("got %v, want errSessionClosed", err)
	}
}

func TestSessionToolRegistry_IsClosed(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	if r.isClosed("s1") {
		t.Error("expected false for never-used session")
	}
	_ = r.add("s1", &Tool{Name: "a", Handler: stubHandler})
	r.removeAll("s1")
	if !r.isClosed("s1") {
		t.Error("expected true after removeAll")
	}
}

func TestServer_AddSessionTool_AfterDisconnect(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "a", Handler: stubHandler})

	// Simulate transport disconnect.
	s.RemoveSessionTools("conn-1")

	// Late in-flight request tries to add — should fail.
	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "b", Handler: stubHandler})
	if err != errSessionClosed {
		t.Errorf("got %v, want errSessionClosed", err)
	}
}

// ── helper ──────────────────────────────────────────────────────────

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Per-session tool limit ──────────────────────────────────────────

func TestSessionToolRegistry_Add_LimitDefault(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()

	// Fill up to DefaultMaxSessionTools.
	for i := 0; i < DefaultMaxSessionTools; i++ {
		err := r.add("s1", &Tool{Name: fmt.Sprintf("t-%03d", i), Handler: stubHandler})
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	// One more should fail.
	err := r.add("s1", &Tool{Name: "over-limit", Handler: stubHandler})
	if err != errSessionToolLimit {
		t.Errorf("got %v, want errSessionToolLimit", err)
	}
}

func TestSessionToolRegistry_Add_LimitCustom(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	r.maxToolsPerSess = 3

	for i := 0; i < 3; i++ {
		if err := r.add("s1", &Tool{Name: fmt.Sprintf("t%d", i), Handler: stubHandler}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	err := r.add("s1", &Tool{Name: "t3", Handler: stubHandler})
	if err != errSessionToolLimit {
		t.Errorf("got %v, want errSessionToolLimit", err)
	}
}

func TestSessionToolRegistry_Add_LimitPerSession(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	r.maxToolsPerSess = 2

	_ = r.add("s1", &Tool{Name: "a", Handler: stubHandler})
	_ = r.add("s1", &Tool{Name: "b", Handler: stubHandler})

	// s1 is full, but s2 should still have room.
	err := r.add("s2", &Tool{Name: "a", Handler: stubHandler})
	if err != nil {
		t.Fatalf("s2 add should succeed: %v", err)
	}
}

func TestServer_WithMaxSessionTools(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithMaxSessionTools(2))

	_ = s.AddSessionTool(context.Background(), "c1", &Tool{Name: "a", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "c1", &Tool{Name: "b", Handler: stubHandler})

	err := s.AddSessionTool(context.Background(), "c1", &Tool{Name: "c", Handler: stubHandler})
	if err != errSessionToolLimit {
		t.Errorf("got %v, want errSessionToolLimit", err)
	}
}

func TestServer_WithMaxSessionTools_Panic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for non-positive value")
		}
	}()
	_ = NewServer("test", "1.0", WithMaxSessionTools(0))
}

// ── Bounded ring buffer for closed sessions ─────────────────────────

func TestSessionToolRegistry_ClosedRingBuffer_Eviction(t *testing.T) {
	t.Parallel()
	r := &sessionToolRegistry{
		sessions:  make(map[string]*sessionTools),
		closed:    make(map[string]struct{}, 5),
		closedRng: make([]string, 5), // small ring for testing
	}

	// Close 5 sessions to fill the ring.
	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("s%d", i)
		_ = r.add(sid, &Tool{Name: "t", Handler: stubHandler})
		r.removeAll(sid)
	}

	// All 5 should be closed.
	for i := 0; i < 5; i++ {
		if !r.isClosed(fmt.Sprintf("s%d", i)) {
			t.Errorf("s%d should be closed", i)
		}
	}

	// Close one more — should evict s0 (the oldest).
	_ = r.add("s5", &Tool{Name: "t", Handler: stubHandler}) // s5 is not closed, so add works
	// Actually s5 hasn't been added because s5 was never closed. Let's just removeAll on a new sid.
	r.removeAll("s5")

	if r.isClosed("s0") {
		t.Error("s0 should have been evicted from closed set")
	}
	if !r.isClosed("s5") {
		t.Error("s5 should be closed")
	}

	// s0 can now be added again (no longer in closed set).
	err := r.add("s0", &Tool{Name: "t", Handler: stubHandler})
	if err != nil {
		t.Errorf("s0 should be addable after eviction: %v", err)
	}
}

// ── Tool name validation in AddSessionTool ──────────────────────────

func TestServer_AddSessionTool_InvalidName_Chars(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	err := s.AddSessionTool(context.Background(), "c1", &Tool{
		Name: "bad tool!", Handler: stubHandler,
	})
	if err != errToolNameChars {
		t.Errorf("got %v, want errToolNameChars", err)
	}
}

func TestServer_AddSessionTool_InvalidName_TooLong(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	longName := strings.Repeat("a", 129)
	err := s.AddSessionTool(context.Background(), "c1", &Tool{
		Name: longName, Handler: stubHandler,
	})
	if err != errToolNameTooLong {
		t.Errorf("got %v, want errToolNameTooLong", err)
	}
}

func TestServer_AddSessionTool_InvalidName_NullByte(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	err := s.AddSessionTool(context.Background(), "c1", &Tool{
		Name: "bad\x00name", Handler: stubHandler,
	})
	if err != errToolNameChars {
		t.Errorf("got %v, want errToolNameChars", err)
	}
}

// ── Context cancellation in AddSessionTool ──────────────────────────

func TestServer_AddSessionTool_ContextCancelled(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	err := s.AddSessionTool(ctx, "c1", &Tool{Name: "t", Handler: stubHandler})
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}

	// Tool should NOT have been registered.
	if got := s.SessionTools("c1"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// ── mergeSessionTools ───────────────────────────────────────────────

func TestMergeSessionTools_BothEmpty(t *testing.T) {
	t.Parallel()
	got := mergeSessionTools(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestMergeSessionTools_GlobalOnly(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "a"}, {Name: "c"}, {Name: "e"}}
	got := mergeSessionTools(global, nil)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	for i, g := range global {
		if got[i].Name != g.Name {
			t.Errorf("index %d: got %q, want %q", i, got[i].Name, g.Name)
		}
	}
}

func TestMergeSessionTools_SessionOnly(t *testing.T) {
	t.Parallel()
	session := []*Tool{{Name: "b"}, {Name: "d"}}
	got := mergeSessionTools(nil, session)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	for i, s := range session {
		if got[i].Name != s.Name {
			t.Errorf("index %d: got %q, want %q", i, got[i].Name, s.Name)
		}
	}
}

func TestMergeSessionTools_Interleave(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "a"}, {Name: "c"}, {Name: "e"}}
	session := []*Tool{{Name: "b"}, {Name: "d"}}
	got := mergeSessionTools(global, session)

	want := []string{"a", "b", "c", "d", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("index %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestMergeSessionTools_Shadow(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "shared", Description: "global"}, {Name: "z"}}
	session := []*Tool{{Name: "shared", Description: "session"}}
	got := mergeSessionTools(global, session)

	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	// "shared" should be the session version.
	if got[0].Name != "shared" || got[0].Description != "session" {
		t.Errorf("got %q/%q, want shared/session", got[0].Name, got[0].Description)
	}
	if got[1].Name != "z" {
		t.Errorf("got %q, want z", got[1].Name)
	}
}

func TestMergeSessionTools_MultipleShadows(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	session := []*Tool{{Name: "a", Description: "sa"}, {Name: "b", Description: "sb"}, {Name: "d"}}
	got := mergeSessionTools(global, session)

	want := []struct{ name, desc string }{
		{"a", "sa"}, {"b", "sb"}, {"c", ""}, {"d", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name {
			t.Errorf("index %d: got name %q, want %q", i, got[i].Name, w.name)
		}
		if got[i].Description != w.desc {
			t.Errorf("index %d: got desc %q, want %q", i, got[i].Description, w.desc)
		}
	}
}

// ── removeAll double-call safety ────────────────────────────────────

func TestSessionToolRegistry_RemoveAll_DoubleCall(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	_ = r.add("s1", &Tool{Name: "t", Handler: stubHandler})

	r.removeAll("s1")
	r.removeAll("s1") // double-call — must not corrupt ring buffer

	if !r.isClosed("s1") {
		t.Error("s1 should still be closed after double removeAll")
	}

	// Verify the ring buffer only consumed one slot.
	r.mu.RLock()
	count := 0
	for _, id := range r.closedRng {
		if id == "s1" {
			count++
		}
	}
	r.mu.RUnlock()
	if count != 1 {
		t.Errorf("expected 1 ring entry for s1, got %d", count)
	}
}

func TestSessionToolRegistry_RemoveAll_DoubleCall_NoEvictionCorruption(t *testing.T) {
	t.Parallel()
	// Use a small ring buffer to exercise eviction.
	r := &sessionToolRegistry{
		sessions:  make(map[string]*sessionTools),
		closed:    make(map[string]struct{}, 3),
		closedRng: make([]string, 3),
	}

	// Fill ring: [s0, s1, s2]
	for i := 0; i < 3; i++ {
		r.removeAll(fmt.Sprintf("s%d", i))
	}

	// Double-call s0 — should NOT take another slot.
	r.removeAll("s0")

	// Add s3 — should evict s0 (slot 0), not leave s0 orphaned.
	r.removeAll("s3")

	if r.isClosed("s0") {
		t.Error("s0 should have been evicted")
	}
	if !r.isClosed("s3") {
		t.Error("s3 should be closed")
	}
	if !r.isClosed("s1") {
		t.Error("s1 should still be closed")
	}
	if !r.isClosed("s2") {
		t.Error("s2 should still be closed")
	}
}

// ── Total concurrent session limit ──────────────────────────────────

func TestSessionToolRegistry_Add_SessionLimit(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	r.maxSessions = 3

	for i := 0; i < 3; i++ {
		err := r.add(fmt.Sprintf("s%d", i), &Tool{Name: "t", Handler: stubHandler})
		if err != nil {
			t.Fatalf("add session %d: %v", i, err)
		}
	}

	// 4th session should fail.
	err := r.add("s3", &Tool{Name: "t", Handler: stubHandler})
	if err != errSessionLimitReached {
		t.Errorf("got %v, want errSessionLimitReached", err)
	}
}

func TestSessionToolRegistry_Add_SessionLimit_ExistingSessionOK(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	r.maxSessions = 2

	_ = r.add("s0", &Tool{Name: "a", Handler: stubHandler})
	_ = r.add("s1", &Tool{Name: "a", Handler: stubHandler})

	// Adding a second tool to an existing session should succeed.
	err := r.add("s0", &Tool{Name: "b", Handler: stubHandler})
	if err != nil {
		t.Fatalf("same session add should succeed: %v", err)
	}
}

func TestSessionToolRegistry_Add_SessionLimit_AfterRemoval(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()
	r.maxSessions = 2

	_ = r.add("s0", &Tool{Name: "t", Handler: stubHandler})
	_ = r.add("s1", &Tool{Name: "t", Handler: stubHandler})

	// Remove s0's only tool — garbage-collects the session entry.
	_ = r.remove("s0", "t")

	// Now a new session should fit.
	err := r.add("s2", &Tool{Name: "t", Handler: stubHandler})
	if err != nil {
		t.Fatalf("should succeed after removal: %v", err)
	}
}

func TestServer_WithMaxSessions(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0", WithMaxSessions(2))

	_ = s.AddSessionTool(context.Background(), "c1", &Tool{Name: "t", Handler: stubHandler})
	_ = s.AddSessionTool(context.Background(), "c2", &Tool{Name: "t", Handler: stubHandler})

	err := s.AddSessionTool(context.Background(), "c3", &Tool{Name: "t", Handler: stubHandler})
	if err != errSessionLimitReached {
		t.Errorf("got %v, want errSessionLimitReached", err)
	}
}

func TestServer_WithMaxSessions_Panic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for non-positive value")
		}
	}()
	_ = NewServer("test", "1.0", WithMaxSessions(0))
}

// ── Lazy-init closed ring buffer ────────────────────────────────────

func TestSessionToolRegistry_LazyInit_ClosedNilUntilRemoveAll(t *testing.T) {
	t.Parallel()
	r := newSessionToolRegistry()

	// Before any removeAll, closed is nil.
	if r.isClosed("x") {
		t.Error("should return false when closed map is nil")
	}

	// add should work without closed being initialized.
	err := r.add("s1", &Tool{Name: "t", Handler: stubHandler})
	if err != nil {
		t.Fatalf("add before removeAll: %v", err)
	}

	// Now removeAll triggers lazy init.
	r.removeAll("s1")
	if !r.isClosed("s1") {
		t.Error("s1 should be closed after removeAll")
	}

	// Verify the ring buffer was allocated.
	r.mu.RLock()
	hasRng := r.closedRng != nil
	r.mu.RUnlock()
	if !hasRng {
		t.Error("closedRng should be allocated after first removeAll")
	}
}

// ── Task-augmented call with session tool ───────────────────────────

func TestE2E_TaskAugmented_SessionTool(t *testing.T) {
	t.Parallel()
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	called := make(chan string, 1)
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "session-task-tool",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			called <- SubscriberIDFromCtx(ctx)
			return []byte("ok"), nil
		},
	})

	initSessionTestServer(t, s)

	// Issue a task-augmented call as conn-1.
	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "session-task-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, err := s.HandleMessage(ctxWithSession("conn-1"), callMsg)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	// The goroutine should receive "conn-1" as its subscriber ID.
	select {
	case sid := <-called:
		if sid != "conn-1" {
			t.Errorf("goroutine got SubscriberID %q, want %q", sid, "conn-1")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session tool handler to be called")
	}
}

func TestE2E_TaskAugmented_SessionTool_TenantFilter(t *testing.T) {
	t.Parallel()
	ts := NewTaskStore()
	s := NewServer("test", "1.0", WithTaskStore(ts))

	// Register a global tool and a session tool.
	_ = s.RegisterTool(&Tool{
		Name: "global-tool",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte("global-ok"), nil
		},
	})
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{
		Name: "session-tool",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			// The goroutine should preserve the tenant filter. If the
			// filter is lost, AllowTool returns true and this call succeeds
			// even though the filter should block it.
			return []byte("session-ok"), nil
		},
	})

	// Set up a tenant resolver that blocks "session-tool".
	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		return &ItemFilter{
			Tool: func(tool *Tool) bool {
				return tool.Name != "session-tool"
			},
		}, nil
	})

	initSessionTestServer(t, s)

	// Task-augmented call for the filtered session tool should fail.
	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "session-tool",
		"arguments": map[string]any{},
		"task":      map[string]any{},
	})
	resp, _ := s.HandleMessage(ctxWithSession("conn-1"), callMsg)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error for tenant-filtered session tool in task-augmented call")
	}
}

// ── Resolver not called when ctx already has filter ─────────────────

func TestServer_AddSessionTool_SkipsResolver_WhenFilterOnCtx(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	resolverCalled := false
	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		resolverCalled = true
		return nil, nil
	})

	// Inject a pre-resolved filter on the context (simulates the dispatch
	// layer having already called the resolver for this request).
	ctx := withItemFilter(context.Background(), &ItemFilter{})

	_ = s.AddSessionTool(ctx, "conn-1", &Tool{Name: "t", Handler: stubHandler})

	if resolverCalled {
		t.Error("resolver should NOT have been called when itemFilter is already on ctx")
	}
}

func TestServer_AddSessionTool_CallsResolver_WhenNoFilterOnCtx(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	resolverCalled := false
	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		resolverCalled = true
		return nil, nil
	})

	// No filter on context — resolver must be called.
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "t", Handler: stubHandler})

	if !resolverCalled {
		t.Error("resolver should be called when no itemFilter on ctx")
	}
}

func TestServer_AddSessionTool_CtxFilterDenies(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	ctx := withItemFilter(context.Background(), &ItemFilter{
		Tool: func(t *Tool) bool { return t.Name != "blocked" },
	})

	err := s.AddSessionTool(ctx, "conn-1", &Tool{Name: "blocked", Handler: stubHandler})
	if err != errSessionToolDenied {
		t.Errorf("got %v, want errSessionToolDenied", err)
	}
}

// ── sessionID validated before resolver ──────────────────────────────

func TestServer_AddSessionTool_EmptyID_NoResolverCall(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	resolverCalled := false
	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		resolverCalled = true
		return nil, nil
	})

	err := s.AddSessionTool(context.Background(), "", &Tool{Name: "t", Handler: stubHandler})
	if err != errSessionIDEmpty {
		t.Errorf("got %v, want errSessionIDEmpty", err)
	}
	if resolverCalled {
		t.Error("resolver should NOT fire for empty sessionID")
	}
}

func TestServer_AddSessionTool_ClosedSession_NoResolverCall(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")
	_ = s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "a", Handler: stubHandler})
	s.RemoveSessionTools("conn-1") // marks closed

	resolverCalled := false
	s.SetTenantResolver(func(ctx context.Context) (*ItemFilter, error) {
		resolverCalled = true
		return nil, nil
	})

	err := s.AddSessionTool(context.Background(), "conn-1", &Tool{Name: "b", Handler: stubHandler})
	if err != errSessionClosed {
		t.Errorf("got %v, want errSessionClosed", err)
	}
	if resolverCalled {
		t.Error("resolver should NOT fire for closed session")
	}
}

// ── mergeSessionToolsFiltered ───────────────────────────────────────

func TestMergeSessionToolsFiltered_NilFilter(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "a"}, {Name: "c"}}
	session := []*Tool{{Name: "b"}}
	got := mergeSessionToolsFiltered(global, session, nil)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("index %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestMergeSessionToolsFiltered_FiltersDuringMerge(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "a"}, {Name: "blocked"}, {Name: "c"}}
	session := []*Tool{{Name: "b"}, {Name: "d"}}
	f := &ItemFilter{
		Tool: func(t *Tool) bool { return t.Name != "blocked" && t.Name != "d" },
	}
	got := mergeSessionToolsFiltered(global, session, f)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("index %d: got %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestMergeSessionToolsFiltered_ShadowFiltered(t *testing.T) {
	t.Parallel()
	global := []*Tool{{Name: "shared", Description: "global"}, {Name: "z"}}
	session := []*Tool{{Name: "shared", Description: "session"}}
	// Filter blocks session version of "shared".
	f := &ItemFilter{
		Tool: func(t *Tool) bool { return t.Description != "session" },
	}
	got := mergeSessionToolsFiltered(global, session, f)
	// "shared" (session) is filtered out, but the global is also shadowed/skipped.
	// Only "z" survives.
	if len(got) != 1 || got[0].Name != "z" {
		names := make([]string, len(got))
		for i, t := range got {
			names[i] = t.Name
		}
		t.Errorf("got %v, want [z]", names)
	}
}
