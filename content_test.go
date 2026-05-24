package finemcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextContent_MarshalJSON(t *testing.T) {
	t.Parallel()

	tc := TextContent{Text: "hello world"}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	want := `{"type":"text","text":"hello world"}`
	if string(data) != want {
		t.Errorf("expected %s, got %s", want, string(data))
	}
}

func TestTextContent_MarshalJSON_EmptyText(t *testing.T) {
	t.Parallel()

	tc := TextContent{Text: ""}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	want := `{"type":"text","text":""}`
	if string(data) != want {
		t.Errorf("expected %s, got %s", want, string(data))
	}
}

func TestTextContent_ContentType(t *testing.T) {
	t.Parallel()

	tc := TextContent{Text: "test"}
	if tc.contentType() != "text" {
		t.Errorf("expected contentType %q, got %q", "text", tc.contentType())
	}
}

func TestTextContent_ImplementsContent(t *testing.T) {
	t.Parallel()

	// Compile-time check: TextContent satisfies the Content interface.
	var _ Content = TextContent{}
}

// --- ImageContent ---

func TestImageContent_ContentType(t *testing.T) {
	t.Parallel()
	ic := NewImageContent("image/png", []byte{0x89, 0x50, 0x4E, 0x47})
	if ic.contentType() != "image" {
		t.Errorf("expected contentType %q, got %q", "image", ic.contentType())
	}
}

func TestImageContent_ImplementsContent(t *testing.T) {
	t.Parallel()
	var _ Content = ImageContent{}
}

func TestImageContent_MarshalJSON(t *testing.T) {
	t.Parallel()

	data := []byte{0x01, 0x02, 0x03}
	ic := NewImageContent("image/jpeg", data)

	out, err := json.Marshal(ic)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if m["type"] != "image" {
		t.Errorf("type = %q, want %q", m["type"], "image")
	}
	if m["mimeType"] != "image/jpeg" {
		t.Errorf("mimeType = %q, want %q", m["mimeType"], "image/jpeg")
	}
	// Data should be base64-encoded
	if m["data"] != "AQID" { // base64(0x01,0x02,0x03)
		t.Errorf("data = %q, want %q", m["data"], "AQID")
	}
	if _, ok := m["text"]; ok {
		t.Error("expected no 'text' field in image content")
	}
}

// --- AudioContent ---

func TestAudioContent_ContentType(t *testing.T) {
	t.Parallel()
	ac := NewAudioContent("audio/wav", []byte{0x52, 0x49, 0x46, 0x46})
	if ac.contentType() != "audio" {
		t.Errorf("expected contentType %q, got %q", "audio", ac.contentType())
	}
}

func TestAudioContent_ImplementsContent(t *testing.T) {
	t.Parallel()
	var _ Content = AudioContent{}
}

func TestAudioContent_MarshalJSON(t *testing.T) {
	t.Parallel()

	data := []byte{0x01, 0x02, 0x03}
	ac := NewAudioContent("audio/mpeg", data)

	out, err := json.Marshal(ac)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if m["type"] != "audio" {
		t.Errorf("type = %q, want %q", m["type"], "audio")
	}
	if m["mimeType"] != "audio/mpeg" {
		t.Errorf("mimeType = %q, want %q", m["mimeType"], "audio/mpeg")
	}
	// Data should be base64-encoded
	if m["data"] != "AQID" { // base64(0x01,0x02,0x03)
		t.Errorf("data = %q, want %q", m["data"], "AQID")
	}
	if _, ok := m["text"]; ok {
		t.Error("expected no 'text' field in audio content")
	}
}

func TestAudioContent_MarshalJSON_EmptyData(t *testing.T) {
	t.Parallel()

	ac := NewAudioContent("audio/wav", nil)

	out, err := json.Marshal(ac)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if m["type"] != "audio" {
		t.Errorf("type = %q, want %q", m["type"], "audio")
	}
	// Nil or empty byte slices should encode to an empty string.
	if m["data"] != "" {
		t.Errorf("data = %q, want %q", m["data"], "")
	}
	if m["mimeType"] != "audio/wav" {
		t.Errorf("mimeType = %q, want %q", m["mimeType"], "audio/wav")
	}
}

// --- EmbeddedResource ---

func TestEmbeddedResource_ContentType(t *testing.T) {
	t.Parallel()
	rc := NewTextResourceContent("file:///foo.txt", "hello")
	er := NewEmbeddedResource(rc)
	if er.contentType() != "resource" {
		t.Errorf("expected contentType %q, got %q", "resource", er.contentType())
	}
}

