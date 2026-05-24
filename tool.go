package finemcp

import (
	"context"
)

// ToolHandler is a low-level tool handler that receives the raw JSON-encoded
// input bytes and returns raw JSON-encoded output bytes. Use [NewTypedTool] for
// a typed variant that handles JSON marshaling automatically.
type ToolHandler func(ctx context.Context, input []byte) ([]byte, error)

// Tool is a registered MCP tool that the server exposes to clients.
// Create tools via [NewTool] or [NewTypedTool]; do not construct directly.
type Tool struct {
	Name           string
	Description    string
	Handler        ToolHandler
	Simulator      SimulatorFunc // optional dry-run handler for simulation middleware
	Roles          []string
	InputSchema    any
	Annotations    *ToolAnnotations // optional MCP tool annotations
	SkipValidation bool             // when true, validation middleware skips this tool
}

// ToolAnnotations contains optional hints that describe a tool's behavior to
// clients without affecting server-side execution. All fields use *bool so
// that nil ("unknown") is distinguishable from an explicit false.
//
// Per MCP spec 2025-11-25, these are hints only — clients SHOULD NOT rely on
// them for correctness or security. Servers SHOULD set them accurately when
// the tool's behavior is known.
//
// Note on contradictory hints: the library does not reject semantically
// conflicting combinations (e.g., ReadOnlyHint=true + DestructiveHint=true).
// It is the caller's responsibility to set consistent values; clients may
// exhibit undefined behavior when receiving contradictory hints.
type ToolAnnotations struct {
	// ReadOnlyHint indicates the tool does not modify any external state.
	// A true value signals that the tool is safe to call speculatively.
	ReadOnlyHint *bool `json:"readOnlyHint,omitempty"`

	// DestructiveHint indicates the tool may perform irreversible operations
	// (e.g., deleting data). Clients may use this to add confirmation prompts.
	// Only meaningful when ReadOnlyHint is false or nil.
	DestructiveHint *bool `json:"destructiveHint,omitempty"`

	// IdempotentHint indicates that calling the tool repeatedly with the
	// same arguments produces the same result. Clients may use this to
	// enable automatic retries.
	IdempotentHint *bool `json:"idempotentHint,omitempty"`

	// Title is an optional human-readable title for the tool, used for
	// display purposes (e.g., UI labels).
	//
	// This field is a common extension adopted by several MCP SDKs,
	// complementing the base readOnlyHint/destructiveHint/idempotentHint
	// triple defined in the MCP spec.
	Title string `json:"title,omitempty"`

	// OpenWorldHint indicates whether the tool accepts additional properties
	// beyond those defined in its input schema. Default interpretation when
	// nil is false (closed schema).
	//
	// This field is a common extension adopted by several MCP SDKs;
	// it is not part of the base MCP 2025-11-25 annotations specification.
	OpenWorldHint *bool `json:"openWorldHint,omitempty"`
}

// ToolOption is a functional option for configuring a Tool.
type ToolOption func(*Tool)

// NewTool creates a Tool with the given name, handler, and options.
// The name must be 1–128 characters and may contain A–Z, a–z, 0–9, _, -, and .
// Returns an error if the name is invalid or the handler is nil.
// For strongly typed inputs/outputs, use [NewTypedTool] instead.
func NewTool(name string, handler ToolHandler, opts ...ToolOption) (*Tool, error) {
	err := validateToolName(name)
	if err != nil {
		return nil, err
	}

	err = validateHandler(handler)
	if err != nil {
		return nil, err
	}

	t := &Tool{
		Name:    name,
		Handler: handler,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t, nil
}

// WithRoles sets the roles required to execute this tool.
// Empty strings are silently filtered out. Roles are case-sensitive.
func WithRoles(roles ...string) ToolOption {
	return func(t *Tool) {
		t.Roles = make([]string, 0, len(roles))
		for _, r := range roles {
			if r != "" {
				t.Roles = append(t.Roles, r)
			}
		}
	}
}

// WithSimulator registers a custom dry-run handler for the tool.
// When the Simulation middleware is active and the client sends
// _meta.dryRun: true, this function is called instead of the real handler.
//
// The simulator MUST NOT perform real side effects. It should return
// output in the same format the real handler uses (typically JSON) so
// that callers can parse it consistently. The simulator must be safe for
// concurrent use — see SimulatorFunc for details.
func WithSimulator(fn SimulatorFunc) ToolOption {
	return func(t *Tool) {
		t.Simulator = fn
	}
}

// WithDescription sets the tool's human-readable description.
func WithDescription(desc string) ToolOption {
	return func(t *Tool) {
		t.Description = desc
	}
}

// WithInputSchema sets the JSON Schema defining the tool's expected input parameters.
// Accepts map[string]any, json.RawMessage, or any type that marshals to valid JSON Schema.
func WithInputSchema(schema any) ToolOption {
	return func(t *Tool) {
		t.InputSchema = schema
	}
}

// WithValidation controls whether input validation is enabled for this tool.
// By default validation is enabled (when the Validation middleware is in the chain).
// Pass false to skip validation for tools that accept arbitrary JSON.
func WithValidation(enabled bool) ToolOption {
	return func(t *Tool) {
		t.SkipValidation = !enabled
	}
}

// WithAnnotations sets the full ToolAnnotations struct on the tool.
// Use this when you need to set multiple annotations at once.
func WithAnnotations(a ToolAnnotations) ToolOption {
	return func(t *Tool) {
		t.Annotations = &a
	}
}

// WithReadOnly marks the tool as read-only (does not modify external state).
// Convenience shorthand for setting ReadOnlyHint = true.
func WithReadOnly() ToolOption {
	return func(t *Tool) {
		t.ensureAnnotations().ReadOnlyHint = BoolPtr(true)
	}
}

// WithDestructive marks the tool as destructive (may perform irreversible operations).
// Convenience shorthand for setting DestructiveHint = true.
func WithDestructive() ToolOption {
	return func(t *Tool) {
		t.ensureAnnotations().DestructiveHint = BoolPtr(true)
	}
}

// WithIdempotent marks the tool as idempotent (same input → same result).
// Convenience shorthand for setting IdempotentHint = true.
func WithIdempotent() ToolOption {
	return func(t *Tool) {
		t.ensureAnnotations().IdempotentHint = BoolPtr(true)
	}
}

// WithTitle sets the human-readable title annotation for display purposes.
func WithTitle(title string) ToolOption {
	return func(t *Tool) {
		t.ensureAnnotations().Title = title
	}
}

// WithOpenWorld marks the tool as accepting additional properties beyond
// those defined in its input schema.
func WithOpenWorld() ToolOption {
	return func(t *Tool) {
		t.ensureAnnotations().OpenWorldHint = BoolPtr(true)
	}
}

// ensureAnnotations lazily initializes the Annotations field.
func (t *Tool) ensureAnnotations() *ToolAnnotations {
	if t.Annotations == nil {
		t.Annotations = &ToolAnnotations{}
	}
	return t.Annotations
}

// BoolPtr returns a pointer to the given bool value.
// Exported so callers can build ToolAnnotations literals without a local helper.
func BoolPtr(v bool) *bool { return &v }
