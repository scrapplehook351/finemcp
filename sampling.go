package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ── Sampling handler types ──────────────────────────────────────────

// RequestSender is a function that transports implement to send a JSON-RPC
// request from the server to the client and wait for the client's response.
// This enables server-initiated requests such as sampling/createMessage.
//
// The transport must:
//  1. Assign an "id" to the outgoing request.
//  2. Write the request to the client.
//  3. Block until the client sends a JSON-RPC response with the matching "id".
//  4. Return the response, or an error if the context expires / transport fails.
//
// The context controls the deadline for the round-trip. Implementations
// should select on ctx.Done() while waiting for the client response.
type RequestSender func(ctx context.Context, method string, params any) (*JSONRPCResponse, error)

// ── Sampling wire types ─────────────────────────────────────────────

// SamplingMessage represents a single message in a sampling request or
// response. It mirrors the MCP SamplingMessage schema.
type SamplingMessage struct {
	// Role is the speaker role: "user" or "assistant".
	Role string `json:"role"`

	// Content is the message content (text, image, or audio).
	Content Content `json:"content"`
}

// MarshalJSON marshals a SamplingMessage, delegating content serialization
// to the Content interface implementation.
func (m SamplingMessage) MarshalJSON() ([]byte, error) {
	type alias struct {
		Role    string  `json:"role"`
		Content Content `json:"content"`
	}
	return json.Marshal(alias(m))
}

// maxSamplingMessageSize is the maximum byte size of a single SamplingMessage
// JSON payload accepted by UnmarshalJSON.
const maxSamplingMessageSize = 10 * 1024 * 1024 // 10 MB

