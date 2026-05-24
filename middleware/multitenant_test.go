package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

// ── TenantExtractor tests ───────────────────────────────────────────

func TestTenantFromAuthSubject_ReturnsSubject(t *testing.T) {
	t.Parallel()
	extractor := middleware.TenantFromAuthSubject()

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	if got := extractor(ctx); got != "acme" {
		t.Errorf("got %q, want %q", got, "acme")
	}
}

func TestTenantFromAuthSubject_NoAuthInfo(t *testing.T) {
	t.Parallel()
	extractor := middleware.TenantFromAuthSubject()

	if got := extractor(context.Background()); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTenantFromAuthMeta_ReturnsMetaValue(t *testing.T) {
	t.Parallel()
	extractor := middleware.TenantFromAuthMeta("tenant_id")

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Meta:    map[string]any{"tenant_id": "globex"},
	})
	if got := extractor(ctx); got != "globex" {
		t.Errorf("got %q, want %q", got, "globex")
	}
}

func TestTenantFromAuthMeta_MissingKey(t *testing.T) {
	t.Parallel()
	extractor := middleware.TenantFromAuthMeta("tenant_id")

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Meta:    map[string]any{"other_key": "value"},
	})
	if got := extractor(ctx); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTenantFromAuthMeta_NotString(t *testing.T) {
	t.Parallel()
	extractor := middleware.TenantFromAuthMeta("tenant_id")

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Meta:    map[string]any{"tenant_id": 42},
	})
	if got := extractor(ctx); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTenantFromAuthMeta_NoAuthInfo(t *testing.T) {
	t.Parallel()
	extractor := middleware.TenantFromAuthMeta("tenant_id")

	if got := extractor(context.Background()); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTenantFromAuthMeta_EmptyKeyPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty key")
		}
	}()
	middleware.TenantFromAuthMeta("")
}

// ── StaticTenantStore tests ─────────────────────────────────────────

func TestStaticTenantStore_LookupFound(t *testing.T) {
	t.Parallel()
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name == "search" },
		},
	})

	cfg, err := store.Lookup(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestStaticTenantStore_LookupNotFound(t *testing.T) {
	t.Parallel()
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})

	_, err := store.Lookup(context.Background(), "unknown")
	if !errors.Is(err, middleware.ErrTenantNotFound) {
		t.Errorf("got error %v, want ErrTenantNotFound", err)
	}
}

func TestStaticTenantStore_DefensiveCopy(t *testing.T) {
	t.Parallel()
	original := map[string]*middleware.TenantConfig{
		"acme": {},
	}
	store := middleware.NewStaticTenantStore(original)

	// Mutate the original map after construction.
	original["evil"] = &middleware.TenantConfig{}

	// The store should NOT see the mutation.
	_, err := store.Lookup(context.Background(), "evil")
	if !errors.Is(err, middleware.ErrTenantNotFound) {
		t.Error("expected store to be immune to original map mutation")
	}
}

func TestStaticTenantStore_NilPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil configs")
		}
	}()
	middleware.NewStaticTenantStore(nil)
}

func TestStaticTenantStore_EmptyMap(t *testing.T) {
	t.Parallel()
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{})

	_, err := store.Lookup(context.Background(), "any")
	if !errors.Is(err, middleware.ErrTenantNotFound) {
		t.Errorf("got error %v, want ErrTenantNotFound", err)
	}
}

// ── NewTenantResolver tests ─────────────────────────────────────────

func TestNewTenantResolver_HappyPath(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name == "search" },
		},
	})
	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	filter, err := resolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if filter == nil {
		t.Fatal("expected non-nil filter")
	}
	if filter.TenantID != "acme" {
		t.Errorf("TenantID = %q, want %q", filter.TenantID, "acme")
	}
}

func TestNewTenantResolver_NoTenantID_Rejects(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	// No AuthInfo in context → extractor returns "" → rejection.
	_, err := resolver(context.Background())
	if err == nil {
		t.Fatal("expected error when no tenant ID")
	}
	if !errors.Is(err, middleware.ErrTenantRequired) {
		t.Errorf("got error %v, want ErrTenantRequired", err)
	}
}

func TestNewTenantResolver_UnknownTenant_Rejects(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "unknown"})
	_, err := resolver(ctx)
	if err == nil {
		t.Fatal("expected error for unknown tenant")
	}
}

func TestNewTenantResolver_WithFallbackTenant(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"default": {
			ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name == "public" },
		},
	})
	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
		middleware.WithFallbackTenant("default"),
	)

	// No AuthInfo → extractor returns "" → fallback to "default".
	filter, err := resolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filter.TenantID != "default" {
		t.Errorf("TenantID = %q, want %q", filter.TenantID, "default")
	}
}

