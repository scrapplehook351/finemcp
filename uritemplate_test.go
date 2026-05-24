package finemcp

import (
	"strings"
	"testing"
)

// ── uriTemplateMatches ──────────────────────────────────────────────

func TestUriTemplateMatches(t *testing.T) {
	tests := []struct {
		name     string
		template string
		uri      string
		want     bool
	}{
		// ── Basic matching ──────────────────────────────────────────
		{"single placeholder mid-path", "file:///logs/{date}.log", "file:///logs/2025-01-01.log", true},
		{"single placeholder full segment", "/users/{id}", "/users/alice", true},
		{"multiple placeholders", "/repos/{owner}/{repo}", "/repos/alice/myapp", true},
		{"three placeholders", "/repos/{owner}/{repo}/issues/{id}", "/repos/alice/myapp/issues/42", true},
		{"placeholder at start", "{scheme}://host", "https://host", true},
		{"placeholder at end", "/files/{name}", "/files/readme.md", true},
		{"reserved expansion +var", "/path/{+var}", "/path/hello", true},
		{"exact literal no placeholders", "/exact/path", "/exact/path", true},

		// ── Non-matching ────────────────────────────────────────────
		{"different literal prefix", "/logs/{date}", "/data/2025-01-01", false},
		{"different literal suffix", "/logs/{date}.log", "/logs/2025-01-01.txt", false},
		{"extra trailing segment", "/users/{id}", "/users/alice/extra", false},
		{"missing segment", "/a/{b}/c", "/a/c", false},
		{"empty URI vs placeholder", "/{x}", "/", false},
		{"empty URI vs template", "/a", "", false},
		{"empty both", "", "", true},
		{"template longer than URI", "/a/b/c", "/a/b", false},
		{"URI longer than template", "/a", "/a/b", false},

		// ── Slash boundary ──────────────────────────────────────────
		{"slash blocks wildcard", "/x/{id}/y", "/x/a/b/y", false},
		{"trailing slash match", "/{a}/", "/x/", true},
		{"trailing slash mismatch", "/{a}/", "/x", false},
		{"adjacent segments", "/{a}/{b}", "/x/y", true},
		{"adjacent segments slash blocked", "/{a}/{b}", "/x/y/z", false},

		// ── Consecutive placeholders (no separator) ─────────────────
		{"consecutive placeholders", "/{a}{b}", "/xy", true},
		{"consecutive placeholders 3-char URI", "/{a}{b}", "/xyz", true},
		{"consecutive placeholders 1-char URI fails", "/{a}{b}", "/x", false},

		// ── Reserved expansion (+var) slash handling ─────────────────
		// NOTE: RFC 6570 defines {+var} as allowing '/' in values, but our
		// conservative implementation intentionally treats {+var} identically
		// to {var} — wildcards never cross path separators.
		{"+var does not cross slash (intentional deviation from RFC 6570)",
			"/path/{+var}", "/path/a/b", false},

		// ── Malformed templates ─────────────────────────────────────
		{"unclosed brace", "/logs/{date", "/logs/2025", false},
		{"empty placeholder {}", "/logs/{}.log", "/logs/x.log", false},
		{"whitespace-only placeholder", "/{ }", "/x", false},
		{"plus-only placeholder", "/logs/{+}", "/logs/x", false},

		// ── Special characters in literals ───────────────────────────
		{"query literal", "/search?q={q}", "/search?q=hello", true},
		{"at sign in literal", "/user@{host}", "/user@example.com", true},
		{"percent in URI", "/files/{name}", "/files/my%20file", true},
		{"hash in literal", "/page#{section}", "/page#intro", true},

		// ── Unicode ─────────────────────────────────────────────────
		{"unicode in literal", "/données/{id}", "/données/42", true},
		{"unicode in wildcard value", "/files/{name}", "/files/日本語", true},

		// ── Edge cases ──────────────────────────────────────────────
		{"only a placeholder", "{id}", "42", true},
		{"placeholder must consume at least one char", "{id}", "", false},
		{"long URI with many segments", "/a/{b}/c/{d}/e/{f}", "/a/1/c/2/e/3", true},
		{"long URI mismatch in last segment", "/a/{b}/c/{d}/e/{f}", "/a/1/c/2/e/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uriTemplateMatches(tt.template, tt.uri)
			if got != tt.want {
				t.Errorf("uriTemplateMatches(%q, %q) = %v, want %v",
					tt.template, tt.uri, got, tt.want)
			}
		})
	}
}

