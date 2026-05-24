package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const methodElicitationCreate = "elicitation/create"

var errElicitationNotSupported = errors.New("client does not support elicitation")

// ElicitationParams is the server's payload for an "elicitation/create" request
// sent to the client.
type ElicitationParams struct {
	// Prompt is the message to display to the user.
	Prompt string `json:"prompt"`

	// Type indicates the expected response type: "text", "secret", "file", etc.
	Type string `json:"type,omitempty"`

	// Default is an optional default value to present to the user.
	Default string `json:"default,omitempty"`

	// Meta holds MCP request metadata such as a progress token.
	Meta map[string]any `json:"_meta,omitempty"`
}

// ElicitationResult is the client's response to an "elicitation/create" request.
type ElicitationResult struct {
	// Value is the user's input.
	Value string `json:"value"`

	// Cancelled indicates whether the user cancelled the prompt.
	Cancelled bool `json:"cancelled,omitempty"`
}

// ElicitUser sends an elicitation/create request to the client and blocks
// until the user responds with input.
//
// This is a server-initiated request: the server asks the client to prompt
// the user for input on its behalf. The client must have declared elicitation
// support in its capabilities during initialization.
//
// The context controls the deadline for the entire round-trip.
//
// Prerequisites:
//   - The server must be initialized (initialize handshake completed).
//   - The client must have declared elicitation capability (ClientCaps.Elicitation != nil).
//   - The transport must provide a RequestSender via context.
func (s *Server) ElicitUser(ctx context.Context, params ElicitationParams) (*ElicitationResult, error) {
	if !s.initialized.Load() {
		return nil, errNotInitializedYet
	}

	s.mu.RLock()
	hasElicitation := s.clientCaps.Elicitation != nil
	s.mu.RUnlock()

	if !hasElicitation {
		return nil, errElicitationNotSupported
	}

	sender := RequestSenderFromCtx(ctx)
	if sender == nil {
		return nil, errNoRequestSender
	}

	// Validate params
	if params.Prompt == "" {
		return nil, errors.New("prompt must not be empty")
	}

	resp, err := sender(ctx, methodElicitationCreate, params)
	if err != nil {
		return nil, fmt.Errorf("elicitation request failed: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("client returned error: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}

	// Decode the result
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	var result ElicitationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to decode elicitation result: %w", err)
	}

	return &result, nil
}