// UnmarshalJSON decodes a SamplingMessage, using DecodeContent for the
// polymorphic Content field.
func (m *SamplingMessage) UnmarshalJSON(data []byte) error {
	if len(data) > maxSamplingMessageSize {
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

// ModelPreferences expresses the server's model selection preferences.
// Clients use these as hints when choosing which model to sample with.
type ModelPreferences struct {
	// Hints is an ordered list of model name hints (most preferred first).
	// Each hint may contain a partial or full model name.
	Hints []ModelHint `json:"hints,omitempty"`

	// CostPriority indicates the relative importance of low cost (0–1).
	// A nil value means unset; 0.0 is a valid explicit preference.
	CostPriority *float64 `json:"costPriority,omitempty"`

	// SpeedPriority indicates the relative importance of low latency (0–1).
	// A nil value means unset; 0.0 is a valid explicit preference.
	SpeedPriority *float64 `json:"speedPriority,omitempty"`

	// IntelligencePriority indicates the relative importance of high
	// capability (0–1). A nil value means unset; 0.0 is a valid explicit preference.
	IntelligencePriority *float64 `json:"intelligencePriority,omitempty"`
}

// ModelHint is a partial or full model name hint for model selection.
type ModelHint struct {
	// Name is a substring or full model identifier (e.g. "claude-4-sonnet").
	Name string `json:"name,omitempty"`
}

// CreateMessageParams is the server's payload for a "sampling/createMessage"
// request sent to the client.
type CreateMessageParams struct {
	// Messages is the conversation history to send to the LLM.
	Messages []SamplingMessage `json:"messages"`

	// ModelPreferences expresses optional model selection preferences.
	ModelPreferences *ModelPreferences `json:"modelPreferences,omitempty"`

	// SystemPrompt is an optional system prompt to prepend.
	SystemPrompt string `json:"systemPrompt,omitempty"`

	// IncludeContext controls how much MCP context the client should
	// include. Valid values: "none", "thisServer", "allServers".
	IncludeContext string `json:"includeContext,omitempty"`

	// Temperature controls randomness (0–1). Optional.
	Temperature *float64 `json:"temperature,omitempty"`

	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"maxTokens"`

	// StopSequences is an optional list of sequences that stop generation.
	StopSequences []string `json:"stopSequences,omitempty"`

	// Metadata is additional provider-specific parameters.
	Metadata map[string]any `json:"metadata,omitempty"`

	// Meta holds MCP request metadata such as a progress token.
	// Clients use this to correlate progress notifications with the request.
	Meta map[string]any `json:"_meta,omitempty"`
}

// CreateMessageResult is the client's response to a "sampling/createMessage"
// request.
type CreateMessageResult struct {
	// Role is the role of the generated message ("assistant").
	Role string `json:"role"`

	// Content is the generated content (text, image, etc.).
	Content json.RawMessage `json:"content"`

	// Model is the identifier of the model that was used.
	Model string `json:"model"`

	// StopReason indicates why generation stopped.
	// Common values: "endTurn", "stopSequence", "maxTokens".
	StopReason string `json:"stopReason,omitempty"`
}

// ── Sampling method constant ────────────────────────────────────────

const methodSamplingCreateMessage = "sampling/createMessage"

// ── Errors ──────────────────────────────────────────────────────────

var (
	errSamplingNotSupported = errors.New("client does not support sampling")
	errNoRequestSender      = errors.New("transport does not support server-to-client requests")
	errNotInitializedYet    = errors.New("server not initialized")
)

// ── Server method ───────────────────────────────────────────────────

// CreateMessage sends a sampling/createMessage request to the client and
// blocks until the client responds with an LLM-generated message.
//
// This is a server-initiated request: the server asks the client to perform
// LLM inference on its behalf. The client must have declared sampling support
// in its capabilities during initialization.
//
// The context controls the deadline for the entire round-trip. Tool handlers
// call this method when they need the LLM to make a decision mid-execution.
//
// Prerequisites:
//   - The server must be initialized (initialize handshake completed).
//   - The client must have declared sampling capability (ClientCaps.Sampling != nil).
//   - The transport must provide a RequestSender via context.
//
// Like other user-facing methods, CreateMessage is not wrapped with panic
// recovery by default. For production deployments, use a recovery middleware.

// maxSamplingMessages is the maximum number of messages allowed in a
// single CreateMessageParams request to prevent memory exhaustion.
const maxSamplingMessages = 1000

// CreateMessage sends a sampling/createMessage request to the client and
// blocks until the client responds with an LLM-generated message.
//
// This is a server-initiated request: the server asks the client to perform
// LLM inference on its behalf. The client must have declared sampling support
// in its capabilities during initialization.
//
// The context controls the deadline for the entire round-trip. Tool handlers
// call this method when they need the LLM to make a decision mid-execution.
//
// Prerequisites:
//   - The server must be initialized (initialize handshake completed).
//   - The client must have declared sampling capability (ClientCaps.Sampling != nil).
//   - The transport must provide a RequestSender via context.
//
// Like other user-facing methods, CreateMessage is not wrapped with panic
// recovery by default. For production deployments, use a recovery middleware.
func (s *Server) CreateMessage(ctx context.Context, params CreateMessageParams) (*CreateMessageResult, error) {
	if !s.initialized.Load() {
		return nil, errNotInitializedYet
	}

	s.mu.RLock()
	hasSampling := s.clientCaps.Sampling != nil
	s.mu.RUnlock()

	if !hasSampling {
		return nil, errSamplingNotSupported
	}

	sender := RequestSenderFromCtx(ctx)
	if sender == nil {
		return nil, errNoRequestSender
	}

	// Validate params.
	if len(params.Messages) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	if len(params.Messages) > maxSamplingMessages {
		return nil, fmt.Errorf("too many messages (max %d)", maxSamplingMessages)
	}
	if params.MaxTokens <= 0 {
		return nil, errors.New("maxTokens must be positive")
	}
	if c := params.IncludeContext; c != "" && c != "none" && c != "thisServer" && c != "allServers" {
		return nil, fmt.Errorf("invalid includeContext %q: must be none, thisServer, or allServers", c)
	}

	resp, err := sender(ctx, methodSamplingCreateMessage, params)
	if err != nil {
		return nil, fmt.Errorf("sampling request failed: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("client returned error: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	// Decode the result.
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	var result CreateMessageResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to decode sampling result: %w", err)
	}

	return &result, nil
}
