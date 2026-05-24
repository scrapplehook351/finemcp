package finemcp

import "context"

// ── Completion handler type ─────────────────────────────────────────

// CompleteHandler provides argument auto-completions for a prompt or
// resource template. The server invokes the handler when a client sends
// a completion/complete request, passing the reference to the prompt or
// template and the partial argument value typed so far.
//
// Implementations should return a CompletionResult with matching values.
// Returning nil is equivalent to returning an empty result (no suggestions).
// The server normalizes nil Values to an empty slice before serialization,
// so handlers may safely return a nil slice when there are no matches.
//
// The server enforces the MCP-recommended limit of 100 values per response.
// If Values contains more than 100 items, the server truncates the slice to
// 100 entries and sets HasMore to true automatically. Total is preserved if
// the handler set it; otherwise it is set to the original (pre-truncation)
// length.
//
// Like other user-provided handlers, a CompleteHandler is not wrapped with
// panic recovery by default. For production deployments, use a recovery
// middleware to prevent a panicking completer from crashing the server.
type CompleteHandler func(ctx context.Context, params CompleteRequest) (*CompletionResult, error)

// ── Wire types ──────────────────────────────────────────────────────

// CompleteParams is the client's payload for a "completion/complete" request.
type CompleteParams struct {
	Ref      CompletionRef      `json:"ref"`
	Argument CompletionArgument `json:"argument"`
	Meta     map[string]any     `json:"_meta,omitempty"`
}

// CompletionRef identifies the prompt or resource template being completed.
type CompletionRef struct {
	// Type is "ref/prompt" or "ref/resource".
	Type string `json:"type"`

	// Name is the prompt name (for ref/prompt) or the resource URI template
	// string (for ref/resource).
	Name string `json:"name,omitempty"`

	// URI is the resource template URI (for ref/resource).
	// Some clients use "uri" instead of "name" for resource references.
	URI string `json:"uri,omitempty"`
}

// CompletionArgument holds the argument name and partial value for which
// the client is requesting completions.
type CompletionArgument struct {
	// Name is the argument identifier.
	Name string `json:"name"`

	// Value is the partial value typed so far.
	Value string `json:"value"`
}

// CompleteRequest is the input passed to a CompleteHandler.
// It wraps the reference and argument from the client's request.
type CompleteRequest struct {
	// Ref identifies the prompt or resource template being completed.
	Ref CompletionRef

	// Argument holds the argument name and partial value.
	Argument CompletionArgument
}

// maxCompletionValues is the MCP-recommended upper bound on the number of
// completion values per response. The server enforces this limit by
// truncating Values and setting HasMore when the handler returns more.
const maxCompletionValues = 100

// CompletionResult holds the auto-completion suggestions returned by a
// CompleteHandler.
type CompletionResult struct {
	// Values is the list of completion suggestions. The server enforces the
	// MCP-recommended limit of 100 items: if the handler returns more, the
	// slice is truncated and HasMore is set to true automatically.
	Values []string `json:"values"`

	// Total is the total number of available completions (optional).
	// When set, clients can display "showing X of Y" to the user.
	// If the server truncates Values, Total is preserved when the handler
	// set it explicitly; otherwise it is set to the original length.
	Total int `json:"total,omitempty"`

	// HasMore indicates that additional completions exist beyond the
	// returned values.
	HasMore bool `json:"hasMore,omitempty"`
}

// CompleteResult is the server's response to a "completion/complete" request.
type CompleteResult struct {
	Completion CompletionResult `json:"completion"`
	Meta       map[string]any   `json:"_meta,omitempty"`
}

// Ref type constants.
const (
	RefTypePrompt   = "ref/prompt"
	RefTypeResource = "ref/resource"
)

// ── Completion options for Prompt and ResourceTemplate ───────────────

// WithCompleter attaches a CompleteHandler to a Prompt.
// When a client sends completion/complete for this prompt,
// the handler is invoked with the argument details.
func WithCompleter(handler CompleteHandler) PromptOption {
	return func(p *Prompt) { p.Completer = handler }
}

// WithTemplateCompleter attaches a CompleteHandler to a ResourceTemplate.
// When a client sends completion/complete for this template,
// the handler is invoked with the argument details.
func WithTemplateCompleter(handler CompleteHandler) ResourceTemplateOption {
	return func(t *ResourceTemplate) { t.Completer = handler }
}
