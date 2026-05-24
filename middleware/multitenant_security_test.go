package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

// ── Deep copy prevents post-construction mutation ───────────────────

func TestStaticTenantStore_DeepCopy_MetadataMutation(t *testing.T) {
	t.Parallel()
	meta := map[string]any{"plan": "free"}
	original := map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(t *finemcp.Tool) bool { return true },
			Metadata:   meta,
		},
	}
	store := middleware.NewStaticTenantStore(original)

	// Mutate the original metadata after construction.
	meta["plan"] = "enterprise"
	meta["injected"] = "evil"

	cfg, err := store.Lookup(context.Background(), "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The store's copy should not reflect the mutation.
	if cfg.Metadata["plan"] != "free" {
		t.Errorf("metadata was mutated: got plan=%v, want free", cfg.Metadata["plan"])
	}
	if _, exists := cfg.Metadata["injected"]; exists {
		t.Error("injected key should not exist in store's copy")
	}
}

func TestStaticTenantStore_DeepCopy_ConfigPointerMutation(t *testing.T) {
	t.Parallel()
	cfg := &middleware.TenantConfig{
		Metadata: map[string]any{"tier": "basic"},
	}
	original := map[string]*middleware.TenantConfig{"acme": cfg}
	store := middleware.NewStaticTenantStore(original)

	// Mutate the original config pointer after construction.
	cfg.Metadata["tier"] = "premium"

	lookup, err := store.Lookup(context.Background(), "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lookup.Metadata["tier"] != "basic" {
		t.Errorf("deep copy failed: got tier=%v, want basic", lookup.Metadata["tier"])
	}
}

func TestStaticTenantStore_DeepCopy_NilConfig(t *testing.T) {
	t.Parallel()
	original := map[string]*middleware.TenantConfig{"acme": nil}
	store := middleware.NewStaticTenantStore(original)

	cfg, err := store.Lookup(context.Background(), "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Error("nil config should remain nil after deep copy")
	}
}

func TestStaticTenantStore_DeepCopy_NilMetadata(t *testing.T) {
	t.Parallel()
	original := map[string]*middleware.TenantConfig{
		"acme": {Metadata: nil},
	}
	store := middleware.NewStaticTenantStore(original)

	cfg, err := store.Lookup(context.Background(), "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Metadata != nil {
		t.Error("nil metadata should remain nil")
	}
}

// ── Panic recovery in Allow* methods ────────────────────────────────

func TestAllowTool_PanicRecovery(t *testing.T) {
	t.Parallel()
	f := &finemcp.ItemFilter{
		Tool: func(*finemcp.Tool) bool { panic("boom") },
	}
	// Should not panic — recovered and returns false (fail-secure).
	if f.AllowTool(&finemcp.Tool{Name: "test"}) {
		t.Error("panicking filter should deny (return false)")
	}
}

func TestAllowResource_PanicRecovery(t *testing.T) {
	t.Parallel()
	f := &finemcp.ItemFilter{
		Resource: func(*finemcp.Resource) bool { panic("boom") },
	}
	if f.AllowResource(&finemcp.Resource{URI: "res://x", Name: "x"}) {
		t.Error("panicking filter should deny (return false)")
	}
}

func TestAllowResourceTemplate_PanicRecovery(t *testing.T) {
	t.Parallel()
	f := &finemcp.ItemFilter{
		ResourceTemplate: func(*finemcp.ResourceTemplate) bool { panic("boom") },
	}
	if f.AllowResourceTemplate(&finemcp.ResourceTemplate{URITemplate: "tmpl://{x}", Name: "x"}) {
		t.Error("panicking filter should deny (return false)")
	}
}

func TestAllowPrompt_PanicRecovery(t *testing.T) {
	t.Parallel()
	f := &finemcp.ItemFilter{
		Prompt: func(*finemcp.Prompt) bool { panic("boom") },
	}
	if f.AllowPrompt(&finemcp.Prompt{Name: "test"}) {
		t.Error("panicking filter should deny (return false)")
	}
}

func TestAllowTool_PanicRecovery_NilArgPanic(t *testing.T) {
	t.Parallel()
	f := &finemcp.ItemFilter{
		Tool: func(t *finemcp.Tool) bool {
			// Simulate a nil dereference bug in a user-provided filter.
			_ = t.Description + t.Name
			return true
		},
	}
	// Passing nil triggers the nil dereference panic in the filter func.
	if f.AllowTool(nil) {
		t.Error("panicking filter should deny (return false)")
	}
}

// ── Tenant ID not leaked in error values ────────────────────────────

func TestStaticTenantStore_LookupNotFound_NoTenantIDInError(t *testing.T) {
	t.Parallel()
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})

	secretID := "secret-tenant-42"
	_, err := store.Lookup(context.Background(), secretID)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, middleware.ErrTenantNotFound) {
		t.Errorf("got error %v, want ErrTenantNotFound", err)
	}
	// The error string must NOT contain the tenant ID.
	if strings.Contains(err.Error(), secretID) {
		t.Errorf("error leaks tenant ID: %q", err.Error())
	}
}