func TestEmbeddedResource_ImplementsContent(t *testing.T) {
	t.Parallel()
	var _ Content = EmbeddedResource{}
}

func TestEmbeddedResource_MarshalJSON(t *testing.T) {
	t.Parallel()

	rc := NewTextResourceContent("file:///readme.md", "# Hello")
	er := NewEmbeddedResource(rc)

	out, err := json.Marshal(er)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if m["type"] != "resource" {
		t.Errorf("type = %q, want %q", m["type"], "resource")
	}
	res, ok := m["resource"].(map[string]any)
	if !ok {
		t.Fatalf("expected resource object, got %T", m["resource"])
	}
	if res["uri"] != "file:///readme.md" {
		t.Errorf("resource.uri = %q, want %q", res["uri"], "file:///readme.md")
	}
}

// ── DecodeContent tests ─────────────────────────────────────────────

func TestDecodeContent_NullInput(t *testing.T) {
	t.Parallel()
	_, err := DecodeContent(nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	_, err = DecodeContent(json.RawMessage("null"))
	if err == nil {
		t.Fatal("expected error for null input")
	}
}

func TestDecodeContent_EmptyInput(t *testing.T) {
	t.Parallel()
	_, err := DecodeContent(json.RawMessage(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestDecodeContent_OversizedPayload(t *testing.T) {
	t.Parallel()

	// Build a JSON text content exceeding maxContentItemSize (10 MB).
	filler := strings.Repeat("x", maxContentItemSize+1)
	big := `{"type":"text","text":"` + filler + `"}`
	_, err := DecodeContent(json.RawMessage(big))
	if err == nil {
		t.Fatal("expected error for oversized payload")
	}
}

func TestDecodeContent_UnknownType(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"video","url":"http://example.com"}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	// Error must NOT echo back the unknown type value.
	if got := err.Error(); got != "unknown content type" {
		t.Errorf("error = %q, want %q", got, "unknown content type")
	}
}

// ── Malformed content rejection tests ──────────────────────────────

func TestDecodeContent_ImageWrongDataType(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"image","data":123,"mimeType":"image/png"}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for numeric data field")
	}
}

func TestDecodeContent_ImageMissingData(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"image","mimeType":"image/png"}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for missing data field")
	}
}

func TestDecodeContent_AudioWrongMimeType(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"audio","data":"AQIDBA==","mimeType":123}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for numeric mimeType field")
	}
}

func TestDecodeContent_ResourceWrongType(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"resource","resource":"not-an-object"}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for string resource field")
	}
}

func TestDecodeContent_ResourceMissing(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"resource"}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for missing resource field")
	}
}

// ── Edge case tests for required fields and validation ─────────────

func TestDecodeContent_TextFieldOptional(t *testing.T) {
	t.Parallel()
	// Per MCP spec, text field is optional and defaults to empty string.
	raw := json.RawMessage(`{"type":"text"}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatalf("expected text field to be optional, got error: %v", err)
	}
	tc, ok := c.(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", c)
	}
	if tc.Text != "" {
		t.Errorf("expected empty text, got %q", tc.Text)
	}
}

func TestDecodeContent_TextEmptyString(t *testing.T) {
	t.Parallel()
	// Empty text string is valid — must round-trip with MarshalJSON.
	raw := json.RawMessage(`{"type":"text","text":""}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatalf("empty text should be valid: %v", err)
	}
	tc, ok := c.(TextContent)
	if !ok || tc.Text != "" {
		t.Errorf("expected TextContent{Text:\"\"}, got %+v", c)
	}
}

func TestDecodeContent_ImageEmptyDataRoundTrip(t *testing.T) {
	t.Parallel()
	// Empty data encodes to "" via MarshalJSON. DecodeContent must accept it
	// to preserve round-trip compatibility.
	raw := json.RawMessage(`{"type":"image","data":"","mimeType":"image/png"}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatalf("empty data should round-trip: %v", err)
	}
	ic, ok := c.(ImageContent)
	if !ok {
		t.Fatalf("expected ImageContent, got %T", c)
	}
	if len(ic.Data) != 0 {
		t.Errorf("expected empty data, got %d bytes", len(ic.Data))
	}
}

func TestDecodeContent_AudioEmptyDataRoundTrip(t *testing.T) {
	t.Parallel()
	// Empty data encodes to "" via MarshalJSON. DecodeContent must accept it.
	raw := json.RawMessage(`{"type":"audio","data":"","mimeType":"audio/wav"}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatalf("empty data should round-trip: %v", err)
	}
	ac, ok := c.(AudioContent)
	if !ok {
		t.Fatalf("expected AudioContent, got %T", c)
	}
	if len(ac.Data) != 0 {
		t.Errorf("expected empty data, got %d bytes", len(ac.Data))
	}
}

