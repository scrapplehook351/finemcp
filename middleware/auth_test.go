package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/finemcp/finemcp"
)

// ── TokenVerifier tests ─────────────────────────────────────────────

func TestStaticAPIKeyVerifier_ValidKey(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-1": {Subject: "svc-a", Roles: []string{"admin"}},
		"key-2": {Subject: "svc-b", Roles: []string{"reader"}},
	})

	info, err := v.Verify(context.Background(), SchemeAPIKey, "key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "svc-a" {
		t.Errorf("Subject = %q, want %q", info.Subject, "svc-a")
	}
	if len(info.Roles) != 1 || info.Roles[0] != "admin" {
		t.Errorf("Roles = %v, want [admin]", info.Roles)
	}
}

func TestStaticAPIKeyVerifier_InvalidKey(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-1": {Subject: "svc-a"},
	})

	_, err := v.Verify(context.Background(), SchemeAPIKey, "wrong-key")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestStaticAPIKeyVerifier_WrongScheme(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-1": {Subject: "svc-a"},
	})

	_, err := v.Verify(context.Background(), SchemeBearerToken, "key-1")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestStaticBearerTokenVerifier_ValidToken(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok-abc": {Subject: "user-1", Roles: []string{"editor"}, Meta: map[string]any{"org": "acme"}},
	})

	info, err := v.Verify(context.Background(), SchemeBearerToken, "tok-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", info.Subject, "user-1")
	}
	if info.Meta["org"] != "acme" {
		t.Errorf("Meta[org] = %v, want acme", info.Meta["org"])
	}
}

func TestStaticVerifier_DefensiveCopy(t *testing.T) {
	original := map[string]finemcp.AuthInfo{
		"key": {Subject: "svc", Roles: []string{"admin"}, Meta: map[string]any{"k": "v"}},
	}
	v := StaticAPIKeyVerifier(original)

	// Mutate the original map — should not affect the verifier.
	original["key"] = finemcp.AuthInfo{Subject: "hacked"}

	info, err := v.Verify(context.Background(), SchemeAPIKey, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "svc" {
		t.Errorf("Subject = %q, want %q — defensive copy failed", info.Subject, "svc")
	}
}

func TestTokenVerifierFunc(t *testing.T) {
	fn := TokenVerifierFunc(func(_ context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error) {
		if scheme == SchemeBearerToken && token == "magic" {
			return finemcp.AuthInfo{Subject: "wizard"}, nil
		}
		return finemcp.AuthInfo{}, ErrInvalidCredentials
	})

	info, err := fn.Verify(context.Background(), SchemeBearerToken, "magic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "wizard" {
		t.Errorf("Subject = %q, want %q", info.Subject, "wizard")
	}

	_, err = fn.Verify(context.Background(), SchemeBearerToken, "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

// ── RequireAuth tests ───────────────────────────────────────────────

func TestRequireAuth_WithAuthInfo(t *testing.T) {
	checker := RequireAuth()
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "user-1"})

	if err := checker(ctx); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRequireAuth_NoAuthInfo(t *testing.T) {
	checker := RequireAuth()
	if err := checker(context.Background()); err == nil {
		t.Error("expected error for missing AuthInfo")
	}
}

func TestRequireAuth_EmptySubject(t *testing.T) {
	checker := RequireAuth()
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: ""})

	if err := checker(ctx); err == nil {
		t.Error("expected error for empty subject")
	}
}

func TestRequireAuthWithRoles_HasRole(t *testing.T) {
	checker := RequireAuthWithRoles("admin", "editor")
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Roles:   []string{"editor"},
	})

	if err := checker(ctx); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRequireAuthWithRoles_MissingRole(t *testing.T) {
	checker := RequireAuthWithRoles("admin")
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Roles:   []string{"reader"},
	})

	if err := checker(ctx); err == nil {
		t.Error("expected error for missing role")
	}
}

func TestRequireAuthWithRoles_NoAuthInfo(t *testing.T) {
	checker := RequireAuthWithRoles("admin")
	if err := checker(context.Background()); err == nil {
		t.Error("expected error for missing AuthInfo")
	}
}

// ── HTTPAuth tests ──────────────────────────────────────────────────

// authEchoHandler writes the AuthInfo from context as JSON.
func authEchoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := finemcp.AuthInfoFromCtx(r.Context())
		if info == nil {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"anonymous":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
}

func TestHTTPAuth_BearerToken_Success(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"valid-token": {Subject: "user-1", Roles: []string{"admin"}},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var info finemcp.AuthInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", info.Subject, "user-1")
	}
}

