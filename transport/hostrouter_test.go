package transport_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finemcp/finemcp/transport"
)

func TestHostRouter_ExactMatch(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("api.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("api"))
	}))
	router.Handle("admin.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("admin"))
	}))

	tests := []struct {
		name     string
		host     string
		wantCode int
		wantBody string
	}{
		{"api host", "api.example.com", 200, "api"},
		{"admin host", "admin.example.com", 200, "admin"},
		{"unknown host", "unknown.example.com", 421, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestHostRouter_CaseInsensitive(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("API.Example.COM", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		host     string
		wantCode int
	}{
		{"api.example.com", 200},
		{"API.EXAMPLE.COM", 200},
		{"Api.Example.Com", 200},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestHostRouter_StripPort(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("api.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "api.example.com:8080"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (port should be stripped)", rec.Code)
	}
}

func TestHostRouter_WildcardSubdomain(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("*.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("wildcard"))
	}))

	tests := []struct {
		name     string
		host     string
		wantCode int
	}{
		{"single subdomain", "tenant1.example.com", 200},
		{"another subdomain", "tenant2.example.com", 200},
		{"nested subdomain rejected", "a.b.example.com", 421},
		{"bare domain no match", "example.com", 421},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestHostRouter_ExactTakesPrecedence(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("*.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("wildcard"))
	}))
	router.Handle("api.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("exact"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "api.example.com"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Body.String() != "exact" {
		t.Errorf("body = %q, want %q (exact should take precedence over wildcard)", rec.Body.String(), "exact")
	}
}

func TestHostRouter_Fallback(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouterWithOptions(
		transport.WithFallback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("fallback"))
		})),
	)
	router.Handle("api.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("api"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.example.com"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Body.String() != "fallback" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "fallback")
	}
}

func TestHostRouter_Remove(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("api.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	if !router.Remove("api.example.com") {
		t.Fatal("Remove should return true for registered host")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "api.example.com"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("status = %d, want 421 after removal", rec.Code)
	}

	if router.Remove("nonexistent.example.com") {
		t.Error("Remove should return false for unregistered host")
	}
}

func TestHostRouter_RemoveWildcard(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("*.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	if !router.Remove("*.example.com") {
		t.Fatal("Remove should return true for registered wildcard")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "tenant.example.com"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("status = %d, want 421 after wildcard removal", rec.Code)
	}
}

func TestHostRouter_PanicOnDuplicateHost(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate host")
		}
	}()

	router := transport.NewHostRouter()
	router.Handle("api.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.Handle("api.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestHostRouter_PanicOnDuplicateWildcard(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate wildcard")
		}
	}()

	router := transport.NewHostRouter()
	router.Handle("*.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.Handle("*.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestHostRouter_PanicOnEmptyHost(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty host")
		}
	}()

	router := transport.NewHostRouter()
	router.Handle("", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestHostRouter_PanicOnNilHandler(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil handler")
		}
	}()

	router := transport.NewHostRouter()
	router.Handle("api.example.com", nil)
}

func TestHostRouter_PanicOnMaxHosts(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when max hosts exceeded")
		}
	}()

	router := transport.NewHostRouterWithOptions(transport.WithMaxHosts(2))
	router.Handle("a.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.Handle("b.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.Handle("c.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestHostRouter_WithNotFoundStatus(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouterWithOptions(transport.WithNotFoundStatus(http.StatusNotFound))
	router.Handle("api.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.example.com"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHostRouter_WithMCPHandler(t *testing.T) {
	t.Parallel()

	s := initHTTPServer(t)
	router := transport.NewHostRouter()
	router.Handle("mcp.example.com", transport.Handler(s))

	body := `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Host = "mcp.example.com"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHostRouter_IPv6(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("[::1]", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ipv6"))
	}))

	tests := []struct {
		name     string
		host     string
		wantCode int
		wantBody string
	}{
		{"ipv6 no port", "[::1]", 200, "ipv6"},
		{"ipv6 with port", "[::1]:8080", 200, "ipv6"},
		{"ipv6 wrong addr", "[::2]", 421, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestHostRouter_WildcardCaseInsensitive(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("*.EXAMPLE.COM", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("wildcard"))
	}))

	tests := []struct {
		host     string
		wantCode int
	}{
		{"tenant.example.com", 200},
		{"TENANT.EXAMPLE.COM", 200},
		{"Tenant.Example.Com", 200},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestHostRouter_MaxHostsCountsWildcards(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when max hosts exceeded (wildcard counted)")
		}
	}()

	router := transport.NewHostRouterWithOptions(transport.WithMaxHosts(2))
	router.Handle("api.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	router.Handle("*.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	// Third registration should panic — wildcards count towards the limit.
	router.Handle("admin.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestHostRouter_ConcurrentReadWrite(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Pre-register a host.
	router.Handle("api.example.com", handler)

	done := make(chan struct{})

	// Concurrent reader — sends requests.
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "api.example.com"
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
		}
	}()

	// Concurrent writer — adds and removes hosts.
	for i := 0; i < 100; i++ {
		host := "dynamic.example.com"
		router.Handle(host, handler)
		router.Remove(host)
	}

	<-done
}

func TestHostRouter_WildcardBoundaryProtection(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("*.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("matched"))
	}))

	// These should NOT match — the leading dot in the stored suffix
	// (".example.com") acts as a natural boundary.
	attacks := []string{
		"evilexample.com",
		"notexample.com",
		"maliciousexample.com",
		"xexample.com",
	}
	for _, host := range attacks {
		t.Run(host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusMisdirectedRequest {
				t.Errorf("host %q should NOT match wildcard; status = %d, want 421", host, rec.Code)
			}
		})
	}
}

func TestHostRouter_WildcardMultiLevelBoundary(t *testing.T) {
	t.Parallel()

	router := transport.NewHostRouter()
	router.Handle("*.internal.example.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("internal"))
	}))

	tests := []struct {
		name     string
		host     string
		wantCode int
	}{
		// Valid matches
		{"valid subdomain", "api.internal.example.com", 200},
		{"another valid", "db.internal.example.com", 200},
		// Boundary attacks — must NOT match
		{"boundary bypass", "maliciousinternal.example.com", 421},
		{"no dot prefix", "xinternal.example.com", 421},
		{"nested subdomain", "a.b.internal.example.com", 421},
		{"bare domain", "internal.example.com", 421},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("host %q: status = %d, want %d", tc.host, rec.Code, tc.wantCode)
			}
		})
	}
}

func TestHostRouter_PanicOnBareWildcard(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for bare wildcard '*.'")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "must have a domain") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()

	router := transport.NewHostRouter()
	router.Handle("*.", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func TestHostRouter_IPv6MalformedPassthrough(t *testing.T) {
	t.Parallel()

	// Malformed IPv6 (no closing bracket) should pass through without matching anything.
	router := transport.NewHostRouter()
	router.Handle("[::1]", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ipv6"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "[::1:8080" // malformed — no closing bracket
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("malformed IPv6 should not match; status = %d, want 421", rec.Code)
	}
}

func TestHostRouter_EmptyHost(t *testing.T) {
	t.Parallel()

	fallbackCalled := false
	router := transport.NewHostRouterWithOptions(
		transport.WithFallback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackCalled = true
			w.WriteHeader(http.StatusOK)
		})),
	)
	router.Handle("api.example.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if !fallbackCalled {
		t.Error("expected fallback to be called for empty host")
	}
}
