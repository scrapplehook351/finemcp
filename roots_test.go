package finemcp

import (
	"context"
	"encoding/json"
	"testing"
)

// ── Root Registration Tests ────────────────────────────────────────

func TestNewRoot(t *testing.T) {
	r, err := NewRoot("file:///workspace", WithRootName("workspace"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.URI != "file:///workspace" {
		t.Errorf("URI = %q, want %q", r.URI, "file:///workspace")
	}
	if r.Name != "workspace" {
		t.Errorf("Name = %q, want %q", r.Name, "workspace")
	}
}

func TestNewRoot_EmptyURI(t *testing.T) {
	_, err := NewRoot("")
	if err != errRootURIEmpty {
		t.Errorf("err = %v, want %v", err, errRootURIEmpty)
	}
}

func TestRegisterRoot_Success(t *testing.T) {
	s := NewServer("test", "1.0.0")
	r, _ := NewRoot("file:///workspace")

	err := s.RegisterRoot(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	roots := s.ListRoots()
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	if roots[0].URI != "file:///workspace" {
		t.Errorf("URI = %q, want %q", roots[0].URI, "file:///workspace")
	}
}

func TestRegisterRoot_Nil(t *testing.T) {
	s := NewServer("test", "1.0.0")
	err := s.RegisterRoot(nil)
	if err != errRootNil {
		t.Errorf("err = %v, want %v", err, errRootNil)
	}
}

func TestRegisterRoot_Duplicate(t *testing.T) {
	s := NewServer("test", "1.0.0")
	r1, _ := NewRoot("file:///workspace")
	r2, _ := NewRoot("file:///workspace", WithRootName("different"))

	s.RegisterRoot(r1)
	err := s.RegisterRoot(r2)
	if err != errRootAlreadyExists {
		t.Errorf("err = %v, want %v", err, errRootAlreadyExists)
	}
}

func TestRegisterRoot_EmptyURI(t *testing.T) {
	s := NewServer("test", "1.0.0")
	r := &Root{URI: "", Name: "test"}

	err := s.RegisterRoot(r)
	if err != errRootURIEmpty {
		t.Errorf("err = %v, want %v", err, errRootURIEmpty)
	}
}

func TestListRoots_Empty(t *testing.T) {
	s := NewServer("test", "1.0.0")
	roots := s.ListRoots()
	if len(roots) != 0 {
		t.Errorf("expected 0 roots, got %d", len(roots))
	}
}

func TestListRoots_Sorted(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.RegisterRoot(&Root{URI: "file:///workspace/c"})
	s.RegisterRoot(&Root{URI: "file:///workspace/a"})
	s.RegisterRoot(&Root{URI: "file:///workspace/b"})

	roots := s.ListRoots()
	if len(roots) != 3 {
		t.Fatalf("expected 3 roots, got %d", len(roots))
	}
	if roots[0].URI != "file:///workspace/a" {
		t.Errorf("roots[0].URI = %q, want %q", roots[0].URI, "file:///workspace/a")
	}
	if roots[1].URI != "file:///workspace/b" {
		t.Errorf("roots[1].URI = %q, want %q", roots[1].URI, "file:///workspace/b")
	}
	if roots[2].URI != "file:///workspace/c" {
		t.Errorf("roots[2].URI = %q, want %q", roots[2].URI, "file:///workspace/c")
	}
}

// ── Notification Tests ──────────────────────────────────────────────

func TestNotifyRootsListChanged_Broadcast(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")

	var received1, received2 *JSONRPCNotification
	sender1 := func(n *JSONRPCNotification) {
		received1 = n
	}
	sender2 := func(n *JSONRPCNotification) {
		received2 = n
	}

	if err := s.AddSender("client-1", sender1); err != nil {
		t.Fatalf("AddSender client-1: %v", err)
	}
	if err := s.AddSender("client-2", sender2); err != nil {
		t.Fatalf("AddSender client-2: %v", err)
	}

	s.NotifyRootsListChanged()

	if received1 == nil {
		t.Fatal("expected notification to client-1, got nil")
	}
	if received1.Method != "notifications/roots/list_changed" {
		t.Errorf("client-1 method = %q, want %q", received1.Method, "notifications/roots/list_changed")
	}

	if received2 == nil {
		t.Fatal("expected notification to client-2, got nil")
	}
	if received2.Method != "notifications/roots/list_changed" {
		t.Errorf("client-2 method = %q, want %q", received2.Method, "notifications/roots/list_changed")
	}
}

func TestNotifyRootsListChanged_AfterRemoval(t *testing.T) {
	t.Parallel()

	s := NewServer("test", "1.0.0")

	var received *JSONRPCNotification
	sender := func(n *JSONRPCNotification) {
		received = n
	}

	if err := s.AddSender("client-1", sender); err != nil {
		t.Fatalf("AddSender: %v", err)
	}

	s.RemoveSender("client-1")
	received = nil
	s.NotifyRootsListChanged()

	if received != nil {
		t.Errorf("expected no notification after removal, got %v", received)
	}
}

// ── Handler Tests (roots/list) ──────────────────────────────────────

func initServerWithRoots(t *testing.T) *Server {
	t.Helper()
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	r1, _ := NewRoot("file:///workspace/a", WithRootName("Project A"))
	r2, _ := NewRoot("file:///workspace/b", WithRootName("Project B"))
	s.RegisterRoot(r1)
	s.RegisterRoot(r2)
	return s
}

func TestHandleMessage_RootsList(t *testing.T) {
	s := initServerWithRoots(t)

	msg := jsonrpcReq(1, "roots/list", nil)
	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var result ListRootsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(result.Roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(result.Roots))
	}
	if result.Roots[0].URI != "file:///workspace/a" {
		t.Errorf("roots[0].URI = %q, want %q", result.Roots[0].URI, "file:///workspace/a")
	}
	if result.Roots[0].Name != "Project A" {
		t.Errorf("roots[0].Name = %q, want %q", result.Roots[0].Name, "Project A")
	}
}

func TestHandleMessage_RootsList_Empty(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	msg := jsonrpcReq(1, "roots/list", nil)
	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var result ListRootsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(result.Roots) != 0 {
		t.Errorf("expected 0 roots, got %d", len(result.Roots))
	}
}

func TestHandleMessage_RootsList_BeforeInit(t *testing.T) {
	s := NewServer("test", "1.0.0")
	r, _ := NewRoot("file:///workspace")
	s.RegisterRoot(r)

	msg := jsonrpcReq(1, "roots/list", nil)
	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error before init")
	}
}

func TestHandleMessage_RootsList_WithPagination(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)

	// Register 3 roots
	for i := 0; i < 3; i++ {
		r, _ := NewRoot("file:///workspace/" + string(rune('a'+i)))
		s.RegisterRoot(r)
	}

	// Request first page with limit 2
	params := map[string]any{"limit": 2}
	msg := jsonrpcReq(1, "roots/list", params)
	resp, err := s.HandleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var result ListRootsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(result.Roots) != 2 {
		t.Fatalf("expected 2 roots in first page, got %d", len(result.Roots))
	}
	if result.NextCursor == "" {
		t.Error("expected nextCursor for more pages")
	}

	// Request second page
	params2 := map[string]any{"cursor": result.NextCursor, "limit": 2}
	msg2 := jsonrpcReq(2, "roots/list", params2)
	resp2, err := s.HandleMessage(context.Background(), msg2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw2, _ := json.Marshal(resp2.Result)
	var result2 ListRootsResult
	json.Unmarshal(raw2, &result2)

	if len(result2.Roots) != 1 {
		t.Fatalf("expected 1 root in second page, got %d", len(result2.Roots))
	}
	if result2.NextCursor != "" {
		t.Error("expected no nextCursor on last page")
	}
}
