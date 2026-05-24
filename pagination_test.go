package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// ── paginateSlice unit tests ────────────────────────────────────────

func TestPaginateSlice_FirstPage(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	page, next, err := paginateSlice(items, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0] != "a" || page[1] != "b" {
		t.Errorf("page = %v, want [a b]", page)
	}
	if next != "2" {
		t.Errorf("nextCursor = %q, want %q", next, "2")
	}
}

func TestPaginateSlice_MiddlePage(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	page, next, err := paginateSlice(items, "2", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0] != "c" || page[1] != "d" {
		t.Errorf("page = %v, want [c d]", page)
	}
	if next != "4" {
		t.Errorf("nextCursor = %q, want %q", next, "4")
	}
}

func TestPaginateSlice_LastPage(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	page, next, err := paginateSlice(items, "4", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0] != "e" {
		t.Errorf("page = %v, want [e]", page)
	}
	if next != "" {
		t.Errorf("nextCursor = %q, want empty", next)
	}
}

func TestPaginateSlice_CursorAtEnd(t *testing.T) {
	items := []string{"a", "b", "c"}
	page, next, err := paginateSlice(items, "3", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Errorf("expected empty page, got %v", page)
	}
	if next != "" {
		t.Errorf("nextCursor = %q, want empty", next)
	}
}

func TestPaginateSlice_EmptySlice(t *testing.T) {
	page, next, err := paginateSlice([]string{}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Errorf("expected empty page, got %v", page)
	}
	if next != "" {
		t.Errorf("nextCursor = %q, want empty", next)
	}
}