func TestNewTenantResolver_FallbackOverridesEmpty(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"fallback": {},
	})

	// Custom extractor that always returns "".
	emptyExtractor := middleware.TenantExtractor(func(_ context.Context) string { return "" })

	resolver := middleware.NewTenantResolver(
		emptyExtractor,
		store,
		middleware.WithFallbackTenant("fallback"),
	)

	filter, err := resolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filter.TenantID != "fallback" {
		t.Errorf("TenantID = %q, want %q", filter.TenantID, "fallback")
	}
}

func TestNewTenantResolver_NilAllFilters(t *testing.T) {
	t.Parallel()

	// TenantConfig with all nil filters → allow-all behavior.
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	filter, err := resolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// All Allow* methods should return true for nil filter fields.
	t1, _ := finemcp.NewTool("any", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	if !filter.AllowTool(t1) {
		t.Error("AllowTool should return true for nil ToolFilter")
	}
}

func TestNewTenantResolver_NilExtractorPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil extractor")
		}
	}()
	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{})
	middleware.NewTenantResolver(nil, store)
}

func TestNewTenantResolver_NilStorePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil store")
		}
	}()
	middleware.NewTenantResolver(middleware.TenantFromAuthSubject(), nil)
}

// ── ItemFilter nil-safe methods ─────────────────────────────────────

func TestItemFilter_AllowTool_NilFilter(t *testing.T) {
	t.Parallel()
	var f *finemcp.ItemFilter
	tool, _ := finemcp.NewTool("test", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	if !f.AllowTool(tool) {
		t.Error("nil ItemFilter should allow all tools")
	}
}

func TestItemFilter_AllowTool_NilToolFunc(t *testing.T) {
	t.Parallel()
	f := &finemcp.ItemFilter{}
	tool, _ := finemcp.NewTool("test", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	})
	if !f.AllowTool(tool) {
		t.Error("nil Tool func should allow all tools")
	}
}

func TestItemFilter_AllowResource_NilFilter(t *testing.T) {
	t.Parallel()
	var f *finemcp.ItemFilter
	r := &finemcp.Resource{URI: "file://test", Name: "test"}
	if !f.AllowResource(r) {
		t.Error("nil ItemFilter should allow all resources")
	}
}

func TestItemFilter_AllowPrompt_NilFilter(t *testing.T) {
	t.Parallel()
	var f *finemcp.ItemFilter
	p := &finemcp.Prompt{Name: "test"}
	if !f.AllowPrompt(p) {
		t.Error("nil ItemFilter should allow all prompts")
	}
}

func TestItemFilter_AllowResourceTemplate_NilFilter(t *testing.T) {
	t.Parallel()
	var f *finemcp.ItemFilter
	rt := &finemcp.ResourceTemplate{URITemplate: "file://{name}", Name: "test"}
	if !f.AllowResourceTemplate(rt) {
		t.Error("nil ItemFilter should allow all resource templates")
	}
}

// ── Integration: E2E filtering through HandleMessage ────────────────

// initializeServer sends the initialize handshake so the server will accept
// subsequent requests.
func initializeServer(t *testing.T, s *finemcp.Server) {
	t.Helper()
	initMsg := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	resp, err := s.HandleMessage(context.Background(), []byte(initMsg))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp != nil && resp.Error != nil {
		t.Fatalf("initialize error: %s", resp.Error.Message)
	}
	// Send initialized notification.
	_, _ = s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
}

func TestE2E_ToolsList_FilteredByTenant(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	search, _ := finemcp.NewTool("search", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	admin, _ := finemcp.NewTool("admin-panel", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	s.RegisterTool(search)
	s.RegisterTool(admin)

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name == "search" },
		},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	data, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "search" {
		t.Errorf("expected tool 'search', got %q", result.Tools[0].Name)
	}
}

func TestE2E_ToolsCall_FilteredToolReturnsNotFound(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	secret, _ := finemcp.NewTool("secret", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("secret-data"), nil })
	s.RegisterTool(secret)

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name != "secret" },
		},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for filtered tool call")
	}
	if resp.Error.Code != finemcp.ErrCodeInvalidParams {
		t.Errorf("expected error code %d, got %d", finemcp.ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestE2E_ToolsCall_AllowedToolSucceeds(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	allowed, _ := finemcp.NewTool("allowed", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	s.RegisterTool(allowed)

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name == "allowed" },
		},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"allowed"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestE2E_NoTenantID_Rejected(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	// No AuthInfo in context → tenant resolution fails.
	resp, err := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error when no tenant ID")
	}
	if resp.Error.Code != finemcp.ErrCodeTenantRequired {
		t.Errorf("expected error code %d, got %d", finemcp.ErrCodeTenantRequired, resp.Error.Code)
	}
	if resp.Error.Message != "tenant identification required" {
		t.Errorf("error message = %q, want generic", resp.Error.Message)
	}
}

func TestE2E_UnknownTenant_Rejected(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "evil-corp"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown tenant")
	}
	if resp.Error.Code != finemcp.ErrCodeTenantRequired {
		t.Errorf("expected error code %d, got %d", finemcp.ErrCodeTenantRequired, resp.Error.Code)
	}
	if resp.Error.Message != "tenant identification required" {
		t.Errorf("error message = %q, want generic", resp.Error.Message)
	}
}