func TestDecodeContent_ImageNoMimeType(t *testing.T) {
	t.Parallel()
	// MimeType is optional; this should succeed.
	raw := json.RawMessage(`{"type":"image","data":"AQIDBA=="}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ic, ok := c.(ImageContent)
	if !ok {
		t.Fatalf("expected ImageContent, got %T", c)
	}
	if ic.MimeType != "" {
		t.Errorf("expected empty mimeType, got %q", ic.MimeType)
	}
	if len(ic.Data) != 4 {
		t.Errorf("expected 4 bytes of data, got %d", len(ic.Data))
	}
}

func TestDecodeContent_AudioNoMimeType(t *testing.T) {
	t.Parallel()
	// MimeType is optional; this should succeed.
	raw := json.RawMessage(`{"type":"audio","data":"AQIDBA=="}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac, ok := c.(AudioContent)
	if !ok {
		t.Fatalf("expected AudioContent, got %T", c)
	}
	if ac.MimeType != "" {
		t.Errorf("expected empty mimeType, got %q", ac.MimeType)
	}
	if len(ac.Data) != 4 {
		t.Errorf("expected 4 bytes of data, got %d", len(ac.Data))
	}
}

func TestDecodeContent_ResourceEmptyURI(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"resource","resource":{"uri":"","text":"data"}}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for empty URI")
	}
}

func TestDecodeContent_ResourceBothTextAndBlob(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"resource","resource":{"uri":"file:///test","text":"data","blob":"AAAA"}}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error when both text and blob are set")
	}
}

func TestDecodeContent_ResourceNeitherTextNorBlob(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"type":"resource","resource":{"uri":"file:///test"}}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error when neither text nor blob is set")
	}
}

// ── Performance benchmark ───────────────────────────────────────────

// BenchmarkDecodeContent_MixedTypes benchmarks the single-pass unmarshal
// optimization for DecodeContent with varied content types.
func BenchmarkDecodeContent_MixedTypes(b *testing.B) {
	testCases := []struct {
		name string
		json string
	}{
		{"text", `{"type":"text","text":"hello world"}`},
		{"image", `{"type":"image","data":"AQIDBA==","mimeType":"image/png"}`},
		{"audio", `{"type":"audio","data":"AQIDBA==","mimeType":"audio/wav"}`},
		{"resource", `{"type":"resource","resource":{"uri":"file:///test.txt","text":"data"}}`},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			raw := json.RawMessage(tc.json)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = DecodeContent(raw)
			}
		})
	}
}

func TestDecodeContent_MissingType(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"text":"no type field"}`)
	_, err := DecodeContent(raw)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestDecodeContent_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := DecodeContent(json.RawMessage(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	// Error must be sanitized.
	if got := err.Error(); got != "invalid content structure" {
		t.Errorf("error = %q, want sanitized message", got)
	}
}

func TestDecodeContent_TypeMismatch(t *testing.T) {
	t.Parallel()
	// The JSON says "text" but we manually verify the struct parses correctly.
	raw := json.RawMessage(`{"type":"text","text":"hello"}`)
	c, err := DecodeContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	tc, ok := c.(TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", c)
	}
	if tc.Text != "hello" {
		t.Errorf("text = %q, want %q", tc.Text, "hello")
	}
}

func TestDecodeContent_AllTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want string // contentType()
	}{
		{"text", `{"type":"text","text":"hi"}`, "text"},
		{"image", `{"type":"image","data":"AAAA","mimeType":"image/png"}`, "image"},
		{"audio", `{"type":"audio","data":"AAAA","mimeType":"audio/wav"}`, "audio"},
		{"resource", `{"type":"resource","resource":{"uri":"file:///a","text":"b"}}`, "resource"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := DecodeContent(json.RawMessage(tt.json))
			if err != nil {
				t.Fatalf("DecodeContent(%s) error: %v", tt.name, err)
			}
			if c.contentType() != tt.want {
				t.Errorf("contentType() = %q, want %q", c.contentType(), tt.want)
			}
		})
	}
}