func TestHTTPAuth_BearerToken_Invalid(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"valid-token": {Subject: "user-1"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHTTPAuth_APIKey_Success(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"my-key": {Subject: "svc-a", Roles: []string{"operator"}},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Api-Key", "my-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var info finemcp.AuthInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.Subject != "svc-a" {
		t.Errorf("Subject = %q, want %q", info.Subject, "svc-a")
	}
}

func TestHTTPAuth_APIKey_Query(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"qry-key": {Subject: "svc-q"},
	})

	handler := HTTPAuth(v, authEchoHandler(), WithAPIKeyQuery("api_key"))
	req := httptest.NewRequest(http.MethodPost, "/?api_key=qry-key", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHTTPAuth_NoCredentials_Rejected(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Verify JSON-RPC error body.
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), `"code":-32001`) {
		t.Errorf("body should contain JSON-RPC error code, got: %s", body)
	}
}

func TestHTTPAuth_AnonymousAllowed(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user"},
	})

	handler := HTTPAuth(v, authEchoHandler(), WithAnonymousIdentity(finemcp.AuthInfo{
		Subject: "anonymous",
		Roles:   []string{"public"},
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var info finemcp.AuthInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.Subject != "anonymous" {
		t.Errorf("Subject = %q, want %q", info.Subject, "anonymous")
	}
}

func TestHTTPAuth_MultipleAuthHeaders_Rejected(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Add("Authorization", "Bearer tok")
	req.Header.Add("Authorization", "Bearer tok2")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHTTPAuth_EmptyBearerToken_Rejected(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; empty bearer token should be rejected", w.Code, http.StatusUnauthorized)
	}
}

func TestHTTPAuth_VerifierPanic_Recovered(t *testing.T) {
	v := TokenVerifierFunc(func(_ context.Context, _ AuthScheme, _ string) (finemcp.AuthInfo, error) {
		panic("verifier exploded")
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer something")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d after verifier panic", w.Code, http.StatusUnauthorized)
	}
}

func TestHTTPAuth_CustomErrorHandler(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user"},
	})

	called := false
	handler := HTTPAuth(v, authEchoHandler(), WithAuthErrorHandler(func(w http.ResponseWriter, _ *http.Request, _ error) {
		called = true
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("custom error"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !called {
		t.Error("custom error handler was not called")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestHTTPAuth_CustomAPIKeyHeader(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"my-key": {Subject: "svc"},
	})

	handler := HTTPAuth(v, authEchoHandler(), WithAPIKeyHeader("X-Custom-Key"))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Custom-Key", "my-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHTTPAuth_BearerPriority_OverAPIKey(t *testing.T) {
	// Both Bearer and API key verifiers accept, but Bearer should take priority.
	v := TokenVerifierFunc(func(_ context.Context, scheme AuthScheme, token string) (finemcp.AuthInfo, error) {
		if scheme == SchemeBearerToken && token == "bearer-tok" {
			return finemcp.AuthInfo{Subject: "bearer-user"}, nil
		}
		if scheme == SchemeAPIKey && token == "api-key" {
			return finemcp.AuthInfo{Subject: "apikey-user"}, nil
		}
		return finemcp.AuthInfo{}, ErrInvalidCredentials
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer bearer-tok")
	req.Header.Set("X-Api-Key", "api-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var info finemcp.AuthInfo
	_ = json.NewDecoder(w.Body).Decode(&info)
	if info.Subject != "bearer-user" {
		t.Errorf("Subject = %q, want %q — Bearer should have priority", info.Subject, "bearer-user")
	}
}

// ── Context helper tests ────────────────────────────────────────────

func TestWithAuthInfo_DefensiveCopy(t *testing.T) {
	roles := []string{"admin", "editor"}
	meta := map[string]any{"org": "acme"}

	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user",
		Roles:   roles,
		Meta:    meta,
	})

	// Mutate originals.
	roles[0] = "hacked"
	meta["org"] = "evil"

	info := finemcp.AuthInfoFromCtx(ctx)
	if info.Roles[0] != "admin" {
		t.Errorf("Roles[0] = %q, want %q — defensive copy failed", info.Roles[0], "admin")
	}
	if info.Meta["org"] != "acme" {
		t.Errorf("Meta[org] = %v, want acme — defensive copy failed", info.Meta["org"])
	}
}

func TestWithAuthInfo_SetsRolesForRBAC(t *testing.T) {
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user",
		Roles:   []string{"admin"},
	})

	// RBAC reads roles via RolesFromCtx.
	roles := finemcp.RolesFromCtx(ctx)
	if len(roles) != 1 || roles[0] != "admin" {
		t.Errorf("RolesFromCtx = %v, want [admin] — auth should set roles for RBAC", roles)
	}
}

func TestAuthInfoFromCtx_Nil(t *testing.T) {
	info := finemcp.AuthInfoFromCtx(context.Background())
	if info != nil {
		t.Errorf("expected nil AuthInfo, got %+v", info)
	}
}

// ── Integration: HTTPAuth + AuthChecker ─────────────────────────────

func TestHTTPAuth_WithAuthChecker_Integration(t *testing.T) {
	// Set up a minimal MCP server with auth checker.
	server := finemcp.NewServer("test", "1.0.0")
	server.SetAuthChecker(RequireAuth())

	verifier := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"valid": {Subject: "user-1", Roles: []string{"admin"}},
	})

	// Create a simple handler that calls HandleMessage.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		resp, err := server.HandleMessage(r.Context(), body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if resp == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	handler := HTTPAuth(verifier, inner)

	// Unauthenticated request should be rejected at HTTP level.
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	// Authenticated request (server not initialized, but auth passes).
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer valid")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Server is not initialized, so we get JSON-RPC error (not 401).
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated: status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ── Panic tests ─────────────────────────────────────────────────────

func TestHTTPAuth_NilVerifier_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil verifier")
		}
	}()
	HTTPAuth(nil, authEchoHandler())
}

