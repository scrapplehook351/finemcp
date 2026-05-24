package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
	httptransport "github.com/finemcp/finemcp/client/transport/http"
)

// ── Test Helpers ──────────────────────────────────────────────────────

// paginationServer is a mock MCP server that supports infinite pagination for testing
type paginationServer struct {
	totalPages     int // Total pages available (0 = infinite)
	itemsPerPage   int // Items to return per page
	requestCounter int // Track number of requests
}

func (ps *paginationServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var msg finemcp.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch msg.Method {
	case "initialize":
		resp := finemcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: finemcp.InitializeResult{
				ProtocolVersion: finemcp.ProtocolVersion,
				Capabilities:    finemcp.ServerCapabilities{},
				ServerInfo:      finemcp.ProcessInfo{Name: "pagination-test-server", Version: "1.0"},
			},
		}
		json.NewEncoder(w).Encode(resp)

	case "tools/list":
		ps.requestCounter++
		ps.handleListTools(w, msg)

	case "resources/list":
		ps.requestCounter++
		ps.handleListResources(w, msg)

	case "prompts/list":
		ps.requestCounter++
		ps.handleListPrompts(w, msg)

	case "roots/list":
		ps.requestCounter++
		ps.handleListRoots(w, msg)

	default:
		resp := finemcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &finemcp.JSONRPCError{
				Code:    -32601,
				Message: "Method not found",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func (ps *paginationServer) handleListTools(w http.ResponseWriter, msg finemcp.JSONRPCRequest) {
	var params finemcp.ListParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	// Parse cursor to determine current page
	currentPage := 1
	if params.Cursor != "" {
		fmt.Sscanf(params.Cursor, "page-%d", &currentPage)
	}

	// Generate items for this page
	tools := make([]finemcp.ToolInfo, ps.itemsPerPage)
	for i := 0; i < ps.itemsPerPage; i++ {
		tools[i] = finemcp.ToolInfo{
			Name:        fmt.Sprintf("tool-%d-%d", currentPage, i+1),
			Description: fmt.Sprintf("Tool %d on page %d", i+1, currentPage),
		}
	}

	// Generate next cursor (infinite if totalPages == 0)
	nextCursor := ""
	if ps.totalPages == 0 || currentPage < ps.totalPages {
		nextCursor = fmt.Sprintf("page-%d", currentPage+1)
	}

	resp := finemcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: finemcp.ListToolsResult{
			Tools:      tools,
			NextCursor: nextCursor,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func (ps *paginationServer) handleListResources(w http.ResponseWriter, msg finemcp.JSONRPCRequest) {
	var params finemcp.ListParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	currentPage := 1
	if params.Cursor != "" {
		fmt.Sscanf(params.Cursor, "page-%d", &currentPage)
	}

	resources := make([]finemcp.ResourceInfo, ps.itemsPerPage)
	for i := 0; i < ps.itemsPerPage; i++ {
		resources[i] = finemcp.ResourceInfo{
			URI:  fmt.Sprintf("resource://%d/%d", currentPage, i+1),
			Name: fmt.Sprintf("resource-%d-%d", currentPage, i+1),
		}
	}

	nextCursor := ""
	if ps.totalPages == 0 || currentPage < ps.totalPages {
		nextCursor = fmt.Sprintf("page-%d", currentPage+1)
	}

	resp := finemcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: finemcp.ListResourcesResult{
			Resources:  resources,
			NextCursor: nextCursor,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func (ps *paginationServer) handleListPrompts(w http.ResponseWriter, msg finemcp.JSONRPCRequest) {
	var params finemcp.ListParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	currentPage := 1
	if params.Cursor != "" {
		fmt.Sscanf(params.Cursor, "page-%d", &currentPage)
	}

	prompts := make([]finemcp.PromptInfo, ps.itemsPerPage)
	for i := 0; i < ps.itemsPerPage; i++ {
		prompts[i] = finemcp.PromptInfo{
			Name:        fmt.Sprintf("prompt-%d-%d", currentPage, i+1),
			Description: fmt.Sprintf("Prompt %d on page %d", i+1, currentPage),
		}
	}

	nextCursor := ""
	if ps.totalPages == 0 || currentPage < ps.totalPages {
		nextCursor = fmt.Sprintf("page-%d", currentPage+1)
	}

	resp := finemcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: finemcp.ListPromptsResult{
			Prompts:    prompts,
			NextCursor: nextCursor,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func (ps *paginationServer) handleListRoots(w http.ResponseWriter, msg finemcp.JSONRPCRequest) {
	var params finemcp.ListParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	currentPage := 1
	if params.Cursor != "" {
		fmt.Sscanf(params.Cursor, "page-%d", &currentPage)
	}

	roots := make([]finemcp.RootInfo, ps.itemsPerPage)
	for i := 0; i < ps.itemsPerPage; i++ {
		roots[i] = finemcp.RootInfo{
			URI:  fmt.Sprintf("root://%d/%d", currentPage, i+1),
			Name: fmt.Sprintf("root-%d-%d", currentPage, i+1),
		}
	}

	nextCursor := ""
	if ps.totalPages == 0 || currentPage < ps.totalPages {
		nextCursor = fmt.Sprintf("page-%d", currentPage+1)
	}

	resp := finemcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result: finemcp.ListRootsResult{
			Roots:      roots,
			NextCursor: nextCursor,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

// createTestClient creates a client connected to a pagination test server
func createTestClient(t *testing.T, ps *paginationServer) (*client.Client, *httptest.Server) {
	server := httptest.NewServer(ps)

	tr := httptransport.New(httptransport.Config{
		BaseURL: server.URL,
	})

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "pagination-test-client", Version: "1.0"},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	return c, server
}

// ── MaxPages Limit Tests ──────────────────────────────────────────────

func TestPagination_ToolsIterator_MaxPages(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0, // Infinite pages
		itemsPerPage: 10,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	// Should stop after 5 pages
	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 5,
		MaxItems: 0, // Unlimited items
	})

	count := 0
	for {
		tool, err := iter.Next(ctx)
		if err != nil {
			if err == io.EOF {
				t.Fatal("got EOF, expected max pages limit error")
			}
			// Expected: max pages limit error
			if !strings.Contains(err.Error(), "max pages limit") {
				t.Fatalf("unexpected error: %v", err)
			}
			break
		}
		count++
		if count > 60 { // 5 pages * 10 items + safety margin
			t.Fatalf("MaxPages limit not enforced, already fetched %d items", count)
		}
		_ = tool
	}

	expectedItems := 50 // 5 pages * 10 items per page
	if count != expectedItems {
		t.Errorf("expected %d items, got %d", expectedItems, count)
	}

	// Verify exactly 5 pages were requested
	if ps.requestCounter != 5 {
		t.Errorf("expected 5 page requests, got %d", ps.requestCounter)
	}
}

func TestPagination_ResourcesIterator_MaxPages(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0, // Infinite pages
		itemsPerPage: 5,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateResources(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 3,
		MaxItems: 0,
	})

	count := 0
	for {
		_, err := iter.Next(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "max pages limit") {
				break
			}
			t.Fatalf("unexpected error: %v", err)
		}
		count++
		if count > 20 {
			t.Fatalf("MaxPages limit not enforced")
		}
	}

	expectedItems := 15 // 3 pages * 5 items
	if count != expectedItems {
		t.Errorf("expected %d items, got %d", expectedItems, count)
	}
}

func TestPagination_PromptsIterator_MaxPages(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0,
		itemsPerPage: 8,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IteratePrompts(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 2,
		MaxItems: 0,
	})

	count := 0
	for {
		_, err := iter.Next(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "max pages limit") {
				break
			}
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}

	expectedItems := 16 // 2 pages * 8 items
	if count != expectedItems {
		t.Errorf("expected %d items, got %d", expectedItems, count)
	}
}

func TestPagination_RootsIterator_MaxPages(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0,
		itemsPerPage: 3,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateRoots(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 4,
		MaxItems: 0,
	})

	count := 0
	for {
		_, err := iter.Next(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "max pages limit") {
				break
			}
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}

	expectedItems := 12 // 4 pages * 3 items
	if count != expectedItems {
		t.Errorf("expected %d items, got %d", expectedItems, count)
	}
}

// ── MaxItems Limit Tests ──────────────────────────────────────────────

func TestPagination_ToolsIterator_MaxItems_ViaAll(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0, // Infinite pages
		itemsPerPage: 10,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 0, // Unlimited pages
		MaxItems: 25,
	})

	tools, err := iter.All(ctx)
	if err == nil {
		t.Fatal("expected max items limit error, got nil")
	}
	if !strings.Contains(err.Error(), "max items limit") {
		t.Fatalf("expected max items limit error, got: %v", err)
	}

	// Should have stopped at exactly MaxItems
	if len(tools) != 25 {
		t.Errorf("expected 25 items before error, got %d", len(tools))
	}
}

func TestPagination_ResourcesIterator_MaxItems_ViaAll(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0,
		itemsPerPage: 7,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateResources(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 0,
		MaxItems: 20,
	})

	resources, err := iter.All(ctx)
	if err == nil {
		t.Fatal("expected max items limit error")
	}
	if !strings.Contains(err.Error(), "max items limit") {
		t.Fatalf("expected max items limit error, got: %v", err)
	}

	if len(resources) != 20 {
		t.Errorf("expected 20 items before error, got %d", len(resources))
	}
}

