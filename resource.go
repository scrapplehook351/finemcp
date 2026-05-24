package finemcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ResourceHandler reads a resource identified by its URI.
// It receives the context and the resource URI, and returns
// a slice of ResourceContent items (text or blob).
type ResourceHandler func(ctx context.Context, uri string) ([]ResourceContent, error)

// Resource represents a registered MCP resource.
type Resource struct {
	// URI is the unique identifier for this resource (e.g. "file:///etc/hosts").
	URI string

	// Name is a human-readable name for the resource.
	Name string

	// Description is an optional human-readable description.
	Description string

	// MimeType is the MIME type of the resource content (e.g. "text/plain").
	MimeType string

	// Handler reads the resource content.
	Handler ResourceHandler
}

// ResourceOption is a functional option for configuring a Resource.
type ResourceOption func(*Resource)

// WithResourceDescription sets the resource's human-readable description.
func WithResourceDescription(desc string) ResourceOption {
	return func(r *Resource) { r.Description = desc }
}

// WithResourceMimeType sets the resource's MIME type.
func WithResourceMimeType(mime string) ResourceOption {
	return func(r *Resource) { r.MimeType = mime }
}

// NewResource creates a new Resource with the given URI, name, handler, and options.
func NewResource(uri, name string, handler ResourceHandler, opts ...ResourceOption) (*Resource, error) {
	if uri == "" {
		return nil, errResourceURIEmpty
	}
	if name == "" {
		return nil, errResourceNameEmpty
	}
	if handler == nil {
		return nil, errResourceHandlerNil
	}

	r := &Resource{
		URI:     uri,
		Name:    name,
		Handler: handler,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// ResourceTemplate represents a URI-template resource that can be
// parameterized at read time (e.g. "file:///logs/{date}.log").
//
// When a client sends a resources/read request and no static resource matches
// the URI, the server tries each registered template in lexicographic
// URITemplate order and invokes the first matching handler with the concrete
// URI. Overlapping templates are not recommended; the winner depends on
// alphabetic order, which may be surprising.
type ResourceTemplate struct {
	// URITemplate is an RFC 6570 URI template.
	URITemplate string

	// Name is a human-readable name for the template.
	Name string

	// Description is an optional human-readable description.
	Description string

	// MimeType is the MIME type of the resource content.
	MimeType string

	// Handler reads the resource; the URI parameter is the expanded template.
	Handler ResourceHandler

	// Completer provides argument auto-completions for this template.
	// When nil, the server returns an empty completion result.
	Completer CompleteHandler

	// parsedSegs is the pre-parsed segment representation of URITemplate,
	// populated at registration time to avoid per-match parsing overhead.
	parsedSegs []segment
}

// ResourceTemplateOption is a functional option for configuring a ResourceTemplate.
type ResourceTemplateOption func(*ResourceTemplate)

// WithTemplateDescription sets the template's human-readable description.
func WithTemplateDescription(desc string) ResourceTemplateOption {
	return func(t *ResourceTemplate) { t.Description = desc }
}

// WithTemplateMimeType sets the template's MIME type.
func WithTemplateMimeType(mime string) ResourceTemplateOption {
	return func(t *ResourceTemplate) { t.MimeType = mime }
}

// NewResourceTemplate creates a new ResourceTemplate with the given URI template, name, handler, and options.
// It validates the template syntax at creation time, returning errTemplateInvalid
// for malformed or unsupported templates.
func NewResourceTemplate(uriTemplate, name string, handler ResourceHandler, opts ...ResourceTemplateOption) (*ResourceTemplate, error) {
	if uriTemplate == "" {
		return nil, errTemplateURIEmpty
	}
	if name == "" {
		return nil, errTemplateNameEmpty
	}
	if handler == nil {
		return nil, errTemplateHandlerNil
	}
	if !isValidURITemplate(uriTemplate) {
		return nil, errTemplateInvalid
	}

	t := &ResourceTemplate{
		URITemplate: uriTemplate,
		Name:        name,
		Handler:     handler,
		parsedSegs:  parseTemplate(uriTemplate),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

// ── Resource content ────────────────────────────────────────────────

// ResourceContent represents the content of a resource.
// Exactly one of Text or Blob must be set.
type ResourceContent struct {
	// URI is the resource URI this content belongs to.
	URI string `json:"uri"`

	// MimeType is the MIME type of this content.
	MimeType string `json:"mimeType,omitempty"`

	// Text holds the text content (mutually exclusive with Blob).
	Text *string `json:"text,omitempty"`

	// Blob holds base64-encoded binary content (mutually exclusive with Text).
	Blob *string `json:"blob,omitempty"`
}

// NewTextResourceContent creates a text resource content item.
func NewTextResourceContent(uri, text string) ResourceContent {
	return ResourceContent{URI: uri, Text: &text}
}

// NewBlobResourceContent creates a binary resource content item.
// The data is base64-encoded automatically.
func NewBlobResourceContent(uri string, data []byte) ResourceContent {
	blob := base64.StdEncoding.EncodeToString(data)
	return ResourceContent{URI: uri, Blob: &blob}
}

// Validate checks that the ResourceContent is well-formed:
// URI must be non-empty and exactly one of Text or Blob must be set.
func (c ResourceContent) Validate() error {
	if c.URI == "" {
		return errors.New("resource content: URI must not be empty")
	}
	if c.Text != nil && c.Blob != nil {
		return errors.New("resource content: exactly one of Text or Blob must be set, got both")
	}
	if c.Text == nil && c.Blob == nil {
		return errors.New("resource content: exactly one of Text or Blob must be set, got neither")
	}
	return nil
}

// MarshalJSON implements json.Marshaler for ResourceContent.
// It enforces that exactly one of Text or Blob is present in the output.
func (c ResourceContent) MarshalJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	// Use an alias to avoid infinite recursion.
	type alias ResourceContent
	return json.Marshal(alias(c))
}

// ── Wire types ──────────────────────────────────────────────────────

// ResourceInfo is the wire representation of a resource in a resources/list response.
type ResourceInfo struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceTemplateInfo is the wire representation in a resources/templates/list response.
type ResourceTemplateInfo struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ListResourcesResult is the server's response to a "resources/list" request.
type ListResourcesResult struct {
	Resources  []ResourceInfo `json:"resources"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// ListResourceTemplatesResult is the server's response to a "resources/templates/list" request.
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplateInfo `json:"resourceTemplates"`
	NextCursor        string                 `json:"nextCursor,omitempty"`
	Meta              map[string]any         `json:"_meta,omitempty"`
}

// ReadResourceParams is the client's payload for a "resources/read" request.
type ReadResourceParams struct {
	URI  string         `json:"uri"`
	Meta map[string]any `json:"_meta,omitempty"`
}

// ReadResourceResult is the server's response to a "resources/read" request.
type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
	Meta     map[string]any    `json:"_meta,omitempty"`
}

// ── Helpers ─────────────────────────────────────────────────────────

// ReadResourceResultFromText is a convenience for returning a single text resource.
func ReadResourceResultFromText(uri, text string) *ReadResourceResult {
	return &ReadResourceResult{
		Contents: []ResourceContent{NewTextResourceContent(uri, text)},
	}
}

// MarshalJSON implements json.Marshaler. No custom logic needed, but having
// the method makes the intent explicit and prevents accidental breakage.
func (r ReadResourceResult) MarshalJSON() ([]byte, error) {
	type alias ReadResourceResult
	return json.Marshal(alias(r))
}
