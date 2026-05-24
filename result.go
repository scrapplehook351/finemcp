package finemcp

import (
	"encoding/json"
	"fmt"
)

// CallToolResult is the server's response to a tools/call request.
type CallToolResult struct {
	Content           []Content      `json:"content"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

// maxContentItems is the maximum number of content items allowed in
// a single CallToolResult to prevent memory exhaustion from untrusted input.
const maxContentItems = 1000

// maxResultPayloadSize is the maximum total byte size of a CallToolResult
// JSON payload accepted by UnmarshalJSON.
const maxResultPayloadSize = 50 * 1024 * 1024 // 50 MB

// UnmarshalJSON decodes a CallToolResult, handling the Content interface slice.
func (r *CallToolResult) UnmarshalJSON(data []byte) error {
	if len(data) > maxResultPayloadSize {
		return fmt.Errorf("invalid tool result")
	}
	var raw struct {
		Content           []json.RawMessage `json:"content"`
		IsError           bool              `json:"isError,omitempty"`
		StructuredContent any               `json:"structuredContent,omitempty"`
		Meta              map[string]any    `json:"_meta,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid tool result")
	}
	if len(raw.Content) > maxContentItems {
		return fmt.Errorf("tool result exceeds maximum content items (%d)", maxContentItems)
	}
	r.IsError = raw.IsError
	r.StructuredContent = raw.StructuredContent
	r.Meta = raw.Meta
	r.Content = make([]Content, 0, len(raw.Content))
	for _, rc := range raw.Content {
		c, err := DecodeContent(rc)
		if err != nil {
			return fmt.Errorf("invalid tool result content")
		}
		r.Content = append(r.Content, c)
	}
	return nil
}

// NewTextResult creates a successful result with a single text content block.
func NewTextResult(text string) *CallToolResult {
	return &CallToolResult{
		Content: []Content{TextContent{Text: text}},
	}
}

// NewErrorResult creates an error result with a single text content block.
// Tool errors are returned as content with IsError=true, not as protocol errors.
func NewErrorResult(text string) *CallToolResult {
	return &CallToolResult{
		Content: []Content{TextContent{Text: text}},
		IsError: true,
	}
}

// NewImageResult creates a successful result containing a single image.
// data is the raw image bytes; mimeType should be e.g. "image/png" or "image/jpeg".
func NewImageResult(mimeType string, data []byte) *CallToolResult {
	return &CallToolResult{
		Content: []Content{NewImageContent(mimeType, data)},
	}
}

// NewEmbeddedResourceResult creates a successful result containing a single embedded resource.
func NewEmbeddedResourceResult(rc ResourceContent) *CallToolResult {
	return &CallToolResult{
		Content: []Content{NewEmbeddedResource(rc)},
	}
}

// NewAudioResult creates a successful result containing a single audio clip.
// data is the raw audio bytes; mimeType should be e.g. "audio/wav" or "audio/mpeg".
func NewAudioResult(mimeType string, data []byte) *CallToolResult {
	return &CallToolResult{
		Content: []Content{NewAudioContent(mimeType, data)},
	}
}
