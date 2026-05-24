package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	mcwmiddleware "github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

// newDocsServer creates a test HTTP server with two pre-registered tools.
func newDocsServer(t *testing.T, opts ...transport.DocsOption) (*httptest.Server, *finemcp.Server) {
	t.Helper()
	s := finemcp.NewServer("test-mcp", "1.0")

	tool1, err := finemcp.NewTool("greet",
		func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte(`{"message":"hello"}`), nil
		},
		finemcp.WithDescription("Greet a user.\n\n**Required** for all users."),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "description": "User name"},
			},
			"required": []string{"name"},
		}),
	)
	if err != nil {
		t.Fatalf("NewTool greet: %v", err)
	}
	if err := s.RegisterTool(tool1); err != nil {
		t.Fatalf("RegisterTool greet: %v", err)
	}

	ro := true
	tool2, err := finemcp.NewTool("list-items",
		func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte(`["a","b","c"]`), nil
		},
		finemcp.WithDescription("List all items."),
		finemcp.WithAnnotations(finemcp.ToolAnnotations{ReadOnlyHint: &ro}),
		finemcp.WithRoles("admin"),
		finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte(`["simulated"]`), nil
		}),
	)
	if err != nil {
		t.Fatalf("NewTool list-items: %v", err)
	}
	if err := s.RegisterTool(tool2); err != nil {
		t.Fatalf("RegisterTool list-items: %v", err)
	}

	srv := httptest.NewServer(transport.DocsHandler(s, opts...))
	t.Cleanup(srv.Close)
	return srv, s
}

// ── Tests ──────────────────────────────────────────────────────────────────

func TestDocsHandler_IndexReturns200(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html Content-Type, got %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Error("expected HTML document in response body")
	}
}

func TestDocsHandler_ToolsJSON_Shape(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatalf("GET /tools: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}

	var tools []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tools); err != nil {
		t.Fatalf("decode /tools JSON: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}
	for _, field := range []string{"name", "description", "inputSchema"} {
		if _, ok := tools[0][field]; !ok {
			t.Errorf("toolDoc missing field %q", field)
		}
	}
}

func TestDocsHandler_ToolsJSON_AllToolsPresent(t *testing.T) {
	t.Parallel()
	srv, s := newDocsServer(t)

	t3, _ := finemcp.NewTool("delete",
		func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil },
	)
	_ = s.RegisterTool(t3)

	resp, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatalf("GET /tools: %v", err)
	}
	defer resp.Body.Close()

	var tools []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tools)

	names := map[string]bool{}
	for _, tl := range tools {
		if n, ok := tl["name"].(string); ok {
			names[n] = true
		}
	}
	for _, want := range []string{"greet", "list-items", "delete"} {
		if !names[want] {
			t.Errorf("tool %q missing from /tools; got %v", want, names)
		}
	}
}

func TestDocsHandler_Execute_ValidCall(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	body := `{"tool":"greet","input":{"name":"World"}}`
	resp, err := http.Post(srv.URL+"/execute", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	// Content is a sealed interface; decode only the isError sentinel.
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.IsError {
		t.Error("expected success result, got IsError=true")
	}
}

func TestDocsHandler_Execute_ToolNotFound(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Post(srv.URL+"/execute", "application/json",
		strings.NewReader(`{"tool":"no-such-tool","input":{}}`))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDocsHandler_Execute_InvalidJSON(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Post(srv.URL+"/execute", "application/json",
		strings.NewReader("{bad json"))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDocsHandler_Execute_BodyTooLarge(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	big := bytes.Repeat([]byte("x"), 1<<20+10)
	// wrap in a valid but huge JSON string value
	payload := append([]byte(`{"tool":"greet","input":{"name":"`), big...)
	payload = append(payload, '"', '}', '}')

	resp, err := http.Post(srv.URL+"/execute", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

func TestDocsHandler_Execute_DryRun(t *testing.T) {
	t.Parallel()

	// Build a dedicated server with the Simulation middleware so that
	// dryRun:true in the request meta triggers the tool's simulator.
	s := finemcp.NewServer("test-mcp", "1.0")
	s.Use(mcwmiddleware.Simulation())

	ro := true
	tool, err := finemcp.NewTool("list-items",
		func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte(`["a","b","c"]`), nil
		},
		finemcp.WithDescription("List all items."),
		finemcp.WithAnnotations(finemcp.ToolAnnotations{ReadOnlyHint: &ro}),
		finemcp.WithSimulator(func(_ context.Context, _ []byte) ([]byte, error) {
			return []byte(`["simulated"]`), nil
		}),
	)
	if err != nil {
		t.Fatalf("NewTool list-items: %v", err)
	}
	if err := s.RegisterTool(tool); err != nil {
		t.Fatalf("RegisterTool list-items: %v", err)
	}

	srv := httptest.NewServer(transport.DocsHandler(s))
	t.Cleanup(srv.Close)

	// list-items has a simulator that returns ["simulated"].
	body := `{"tool":"list-items","input":{},"dryRun":true}`
	resp, err := http.Post(srv.URL+"/execute", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "simulated") {
		t.Errorf("expected simulator output, got: %s", b)
	}
}

func TestDocsHandler_Export_Returns200(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Get(srv.URL + "/export")
	if err != nil {
		t.Fatalf("GET /export: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment Content-Disposition, got %q", cd)
	}
	if !strings.Contains(cd, "docs.html") {
		t.Errorf("expected filename docs.html, got %q", cd)
	}
}

func TestDocsHandler_Export_ContainsToolData(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Get(srv.URL + "/export")
	if err != nil {
		t.Fatalf("GET /export: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	for _, name := range []string{"greet", "list-items"} {
		if !strings.Contains(html, name) {
			t.Errorf("exported HTML should contain tool name %q", name)
		}
	}
}

func TestDocsHandler_WithBaseURL_EmbeddedInPage(t *testing.T) {
	t.Parallel()
	const wantURL = "https://api.example.com/docs"
	srv, _ := newDocsServer(t, transport.WithDocsBaseURL(wantURL))

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), wantURL) {
		t.Errorf("expected base URL %q embedded in HTML", wantURL)
	}
}

func TestDocsHandler_MethodNotAllowed_Execute(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Get(srv.URL + "/execute")
	if err != nil {
		t.Fatalf("GET /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestDocsHandler_TitleOption(t *testing.T) {
	t.Parallel()
	const title = "My Custom API Docs"
	srv, _ := newDocsServer(t, transport.WithDocsTitle(title))

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), title) {
		t.Errorf("expected custom title %q in HTML", title)
	}
}

func TestDocsHandler_NilSchema_DoesNotPanic(t *testing.T) {
	t.Parallel()
	s := finemcp.NewServer("ns", "1.0")
	tool, _ := finemcp.NewTool("no-schema",
		func(_ context.Context, _ []byte) ([]byte, error) { return []byte("ok"), nil },
	)
	_ = s.RegisterTool(tool)

	srv := httptest.NewServer(transport.DocsHandler(s))
	t.Cleanup(srv.Close)

	for _, path := range []string{"/tools", "/"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d", path, resp.StatusCode)
		}
	}
}

func TestDocsHandler_Execute_MissingToolField(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Post(srv.URL+"/execute", "application/json",
		strings.NewReader(`{"input":{}}`))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing tool name, got %d", resp.StatusCode)
	}
}

// ── Tests for review fixes ────────────────────────────────────────────────

func TestDocsHandler_Execute_WrongContentType(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Post(srv.URL+"/execute", "text/plain",
		strings.NewReader(`{"tool":"greet","input":{}}`))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", resp.StatusCode)
	}
}

