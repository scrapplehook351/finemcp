package middleware

import "testing"

func TestShouldProcess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tool    string
		include map[string]struct{}
		exclude map[string]struct{}
		want    bool
	}{
		{"no filters", "any", nil, nil, true},
		{"included", "x", map[string]struct{}{"x": {}}, nil, true},
		{"not included", "y", map[string]struct{}{"x": {}}, nil, false},
		{"excluded", "x", nil, map[string]struct{}{"x": {}}, false},
		{"not excluded", "y", nil, map[string]struct{}{"x": {}}, true},
		{"include overrides exclude", "x", map[string]struct{}{"x": {}}, map[string]struct{}{"x": {}}, true},
		{"empty tool name, no filters", "", nil, nil, true},
		{"empty tool name, included empty", "", map[string]struct{}{"": {}}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldProcess(tt.tool, tt.include, tt.exclude)
			if got != tt.want {
				t.Errorf("shouldProcess(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}

func TestFormatPanicValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"string value", "boom", "boom"},
		{"error value", errForTest("oops"), "oops"},
		{"int value", 42, "unknown panic"},
		{"nil value", nil, "unknown panic"},
		{"struct value", struct{ X int }{1}, "unknown panic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatPanicValue(tt.input)
			if got != tt.want {
				t.Errorf("formatPanicValue(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// errForTest implements the error interface for testing.
type errForTest string

func (e errForTest) Error() string { return string(e) }
