package finemcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func stubHandler(_ context.Context, _ []byte) ([]byte, error) {
	return nil, nil
}

func TestToolName_FailedValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		wantErr  error
	}{
		{
			name:     "empty name",
			toolName: "",
			wantErr:  errToolNameEmpty,
		},
		{
			name:     "too long name",
			toolName: string(make([]byte, 129)),
			wantErr:  errToolNameTooLong,
		},
		{
			name:     "contains space",
			toolName: "has space",
			wantErr:  errToolNameChars,
		},
		{
			name:     "contains comma",
			toolName: "has,comma",
			wantErr:  errToolNameChars,
		},
		{
			name:     "contains slash",
			toolName: "has/slash",
			wantErr:  errToolNameChars,
		},
		{
			name:     "contains @",
			toolName: "has@symbol",
			wantErr:  errToolNameChars,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tool, err := NewTool(tt.toolName, stubHandler)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err != tt.wantErr {
				t.Errorf("expected error %q, got %q", tt.wantErr, err)
			}
			if tool != nil {
				t.Error("expected nil tool on validation failure")
			}
		})
	}
}

func TestNewTool_ValidConstruction(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("get_weather", stubHandler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.Name != "get_weather" {
		t.Errorf("expected name %q, got %q", "get_weather", tool.Name)
	}
	if tool.Handler == nil {
		t.Error("expected handler to be set")
	}
	if tool.Description != "" {
		t.Errorf("expected empty description, got %q", tool.Description)
	}
}

func TestNewTool_NameMaxLength(t *testing.T) {
	t.Parallel()

	name128 := strings.Repeat("a", 128)
	tool, err := NewTool(name128, stubHandler)
	if err != nil {
		t.Fatalf("expected 128-char name to be valid, got error: %v", err)
	}
	if tool.Name != name128 {
		t.Error("expected name to match")
	}
}

func TestWithDescription_SetsValue(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("ping", stubHandler, WithDescription("A simple ping tool"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.Description != "A simple ping tool" {
		t.Errorf("expected description %q, got %q", "A simple ping tool", tool.Description)
	}
}

func TestWithRoles_SetsRoles(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("admin_task", stubHandler, WithRoles("admin", "superuser"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedRoles := []string{"admin", "superuser"}
	if len(tool.Roles) != len(expectedRoles) {
		t.Fatalf("expected %d roles, got %d", len(expectedRoles), len(tool.Roles))
	}
	for i, role := range expectedRoles {
		if tool.Roles[i] != role {
			t.Errorf("expected role %q at index %d, got %q", role, i, tool.Roles[i])
		}
	}
}

func TestWithRoles_FiltersEmptyStrings(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("mixed_roles", stubHandler, WithRoles("user", "", "admin", ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedRoles := []string{"user", "admin"}
	if len(tool.Roles) != len(expectedRoles) {
		t.Fatalf("expected %d roles, got %d", len(expectedRoles), len(tool.Roles))
	}
	for i, role := range expectedRoles {
		if tool.Roles[i] != role {
			t.Errorf("expected role %q at index %d, got %q", role, i, tool.Roles[i])
		}
	}
}

func TestWithRoles_CaseSensitivity(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("case_test", stubHandler, WithRoles("Admin", "admin"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedRoles := []string{"Admin", "admin"}
	if len(tool.Roles) != len(expectedRoles) {
		t.Fatalf("expected %d roles, got %d", len(expectedRoles), len(tool.Roles))
	}
	for i, role := range expectedRoles {
		if tool.Roles[i] != role {
			t.Errorf("expected role %q at index %d, got %q", role, i, tool.Roles[i])
		}
	}
}

func TestWithRoles_DefaultNilRoles(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("no_roles", stubHandler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.Roles != nil {
		t.Errorf("expected nil Roles when not set, got %v", tool.Roles)
	}
}

func TestWithRoles_NoCopyAliasing(t *testing.T) {
	t.Parallel()

	roles := []string{"admin", "user"}
	tool, err := NewTool("alias_test", stubHandler, WithRoles(roles...))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tool.Roles) != len(roles) {
		t.Fatalf("expected %d roles, got %d", len(roles), len(tool.Roles))
	}
	for i, role := range roles {
		if tool.Roles[i] != role {
			t.Errorf("expected role %q at index %d, got %q", role, i, tool.Roles[i])
		}
	}
	// Modify the original slice and ensure it doesn't affect the tool's roles
	roles[0] = "modified"
	if tool.Roles[0] == "modified" {
		t.Errorf("expected tool roles to be independent of the original slice")
	}
}

func TestNewTool_NilHandler(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("nil_handler", nil)
	if err == nil {
		t.Fatal("expected error for nil handler, got nil")
	}

	if tool != nil {
		t.Errorf("expected nil tool for nil handler, got %v", tool)
	}

	if err != errToolHandlerNil {
		t.Errorf("expected error %q, got %q", errToolHandlerNil, err)
	}
}

func TestWithInputSchema_SetsSchema(t *testing.T) {
	t.Parallel()

	schema := struct {
		Field1 string
		Field2 int
	}{}

	tool, err := NewTool("input_schema_test", stubHandler, WithInputSchema(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tool.InputSchema != schema {
		t.Errorf("expected input schema %v, got %v", schema, tool.InputSchema)
	}
}

func TestNewTool_DefaultInputSchemaNil(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("default_schema", stubHandler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tool.InputSchema != nil {
		t.Errorf("expected nil input schema, got %v", tool.InputSchema)
	}
}

func TestWithInputSchema_MapSchema(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}

	tool, err := NewTool("map_schema", stubHandler, WithInputSchema(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(tool.InputSchema, schema) {
		t.Errorf("expected input schema %v, got %v", schema, tool.InputSchema)
	}
}

func TestWithInputSchema_RawJSON(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object"}`)

	tool, err := NewTool("raw_schema", stubHandler, WithInputSchema(schema))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := tool.InputSchema.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", tool.InputSchema)
	}

	if !reflect.DeepEqual(got, schema) {
		t.Errorf("expected %s, got %s", schema, got)
	}
}

func TestWithInputSchema_ExplicitNil(t *testing.T) {
	t.Parallel()

	tool, err := NewTool("nil_schema", stubHandler, WithInputSchema(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tool.InputSchema != nil {
		t.Errorf("expected nil input schema, got %v", tool.InputSchema)
	}
}

func TestWithInputSchema_LastOneWins(t *testing.T) {
	t.Parallel()

	first := map[string]any{"type": "object"}
	second := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}}

	tool, err := NewTool("override_schema", stubHandler, WithInputSchema(first), WithInputSchema(second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(tool.InputSchema, second) {
		t.Errorf("expected last schema to win, got %v", tool.InputSchema)
	}
}

// ── ToolAnnotations ─────────────────────────────────────────────────

func TestNewTool_DefaultAnnotationsNil(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("no_annotations", stubHandler)
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations != nil {
		t.Errorf("expected nil Annotations by default, got %+v", tool.Annotations)
	}
}

func TestWithAnnotations_SetsFullStruct(t *testing.T) {
	t.Parallel()
	ro := true
	tool, err := NewTool("full_anno", stubHandler, WithAnnotations(ToolAnnotations{
		ReadOnlyHint:    &ro,
		IdempotentHint:  BoolPtr(true),
		DestructiveHint: BoolPtr(false),
		Title:           "Full Annotations Test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations == nil {
		t.Fatal("expected Annotations to be set")
	}
	if tool.Annotations.ReadOnlyHint == nil || *tool.Annotations.ReadOnlyHint != true {
		t.Error("ReadOnlyHint not set correctly")
	}
	if tool.Annotations.IdempotentHint == nil || *tool.Annotations.IdempotentHint != true {
		t.Error("IdempotentHint not set correctly")
	}
	if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != false {
		t.Error("DestructiveHint not set correctly (expected explicit false)")
	}
	if tool.Annotations.Title != "Full Annotations Test" {
		t.Errorf("Title = %q, want %q", tool.Annotations.Title, "Full Annotations Test")
	}
}

func TestWithReadOnly_SetsHint(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("readonly_tool", stubHandler, WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations == nil || tool.Annotations.ReadOnlyHint == nil {
		t.Fatal("expected ReadOnlyHint to be set")
	}
	if *tool.Annotations.ReadOnlyHint != true {
		t.Error("ReadOnlyHint should be true")
	}
	// Other hints should remain nil (unknown).
	if tool.Annotations.DestructiveHint != nil {
		t.Error("DestructiveHint should be nil")
	}
	if tool.Annotations.IdempotentHint != nil {
		t.Error("IdempotentHint should be nil")
	}
}

func TestWithDestructive_SetsHint(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("destructive_tool", stubHandler, WithDestructive())
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil {
		t.Fatal("expected DestructiveHint to be set")
	}
	if *tool.Annotations.DestructiveHint != true {
		t.Error("DestructiveHint should be true")
	}
}

func TestWithIdempotent_SetsHint(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("idempotent_tool", stubHandler, WithIdempotent())
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations == nil || tool.Annotations.IdempotentHint == nil {
		t.Fatal("expected IdempotentHint to be set")
	}
	if *tool.Annotations.IdempotentHint != true {
		t.Error("IdempotentHint should be true")
	}
}

func TestWithTitle_SetsAnnotationTitle(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("titled_tool", stubHandler, WithTitle("My Tool"))
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations == nil {
		t.Fatal("expected Annotations to be set")
	}
	if tool.Annotations.Title != "My Tool" {
		t.Errorf("Title = %q, want %q", tool.Annotations.Title, "My Tool")
	}
}

func TestWithOpenWorld_SetsHint(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("open_world_tool", stubHandler, WithOpenWorld())
	if err != nil {
		t.Fatal(err)
	}
	if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil {
		t.Fatal("expected OpenWorldHint to be set")
	}
	if *tool.Annotations.OpenWorldHint != true {
		t.Error("OpenWorldHint should be true")
	}
}

func TestConvenienceOptions_ComposeTogether(t *testing.T) {
	t.Parallel()
	tool, err := NewTool("composed", stubHandler,
		WithReadOnly(),
		WithIdempotent(),
		WithTitle("Composed Tool"),
	)
	if err != nil {
		t.Fatal(err)
	}
	a := tool.Annotations
	if a == nil {
		t.Fatal("expected Annotations to be set")
	}
	if a.ReadOnlyHint == nil || !*a.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true")
	}
	if a.IdempotentHint == nil || !*a.IdempotentHint {
		t.Error("IdempotentHint should be true")
	}
	if a.Title != "Composed Tool" {
		t.Errorf("Title = %q", a.Title)
	}
	// Unset hints remain nil.
	if a.DestructiveHint != nil {
		t.Error("DestructiveHint should be nil")
	}
}

func TestAnnotations_JSONOmitsNilFields(t *testing.T) {
	t.Parallel()
	a := ToolAnnotations{
		ReadOnlyHint: BoolPtr(true),
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"readOnlyHint":true`) {
		t.Errorf("expected readOnlyHint in JSON, got %s", s)
	}
	if strings.Contains(s, "destructiveHint") {
		t.Errorf("nil destructiveHint should be omitted, got %s", s)
	}
	if strings.Contains(s, "idempotentHint") {
		t.Errorf("nil idempotentHint should be omitted, got %s", s)
	}
}

func TestAnnotations_JSONExplicitFalse(t *testing.T) {
	t.Parallel()
	a := ToolAnnotations{
		ReadOnlyHint:    BoolPtr(false),
		DestructiveHint: BoolPtr(true),
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"readOnlyHint":false`) {
		t.Errorf("explicit false should be present, got %s", s)
	}
	if !strings.Contains(s, `"destructiveHint":true`) {
		t.Errorf("destructiveHint=true should be present, got %s", s)
	}
}

func TestToolInfo_AnnotationsInJSON(t *testing.T) {
	t.Parallel()
	info := ToolInfo{
		Name:        "test_tool",
		InputSchema: map[string]string{"type": "object"},
		Annotations: &ToolAnnotations{
			ReadOnlyHint:   BoolPtr(true),
			IdempotentHint: BoolPtr(true),
		},
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"annotations"`) {
		t.Errorf("annotations field missing from ToolInfo JSON: %s", s)
	}
	if !strings.Contains(s, `"readOnlyHint":true`) {
		t.Errorf("readOnlyHint missing: %s", s)
	}
}

func TestToolInfo_NilAnnotationsOmitted(t *testing.T) {
	t.Parallel()
	info := ToolInfo{
		Name:        "no_anno",
		InputSchema: map[string]string{"type": "object"},
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "annotations") {
		t.Errorf("nil annotations should be omitted: %s", data)
	}
}

func TestEnsureAnnotations_NilSafety(t *testing.T) {
	t.Parallel()
	// Calling multiple convenience options on a tool with nil Annotations
	// should not panic; ensureAnnotations must lazily initialize the struct.
	tool, err := NewTool("nil_safe", stubHandler,
		WithReadOnly(),
		WithIdempotent(),
		WithTitle("Safe"),
		WithOpenWorld(),
	)
	if err != nil {
		t.Fatal(err)
	}
	a := tool.Annotations
	if a == nil {
		t.Fatal("expected Annotations to be initialized")
	}
	if a.ReadOnlyHint == nil || !*a.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true")
	}
	if a.IdempotentHint == nil || !*a.IdempotentHint {
		t.Error("IdempotentHint should be true")
	}
	if a.Title != "Safe" {
		t.Errorf("Title = %q, want %q", a.Title, "Safe")
	}
	if a.OpenWorldHint == nil || !*a.OpenWorldHint {
		t.Error("OpenWorldHint should be true")
	}
	// DestructiveHint was never set — must remain nil.
	if a.DestructiveHint != nil {
		t.Error("DestructiveHint should be nil")
	}
}
