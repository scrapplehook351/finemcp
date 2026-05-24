package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// TypedHandler is a tool handler with strongly typed input and output.
// The framework automatically deserializes JSON input into In and serializes
// Out back to JSON, eliminating manual json.Unmarshal/Marshal boilerplate.
//
// Example:
//
//	func greet(_ context.Context, in GreetInput) (string, error) {
//	    return fmt.Sprintf("Hello, %s!", in.Name), nil
//	}
type TypedHandler[In any, Out any] func(ctx context.Context, input In) (Out, error)

// NewTypedTool creates a tool with a strongly typed handler.
// Input is automatically deserialized from JSON; output is serialized to JSON.
// A JSON Schema is auto-generated from the In type's struct fields and tags.
//
// This is the recommended way to create tools — it provides compile-time
// type safety, removes JSON boilerplate, and auto-documents the input schema.
//
// The auto-generated schema uses struct tags to control output:
//   - json:"name"          → property name
//   - json:",omitempty"    → marks the field as optional
//   - json:"-"             → skips the field
//   - description:"..."    → adds a "description" to the property
//
// If WithInputSchema is also passed, it overrides the auto-generated schema.
//
// Usage:
//
//	type GreetInput struct {
//	    Name string `json:"name" description:"Name to greet"`
//	}
//
//	tool, err := finemcp.NewTypedTool("greet",
//	    func(_ context.Context, in GreetInput) (string, error) {
//	        return fmt.Sprintf("Hello, %s!", in.Name), nil
//	    },
//	    finemcp.WithDescription("Greets someone"),
//	)
func NewTypedTool[In any, Out any](name string, handler TypedHandler[In, Out], opts ...ToolOption) (*Tool, error) {
	if handler == nil {
		return nil, errToolHandlerNil
	}

	// Wrap the typed handler into a raw ToolHandler.
	rawHandler := func(ctx context.Context, input []byte) ([]byte, error) {
		var in In
		if len(input) > 0 {
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
		}

		out, err := handler(ctx, in)
		if err != nil {
			return nil, err
		}

		return json.Marshal(out)
	}

	return NewTool(name, rawHandler, append([]ToolOption{withAutoSchema[In]()}, opts...)...)
}

// withAutoSchema returns a ToolOption that auto-generates a JSON Schema from the
// generic type parameter In. It is prepended to the option list so that an explicit
// WithInputSchema() call by the user will override the auto-generated schema.
func withAutoSchema[In any]() ToolOption {
	return func(t *Tool) {
		t.InputSchema = GenerateSchema(reflect.TypeOf((*In)(nil)).Elem())
	}
}
