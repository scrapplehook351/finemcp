package finemcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Content is a sealed interface for tool result content types.
// Implementations: TextContent, ImageContent, AudioContent, EmbeddedResource.
type Content interface {
	contentType() string // unexported = only types in this package can implement
}

// TextContent represents text provided to or from an LLM.
type TextContent struct {
	Text string `json:"text"`
}

func (TextContent) contentType() string { return "text" }

// MarshalJSON includes the "type" discriminator required by MCP wire format.
func (c TextContent) MarshalJSON() ([]byte, error) {
	type raw struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	return json.Marshal(raw{Type: "text", Text: c.Text})
}

// ImageContent represents a base64-encoded image returned by a tool.
// The MimeType field indicates the image format (e.g. "image/png", "image/jpeg").
// MimeType may be an empty string if the format is unknown or unspecified.
type ImageContent struct {
	// Data is the raw image bytes. They are base64-encoded on the wire.
	Data []byte

	// MimeType is the MIME type of the image (e.g. "image/png").
	// May be empty if the format is unspecified.
	MimeType string
}

func (ImageContent) contentType() string { return "image" }

// MarshalJSON encodes Data as a base64 string and includes the MCP "type" discriminator.
func (c ImageContent) MarshalJSON() ([]byte, error) {
	type raw struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	}
	return json.Marshal(raw{
		Type:     "image",
		Data:     base64.StdEncoding.EncodeToString(c.Data),
		MimeType: c.MimeType,
	})
}

// NewImageContent creates an ImageContent from raw bytes and a MIME type.
// The bytes are stored as-is and base64-encoded when marshaled to JSON.
func NewImageContent(mimeType string, data []byte) ImageContent {
	return ImageContent{Data: data, MimeType: mimeType}
}

// AudioContent represents a base64-encoded audio clip returned by a tool.
// MCP wire format: {"type":"audio","data":"<base64>","mimeType":"audio/*"}.
// The MimeType field indicates the audio format (e.g. "audio/wav", "audio/mpeg").
// MimeType may be an empty string if the format is unknown or unspecified.
type AudioContent struct {
	// Data is the raw audio bytes. They are base64-encoded on the wire.
	Data []byte

	// MimeType is the MIME type of the audio (e.g. "audio/wav").
	// May be empty if the format is unspecified.
	MimeType string
}

func (AudioContent) contentType() string { return "audio" }

// MarshalJSON encodes Data as a base64 string and includes the MCP "type" discriminator.
func (c AudioContent) MarshalJSON() ([]byte, error) {
	type raw struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	}
	return json.Marshal(raw{
		Type:     "audio",
		Data:     base64.StdEncoding.EncodeToString(c.Data),
		MimeType: c.MimeType,
	})
}

// NewAudioContent creates an AudioContent from raw bytes and a MIME type.
// The bytes are stored as-is and base64-encoded when marshaled to JSON.
func NewAudioContent(mimeType string, data []byte) AudioContent {
	return AudioContent{Data: data, MimeType: mimeType}
}

// EmbeddedResource embeds a resource directly inside a tool result,
// as defined by the MCP spec's "resource" content type.
type EmbeddedResource struct {
	Resource ResourceContent
}

func (EmbeddedResource) contentType() string { return "resource" }

// MarshalJSON marshals the embedded resource with the MCP "type" discriminator.
func (e EmbeddedResource) MarshalJSON() ([]byte, error) {
	type raw struct {
		Type     string          `json:"type"`
		Resource ResourceContent `json:"resource"`
	}
	return json.Marshal(raw{Type: "resource", Resource: e.Resource})
}

// NewEmbeddedResource wraps a ResourceContent as an EmbeddedResource content item.
func NewEmbeddedResource(rc ResourceContent) EmbeddedResource {
	return EmbeddedResource{Resource: rc}
}

// maxContentItemSize is the maximum size (in bytes) of a single JSON-encoded
// content block that DecodeContent will accept. Payloads exceeding this limit
// are rejected early to prevent excessive memory allocation from untrusted input.
const maxContentItemSize = 10 * 1024 * 1024 // 10 MB

// DecodeContent decodes a raw JSON content block into the appropriate Content type
// based on the "type" discriminator field. Uses a single-pass unmarshal for efficiency.
//
// Validation rules:
//   - TextContent.Text: optional field; defaults to empty string when absent.
//   - ImageContent/AudioContent.Data: required, must be a non-empty base64 string.
//   - ImageContent/AudioContent.MimeType: optional; defaults to empty string.
//   - ResourceContent.URI: required, must be non-empty.
//   - ResourceContent.Text/Blob: exactly one must be set.
func DecodeContent(raw json.RawMessage) (Content, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("content must not be null or empty")
	}
	if len(raw) > maxContentItemSize {
		return nil, fmt.Errorf("content exceeds maximum allowed size")
	}

	// Single-pass unmarshal: decode to map to extract type and fields.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("invalid content structure")
	}

	typeBytes, ok := fields["type"]
	if !ok {
		return nil, fmt.Errorf("invalid content structure")
	}
	var contentType string
	if err := json.Unmarshal(typeBytes, &contentType); err != nil {
		return nil, fmt.Errorf("invalid content structure")
	}

	switch contentType {
	case "text":
		var text string
		if textBytes, ok := fields["text"]; ok {
			if err := json.Unmarshal(textBytes, &text); err != nil {
				return nil, fmt.Errorf("invalid text content")
			}
		}
		return TextContent{Text: text}, nil

	case "image":
		var dataStr, mimeType string
		dataBytes, hasData := fields["data"]
		if !hasData {
			return nil, fmt.Errorf("invalid image content")
		}
		if err := json.Unmarshal(dataBytes, &dataStr); err != nil {
			return nil, fmt.Errorf("invalid image content")
		}
		if mimeBytes, ok := fields["mimeType"]; ok {
			if err := json.Unmarshal(mimeBytes, &mimeType); err != nil {
				return nil, fmt.Errorf("invalid image content")
			}
		}
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			return nil, fmt.Errorf("invalid image data encoding")
		}
		return ImageContent{Data: data, MimeType: mimeType}, nil

	case "audio":
		var dataStr, mimeType string
		dataBytes, hasData := fields["data"]
		if !hasData {
			return nil, fmt.Errorf("invalid audio content")
		}
		if err := json.Unmarshal(dataBytes, &dataStr); err != nil {
			return nil, fmt.Errorf("invalid audio content")
		}
		if mimeBytes, ok := fields["mimeType"]; ok {
			if err := json.Unmarshal(mimeBytes, &mimeType); err != nil {
				return nil, fmt.Errorf("invalid audio content")
			}
		}
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			return nil, fmt.Errorf("invalid audio data encoding")
		}
		return AudioContent{Data: data, MimeType: mimeType}, nil

	case "resource":
		var rc ResourceContent
		resBytes, hasRes := fields["resource"]
		if !hasRes {
			return nil, fmt.Errorf("invalid resource content")
		}
		if err := json.Unmarshal(resBytes, &rc); err != nil {
			return nil, fmt.Errorf("invalid resource content")
		}
		if err := rc.Validate(); err != nil {
			return nil, fmt.Errorf("invalid resource content")
		}
		return EmbeddedResource{Resource: rc}, nil

	default:
		return nil, fmt.Errorf("unknown content type")
	}
}
