// Package mcptest provides test helpers for MCP servers built with finemcp.
//
// It offers an in-process [Server] that communicates via direct Go calls
// (no network I/O), assertion helpers for common response patterns, and
// fixture / golden-file utilities.
//
// Usage:
//
//	func TestMyTool(t *testing.T) {
//	    ts := mcptest.NewServer(t, mcptest.WithTool("ping", pingHandler))
//	    defer ts.Close()
//
//	    ts.Initialize(t)
//	    resp := ts.CallTool(t, "ping", nil)
//	    mcptest.AssertToolResult(t, resp, "pong")
//	}
package mcptest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
)

const protocolVersion = finemcp.ProtocolVersion

// ── Server ──────────────────────────────────────────────────────────

// Server is an in-process MCP test server. It wraps [finemcp.Server]
// and provides convenience methods for sending JSON-RPC requests
// without any network transport.
type Server struct {
	inner  *finemcp.Server
	nextID int64 // auto-incrementing id for RawCall
}

// Option configures a [Server].
type Option func(*serverConfig)

type serverConfig struct {
	tools      []*finemcp.Tool
	rawTools   []rawTool
	resources  []*finemcp.Resource
	templates  []*finemcp.ResourceTemplate
	prompts    []*finemcp.Prompt
	middleware []finemcp.Middleware
}

type rawTool struct {
	name    string
	handler finemcp.ToolHandler
}

// WithTool registers a raw [finemcp.ToolHandler] under the given name.
func WithTool(name string, handler finemcp.ToolHandler) Option {
	return func(c *serverConfig) {
		c.rawTools = append(c.rawTools, rawTool{name: name, handler: handler})
	}
}

// WithRegisteredTool adds an already-constructed [*finemcp.Tool] (e.g. from
// [finemcp.NewTypedTool]).
func WithRegisteredTool(tool *finemcp.Tool) Option {
	return func(c *serverConfig) {
		c.tools = append(c.tools, tool)
	}
}

// WithMiddleware appends middleware to the test server's chain.
func WithMiddleware(mw finemcp.Middleware) Option {
	return func(c *serverConfig) {
		c.middleware = append(c.middleware, mw)
	}
}

// WithResource registers a [*finemcp.Resource] with the test server.
func WithResource(r *finemcp.Resource) Option {
	return func(c *serverConfig) {
		c.resources = append(c.resources, r)
	}
}

// WithResourceTemplate registers a [*finemcp.ResourceTemplate] with the test server.
func WithResourceTemplate(t *finemcp.ResourceTemplate) Option {
	return func(c *serverConfig) {
		c.templates = append(c.templates, t)
	}
}

// WithPrompt registers a [*finemcp.Prompt] with the test server.
func WithPrompt(p *finemcp.Prompt) Option {
	return func(c *serverConfig) {
		c.prompts = append(c.prompts, p)
	}
}

// NewServer creates a test [Server] with the given options.
// The server name is "test" and version is "1.0".
func NewServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	var cfg serverConfig
	for _, o := range opts {
		o(&cfg)
	}

	s := finemcp.NewServer("test", "1.0")

	for _, mw := range cfg.middleware {
		s.Use(mw)
	}

	// Register raw tools.
	for _, rt := range cfg.rawTools {
		tool, err := finemcp.NewTool(rt.name, rt.handler)
		if err != nil {
			t.Fatalf("mcptest: create tool %q: %v", rt.name, err)
		}
		if err := s.RegisterTool(tool); err != nil {
			t.Fatalf("mcptest: register tool %q: %v", rt.name, err)
		}
	}

	// Register pre-built tools.
	for _, tool := range cfg.tools {
		if tool == nil {
			t.Fatalf("mcptest: register tool: nil tool")
		}
		if err := s.RegisterTool(tool); err != nil {
			t.Fatalf("mcptest: register tool %q: %v", tool.Name, err)
		}
	}

	// Register resources.
	for _, r := range cfg.resources {
		if r == nil {
			t.Fatalf("mcptest: register resource: nil resource")
		}
		if err := s.RegisterResource(r); err != nil {
			t.Fatalf("mcptest: register resource %q: %v", r.URI, err)
		}
	}

	// Register resource templates.
	for _, tmpl := range cfg.templates {
		if tmpl == nil {
			t.Fatalf("mcptest: register template: nil template")
		}
		if err := s.RegisterResourceTemplate(tmpl); err != nil {
			t.Fatalf("mcptest: register template %q: %v", tmpl.URITemplate, err)
		}
	}

	// Register prompts.
	for _, p := range cfg.prompts {
		if p == nil {
			t.Fatalf("mcptest: register prompt: nil prompt")
		}
		if err := s.RegisterPrompt(p); err != nil {
			t.Fatalf("mcptest: register prompt %q: %v", p.Name, err)
		}
	}

	return &Server{inner: s}
}