func TestDocsHandler_WithCORS_HeadersPresent(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t, transport.WithCORS("https://example.com"))

	resp, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatalf("GET /tools: %v", err)
	}
	defer resp.Body.Close()

	got := resp.Header.Get("Access-Control-Allow-Origin")
	if got != "https://example.com" {
		t.Errorf("expected CORS origin %q, got %q", "https://example.com", got)
	}
}

func TestDocsHandler_WithCORS_Preflight(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t, transport.WithCORS("*"))

	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/execute", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /execute: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("expected POST in Allow-Methods, got %q", got)
	}
}

func TestDocsHandler_WithToolFilter_HidesTool(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t, transport.WithToolFilter(func(_ *http.Request, tool *finemcp.Tool) bool {
		return tool.Name != "list-items"
	}))

	resp, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatalf("GET /tools: %v", err)
	}
	defer resp.Body.Close()

	var tools []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tools)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool after filtering, got %d", len(tools))
	}
	if tools[0]["name"] != "greet" {
		t.Errorf("expected 'greet', got %q", tools[0]["name"])
	}
}

func TestDocsHandler_WithToolFilter_UIAlsoFilters(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t, transport.WithToolFilter(func(_ *http.Request, tool *finemcp.Tool) bool {
		return tool.Name != "list-items"
	}))

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if strings.Contains(html, "list-items") {
		t.Error("filtered tool 'list-items' should not appear in rendered HTML")
	}
	if !strings.Contains(html, "greet") {
		t.Error("non-filtered tool 'greet' should appear in rendered HTML")
	}
}

func TestDocsHandler_Execute_RateLimit(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t, transport.WithExecuteRateLimit(2))

	body := `{"tool":"greet","input":{"name":"test"}}`
	for i := 0; i < 2; i++ {
		resp, err := http.Post(srv.URL+"/execute", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	// Third request should be rate-limited.
	resp, err := http.Post(srv.URL+"/execute", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request 3: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on third request, got %d", resp.StatusCode)
	}
}

func TestDocsHandler_Execute_InvalidJSON_GenericMessage(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t)

	resp, err := http.Post(srv.URL+"/execute", "application/json",
		strings.NewReader(`{bad json`))
	if err != nil {
		t.Fatalf("POST /execute: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "invalid character") {
		t.Error("error response should not leak internal JSON parse details")
	}
}

func TestDocsHandler_WithToolFilter_PanicRecovery(t *testing.T) {
	t.Parallel()
	srv, _ := newDocsServer(t, transport.WithToolFilter(func(_ *http.Request, _ *finemcp.Tool) bool {
		panic("boom")
	}))

	// GET /tools should survive the panic without crashing the server.
	resp, err := http.Get(srv.URL + "/tools")
	if err != nil {
		t.Fatalf("GET /tools: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var tools []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tools)
	if len(tools) != 0 {
		t.Errorf("expected 0 tools (panicking filter = deny all), got %d", len(tools))
	}
}