func TestPagination_PromptsIterator_MaxItems_ViaAll(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0,
		itemsPerPage: 15,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IteratePrompts(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 0,
		MaxItems: 40,
	})

	prompts, err := iter.All(ctx)
	if err == nil {
		t.Fatal("expected max items limit error")
	}

	if len(prompts) != 40 {
		t.Errorf("expected 40 items before error, got %d", len(prompts))
	}
}

func TestPagination_RootsIterator_MaxItems_ViaAll(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0,
		itemsPerPage: 6,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateRoots(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 0,
		MaxItems: 18,
	})

	roots, err := iter.All(ctx)
	if err == nil {
		t.Fatal("expected max items limit error")
	}

	if len(roots) != 18 {
		t.Errorf("expected 18 items before error, got %d", len(roots))
	}
}

// ── Unbounded Mode Tests ──────────────────────────────────────────────

func TestPagination_ToolsIterator_Unbounded_FinitePages(t *testing.T) {
	ps := &paginationServer{
		totalPages:   3, // Finite pages
		itemsPerPage: 5,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	// Unbounded mode (MaxPages=0, MaxItems=0)
	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 0,
		MaxItems: 0,
	})

	tools, err := iter.All(ctx)
	if err != nil {
		t.Fatalf("unexpected error in unbounded mode with finite pages: %v", err)
	}

	expectedItems := 15 // 3 pages * 5 items
	if len(tools) != expectedItems {
		t.Errorf("expected %d tools, got %d", expectedItems, len(tools))
	}

	// Verify all tools have unique names
	seen := make(map[string]bool)
	for _, tool := range tools {
		if seen[tool.Name] {
			t.Errorf("duplicate tool name: %s", tool.Name)
		}
		seen[tool.Name] = true
	}
}