// Close is a no-op provided for symmetry with other test-server patterns.
func (s *Server) Close() {}

// Inner returns the underlying [*finemcp.Server] for advanced use cases.
func (s *Server) Inner() *finemcp.Server { return s.inner }

// ── Request helpers ─────────────────────────────────────────────────

// Initialize sends an "initialize" request with default client info.
func (s *Server) Initialize(t *testing.T) *finemcp.JSONRPCResponse {
	t.Helper()
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "mcptest", "version": "0.1"},
	}
	return s.RawCall(t, "initialize", marshalJSON(t, params))
}

// CallTool sends a "tools/call" request for the named tool with the given arguments.
func (s *Server) CallTool(t *testing.T, name string, args json.RawMessage) *finemcp.JSONRPCResponse {
	t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	return s.RawCall(t, "tools/call", marshalJSON(t, params))
}

// ListTools sends a "tools/list" request.
func (s *Server) ListTools(t *testing.T) *finemcp.JSONRPCResponse {
	t.Helper()
	return s.RawCall(t, "tools/list", nil)
}

// ListResources sends a "resources/list" request.
func (s *Server) ListResources(t *testing.T) *finemcp.JSONRPCResponse {
	t.Helper()
	return s.RawCall(t, "resources/list", nil)
}

// ReadResource sends a "resources/read" request for the given URI.
func (s *Server) ReadResource(t *testing.T, uri string) *finemcp.JSONRPCResponse {
	t.Helper()
	params := map[string]any{"uri": uri}
	return s.RawCall(t, "resources/read", marshalJSON(t, params))
}

// ListResourceTemplates sends a "resources/templates/list" request.
func (s *Server) ListResourceTemplates(t *testing.T) *finemcp.JSONRPCResponse {
	t.Helper()
	return s.RawCall(t, "resources/templates/list", nil)
}

// ListPrompts sends a "prompts/list" request.
func (s *Server) ListPrompts(t *testing.T) *finemcp.JSONRPCResponse {
	t.Helper()
	return s.RawCall(t, "prompts/list", nil)
}

// GetPrompt sends a "prompts/get" request for the named prompt with optional arguments.
func (s *Server) GetPrompt(t *testing.T, name string, args map[string]string) *finemcp.JSONRPCResponse {
	t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	return s.RawCall(t, "prompts/get", marshalJSON(t, params))
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (s *Server) Notify(t *testing.T, method string, params json.RawMessage) {
	t.Helper()
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	raw := marshalJSON(t, msg)
	// Notifications return nil response; errors are unexpected.
	resp, err := s.inner.HandleMessage(context.Background(), raw)
	if err != nil {
		t.Fatalf("mcptest: notify %q: %v", method, err)
	}
	if resp != nil {
		t.Fatalf("mcptest: notify %q: unexpected response", method)
	}
}

// RawCall sends an arbitrary JSON-RPC request and returns the response.
// An auto-incrementing id is used.
func (s *Server) RawCall(t *testing.T, method string, params json.RawMessage) *finemcp.JSONRPCResponse {
	t.Helper()
	id := s.nextID
	s.nextID++
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	raw := marshalJSON(t, msg)
	resp, err := s.inner.HandleMessage(context.Background(), raw)
	if err != nil {
		t.Fatalf("mcptest: call %q: %v", method, err)
	}
	if resp == nil {
		t.Fatalf("mcptest: call %q: nil response", method)
	}
	return resp
}

// ── Assertion helpers ───────────────────────────────────────────────

// AssertNoError fails the test if resp contains a JSON-RPC error.
func AssertNoError(t *testing.T, resp *finemcp.JSONRPCResponse) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error (code %d): %s", resp.Error.Code, resp.Error.Message)
	}
}

// AssertError fails the test if resp does not contain a JSON-RPC error
// matching the given code and message substring.
func AssertError(t *testing.T, resp *finemcp.JSONRPCResponse, code int, msgSubstr string) {
	t.Helper()
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error, got success")
	}
	if resp.Error.Code != code {
		t.Errorf("error code = %d, want %d", resp.Error.Code, code)
	}
	if !strings.Contains(resp.Error.Message, msgSubstr) {
		t.Errorf("error message = %q, want substring %q", resp.Error.Message, msgSubstr)
	}
}