func TestUriTemplateMatches_BacktrackBudget(t *testing.T) {
	// Pathological input: many consecutive placeholders with a non-matching
	// trailing literal. Without a backtrack budget, this would take
	// exponential time. With the budget, it should return false quickly.
	tmpl := "/{a}{b}{c}{d}{e}{f}{g}{h}{i}{j}Z"
	uri := "/" + strings.Repeat("x", 100)

	got := uriTemplateMatches(tmpl, uri)
	if got {
		t.Error("expected false for pathological non-matching input")
	}
}

// ── isValidURITemplate ──────────────────────────────────────────────

func TestIsValidURITemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     bool
	}{
		// Valid templates.
		{"simple placeholder", "/logs/{date}.log", true},
		{"multiple placeholders", "/repos/{owner}/{repo}", true},
		{"reserved expansion", "/path/{+var}", true},
		{"no placeholders", "/exact/path", true},
		{"empty string", "", true},
		{"dotted variable name", "/path/{var.name}", true},
		{"underscore variable name", "/path/{my_var}", true},

		// Malformed braces.
		{"unclosed brace", "/logs/{date", false},
		{"unmatched close", "/logs/date}", false},
		{"nested braces", "/logs/{{date}}", false},
		{"empty placeholder", "/logs/{}", false},
		{"whitespace-only placeholder", "/logs/{ }", false},
		{"plus-only", "/logs/{+}", false},

		// Unsupported RFC 6570 operators — must be rejected.
		{"fragment operator", "/page/{#section}", false},
		{"query operator", "/search{?query}", false},
		{"path operator", "/files{/path}", false},
		{"explode operator", "/items/{var*}", false},
		{"prefix operator", "/items/{var:3}", false},
		{"label operator", "/items/{.ext}", false},
		{"param operator", "/items/{;param}", false},
		{"form continuation", "/items/{&more}", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidURITemplate(tt.template)
			if got != tt.want {
				t.Errorf("isValidURITemplate(%q) = %v, want %v",
					tt.template, got, tt.want)
			}
		})
	}
}

// ── Fuzz ────────────────────────────────────────────────────────────

func FuzzUriTemplateMatches(f *testing.F) {
	f.Add("file:///logs/{date}.log", "file:///logs/2025-01-01.log")
	f.Add("{a}{b}", "xy")
	f.Add("/{a}/{b}/{c}", "/x/y/z")
	f.Add("", "")
	f.Add("{x}", "")
	f.Fuzz(func(t *testing.T, tmpl, uri string) {
		// Must not panic on any input.
		uriTemplateMatches(tmpl, uri)
	})
}

// ── Benchmarks ──────────────────────────────────────────────────────

func BenchmarkUriTemplateMatches_Simple(b *testing.B) {
	tmpl := "file:///logs/{date}.log"
	uri := "file:///logs/2025-01-01.log"
	for i := 0; i < b.N; i++ {
		uriTemplateMatches(tmpl, uri)
	}
}

func BenchmarkUriTemplateMatches_MultiSegment(b *testing.B) {
	tmpl := "https://api.example.com/repos/{owner}/{repo}/issues/{id}"
	uri := "https://api.example.com/repos/alice/myapp/issues/42"
	for i := 0; i < b.N; i++ {
		uriTemplateMatches(tmpl, uri)
	}
}

func BenchmarkUriTemplateMatches_Consecutive(b *testing.B) {
	// Worst case for backtracking: many consecutive placeholders.
	tmpl := "/{a}{b}{c}{d}Z"
	uri := "/" + strings.Repeat("x", 20) + "Z"
	for i := 0; i < b.N; i++ {
		uriTemplateMatches(tmpl, uri)
	}
}
