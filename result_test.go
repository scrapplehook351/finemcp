package finemcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNewTextResult_Construction(t *testing.T) {
	t.Parallel()

	r := NewTextResult("success")

	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}

	tc, ok := r.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}

	if tc.Text != "success" {
		t.Errorf("expected text %q, got %q", "success", tc.Text)
	}

	if r.IsError {
		t.Error("expected IsError to be false")
	}
}

func TestNewErrorResult_Construction(t *testing.T) {
	t.Parallel()

	r := NewErrorResult("something went wrong")

	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}

	tc, ok := r.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}

	if tc.Text != "something went wrong" {
		t.Errorf("expected text %q, got %q", "something went wrong", tc.Text)
	}

	if !r.IsError {
		t.Error("expected IsError to be true")
	}
}

func TestCallToolResult_MarshalJSON_Success(t *testing.T) {
	t.Parallel()

	r := NewTextResult("hello")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	// Content should be an array
	content, ok := raw["content"].([]any)
	if !ok {
		t.Fatalf("expected content to be array, got %T", raw["content"])
	}
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}

	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected content block to be object, got %T", content[0])
	}
	if block["type"] != "text" {
		t.Errorf("expected type %q, got %q", "text", block["type"])
	}
	if block["text"] != "hello" {
		t.Errorf("expected text %q, got %q", "hello", block["text"])
	}

	// isError should be omitted when false
	if _, exists := raw["isError"]; exists {
		t.Error("expected isError to be omitted when false")
	}
}

func TestCallToolResult_MarshalJSON_Error(t *testing.T) {
	t.Parallel()

	r := NewErrorResult("fail")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	isErr, ok := raw["isError"].(bool)
	if !ok {
		t.Fatalf("expected isError to be bool, got %T", raw["isError"])
	}
	if !isErr {
		t.Error("expected isError to be true")
	}
}

func TestCallToolResult_MarshalJSON_StructuredContentOmitted(t *testing.T) {
	t.Parallel()

	r := NewTextResult("test")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	if _, exists := raw["structuredContent"]; exists {
		t.Error("expected structuredContent to be omitted when nil")
	}
}

func TestCallToolResult_EmptyContent(t *testing.T) {
	t.Parallel()

	r := &CallToolResult{
		Content: []Content{},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	content, ok := raw["content"].([]any)
	if !ok {
		t.Fatalf("expected content to be array, got %T", raw["content"])
	}
	if len(content) != 0 {
		t.Errorf("expected empty content array, got %d elements", len(content))
	}
}

func TestNewImageResult_Construction(t *testing.T) {
	t.Parallel()

	r := NewImageResult("image/png", []byte{0x01, 0x02})

	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}
	if _, ok := r.Content[0].(ImageContent); !ok {
		t.Fatalf("expected ImageContent, got %T", r.Content[0])
	}
	if r.IsError {
		t.Error("expected IsError to be false")
	}

	// Verify wire format
	out, _ := json.Marshal(r)
	var raw map[string]any
	json.Unmarshal(out, &raw)
	contents := raw["content"].([]any)
	block := contents[0].(map[string]any)
	if block["type"] != "image" {
		t.Errorf("type = %q, want \"image\"", block["type"])
	}
	if block["mimeType"] != "image/png" {
		t.Errorf("mimeType = %q, want \"image/png\"", block["mimeType"])
	}
}

func TestNewEmbeddedResourceResult_Construction(t *testing.T) {
	t.Parallel()

	rc := NewTextResourceContent("file:///data.txt", "content")
	r := NewEmbeddedResourceResult(rc)

	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}
	if _, ok := r.Content[0].(EmbeddedResource); !ok {
		t.Fatalf("expected EmbeddedResource, got %T", r.Content[0])
	}
	if r.IsError {
		t.Error("expected IsError to be false")
	}

	// Verify wire format
	out, _ := json.Marshal(r)
	var raw map[string]any
	json.Unmarshal(out, &raw)
	contents := raw["content"].([]any)
	block := contents[0].(map[string]any)
	if block["type"] != "resource" {
		t.Errorf("type = %q, want \"resource\"", block["type"])
	}
}

func TestNewAudioResult_Construction(t *testing.T) {
	t.Parallel()

	data := []byte{0x01, 0x02}
	r := NewAudioResult("audio/wav", data)

	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(r.Content))
	}
	if _, ok := r.Content[0].(AudioContent); !ok {
		t.Fatalf("expected AudioContent, got %T", r.Content[0])
	}
	if r.IsError {
		t.Error("expected IsError to be false")
	}

	// Verify wire format
	out, _ := json.Marshal(r)
	var raw map[string]any
	json.Unmarshal(out, &raw)
	contents := raw["content"].([]any)
	block := contents[0].(map[string]any)
	if block["type"] != "audio" {
		t.Errorf("type = %q, want \"audio\"", block["type"])
	}
	if block["mimeType"] != "audio/wav" {
		t.Errorf("mimeType = %q, want \"audio/wav\"", block["mimeType"])
	}
	// Data should be base64-encoded
	if block["data"] != "AQI=" { // base64(0x01,0x02)
		t.Errorf("data = %q, want %q", block["data"], "AQI=")
	}
}

func TestCallToolResult_UnmarshalJSON_TooManyItems(t *testing.T) {
	t.Parallel()
	// Build a JSON array with more items than maxContentItems.
	items := make([]string, maxContentItems+1)
	for i := range items {
		items[i] = `{"type":"text","text":"x"}`
	}
	js := fmt.Sprintf(`{"content":[%s]}`, strings.Join(items, ","))
	var r CallToolResult
	err := json.Unmarshal([]byte(js), &r)
	if err == nil {
		t.Fatal("expected error for too many content items")
	}
}

func TestCallToolResult_UnmarshalJSON_Valid(t *testing.T) {
	t.Parallel()
	js := `{"content":[{"type":"text","text":"hi"}],"isError":false}`
	var r CallToolResult
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 1 {
		t.Fatalf("got %d content items, want 1", len(r.Content))
	}
	tc, ok := r.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}
	if tc.Text != "hi" {
		t.Errorf("text = %q, want %q", tc.Text, "hi")
	}
}

func TestCallToolResult_UnmarshalJSON_InvalidJSON(t *testing.T) {
	t.Parallel()
	var r CallToolResult
	err := json.Unmarshal([]byte(`{not json`), &r)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestCallToolResult_UnmarshalJSON_OversizedPayload(t *testing.T) {
	t.Parallel()
	// Build a payload larger than maxResultPayloadSize (50 MB).
	filler := strings.Repeat("x", maxResultPayloadSize+1)
	js := `{"content":[{"type":"text","text":"` + filler + `"}]}`
	var r CallToolResult
	if err := json.Unmarshal([]byte(js), &r); err == nil {
		t.Fatal("expected error for oversized payload")
	}
}