func TestHTTPAuth_NilHandler_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil handler")
		}
	}()
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{"t": {Subject: "u"}})
	HTTPAuth(v, nil)
}

func TestWithAnonymousIdentity_EmptySubject_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty subject")
		}
	}()
	WithAnonymousIdentity(finemcp.AuthInfo{Subject: ""})
}

// ── Additional tests from critic review ──────────────────────────────

func TestHTTPAuth_LowercaseBearer_Success(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"valid-token": {Subject: "user-1"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "bearer valid-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; case-insensitive Bearer should work", w.Code, http.StatusOK)
	}

	var info finemcp.AuthInfo
	if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if info.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", info.Subject, "user-1")
	}
}

func TestHTTPAuth_UppercaseBearer_Success(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"valid-token": {Subject: "user-1"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "BEARER valid-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; case-insensitive BEARER should work", w.Code, http.StatusOK)
	}
}

func TestHTTPAuth_ErrorMessageScrubbed(t *testing.T) {
	// Verifier returns a detailed internal error — should NOT leak to client.
	v := TokenVerifierFunc(func(_ context.Context, _ AuthScheme, _ string) (finemcp.AuthInfo, error) {
		return finemcp.AuthInfo{}, errors.New("LDAP connection refused to 10.0.1.5:389")
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer something")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "LDAP") {
		t.Errorf("response body leaks verifier error: %s", body)
	}
	if strings.Contains(body, "10.0.1.5") {
		t.Errorf("response body leaks internal IP: %s", body)
	}
	if !strings.Contains(body, "authentication failed") {
		t.Errorf("expected generic error message, got: %s", body)
	}
}

func TestHTTPAuth_WWWAuthenticateHeader(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user"},
	})

	handler := HTTPAuth(v, authEchoHandler())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	wwwAuth := w.Header().Get("WWW-Authenticate")
	if wwwAuth == "" {
		t.Error("missing WWW-Authenticate header on 401 response")
	}
	if !strings.Contains(wwwAuth, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want Bearer challenge", wwwAuth)
	}
}

func TestWithAuthInfo_CalledTwice_OverridesRoles(t *testing.T) {
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Roles:   []string{"admin"},
	})

	// Second call with empty roles should clear RBAC roles.
	ctx = finemcp.WithAuthInfo(ctx, finemcp.AuthInfo{
		Subject: "user-2",
		Roles:   nil,
	})

	info := finemcp.AuthInfoFromCtx(ctx)
	if info.Subject != "user-2" {
		t.Errorf("Subject = %q, want %q", info.Subject, "user-2")
	}

	// RBAC roles should be cleared (not carry over from first call).
	roles := finemcp.RolesFromCtx(ctx)
	if len(roles) != 0 {
		t.Errorf("RolesFromCtx = %v, want empty — stale roles from first WithAuthInfo leaked", roles)
	}
}

func TestRequireAuthWithRoles_EmptyRequired(t *testing.T) {
	// With no required roles, any authenticated user should pass.
	checker := RequireAuthWithRoles()
	ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{
		Subject: "user-1",
		Roles:   []string{"reader"},
	})

	if err := checker(ctx); err != nil {
		t.Errorf("unexpected error: %v — empty required list should pass for any authenticated user", err)
	}
}