// AssertToolResult fails the test if the tool result's first text content
// does not match the expected string.
func AssertToolResult(t *testing.T, resp *finemcp.JSONRPCResponse, expected string) {
	t.Helper()
	AssertNoError(t, resp)

	// resp.Result is a CallToolResult; re-marshal/unmarshal to extract text.
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.IsError {
		t.Fatal("tool result has isError=true")
	}
	// Find the first text content item.
	var (
		firstText      string
		foundTextEntry bool
	)
	for _, c := range result.Content {
		if c.Type == "text" {
			firstText = c.Text
			foundTextEntry = true
			break
		}
	}
	if !foundTextEntry {
		t.Fatal("tool result has no text content")
	}
	if firstText != expected {
		t.Errorf("tool result text = %q, want %q", firstText, expected)
	}
}

// AssertToolCount fails the test if the tools/list response does not
// contain exactly n tools.
func AssertToolCount(t *testing.T, resp *finemcp.JSONRPCResponse, n int) {
	t.Helper()
	AssertNoError(t, resp)

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result finemcp.ListToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Tools) != n {
		t.Errorf("tool count = %d, want %d", len(result.Tools), n)
	}
}

// AssertResourceCount fails the test if the resources/list response does not
// contain exactly n resources.
func AssertResourceCount(t *testing.T, resp *finemcp.JSONRPCResponse, n int) {
	t.Helper()
	AssertNoError(t, resp)

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result finemcp.ListResourcesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Resources) != n {
		t.Errorf("resource count = %d, want %d", len(result.Resources), n)
	}
}

// AssertResourceText fails the test if the resources/read response's first
// content item does not have the expected text.
func AssertResourceText(t *testing.T, resp *finemcp.JSONRPCResponse, expected string) {
	t.Helper()
	AssertNoError(t, resp)

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result finemcp.ReadResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("resource result has no contents")
	}
	if result.Contents[0].Text == nil || *result.Contents[0].Text != expected {
		t.Errorf("resource text = %v, want %q", result.Contents[0].Text, expected)
	}
}

// AssertPromptCount fails the test if the prompts/list response does not
// contain exactly n prompts.
func AssertPromptCount(t *testing.T, resp *finemcp.JSONRPCResponse, n int) {
	t.Helper()
	AssertNoError(t, resp)

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result finemcp.ListPromptsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Prompts) != n {
		t.Errorf("prompt count = %d, want %d", len(result.Prompts), n)
	}
}

// AssertPromptMessage fails the test if the prompts/get response's first
// message does not have the expected role and text.
func AssertPromptMessage(t *testing.T, resp *finemcp.JSONRPCResponse, role, expectedText string) {
	t.Helper()
	AssertNoError(t, resp)

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result struct {
		Messages []struct {
			Role    string `json:"role"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("prompt result has no messages")
	}
	if result.Messages[0].Role != role {
		t.Errorf("message role = %q, want %q", result.Messages[0].Role, role)
	}
	if result.Messages[0].Content.Text != expectedText {
		t.Errorf("message text = %q, want %q", result.Messages[0].Content.Text, expectedText)
	}
}

// AssertTemplateCount fails the test if the resources/templates/list response
// does not contain exactly n templates.
func AssertTemplateCount(t *testing.T, resp *finemcp.JSONRPCResponse, n int) {
	t.Helper()
	AssertNoError(t, resp)

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var result finemcp.ListResourceTemplatesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.ResourceTemplates) != n {
		t.Errorf("template count = %d, want %d", len(result.ResourceTemplates), n)
	}
}

// ── Fixture & Golden File ───────────────────────────────────────────

// LoadFixture reads a JSON fixture file and returns its contents.
// It calls t.Fatal if the file cannot be read.
func LoadFixture(t *testing.T, path string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test helper; paths come from test code
	if err != nil {
		t.Fatalf("mcptest: load fixture %q: %v", path, err)
	}
	return json.RawMessage(data)
}

// GoldenFile compares got against the contents of the golden file at path.
// If the file does not exist it is created with got as the initial content.
// If the file exists and does not match, the test fails with a diff.
func GoldenFile(t *testing.T, path string, got []byte) {
	t.Helper()

	// Ensure parent directory exists.
	// Permissions 0750/0600: gosec G301/G306 compliant; golden files are
	// local test artifacts that don't need group/world access.
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatalf("mcptest: mkdir for golden %q: %v", path, err)
	}

	existing, err := os.ReadFile(path) // #nosec G304 -- test helper; paths come from test code
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, got, 0600); err != nil {
			t.Fatalf("mcptest: write golden %q: %v", path, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("mcptest: read golden %q: %v", path, err)
	}

	if !bytes.Equal(existing, got) {
		t.Errorf("golden file mismatch %q:\n  want: %s\n   got: %s", path, formatJSON(existing), formatJSON(got))
	}
}

// ── internal ────────────────────────────────────────────────────────

func marshalJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mcptest: marshal: %v", err)
	}
	return data
}

// formatJSON pretty-prints JSON for diff output. Falls back to raw string.
func formatJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	pretty, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return string(data)
	}
	return string(pretty)
}