func TestE2E_NoResolver_AllItemsVisible(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	t1, _ := finemcp.NewTool("tool-a", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	t2, _ := finemcp.NewTool("tool-b", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	s.RegisterTool(t1)
	s.RegisterTool(t2)

	initializeServer(t, s)

	resp, err := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	data, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	json.Unmarshal(data, &result)

	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result.Tools))
	}
}

func TestE2E_PromptsList_FilteredByTenant(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	s.RegisterPrompt(&finemcp.Prompt{
		Name: "public-prompt",
		Handler: func(_ context.Context, _ map[string]string) ([]finemcp.PromptMessage, error) {
			return nil, nil
		},
	})
	s.RegisterPrompt(&finemcp.Prompt{
		Name: "private-prompt",
		Handler: func(_ context.Context, _ map[string]string) ([]finemcp.PromptMessage, error) {
			return nil, nil
		},
	})

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			PromptFilter: func(p *finemcp.Prompt) bool { return p.Name == "public-prompt" },
		},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	data, _ := json.Marshal(resp.Result)
	var result struct {
		Prompts []struct {
			Name string `json:"name"`
		} `json:"prompts"`
	}
	json.Unmarshal(data, &result)

	if len(result.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(result.Prompts))
	}
	if result.Prompts[0].Name != "public-prompt" {
		t.Errorf("expected 'public-prompt', got %q", result.Prompts[0].Name)
	}
}

func TestE2E_PromptsGet_FilteredPromptNotFound(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	s.RegisterPrompt(&finemcp.Prompt{
		Name: "secret-prompt",
		Handler: func(_ context.Context, _ map[string]string) ([]finemcp.PromptMessage, error) {
			return nil, nil
		},
	})

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			PromptFilter: func(p *finemcp.Prompt) bool { return p.Name != "secret-prompt" },
		},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"secret-prompt"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for filtered prompt")
	}
}

func TestE2E_PingBypassesTenantResolution(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	// Ping should work without any auth/tenant context.
	resp, err := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("ping should bypass tenant resolution, got: %s", resp.Error.Message)
	}
}

func TestE2E_MultipleTenants_IsolatedViews(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	t1, _ := finemcp.NewTool("shared", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	t2, _ := finemcp.NewTool("acme-only", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	t3, _ := finemcp.NewTool("globex-only", func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil })
	s.RegisterTool(t1)
	s.RegisterTool(t2)
	s.RegisterTool(t3)

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool {
				return tool.Name == "shared" || tool.Name == "acme-only"
			},
		},
		"globex": {
			ToolFilter: func(tool *finemcp.Tool) bool {
				return tool.Name == "shared" || tool.Name == "globex-only"
			},
		},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	toolNames := func(ctx context.Context) []string {
		resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(resp.Result)
		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		json.Unmarshal(data, &result)
		names := make([]string, len(result.Tools))
		for i, t := range result.Tools {
			names[i] = t.Name
		}
		return names
	}

	acmeCtx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	acmeTools := toolNames(acmeCtx)
	if len(acmeTools) != 2 {
		t.Fatalf("acme: expected 2 tools, got %v", acmeTools)
	}

	globexCtx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "globex"})
	globexTools := toolNames(globexCtx)
	if len(globexTools) != 2 {
		t.Fatalf("globex: expected 2 tools, got %v", globexTools)
	}

	for _, name := range acmeTools {
		if name == "globex-only" {
			t.Error("acme should not see globex-only")
		}
	}
	for _, name := range globexTools {
		if name == "acme-only" {
			t.Error("globex should not see acme-only")
		}
	}
}

func TestE2E_TenantIDInContext_AvailableToHandler(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	var capturedTenantID string
	tool, _ := finemcp.NewTool("check", func(ctx context.Context, _ []byte) ([]byte, error) {
		capturedTenantID = finemcp.TenantIDFromCtx(ctx)
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})
	s.SetTenantResolver(middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	))

	initializeServer(t, s)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	resp, err := s.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if capturedTenantID != "acme" {
		t.Errorf("tenant ID in handler context = %q, want %q", capturedTenantID, "acme")
	}
}

// ── Concurrent access ───────────────────────────────────────────────