// ── Tenant ID length validation ─────────────────────────────────────

func TestNewTenantResolver_OversizedTenantID_Rejected(t *testing.T) {
	t.Parallel()

	longID := strings.Repeat("a", 257) // exceeds maxTenantIDLength (256)
	extractor := func(ctx context.Context) string { return longID }
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		longID: {},
	})

	resolver := middleware.NewTenantResolver(extractor, store)
	_, err := resolver(context.Background())
	if err == nil {
		t.Fatal("expected rejection for oversized tenant ID")
	}
}

func TestNewTenantResolver_MaxLengthTenantID_Accepted(t *testing.T) {
	t.Parallel()

	maxID := strings.Repeat("a", 256) // exactly at limit
	extractor := func(ctx context.Context) string { return maxID }
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		maxID: {},
	})

	resolver := middleware.NewTenantResolver(extractor, store)
	filter, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("unexpected error for max-length tenant ID: %v", err)
	}
	if filter.TenantID != maxID {
		t.Errorf("got tenant ID length %d, want %d", len(filter.TenantID), len(maxID))
	}
}

// ── E2E: Panicking filter doesn't crash the server ─────────────────

func TestE2E_PanickingToolFilter_ServerSurvives(t *testing.T) {
	t.Parallel()

	srv := finemcp.NewServer("test", "1.0")
	_ = srv.RegisterTool(&finemcp.Tool{
		Name:        "safe-tool",
		Description: "a tool",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`"ok"`), nil
		},
	})

	// Set a resolver whose tool filter always panics.
	srv.SetTenantResolver(middleware.NewTenantResolver(
		func(ctx context.Context) string { return "acme" },
		middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
			"acme": {
				ToolFilter: func(*finemcp.Tool) bool { panic("malicious filter") },
			},
		}),
	))

	initializeServer(t, srv)

	// tools/list should return empty (filter panics → deny all), not crash.
	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := srv.HandleMessage(
		finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"}),
		[]byte(listMsg),
	)
	if err != nil {
		t.Fatalf("HandleMessage returned Go error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %s", resp.Error.Message)
	}

	// Parse result — should have zero tools since filter panicked (deny-all).
	raw, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools (panicking filter = deny all), got %d", len(result.Tools))
	}

	// tools/call should also survive — tool is denied.
	callMsg := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"safe-tool","arguments":{}}}`
	resp2, err := srv.HandleMessage(
		finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"}),
		[]byte(callMsg),
	)
	if err != nil {
		t.Fatalf("HandleMessage returned Go error: %v", err)
	}
	if resp2 == nil || resp2.Error == nil {
		t.Fatal("expected error response for denied tool call")
	}
}

// ── E2E: Oversized tenant ID rejected at protocol level ────────────

func TestE2E_OversizedTenantID_RejectedAtProtocol(t *testing.T) {
	t.Parallel()

	longID := strings.Repeat("x", 300)
	srv := finemcp.NewServer("test", "1.0")
	_ = srv.RegisterTool(&finemcp.Tool{
		Name: "t1", Description: "t", Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`"ok"`), nil
		},
	})

	srv.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
			"acme": {},
		}),
	))

	initializeServer(t, srv)

	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := srv.HandleMessage(
		finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: longID}),
		[]byte(listMsg),
	)
	if err != nil {
		t.Fatalf("HandleMessage returned Go error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response for oversized tenant ID")
	}
	if resp.Error.Code != finemcp.ErrCodeTenantRequired {
		t.Errorf("got error code %d, want %d", resp.Error.Code, finemcp.ErrCodeTenantRequired)
	}
}

// ── Sorted cache correctness ────────────────────────────────────────

func TestE2E_ToolsListOrder_Deterministic(t *testing.T) {
	t.Parallel()

	srv := finemcp.NewServer("test", "1.0")
	for _, name := range []string{"zebra", "alpha", "middle"} {
		n := name
		_ = srv.RegisterTool(&finemcp.Tool{
			Name: n, Description: n, Handler: func(ctx context.Context, input []byte) ([]byte, error) {
				return nil, nil
			},
		})
	}
	initializeServer(t, srv)

	listMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for i := 0; i < 3; i++ {
		resp, err := srv.HandleMessage(context.Background(), []byte(listMsg))
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		raw, _ := json.Marshal(resp.Result)
		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("iteration %d unmarshal: %v", i, err)
		}
		if len(result.Tools) != 3 {
			t.Fatalf("iteration %d: got %d tools, want 3", i, len(result.Tools))
		}
		want := []string{"alpha", "middle", "zebra"}
		for j, w := range want {
			if result.Tools[j].Name != w {
				t.Errorf("iteration %d, index %d: got %q, want %q", i, j, result.Tools[j].Name, w)
			}
		}
	}
}