func TestStaticVerifier_DifferentLengthTokens(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"short":                       {Subject: "svc-a"},
		"a-much-longer-api-key-value": {Subject: "svc-b"},
	})

	// Token of different length should not match.
	_, err := v.Verify(context.Background(), SchemeAPIKey, "x")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("short token: err = %v, want ErrInvalidCredentials", err)
	}

	// Valid tokens still work.
	info, err := v.Verify(context.Background(), SchemeAPIKey, "short")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "svc-a" {
		t.Errorf("Subject = %q, want %q", info.Subject, "svc-a")
	}
}

// ── Concurrency tests ───────────────────────────────────────────────

func TestSetAuthChecker_ConcurrentAccess(t *testing.T) {
	// This test validates that concurrent SetAuthChecker calls and
	// HandleMessage calls don't race. The -race flag will catch any
	// data race violations.
	server := finemcp.NewServer("race-test", "1.0.0")

	verifier := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok": {Subject: "user-1"},
	})

	var wg sync.WaitGroup
	const goroutines = 20

	// Half the goroutines update the auth checker.
	for i := range goroutines / 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				server.SetAuthChecker(RequireAuth())
			} else {
				server.SetAuthChecker(nil)
			}
		}(i)
	}

	// Half the goroutines send requests through the server.
	for range goroutines / 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := finemcp.WithAuthInfo(context.Background(), finemcp.AuthInfo{Subject: "user-1"})
			// Server not initialized, so this will return "not initialized" —
			// that's fine, we're testing that the authChecker read doesn't race.
			_, _ = server.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		}()
	}

	wg.Wait()

	_ = verifier // ensure verifier is referenced
}

func TestHTTPAuth_ConcurrentRequests(t *testing.T) {
	v := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok-1": {Subject: "user-1"},
		"tok-2": {Subject: "user-2"},
	})

	handler := HTTPAuth(v, authEchoHandler())

	var wg sync.WaitGroup
	const goroutines = 50
	errors := make(chan string, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := "tok-1"
			wantSubject := "user-1"
			if i%2 == 1 {
				tok = "tok-2"
				wantSubject = "user-2"
			}

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				errors <- "unexpected status"
				return
			}
			var info finemcp.AuthInfo
			if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
				errors <- "decode failed"
				return
			}
			if info.Subject != wantSubject {
				errors <- "wrong subject: " + info.Subject
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for e := range errors {
		t.Errorf("concurrent request error: %s", e)
	}
}

// ── ChainVerifiers tests ────────────────────────────────────────────

func TestChainVerifiers_FirstWins(t *testing.T) {
	v1 := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-a": {Subject: "from-v1"},
	})
	v2 := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-b": {Subject: "from-v2"},
	})

	chain := ChainVerifiers(v1, v2)

	info, err := chain.Verify(context.Background(), SchemeAPIKey, "key-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "from-v1" {
		t.Errorf("Subject = %q, want %q", info.Subject, "from-v1")
	}
}

func TestChainVerifiers_FallsThrough(t *testing.T) {
	v1 := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-a": {Subject: "from-v1"},
	})
	v2 := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok-b": {Subject: "from-v2"},
	})

	chain := ChainVerifiers(v1, v2)

	// key-a not valid as Bearer, v1 rejects wrong scheme, v2 matches.
	info, err := chain.Verify(context.Background(), SchemeBearerToken, "tok-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "from-v2" {
		t.Errorf("Subject = %q, want %q — should fall through to second verifier", info.Subject, "from-v2")
	}
}

func TestChainVerifiers_AllFail(t *testing.T) {
	v1 := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key-a": {Subject: "from-v1"},
	})
	v2 := StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
		"tok-b": {Subject: "from-v2"},
	})

	chain := ChainVerifiers(v1, v2)

	_, err := chain.Verify(context.Background(), SchemeAPIKey, "nonexistent")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestChainVerifiers_SingleVerifier(t *testing.T) {
	v := StaticAPIKeyVerifier(map[string]finemcp.AuthInfo{
		"key": {Subject: "svc"},
	})

	chain := ChainVerifiers(v)

	info, err := chain.Verify(context.Background(), SchemeAPIKey, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Subject != "svc" {
		t.Errorf("Subject = %q, want %q", info.Subject, "svc")
	}
}

func TestChainVerifiers_NoPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty ChainVerifiers")
		}
	}()
	ChainVerifiers()
}
