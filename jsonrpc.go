package finemcp

import (
	"bytes"
	"encoding/json"
)

const jsonrpcVersion = "2.0"

// Standard JSON-RPC 2.0 error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// Application-level JSON-RPC error codes (reserved range: -32000 to -32099).
const (
	// ErrCodeUnauthorized indicates that the request lacks valid authentication
	// credentials. Returned when an AuthChecker is configured and the request
	// context does not contain a verified identity.
	ErrCodeUnauthorized = -32001

	// ErrCodeTenantRequired indicates that tenant identification could not be
	// resolved for the request. Returned when a TenantResolver is configured
	// and the tenant ID is missing, invalid, or not found in the store.
	// The error message is intentionally generic ("tenant identification required")
	// to prevent enumeration of valid tenant IDs.
	ErrCodeTenantRequired = -32002
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 request or notification.
// A notification has no "id" field; use IsNotification() to distinguish.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"-"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	hasID   bool
}

// IsNotification reports whether this message is a notification (no "id" field).
func (r *JSONRPCRequest) IsNotification() bool {
	return !r.hasID
}

// UnmarshalJSON implements custom unmarshaling to detect whether "id" is present.
// JSON-RPC 2.0 distinguishes between absent id (notification) and null id (valid request).
func (r *JSONRPCRequest) UnmarshalJSON(data []byte) error {
	// Use a single-pass struct with json.RawMessage for the id field.
	// This avoids the previous approach of parsing the data twice
	// (once for fields, once for a raw map to detect id presence).
	type alias struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}

	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}

	r.JSONRPC = a.JSONRPC
	r.Method = a.Method
	r.Params = a.Params

	// json.RawMessage is nil when the field is absent from JSON,
	// but []byte("null") when present with value null — exactly
	// the distinction JSON-RPC 2.0 requires.
	r.hasID = a.ID != nil

	if r.hasID {
		// Decode id with UseNumber so numeric IDs are preserved as
		// json.Number, avoiding float64 precision loss for large values.
		dec := json.NewDecoder(bytes.NewReader(a.ID))
		dec.UseNumber()
		if err := dec.Decode(&r.ID); err != nil {
			return err
		}
	}

	return nil
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
// A response has either Result or Error, never both.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface for JSONRPCError.
func (e *JSONRPCError) Error() string {
	return e.Message
}

// NewResponse creates a success response with the given id and result.
func NewResponse(id any, result any) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Result:  result,
	}
}

// NewErrorResponse creates an error response with the given id, code, and message.
func NewErrorResponse(id any, code int, msg string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
	}
}
