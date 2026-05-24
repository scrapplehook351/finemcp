package client_test

// aggregator_test.go — integration tests for N1 Multi-Server Aggregator.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// aggMockServer is a self-contained mock MCP server built on mockTransport.
// It exposes a configurable set of tools and prompts.
type aggMockServer struct {
	transport *mockTransport
	tools     []finemcp.ToolInfo
	prompts   []finemcp.PromptInfo
	resources []finemcp.ResourceInfo
}

func newAggMockServer(tools []finemcp.ToolInfo, prompts []finemcp.PromptInfo, resources []finemcp.ResourceInfo) *aggMockServer {
	s := &aggMockServer{
		transport: newMockTransport(),
		tools:     tools,
		prompts:   prompts,
		resources: resources,
	}
	s.startResponder()
	return s
}

func (s *aggMockServer) startResponder() {
	go func() {
		idx := 0
		for {
			s.transport.mu.Lock()
			closed := s.transport.closed
			count := len(s.transport.sent)
			s.transport.mu.Unlock()
			if closed {
				return
			}
			if count <= idx {
				time.Sleep(time.Millisecond)
				continue
			}
			for ; idx < count; idx++ {
				s.transport.mu.Lock()
				data := s.transport.sent[idx]
				s.transport.mu.Unlock()

				var msg struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Name string `json:"name"`
					} `json:"params"`
				}
				if err := json.Unmarshal(data, &msg); err != nil || msg.ID == nil {
					continue
				}

				switch msg.Method {
				case "initialize":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result: finemcp.InitializeResult{
							ProtocolVersion: finemcp.ProtocolVersion,
							ServerInfo:      finemcp.ProcessInfo{Name: "mock", Version: "1.0"},
						},
					})

				case "ping":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  struct{}{},
					})

				case "tools/list":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  finemcp.ListToolsResult{Tools: s.tools},
					})

				case "tools/call":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result: finemcp.CallToolResult{
							Content: []finemcp.Content{
								finemcp.TextContent{Text: "called: " + msg.Params.Name},
							},
						},
					})

				case "resources/list":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  finemcp.ListResourcesResult{Resources: s.resources},
					})

				case "resources/read":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result: finemcp.ReadResourceResult{
							Contents: []finemcp.ResourceContent{
								finemcp.NewTextResourceContent("file:///test.txt", "content"),
							},
						},
					})

				case "prompts/list":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  finemcp.ListPromptsResult{Prompts: s.prompts},
					})

				case "prompts/get":
					s.transport.enqueueJSON(finemcp.JSONRPCResponse{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result: finemcp.GetPromptResult{
							Messages: []finemcp.PromptMessage{
								{Role: "user", Content: finemcp.TextContent{Text: "prompt: " + msg.Params.Name}},
							},
						},
					})
				}
			}
		}
	}()
}

