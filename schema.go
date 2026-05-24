package finemcp

import (
	"encoding/json"
	"reflect"
	"strings"
)

// rawMessageType is cached for fast comparison in type switches.
var rawMessageType = reflect.TypeOf(json.RawMessage{})

// maxSchemaDepth limits nesting depth during schema generation to prevent
// stack overflow on self-referential types (e.g. type Node struct { Next *Node }).
const maxSchemaDepth = 20

// GenerateSchema produces a JSON Schema (as map[string]any) from a Go reflect.Type.
//
// For struct types it generates an "object" schema with properties derived from
// exported fields. Struct tags control the output:
//
//   - json:"name"          → property name
//   - json:",omitempty"    → field is optional (not in "required")
//   - json:"-"             → field is skipped
//   - description:"..."    → sets the property's "description"
//
// Pointer fields are treated as optional. Nested structs, slices, and maps
// are recursively expanded.
//
// For non-struct types (e.g. string, int) it returns the corresponding
// primitive JSON Schema.
func GenerateSchema(t reflect.Type) map[string]any {
	// Unwrap pointer at the top level.
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	seen := make(map[reflect.Type]bool)

	if t.Kind() != reflect.Struct {
		return schemaForType(t, seen, 0)
	}

	return schemaForStruct(t, seen, 0)
}

// schemaForStruct builds a JSON Schema "object" from a struct type.
func schemaForStruct(t reflect.Type, seen map[reflect.Type]bool, depth int) map[string]any {
	if depth >= maxSchemaDepth || seen[t] {
		return map[string]any{"type": "object"}
	}
	seen[t] = true
	defer delete(seen, t)

	properties := make(map[string]any, t.NumField())
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// Skip unexported fields.
		if !field.IsExported() {
			continue
		}

		name, opts, skip := parseJSONTag(field)
		if skip {
			continue
		}

		fieldType := field.Type
		isPointer := fieldType.Kind() == reflect.Ptr
		if isPointer {
			fieldType = fieldType.Elem()
		}

		propSchema := schemaForType(fieldType, seen, depth+1)

		// Add description from struct tag.
		if desc := field.Tag.Get("description"); desc != "" {
			propSchema["description"] = desc
		}

		properties[name] = propSchema

		// A field is required unless:
		// - it has omitempty in the json tag
		// - it is a pointer type
		if !opts.omitempty && !isPointer {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	return schema
}

// schemaForType maps a reflect.Type to its JSON Schema representation.
func schemaForType(t reflect.Type, seen map[reflect.Type]bool, depth int) map[string]any {
	if depth >= maxSchemaDepth {
		return map[string]any{}
	}

	// Handle special types first.
	if t == rawMessageType {
		return map[string]any{} // any
	}

	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}

	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}

	case reflect.Bool:
		return map[string]any{"type": "boolean"}

	case reflect.Slice:
		// []byte is json.RawMessage-like — treat as any.
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{}
		}
		return map[string]any{
			"type":  "array",
			"items": schemaForType(t.Elem(), seen, depth+1),
		}

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return map[string]any{"type": "object"}
		}
		elemSchema := schemaForType(t.Elem(), seen, depth+1)
		schema := map[string]any{"type": "object"}
		// Only add additionalProperties if the value type is concrete (not any).
		if len(elemSchema) > 0 {
			schema["additionalProperties"] = elemSchema
		}
		return schema

	case reflect.Struct:
		return schemaForStruct(t, seen, depth+1)

	case reflect.Ptr:
		return schemaForType(t.Elem(), seen, depth+1)

	case reflect.Interface:
		return map[string]any{} // any

	default:
		return map[string]any{}
	}
}

// jsonTagOpts holds parsed options from a json struct tag.
type jsonTagOpts struct {
	omitempty bool
}

// parseJSONTag extracts the property name, options, and skip flag from a struct field's json tag.
func parseJSONTag(field reflect.StructField) (name string, opts jsonTagOpts, skip bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name, jsonTagOpts{}, false
	}

	if tag == "-" {
		return "", jsonTagOpts{}, true
	}

	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = field.Name
	}

	for _, p := range parts[1:] {
		if p == "omitempty" {
			opts.omitempty = true
		}
	}

	return name, opts, false
}
