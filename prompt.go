package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// PromptHandler generates prompt messages for the given arguments.
// It receives the context and a map of argument values, and returns
// a slice of PromptMessage items.
type PromptHandler func(ctx context.Context, args map[string]string) ([]PromptMessage, error)

// Prompt represents a registered MCP prompt template.
type Prompt struct {
	// Name is the unique identifier for this prompt.
	Name string

	// Description is an optional human-readable description.
	Description string

	// Arguments defines the expected parameters for this prompt.
	Arguments []PromptArgument

	// Handler generates the prompt messages.
	Handler PromptHandler

	// Completer provides argument auto-completions for this prompt.
	// When nil, the server returns an empty completion result.
	Completer CompleteHandler
}

// PromptArgument describes a single argument accepted by a prompt.
type PromptArgument struct {
	// Name is the argument identifier.
	Name string `json:"name"`

	// Description is an optional human-readable description.
	Description string `json:"description,omitempty"`

	// Required indicates whether this argument must be provided.
	Required bool `json:"required,omitempty"`
}

// PromptOption is a functional option for configuring a Prompt.
type PromptOption func(*Prompt)

// WithPromptDescription sets the prompt's human-readable description.
func WithPromptDescription(desc string) PromptOption {
	return func(p *Prompt) { p.Description = desc }
}

// WithPromptArguments sets the prompt's expected arguments.
// The provided slice is copied to avoid aliasing with the caller's data.
func WithPromptArguments(args ...PromptArgument) PromptOption {
	return func(p *Prompt) { p.Arguments = append([]PromptArgument(nil), args...) }
}

// NewPrompt creates a new Prompt with the given name, handler, and options.
func NewPrompt(name string, handler PromptHandler, opts ...PromptOption) (*Prompt, error) {
	if name == "" {
		return nil, errPromptNameEmpty
	}
	if handler == nil {
		return nil, errPromptHandlerNil
	}

	p := &Prompt{
		Name:    name,
		Handler: handler,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// ── Prompt messages ─────────────────────────────────────────────────

// PromptMessage represents a single message in a prompt's output.
type PromptMessage struct {
	// Role is the speaker role: "user" or "assistant".
	Role string `json:"role"`

	// Content is the message content.
	Content Content `json:"content"`
}

// maxPromptMessageSize is the maximum byte size of a single PromptMessage
// JSON payload accepted by UnmarshalJSON.
const maxPromptMessageSize = 10 * 1024 * 1024 // 10 MB

// UnmarshalJSON decodes a PromptMessage, handling the Content interface field.
func (m *PromptMessage) UnmarshalJSON(data []byte) error {
	if len(data) > maxPromptMessageSize {
		return fmt.Errorf("invalid message")
	}
	var raw struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid message")
	}
	m.Role = raw.Role
	c, err := DecodeContent(raw.Content)
	if err != nil {
		return fmt.Errorf("invalid message")
	}
	m.Content = c
	return nil
}

// NewUserMessage creates a PromptMessage with role "user" and text content.
func NewUserMessage(text string) PromptMessage {
	return PromptMessage{Role: "user", Content: TextContent{Text: text}}
}

// NewAssistantMessage creates a PromptMessage with role "assistant" and text content.
func NewAssistantMessage(text string) PromptMessage {
	return PromptMessage{Role: "assistant", Content: TextContent{Text: text}}
}

// ── Wire types ──────────────────────────────────────────────────────

// PromptInfo is the wire representation of a prompt in a prompts/list response.
type PromptInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// ListPromptsResult is the server's response to a "prompts/list" request.
type ListPromptsResult struct {
	Prompts    []PromptInfo   `json:"prompts"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// GetPromptParams is the client's payload for a "prompts/get" request.
type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
	Meta      map[string]any    `json:"_meta,omitempty"`
}

// GetPromptResult is the server's response to a "prompts/get" request.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
	Meta        map[string]any  `json:"_meta,omitempty"`
}
