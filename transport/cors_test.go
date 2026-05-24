package transport_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finemcp/finemcp/transport"
)

func TestCORS_PreflightWildcard(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"*"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Allow-Methods header missing")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("Allow-Headers header missing")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("Max-Age = %q, want 86400", got)
	}
}

func TestCORS_PreflightSpecificOrigin(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"https://allowed.example.com", "https://other.example.com"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.example.com" {
		t.Errorf("Allow-Origin = %q, want https://allowed.example.com", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"https://allowed.example.com"},
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("inner handler was not called for disallowed origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for disallowed origin", got)
	}
}

func TestCORS_NoOriginHeader(t *testing.T) {
	t.Parallel()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"https://allowed.example.com"},
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("inner handler was not called for non-browser request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty for non-browser request", got)
	}
}

func TestCORS_AllowCredentials(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want https://app.example.com", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true", got)
	}
}

func TestCORS_CustomOptions(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"POST"},
		AllowedHeaders: []string{"Content-Type", "X-Custom"},
		ExposedHeaders: []string{"X-Request-Id"},
		MaxAge:         3600,
	})

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "POST" {
		t.Errorf("Allow-Methods = %q, want POST", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-Custom" {
		t.Errorf("Allow-Headers = %q, want %q", got, "Content-Type, X-Custom")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Errorf("Max-Age = %q, want 3600", got)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req2.Header.Set("Origin", "https://example.com")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)

	if got := rec2.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-Id" {
		t.Errorf("Expose-Headers = %q, want X-Request-Id", got)
	}
}

func TestCORS_WithHandler(t *testing.T) {
	t.Parallel()

	s := initHTTPServer(t)
	handler := transport.CORS(transport.Handler(s), transport.CORSOptions{
		AllowedOrigins: []string{"https://example.com"},
	})

	body := `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want https://example.com", got)
	}

	req2 := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req2.Header.Set("Origin", "https://example.com")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec2.Code)
	}
}

func TestCORS_PanicOnWildcardWithCredentials(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for wildcard + credentials, got none")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "AllowCredentials") {
			t.Errorf("unexpected panic value: %v", r)
		}
	}()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	})
}

func TestCORS_PanicOnEmptyOrigins(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty origins, got none")
		}
	}()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{},
	})
}

func TestCORS_RejectOriginWithControlChars(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"*"},
	})

	cases := []struct {
		name   string
		origin string
	}{
		{"newline", "https://example.com\r\nX-Injected: true"},
		{"null byte", "https://example.com\x00evil"},
		{"tab", "https://\texample.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			req.Header["Origin"] = []string{tc.origin} // bypass net/http validation
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for origin with control chars", rec.Code)
			}
		})
	}
}

func TestCORS_DefensiveCopyOfOrigins(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	origins := []string{"https://example.com"}
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: origins,
	})

	// Mutate the original slice after CORS was constructed.
	origins[0] = "https://evil.com"

	// The original origin should still be allowed (defensive copy).
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want https://example.com (defensive copy failed)", got)
	}
}

func TestCORS_NegativeMaxAge(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := transport.CORS(inner, transport.CORSOptions{
		AllowedOrigins: []string{"*"},
		MaxAge:         -100,
	})

	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Negative MaxAge should be clamped; should fallback to default (86400).
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("Max-Age = %q, want 86400 (negative should fallback to default)", got)
	}
}