func TestPagination_DefaultLimits(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0, // Infinite pages
		itemsPerPage: 10,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	// Pass nil options to use defaults (MaxPages=1000, MaxItems=100,000)
	iter := c.IterateTools(finemcp.ListParams{}, nil)

	count := 0
	for {
		_, err := iter.Next(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "max pages limit") {
				break
			}
			t.Fatalf("unexpected error: %v", err)
		}
		count++
		if count > 11000 { // Safety: 1000 pages * 10 items + margin
			t.Fatal("default MaxPages limit not enforced")
		}
	}

	expectedItems := 10000 // 1000 pages * 10 items
	if count != expectedItems {
		t.Errorf("expected %d items (default MaxPages=1000), got %d", expectedItems, count)
	}
}

// ── Edge Cases ────────────────────────────────────────────────────────

func TestPagination_EmptyResult(t *testing.T) {
	ps := &paginationServer{
		totalPages:   1, // One page with items
		itemsPerPage: 0, // But no items on that page
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 10,
		MaxItems: 0,
	})

	tool, err := iter.Next(ctx)
	if err != io.EOF {
		t.Fatalf("expected io.EOF for empty result, got: %v (tool: %v)", err, tool)
	}
}

func TestPagination_SinglePage(t *testing.T) {
	ps := &paginationServer{
		totalPages:   1, // Only one page
		itemsPerPage: 5,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 100, // High limit, but server only has 1 page
		MaxItems: 0,
	})

	tools, err := iter.All(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(tools))
	}

	// Verify only 1 page was requested
	if ps.requestCounter != 1 {
		t.Errorf("expected 1 page request, got %d", ps.requestCounter)
	}
}