func TestPaginateSlice_DefaultPageSize(t *testing.T) {
	items := make([]int, 60)
	for i := range items {
		items[i] = i
	}
	page, next, err := paginateSlice(items, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 50 {
		t.Errorf("expected default page size of 50, got %d", len(page))
	}
	if next != "50" {
		t.Errorf("nextCursor = %q, want %q", next, "50")
	}
}

func TestPaginateSlice_ExactBoundary(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	page, next, err := paginateSlice(items, "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 4 {
		t.Errorf("expected 4, got %d", len(page))
	}
	if next != "" {
		t.Errorf("expected empty nextCursor at exact boundary, got %q", next)
	}
}

func TestPaginateSlice_InvalidCursor_NonNumeric(t *testing.T) {
	_, _, err := paginateSlice([]string{"a"}, "abc", 5)
	if err == nil {
		t.Fatal("expected error for non-numeric cursor")
	}
}

func TestPaginateSlice_InvalidCursor_Negative(t *testing.T) {
	_, _, err := paginateSlice([]string{"a"}, "-1", 5)
	if err == nil {
		t.Fatal("expected error for negative cursor")
	}
}

func TestPaginateSlice_InvalidCursor_OutOfRange(t *testing.T) {
	_, _, err := paginateSlice([]string{"a", "b"}, "999", 5)
	if err == nil {
		t.Fatal("expected error for out of range cursor")
	}
}

// ── tools/list pagination integration tests ─────────────────────────

func TestHandleMessage_ToolsList_Pagination_MultiplePages(t *testing.T) {
	s := initServer(t)

	// Register 105 tools to test multi-page iteration.
	for i := 0; i < 105; i++ {
		tool, _ := NewTool(fmt.Sprintf("tool_%03d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	// First page: limit=50
	data := jsonrpcReq(1, "tools/list", map[string]any{"limit": 50})
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var page1 ListToolsResult
	json.Unmarshal(raw, &page1)

	if len(page1.Tools) != 50 {
		t.Fatalf("page1: expected 50 tools, got %d", len(page1.Tools))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1: expected nextCursor")
	}

	// Second page
	data = jsonrpcReq(2, "tools/list", map[string]any{"cursor": page1.NextCursor, "limit": 50})
	resp, _ = s.HandleMessage(context.Background(), data)
	raw, _ = json.Marshal(resp.Result)
	var page2 ListToolsResult
	json.Unmarshal(raw, &page2)

	if len(page2.Tools) != 50 {
		t.Fatalf("page2: expected 50, got %d", len(page2.Tools))
	}
	if page2.NextCursor == "" {
		t.Fatal("page2: expected nextCursor")
	}

	// Third page (last 5 items)
	data = jsonrpcReq(3, "tools/list", map[string]any{"cursor": page2.NextCursor, "limit": 50})
	resp, _ = s.HandleMessage(context.Background(), data)
	raw, _ = json.Marshal(resp.Result)
	var page3 ListToolsResult
	json.Unmarshal(raw, &page3)

	if len(page3.Tools) != 5 {
		t.Fatalf("page3: expected 5, got %d", len(page3.Tools))
	}
	if page3.NextCursor != "" {
		t.Fatalf("page3: expected empty nextCursor, got %q", page3.NextCursor)
	}
}

func TestHandleMessage_ToolsList_Pagination_InvalidCursor(t *testing.T) {
	s := initServer(t)
	for i := 0; i < 10; i++ {
		tool, _ := NewTool(fmt.Sprintf("tool_%d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	tests := []struct {
		name   string
		cursor string
	}{
		{"non-numeric", "bad"},
		{"negative", "-5"},
		{"out of range", "999"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := jsonrpcReq(1, "tools/list", map[string]any{"cursor": tc.cursor, "limit": 5})
			resp, err := s.HandleMessage(context.Background(), data)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Error == nil {
				t.Fatal("expected error response")
			}
			if resp.Error.Code != ErrCodeInvalidParams {
				t.Errorf("code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
			}
		})
	}
}

func TestHandleMessage_ToolsList_Pagination_EmptyParams(t *testing.T) {
	s := initServer(t)
	for i := 0; i < 100; i++ {
		tool, _ := NewTool(fmt.Sprintf("tool_%d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	// Explicit empty params {} should return the full list (backward compat).
	data := jsonrpcReq(1, "tools/list", map[string]any{})
	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	json.Unmarshal(raw, &result)

	if len(result.Tools) != 100 {
		t.Fatalf("expected full list of 100, got %d", len(result.Tools))
	}
	if result.NextCursor != "" {
		t.Fatalf("expected no nextCursor, got %q", result.NextCursor)
	}
}

func TestHandleMessage_ToolsList_Pagination_NilParams(t *testing.T) {
	s := initServer(t)
	for i := 0; i < 60; i++ {
		tool, _ := NewTool(fmt.Sprintf("tool_%d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	// nil params should return the full list (backward compat).
	data := jsonrpcReq(1, "tools/list", nil)
	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	json.Unmarshal(raw, &result)

	if len(result.Tools) != 60 {
		t.Fatalf("expected 60, got %d", len(result.Tools))
	}
	if result.NextCursor != "" {
		t.Fatalf("expected no nextCursor, got %q", result.NextCursor)
	}
}

func TestHandleMessage_ToolsList_Pagination_CursorAtEnd(t *testing.T) {
	s := initServer(t)
	for i := 0; i < 10; i++ {
		tool, _ := NewTool(fmt.Sprintf("tool_%d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	// Cursor pointing exactly at len(items) — empty page, no error.
	data := jsonrpcReq(1, "tools/list", map[string]any{"cursor": "10", "limit": 5})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	json.Unmarshal(raw, &result)

	if len(result.Tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(result.Tools))
	}
	if result.NextCursor != "" {
		t.Fatalf("expected no nextCursor, got %q", result.NextCursor)
	}
}

// ── resources/list pagination integration tests ─────────────────────

func TestHandleMessage_ResourcesList_Pagination(t *testing.T) {
	s := initServer(t)

	for i := 0; i < 15; i++ {
		r, _ := NewResource(fmt.Sprintf("file:///r%d", i), fmt.Sprintf("r%d", i),
			func(_ context.Context, _ string) ([]ResourceContent, error) { return nil, nil })
		s.RegisterResource(r)
	}

	// First page
	data := jsonrpcReq(1, "resources/list", map[string]any{"limit": 10})
	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var page1 ListResourcesResult
	json.Unmarshal(raw, &page1)

	if len(page1.Resources) != 10 {
		t.Fatalf("page1: expected 10, got %d", len(page1.Resources))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1: expected nextCursor")
	}

	// Second page
	data = jsonrpcReq(2, "resources/list", map[string]any{"cursor": page1.NextCursor, "limit": 10})
	resp, _ = s.HandleMessage(context.Background(), data)
	raw, _ = json.Marshal(resp.Result)
	var page2 ListResourcesResult
	json.Unmarshal(raw, &page2)

	if len(page2.Resources) != 5 {
		t.Fatalf("page2: expected 5, got %d", len(page2.Resources))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2: expected empty nextCursor, got %q", page2.NextCursor)
	}
}

// ── resources/templates/list pagination integration tests ───────────

func TestHandleMessage_ResourcesTemplatesList_Pagination(t *testing.T) {
	s := initServer(t)

	for i := 0; i < 15; i++ {
		tmpl, _ := NewResourceTemplate(fmt.Sprintf("file:///t%d/{id}", i), fmt.Sprintf("t%d", i),
			func(_ context.Context, _ string) ([]ResourceContent, error) { return nil, nil })
		s.RegisterResourceTemplate(tmpl)
	}

	data := jsonrpcReq(1, "resources/templates/list", map[string]any{"limit": 10})
	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var page1 ListResourceTemplatesResult
	json.Unmarshal(raw, &page1)

	if len(page1.ResourceTemplates) != 10 {
		t.Fatalf("page1: expected 10, got %d", len(page1.ResourceTemplates))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1: expected nextCursor")
	}

	data = jsonrpcReq(2, "resources/templates/list", map[string]any{"cursor": page1.NextCursor, "limit": 10})
	resp, _ = s.HandleMessage(context.Background(), data)
	raw, _ = json.Marshal(resp.Result)
	var page2 ListResourceTemplatesResult
	json.Unmarshal(raw, &page2)

	if len(page2.ResourceTemplates) != 5 {
		t.Fatalf("page2: expected 5, got %d", len(page2.ResourceTemplates))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2: expected empty nextCursor, got %q", page2.NextCursor)
	}
}

// ── prompts/list pagination integration tests ───────────────────────

func TestHandleMessage_PromptsList_Pagination(t *testing.T) {
	s := initServer(t)

	for i := 0; i < 15; i++ {
		p, _ := NewPrompt(fmt.Sprintf("prompt_%d", i),
			func(_ context.Context, _ map[string]string) ([]PromptMessage, error) { return nil, nil })
		s.RegisterPrompt(p)
	}

	data := jsonrpcReq(1, "prompts/list", map[string]any{"limit": 10})
	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var page1 ListPromptsResult
	json.Unmarshal(raw, &page1)

	if len(page1.Prompts) != 10 {
		t.Fatalf("page1: expected 10, got %d", len(page1.Prompts))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1: expected nextCursor")
	}

	data = jsonrpcReq(2, "prompts/list", map[string]any{"cursor": page1.NextCursor, "limit": 10})
	resp, _ = s.HandleMessage(context.Background(), data)
	raw, _ = json.Marshal(resp.Result)
	var page2 ListPromptsResult
	json.Unmarshal(raw, &page2)

	if len(page2.Prompts) != 5 {
		t.Fatalf("page2: expected 5, got %d", len(page2.Prompts))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2: expected empty nextCursor, got %q", page2.NextCursor)
	}
}

// ── handlePaginatedList edge case tests ─────────────────────────────

func TestHandleMessage_ToolsList_Pagination_DefaultSizeWithCursor(t *testing.T) {
	s := initServer(t)
	for i := 0; i < 100; i++ {
		tool, _ := NewTool(fmt.Sprintf("tool_%03d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	// limit=0 with non-empty cursor should use default page size (50).
	data := jsonrpcReq(1, "tools/list", map[string]any{"cursor": "10", "limit": 0})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	raw, _ := json.Marshal(resp.Result)
	var result ListToolsResult
	json.Unmarshal(raw, &result)

	if len(result.Tools) != 50 {
		t.Errorf("expected default page size 50, got %d", len(result.Tools))
	}
}

func TestPaginateSlice_MaxPageSize(t *testing.T) {
	items := make([]int, 2000)
	for i := range items {
		items[i] = i
	}
	page, next, err := paginateSlice(items, "", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1000 {
		t.Errorf("expected max page size of 1000, got %d", len(page))
	}
	if next != "1000" {
		t.Errorf("nextCursor = %q, want %q", next, "1000")
	}
}

func TestHandleMessage_ToolsList_Pagination_DeterministicOrder(t *testing.T) {
	s := initServer(t)
	// Register tools in reverse order to verify sorting.
	for i := 9; i >= 0; i-- {
		tool, _ := NewTool(fmt.Sprintf("tool_%d", i), func(_ context.Context, _ []byte) ([]byte, error) {
			return nil, nil
		})
		s.RegisterTool(tool)
	}

	data := jsonrpcReq(1, "tools/list", map[string]any{"limit": 5})
	resp, _ := s.HandleMessage(context.Background(), data)
	raw, _ := json.Marshal(resp.Result)
	var page1 ListToolsResult
	json.Unmarshal(raw, &page1)

	if len(page1.Tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(page1.Tools))
	}
	// Should be sorted by name: tool_0 .. tool_4
	for i, ti := range page1.Tools {
		want := fmt.Sprintf("tool_%d", i)
		if ti.Name != want {
			t.Errorf("page1[%d] = %q, want %q", i, ti.Name, want)
		}
	}

	data = jsonrpcReq(2, "tools/list", map[string]any{"cursor": page1.NextCursor, "limit": 5})
	resp, _ = s.HandleMessage(context.Background(), data)
	raw, _ = json.Marshal(resp.Result)
	var page2 ListToolsResult
	json.Unmarshal(raw, &page2)

	// Should be tool_5 .. tool_9
	for i, ti := range page2.Tools {
		want := fmt.Sprintf("tool_%d", i+5)
		if ti.Name != want {
			t.Errorf("page2[%d] = %q, want %q", i, ti.Name, want)
		}
	}
}

func TestHandlePaginatedList_MalformedJSON(t *testing.T) {
	s := initServer(t)

	tool, _ := NewTool("t", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	s.RegisterTool(tool)

	// Send params that are not a valid JSON object.
	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":"bad"}`)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", resp)
	}
}