// buildAggregator creates an Aggregator with two servers ("alpha" and "beta").
// alpha has tools: create_issue, list_prs. beta has tools: send_message, list_channels.
// Both have a "ping_tool" (for ambiguity tests).
func buildAggregator(t *testing.T) (*client.Aggregator, *aggMockServer, *aggMockServer) {
	t.Helper()

	alpha := newAggMockServer(
		[]finemcp.ToolInfo{{Name: "create_issue"}, {Name: "list_prs"}, {Name: "ping_tool"}},
		[]finemcp.PromptInfo{{Name: "code_review"}},
		[]finemcp.ResourceInfo{{URI: "file:///alpha.txt", Name: "alpha"}},
	)
	beta := newAggMockServer(
		[]finemcp.ToolInfo{{Name: "send_message"}, {Name: "list_channels"}, {Name: "ping_tool"}},
		[]finemcp.PromptInfo{{Name: "summarize"}},
		[]finemcp.ResourceInfo{{URI: "file:///beta.txt", Name: "beta"}},
	)

	agg := client.NewAggregator(client.AggregatorOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agg.AddServer(ctx, "alpha", alpha.transport, client.Options{}); err != nil {
		t.Fatalf("AddServer alpha: %v", err)
	}
	if err := agg.AddServer(ctx, "beta", beta.transport, client.Options{}); err != nil {
		t.Fatalf("AddServer beta: %v", err)
	}

	t.Cleanup(func() { _ = agg.Close() })
	return agg, alpha, beta
}

// ── tests ────────────────────────────────────────────────────────────────────

// TestAggregator_AddAndRemoveServer verifies that servers can be added and
// removed while the aggregator is running.
func TestAggregator_AddAndRemoveServer(t *testing.T) {
	agg, _, _ := buildAggregator(t)

	ids := agg.ServerIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 servers, got %d", len(ids))
	}
	sort.Strings(ids)
	if ids[0] != "alpha" || ids[1] != "beta" {
		t.Errorf("unexpected server IDs: %v", ids)
	}

	if err := agg.RemoveServer("beta"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	ids = agg.ServerIDs()
	if len(ids) != 1 || ids[0] != "alpha" {
		t.Errorf("expected only alpha after removing beta, got %v", ids)
	}
}

// TestAggregator_DuplicateServerID verifies that registering the same ID twice
// returns ErrServerExists.
func TestAggregator_DuplicateServerID(t *testing.T) {
	agg, alpha, _ := buildAggregator(t)
	ctx := context.Background()

	// Try to add alpha again with a fresh mock transport.
	extra := newAggMockServer(nil, nil, nil)
	err := agg.AddServer(ctx, "alpha", extra.transport, client.Options{})
	_ = alpha // avoid unused warning
	if !errors.Is(err, client.ErrServerExists) {
		t.Errorf("expected ErrServerExists, got: %v", err)
	}
}

// TestAggregator_InvalidServerID verifies that empty or dot-containing IDs are rejected.
func TestAggregator_InvalidServerID(t *testing.T) {
	agg := client.NewAggregator(client.AggregatorOptions{})
	defer agg.Close()
	ctx := context.Background()
	mt := newMockTransport()

	if err := agg.AddServer(ctx, "", mt, client.Options{}); err == nil {
		t.Error("expected error for empty server ID")
	}
	if err := agg.AddServer(ctx, "has.dot", mt, client.Options{}); err == nil {
		t.Error("expected error for server ID containing '.'")
	}
}

// TestAggregator_ListTools_MergesAndQualifies verifies that ListTools returns
// the merged tool list with "serverID.toolName" qualified names.
func TestAggregator_ListTools_MergesAndQualifies(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := agg.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// Expect 6 tools: 3 from alpha + 3 from beta, all qualified.
	if len(tools) != 6 {
		t.Errorf("expected 6 tools, got %d: %+v", len(tools), tools)
	}

	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}

	// Check qualified names.
	expected := []string{
		"alpha.create_issue", "alpha.list_prs", "alpha.ping_tool",
		"beta.send_message", "beta.list_channels", "beta.ping_tool",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected tool %q not found in: %v", name, tools)
		}
	}
}