func TestPagination_HasMore(t *testing.T) {
	ps := &paginationServer{
		totalPages:   3,
		itemsPerPage: 5,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 10,
		MaxItems: 0,
	})

	// Initially should have more (before first fetch)
	if !iter.HasMore() {
		t.Error("HasMore() should return true before first fetch")
	}

	// Fetch all items
	count := 0
	for {
		_, err := iter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}

	// After exhausting, should not have more
	if iter.HasMore() {
		t.Error("HasMore() should return false after exhausting iterator")
	}

	if count != 15 {
		t.Errorf("expected 15 items, got %d", count)
	}
}

func TestPagination_ContextCancellation(t *testing.T) {
	ps := &paginationServer{
		totalPages:   0, // Infinite
		itemsPerPage: 10,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())

	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 0, // Unbounded
		MaxItems: 0,
	})

	// Fetch a few items, then cancel
	for i := 0; i < 5; i++ {
		_, err := iter.Next(ctx)
		if err != nil {
			t.Fatalf("unexpected error on item %d: %v", i, err)
		}
	}

	cancel() // Cancel context

	// Keep calling Next() until we hit context cancellation
	// (iterator may have buffered items from the previous page)
	gotCancellation := false
	for i := 0; i < 20; i++ { // Try up to 20 more calls
		_, err := iter.Next(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "context canceled") {
				gotCancellation = true
				break
			}
			// Some other error occurred
			t.Fatalf("unexpected error after cancellation: %v", err)
		}
		// Iterator returned a buffered item, continue
	}

	if !gotCancellation {
		t.Error("expected context cancellation error within 20 Next() calls after cancel()")
	}
}

func TestPagination_MaxPagesEqualsOne(t *testing.T) {
	ps := &paginationServer{
		totalPages:   10,
		itemsPerPage: 7,
	}

	c, server := createTestClient(t, ps)
	defer server.Close()
	defer c.Close()

	ctx := context.Background()

	iter := c.IterateTools(finemcp.ListParams{}, &client.IteratorOptions{
		MaxPages: 1, // Only one page allowed
		MaxItems: 0,
	})

	count := 0
	for {
		_, err := iter.Next(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "max pages limit") {
				break
			}
			t.Fatalf("unexpected error: %v", err)
		}
		count++
		if count > 10 {
			t.Fatal("MaxPages=1 limit not enforced")
		}
	}

	expectedItems := 7 // 1 page * 7 items
	if count != expectedItems {
		t.Errorf("expected %d items, got %d", expectedItems, count)
	}

	if ps.requestCounter != 1 {
		t.Errorf("expected exactly 1 page request, got %d", ps.requestCounter)
	}
}