func TestSetTenantResolver_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	server := finemcp.NewServer("race-test", "1.0.0")

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {},
	})

	var wg sync.WaitGroup
	const goroutines = 20

	// Half update the resolver.
	for i := range goroutines / 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				server.SetTenantResolver(middleware.NewTenantResolver(
					middleware.TenantFromAuthSubject(),
					store,
				))
			} else {
				server.SetTenantResolver(nil)
			}
		}(i)
	}

	// Half send requests.
	for range goroutines / 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
			_, _ = server.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		}()
	}

	wg.Wait()
}

func TestNewTenantResolver_ConcurrentLookups(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme":   {ToolFilter: func(t *finemcp.Tool) bool { return t.Name == "search" }},
		"globex": {ToolFilter: func(t *finemcp.Tool) bool { return t.Name == "admin" }},
	})

	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	var wg sync.WaitGroup
	const goroutines = 50
	errs := make(chan error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tenant := "acme"
			if i%2 == 1 {
				tenant = "globex"
			}
			ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: tenant})
			filter, err := resolver(ctx)
			if err != nil {
				errs <- err
				return
			}
			if filter.TenantID != tenant {
				errs <- errors.New("wrong tenant ID: " + filter.TenantID)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// ── TenantConfig.toItemFilter ───────────────────────────────────────

func TestTenantConfig_NilFilters_AllowAll(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {}, // all nil filters
	})

	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	filter, err := resolver(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// All Allow* methods should return true.
	tool, _ := finemcp.NewTool("any", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	if !filter.AllowTool(tool) {
		t.Error("nil ToolFilter should allow all tools")
	}
	if !filter.AllowResource(&finemcp.Resource{URI: "any", Name: "any"}) {
		t.Error("nil ResourceFilter should allow all resources")
	}
	if !filter.AllowPrompt(&finemcp.Prompt{Name: "any"}) {
		t.Error("nil PromptFilter should allow all prompts")
	}
	if !filter.AllowResourceTemplate(&finemcp.ResourceTemplate{URITemplate: "any", Name: "any"}) {
		t.Error("nil ResourceTemplateFilter should allow all resource templates")
	}
}

func TestTenantConfig_ToolFilter_FiltersCorrectly(t *testing.T) {
	t.Parallel()

	store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
		"acme": {
			ToolFilter: func(tool *finemcp.Tool) bool {
				return tool.Name == "search" || tool.Name == "summarize"
			},
		},
	})

	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "acme"})
	filter, err := resolver(ctx)
	if err != nil {
		t.Fatal(err)
	}

	allowed, _ := finemcp.NewTool("search", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	denied, _ := finemcp.NewTool("admin-panel", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })

	if !filter.AllowTool(allowed) {
		t.Error("expected search to be allowed")
	}
	if filter.AllowTool(denied) {
		t.Error("expected admin-panel to be denied")
	}
}

// ── Context helpers ─────────────────────────────────────────────────

func TestWithTenantID_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := finemcp.WithTenantID(context.Background(), "acme")
	if got := finemcp.TenantIDFromCtx(ctx); got != "acme" {
		t.Errorf("got %q, want %q", got, "acme")
	}
}

func TestTenantIDFromCtx_Empty(t *testing.T) {
	t.Parallel()

	if got := finemcp.TenantIDFromCtx(context.Background()); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ── Dynamic TenantStore ─────────────────────────────────────────────

type mockTenantStore struct {
	lookupFn func(ctx context.Context, tenantID string) (*middleware.TenantConfig, error)
}

func (m *mockTenantStore) Lookup(ctx context.Context, tenantID string) (*middleware.TenantConfig, error) {
	return m.lookupFn(ctx, tenantID)
}

func TestNewTenantResolver_DynamicStore(t *testing.T) {
	t.Parallel()

	store := &mockTenantStore{
		lookupFn: func(_ context.Context, tenantID string) (*middleware.TenantConfig, error) {
			if tenantID == "dynamic" {
				return &middleware.TenantConfig{
					ToolFilter: func(tool *finemcp.Tool) bool { return tool.Name == "api" },
				}, nil
			}
			return nil, middleware.ErrTenantNotFound
		},
	}

	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "dynamic"})
	filter, err := resolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if filter.TenantID != "dynamic" {
		t.Errorf("TenantID = %q, want %q", filter.TenantID, "dynamic")
	}
}

func TestNewTenantResolver_StoreError_Rejects(t *testing.T) {
	t.Parallel()

	store := &mockTenantStore{
		lookupFn: func(_ context.Context, _ string) (*middleware.TenantConfig, error) {
			return nil, errors.New("database connection failed")
		},
	}

	resolver := middleware.NewTenantResolver(
		middleware.TenantFromAuthSubject(),
		store,
	)

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "tenant"})
	_, err := resolver(ctx)
	if err == nil {
		t.Fatal("expected error from store failure")
	}
}