// TestAggregator_CallTool_Qualified verifies that a qualified tool path routes
// to the correct server.
func TestAggregator_CallTool_Qualified(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := agg.CallTool(ctx, "alpha.create_issue", finemcp.CallToolParams{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	// The mock server echoes "called: <tool_name>".
	text := result.Content[0].(finemcp.TextContent).Text
	if text != "called: create_issue" {
		t.Errorf("expected 'called: create_issue', got %q", text)
	}
}

// TestAggregator_CallTool_Unqualified_Unique verifies that an unqualified name
// that exists on exactly one server is routed correctly.
func TestAggregator_CallTool_Unqualified_Unique(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "send_message" is only on beta.
	result, err := agg.CallTool(ctx, "send_message", finemcp.CallToolParams{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	text := result.Content[0].(finemcp.TextContent).Text
	if text != "called: send_message" {
		t.Errorf("expected 'called: send_message', got %q", text)
	}
}

// TestAggregator_CallTool_Unqualified_Ambiguous verifies that an unqualified
// name present on multiple servers returns ErrToolAmbiguous.
func TestAggregator_CallTool_Unqualified_Ambiguous(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "ping_tool" exists on both alpha and beta.
	_, err := agg.CallTool(ctx, "ping_tool", finemcp.CallToolParams{})
	if !errors.Is(err, client.ErrToolAmbiguous) {
		t.Errorf("expected ErrToolAmbiguous, got: %v", err)
	}
}

// TestAggregator_CallTool_NotFound verifies that calling a tool that doesn't
// exist on any server returns ErrToolNotFound.
func TestAggregator_CallTool_NotFound(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := agg.CallTool(ctx, "nonexistent_tool", finemcp.CallToolParams{})
	if !errors.Is(err, client.ErrToolNotFound) {
		t.Errorf("expected ErrToolNotFound, got: %v", err)
	}
}

// TestAggregator_CallTool_UnknownServer verifies that a qualified path with an
// unregistered server ID returns ErrServerNotFound.
func TestAggregator_CallTool_UnknownServer(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := agg.CallTool(ctx, "unknown.create_issue", finemcp.CallToolParams{})
	if !errors.Is(err, client.ErrServerNotFound) {
		t.Errorf("expected ErrServerNotFound, got: %v", err)
	}
}

// TestAggregator_ListResources_Merges verifies that resource lists are merged
// across all healthy servers.
func TestAggregator_ListResources_Merges(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resources, err := agg.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(resources))
	}
}

// TestAggregator_ReadResource verifies that ReadResource routes to the
// correct server.
func TestAggregator_ReadResource(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := agg.ReadResource(ctx, "alpha", finemcp.ReadResourceParams{URI: "file:///alpha.txt"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
}

// TestAggregator_ReadResource_UnknownServer verifies that ReadResource with an
// unknown server returns ErrServerNotFound.
func TestAggregator_ReadResource_UnknownServer(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := agg.ReadResource(ctx, "ghost", finemcp.ReadResourceParams{URI: "file:///x.txt"})
	if !errors.Is(err, client.ErrServerNotFound) {
		t.Errorf("expected ErrServerNotFound, got: %v", err)
	}
}

// TestAggregator_ListPrompts_MergesAndQualifies verifies prompt list merging.
func TestAggregator_ListPrompts_MergesAndQualifies(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prompts, err := agg.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts) != 2 {
		t.Errorf("expected 2 prompts, got %d", len(prompts))
	}

	names := make(map[string]bool, len(prompts))
	for _, p := range prompts {
		names[p.Name] = true
	}
	if !names["alpha.code_review"] || !names["beta.summarize"] {
		t.Errorf("expected qualified prompt names, got %v", prompts)
	}
}

// TestAggregator_GetPrompt_Qualified verifies qualified prompt routing.
func TestAggregator_GetPrompt_Qualified(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := agg.GetPrompt(ctx, "alpha.code_review", finemcp.GetPromptParams{})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	text := result.Messages[0].Content.(finemcp.TextContent).Text
	if text != "prompt: code_review" {
		t.Errorf("expected 'prompt: code_review', got %q", text)
	}
}

// TestAggregator_GetPrompt_Unqualified_Unique verifies unqualified prompt routing.
func TestAggregator_GetPrompt_Unqualified_Unique(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "summarize" only exists on beta.
	result, err := agg.GetPrompt(ctx, "summarize", finemcp.GetPromptParams{})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
}

// TestAggregator_HealthCheck verifies that HealthCheck pings all servers.
func TestAggregator_HealthCheck(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agg.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !agg.IsHealthy("alpha") {
		t.Error("alpha should be healthy after HealthCheck")
	}
	if !agg.IsHealthy("beta") {
		t.Error("beta should be healthy after HealthCheck")
	}
}

// TestAggregator_IsHealthy_UnknownServer verifies false for unknown server ID.
func TestAggregator_IsHealthy_UnknownServer(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	if agg.IsHealthy("ghost") {
		t.Error("expected IsHealthy to return false for unknown server")
	}
}

// TestAggregator_ToolCaching verifies that tool lists are cached and that
// InvalidateToolCache forces a re-fetch.
func TestAggregator_ToolCaching(t *testing.T) {
	alpha := newAggMockServer(
		[]finemcp.ToolInfo{{Name: "tool_a"}},
		nil, nil,
	)
	agg := client.NewAggregator(client.AggregatorOptions{
		ToolCacheTTL: 10 * time.Second, // long TTL to prove caching
	})
	defer agg.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agg.AddServer(ctx, "alpha", alpha.transport, client.Options{}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	// First ListTools - fetches from server.
	tools1, err := agg.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools 1st: %v", err)
	}

	// Second ListTools - should use cache (no additional tools/list request).
	tools2, err := agg.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools 2nd: %v", err)
	}

	// Content should be identical.
	if len(tools1) != len(tools2) {
		t.Errorf("cached result differs: %v vs %v", tools1, tools2)
	}

	// Count tools/list wire requests: first fetch + second via cache = 1.
	sent := countSentRequestsByMethod(alpha.transport, "tools/list")
	if sent != 1 {
		t.Errorf("expected 1 tools/list request (cached), got %d", sent)
	}

	// Invalidate and re-fetch.
	agg.InvalidateToolCache("alpha")
	if _, err := agg.ListTools(ctx); err != nil {
		t.Fatalf("ListTools after invalidation: %v", err)
	}
	sent = countSentRequestsByMethod(alpha.transport, "tools/list")
	if sent != 2 {
		t.Errorf("expected 2 tools/list requests after invalidation, got %d", sent)
	}
}

// TestAggregator_HotSwap verifies that servers can be added and removed while
// the aggregator is in use, with immediate effect on subsequent operations.
func TestAggregator_HotSwap(t *testing.T) {
	agg, _, _ := buildAggregator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Remove beta.
	if err := agg.RemoveServer("beta"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	// ListTools should now only include alpha's tools.
	tools, err := agg.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools after remove: %v", err)
	}
	for _, tool := range tools {
		if len(tool.Name) > 5 && tool.Name[:5] == "beta." {
			t.Errorf("beta tool unexpectedly present after removal: %s", tool.Name)
		}
	}

	// Hot-add a "gamma" server.
	gamma := newAggMockServer(
		[]finemcp.ToolInfo{{Name: "analyze"}},
		nil, nil,
	)
	if err := agg.AddServer(ctx, "gamma", gamma.transport, client.Options{}); err != nil {
		t.Fatalf("AddServer gamma: %v", err)
	}

	// CallTool on gamma should now succeed.
	result, err := agg.CallTool(ctx, "gamma.analyze", finemcp.CallToolParams{})
	if err != nil {
		t.Fatalf("CallTool gamma.analyze: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content from gamma")
	}
}

// TestAggregator_NoServers verifies that operations on an empty aggregator
// return ErrNoHealthyServers.
func TestAggregator_NoServers(t *testing.T) {
	agg := client.NewAggregator(client.AggregatorOptions{})
	defer agg.Close()
	ctx := context.Background()

	_, err := agg.ListTools(ctx)
	if !errors.Is(err, client.ErrNoHealthyServers) {
		t.Errorf("expected ErrNoHealthyServers from ListTools, got: %v", err)
	}
	_, err = agg.ListResources(ctx)
	if !errors.Is(err, client.ErrNoHealthyServers) {
		t.Errorf("expected ErrNoHealthyServers from ListResources, got: %v", err)
	}
	_, err = agg.ListPrompts(ctx)
	if !errors.Is(err, client.ErrNoHealthyServers) {
		t.Errorf("expected ErrNoHealthyServers from ListPrompts, got: %v", err)
	}
}
