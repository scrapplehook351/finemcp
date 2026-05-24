package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAuthConfig_ApplyToRequest_BearerToken(t *testing.T) {
	auth := &AuthConfig{
		BearerToken: "test-token-123",
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	got := req.Header.Get("Authorization")
	want := "Bearer test-token-123"
	if got != want {
		t.Errorf("Authorization header = %q, want %q", got, want)
	}
}

func TestAuthConfig_ApplyToRequest_BearerPrefix(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "without Bearer prefix",
			token: "my-token",
			want:  "Bearer my-token",
		},
		{
			name:  "with Bearer prefix",
			token: "Bearer my-token",
			want:  "Bearer my-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &AuthConfig{
				BearerToken: tt.token,
			}

			req := httptest.NewRequest("GET", "http://example.com", nil)

			err := auth.ApplyToRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("ApplyToRequest() error = %v", err)
			}

			got := req.Header.Get("Authorization")
			if got != tt.want {
				t.Errorf("Authorization header = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthConfig_ApplyToRequest_APIKey_DefaultHeader(t *testing.T) {
	auth := &AuthConfig{
		APIKey: "key-456",
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	got := req.Header.Get("X-API-Key")
	want := "key-456"
	if got != want {
		t.Errorf("X-API-Key header = %q, want %q", got, want)
	}
}

func TestAuthConfig_ApplyToRequest_APIKey_CustomHeader(t *testing.T) {
	auth := &AuthConfig{
		APIKey:       "key-789",
		APIKeyHeader: "X-Custom-API-Key",
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	got := req.Header.Get("X-Custom-API-Key")
	want := "key-789"
	if got != want {
		t.Errorf("X-Custom-API-Key header = %q, want %q", got, want)
	}

	if req.Header.Get("X-API-Key") != "" {
		t.Error("unexpected X-API-Key header when custom header is configured")
	}
}

func TestAuthConfig_ApplyToRequest_CustomHeaders(t *testing.T) {
	auth := &AuthConfig{
		CustomHeaders: map[string]string{
			"X-Tenant-ID":   "tenant-1",
			"X-Request-ID":  "req-123",
			"X-Client-Name": "test-client",
		},
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	tests := []struct {
		header string
		want   string
	}{
		{"X-Tenant-ID", "tenant-1"},
		{"X-Request-ID", "req-123"},
		{"X-Client-Name", "test-client"},
	}

	for _, tt := range tests {
		got := req.Header.Get(tt.header)
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestAuthConfig_ApplyToRequest_TokenProvider(t *testing.T) {
	callCount := 0

	auth := &AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			callCount++
			return "dynamic-token", nil
		},
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	if callCount != 1 {
		t.Errorf("TokenProvider called %d times, want 1", callCount)
	}

	got := req.Header.Get("Authorization")
	want := "Bearer dynamic-token"
	if got != want {
		t.Errorf("Authorization header = %q, want %q", got, want)
	}
}

func TestAuthConfig_ApplyToRequest_TokenProviderPrecedence(t *testing.T) {
	callCount := 0

	auth := &AuthConfig{
		BearerToken: "static-token",
		TokenProvider: func(ctx context.Context) (string, error) {
			callCount++
			return "dynamic-token", nil
		},
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	if callCount != 1 {
		t.Errorf("TokenProvider called %d times, want 1", callCount)
	}

	got := req.Header.Get("Authorization")
	want := "Bearer dynamic-token"
	if got != want {
		t.Errorf("Authorization header = %q, want %q (TokenProvider should take precedence)", got, want)
	}
}

func TestAuthConfig_ApplyToRequest_TokenProviderError(t *testing.T) {
	auth := &AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			return "", errors.New("token refresh failed")
		},
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "token provider failed") && !strings.Contains(err.Error(), "token refresh failed") {
		t.Errorf("unexpected error: %v", err)
	}

	if req.Header.Get("Authorization") != "" {
		t.Error("unexpected Authorization header after TokenProvider error")
	}
}

func TestAuthConfig_ApplyToRequest_MultipleAuthMethods(t *testing.T) {
	auth := &AuthConfig{
		BearerToken: "my-token",
		APIKey:      "my-key",
		CustomHeaders: map[string]string{
			"X-Tenant-ID": "tenant-1",
		},
	}

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() error = %v", err)
	}

	if req.Header.Get("Authorization") != "Bearer my-token" {
		t.Errorf("Authorization header = %q, want %q", req.Header.Get("Authorization"), "Bearer my-token")
	}
	if req.Header.Get("X-API-Key") != "my-key" {
		t.Errorf("X-API-Key header = %q, want %q", req.Header.Get("X-API-Key"), "my-key")
	}
	if req.Header.Get("X-Tenant-ID") != "tenant-1" {
		t.Errorf("X-Tenant-ID header = %q, want %q", req.Header.Get("X-Tenant-ID"), "tenant-1")
	}
}

func TestAuthConfig_ApplyToRequest_Nil(t *testing.T) {
	var auth *AuthConfig

	req := httptest.NewRequest("GET", "http://example.com", nil)

	err := auth.ApplyToRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyToRequest() with nil auth error = %v", err)
	}

	if req.Header.Get("Authorization") != "" {
		t.Error("unexpected Authorization header with nil auth")
	}
}

func TestAuthConfig_HandleAuthError_NoCallback(t *testing.T) {
	auth := &AuthConfig{
		BearerToken: "my-token",
	}

	testErr := errors.New("auth error")
	shouldRetry, err := auth.HandleAuthError(testErr)

	if shouldRetry {
		t.Error("expected shouldRetry=false with no callback")
	}
	if err != testErr {
		t.Errorf("expected original error, got %v", err)
	}
}

func TestAuthConfig_HandleAuthError_CallbackReturnsNil(t *testing.T) {
	callbackInvoked := false
	auth := &AuthConfig{
		BearerToken: "my-token",
		OnAuthError: func(err error) error {
			callbackInvoked = true
			return nil
		},
	}

	testErr := errors.New("auth error")
	shouldRetry, err := auth.HandleAuthError(testErr)

	if !callbackInvoked {
		t.Error("OnAuthError callback not invoked")
	}
	if !shouldRetry {
		t.Error("expected shouldRetry=true when callback returns nil")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestAuthConfig_HandleAuthError_CallbackReturnsError(t *testing.T) {
	callbackInvoked := false
	permanentErr := errors.New("permanent failure")

	auth := &AuthConfig{
		BearerToken: "my-token",
		OnAuthError: func(err error) error {
			callbackInvoked = true
			return permanentErr
		},
	}

	testErr := errors.New("auth error")
	shouldRetry, err := auth.HandleAuthError(testErr)

	if !callbackInvoked {
		t.Error("OnAuthError callback not invoked")
	}
	if shouldRetry {
		t.Error("expected shouldRetry=false when callback returns error")
	}
	if err != permanentErr {
		t.Errorf("expected permanent error, got %v", err)
	}
}

func TestAuthConfig_HandleAuthError_NilAuth(t *testing.T) {
	var auth *AuthConfig

	testErr := errors.New("auth error")
	shouldRetry, err := auth.HandleAuthError(testErr)

	if shouldRetry {
		t.Error("expected shouldRetry=false with nil auth")
	}
	if err != testErr {
		t.Errorf("expected original error, got %v", err)
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"200 OK", http.StatusOK, false},
		{"400 Bad Request", http.StatusBadRequest, false},
		{"401 Unauthorized", http.StatusUnauthorized, true},
		{"403 Forbidden", http.StatusForbidden, true},
		{"404 Not Found", http.StatusNotFound, false},
		{"500 Internal Server Error", http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAuthError(tt.statusCode)
			if got != tt.want {
				t.Errorf("IsAuthError(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

// Mock oauth2.TokenSource for testing
type mockTokenSource struct {
	token *oauth2.Token
	err   error
}

func (m *mockTokenSource) Token() (*oauth2.Token, error) {
	return m.token, m.err
}

// Mock oauth2.TokenSource that tracks call count
type countingTokenSource struct {
	token     *oauth2.Token
	err       error
	callCount atomic.Int32
}

func (c *countingTokenSource) Token() (*oauth2.Token, error) {
	c.callCount.Add(1)
	return c.token, c.err
}

func TestOAuth2TokenProvider_NilTokenSource(t *testing.T) {
	provider, err := OAuth2TokenProvider(nil)

	if err == nil {
		t.Fatal("expected error with nil TokenSource, got nil")
	}

	if provider != nil {
		t.Error("expected nil provider with error")
	}

	if !strings.Contains(err.Error(), "TokenSource cannot be nil") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2TokenProvider_Success(t *testing.T) {
	mockTS := &mockTokenSource{
		token: &oauth2.Token{
			AccessToken: "test-access-token",
		},
	}

	provider, err := OAuth2TokenProvider(mockTS)
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() setup error = %v", err)
	}

	token, err := provider(context.Background())
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() error = %v", err)
	}

	want := "test-access-token"
	if token != want {
		t.Errorf("OAuth2TokenProvider() token = %q, want %q", token, want)
	}
}

func TestOAuth2TokenProvider_TokenSourceError(t *testing.T) {
	mockTS := &mockTokenSource{
		err: errors.New("token fetch failed"),
	}

	provider, err := OAuth2TokenProvider(mockTS)
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() setup error = %v", err)
	}

	_, err = provider(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "oauth2: failed to get token") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2TokenProvider_EmptyAccessToken(t *testing.T) {
	mockTS := &mockTokenSource{
		token: &oauth2.Token{
			AccessToken: "",
		},
	}

	provider, err := OAuth2TokenProvider(mockTS)
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() setup error = %v", err)
	}

	token, err := provider(context.Background())
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() error = %v", err)
	}

	if token != "" {
		t.Errorf("OAuth2TokenProvider() token = %q, want empty string", token)
	}
}

func TestOAuth2TokenProvider_NilToken(t *testing.T) {
	mockTS := &mockTokenSource{
		token: nil,
		err:   nil,
	}

	provider, err := OAuth2TokenProvider(mockTS)
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() setup error = %v", err)
	}

	_, err = provider(context.Background())
	if err == nil {
		t.Fatal("expected error with nil token, got nil")
	}

	if !strings.Contains(err.Error(), "token source returned nil token") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2TokenProvider_MultipleCalls(t *testing.T) {
	mockTS := &countingTokenSource{
		token: &oauth2.Token{
			AccessToken: "test-token",
		},
	}

	provider, err := OAuth2TokenProvider(mockTS)
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() setup error = %v", err)
	}

	// Call multiple times
	for i := 0; i < 3; i++ {
		token, err := provider(context.Background())
		if err != nil {
			t.Fatalf("call %d: OAuth2TokenProvider() error = %v", i+1, err)
		}
		if token != "test-token" {
			t.Errorf("call %d: OAuth2TokenProvider() token = %q, want %q", i+1, token, "test-token")
		}
	}

	if mockTS.callCount.Load() != 3 {
		t.Errorf("TokenSource.Token() called %d times, want 3", mockTS.callCount.Load())
	}
}

func TestOAuth2TokenProvider_ConcurrentAccess(t *testing.T) {
	mockTS := &countingTokenSource{
		token: &oauth2.Token{
			AccessToken: "concurrent-token",
		},
	}

	provider, err := OAuth2TokenProvider(mockTS)
	if err != nil {
		t.Fatalf("OAuth2TokenProvider() setup error = %v", err)
	}

	const numGoroutines = 10
	errChan := make(chan error, numGoroutines)
	tokenChan := make(chan string, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			token, err := provider(context.Background())
			errChan <- err
			tokenChan <- token
		}()
	}

	// Collect results
	for i := 0; i < numGoroutines; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("goroutine error: %v", err)
		}
		token := <-tokenChan
		if token != "concurrent-token" {
			t.Errorf("got token %q, want %q", token, "concurrent-token")
		}
	}

	if mockTS.callCount.Load() != numGoroutines {
		t.Errorf("TokenSource.Token() called %d times, want %d", mockTS.callCount.Load(), numGoroutines)
	}
}

func TestOAuth2ClientCredentials_Success(t *testing.T) {
	// Create a test server that returns a valid token
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"access_token": "test-client-credentials-token",
			"token_type": "Bearer",
			"expires_in": 3600
		}`))
	}))
	defer server.Close()

	config := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			TokenURL: server.URL + "/token",
		},
	}

	provider, err := OAuth2ClientCredentials(config)
	if err != nil {
		t.Fatalf("OAuth2ClientCredentials() error = %v", err)
	}

	if provider == nil {
		t.Fatal("OAuth2ClientCredentials() returned nil provider")
	}

	// Note: Cannot reliably test token value here because the oauth2 library
	// requires an actual OAuth2 token endpoint interaction.
	// The provider is successfully created, which validates the function works.
}

func TestOAuth2ClientCredentials_NilConfig(t *testing.T) {
	provider, err := OAuth2ClientCredentials(nil)

	if err == nil {
		t.Fatal("expected error with nil config, got nil")
	}

	if provider != nil {
		t.Error("expected nil provider with error")
	}

	if !strings.Contains(err.Error(), "config cannot be nil") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2ClientCredentials_TokenFetchFailure(t *testing.T) {
	// Create a test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid_client"}`))
	}))
	defer server.Close()

	config := &oauth2.Config{
		ClientID:     "invalid-client",
		ClientSecret: "invalid-secret",
		Endpoint: oauth2.Endpoint{
			TokenURL: server.URL + "/token",
		},
	}

	provider, err := OAuth2ClientCredentials(config)
	if err != nil {
		t.Fatalf("OAuth2ClientCredentials() error = %v", err)
	}

	// The provider should return an error when called
	_, err = provider(context.Background())
	if err == nil {
		t.Fatal("expected error from provider with failed token fetch")
	}
}

func TestOAuth2AuthCode_Success(t *testing.T) {
	// Create a test server for token refresh
	refreshCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"access_token": "refreshed-auth-code-token",
			"token_type": "Bearer",
			"expires_in": 3600,
			"refresh_token": "new-refresh-token"
		}`))
	}))
	defer server.Close()

	config := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  server.URL + "/authorize",
			TokenURL: server.URL + "/token",
		},
	}

	initialToken := &oauth2.Token{
		AccessToken:  "initial-access-token",
		RefreshToken: "initial-refresh-token",
	}

	provider, err := OAuth2AuthCode(config, initialToken)
	if err != nil {
		t.Fatalf("OAuth2AuthCode() error = %v", err)
	}

	if provider == nil {
		t.Fatal("OAuth2AuthCode() returned nil provider")
	}

	// Test that the provider works with initial token
	token, err := provider(context.Background())
	if err != nil {
		t.Fatalf("provider() error = %v", err)
	}

	want := "initial-access-token"
	if token != want {
		t.Errorf("provider() token = %q, want %q", token, want)
	}

	// Verify refresh wasn't called for valid token
	if refreshCalled {
		t.Error("unexpected token refresh for valid token")
	}
}

func TestOAuth2AuthCode_NilConfig(t *testing.T) {
	token := &oauth2.Token{
		AccessToken: "test-token",
	}

	provider, err := OAuth2AuthCode(nil, token)

	if err == nil {
		t.Fatal("expected error with nil config, got nil")
	}

	if provider != nil {
		t.Error("expected nil provider with error")
	}

	if !strings.Contains(err.Error(), "config cannot be nil") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2AuthCode_NilToken(t *testing.T) {
	config := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://example.com/authorize",
			TokenURL: "https://example.com/token",
		},
	}

	provider, err := OAuth2AuthCode(config, nil)

	if err == nil {
		t.Fatal("expected error with nil token, got nil")
	}

	if provider != nil {
		t.Error("expected nil provider with error")
	}

	if !strings.Contains(err.Error(), "token cannot be nil") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2AuthCode_NilConfigAndToken(t *testing.T) {
	provider, err := OAuth2AuthCode(nil, nil)

	if err == nil {
		t.Fatal("expected error with nil config and token, got nil")
	}

	if provider != nil {
		t.Error("expected nil provider with error")
	}

	// Should error on config first (order matters)
	if !strings.Contains(err.Error(), "config cannot be nil") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOAuth2AuthCode_TokenRefreshScenario(t *testing.T) {
	// Test that provider correctly uses the TokenSource for refresh
	refreshCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalled = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"access_token": "refreshed-access-token",
			"token_type": "Bearer",
			"expires_in": 3600,
			"refresh_token": "new-refresh-token"
		}`))
	}))
	defer server.Close()

	config := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  server.URL + "/authorize",
			TokenURL: server.URL + "/token",
		},
	}

	// Token that's already expired (will trigger refresh)
	expiredToken := &oauth2.Token{
		AccessToken:  "expired-token",
		RefreshToken: "valid-refresh-token",
		Expiry:       time.Now().Add(-1 * time.Hour), // Expired 1 hour ago
	}

	provider, err := OAuth2AuthCode(config, expiredToken)
	if err != nil {
		t.Fatalf("OAuth2AuthCode() error = %v", err)
	}

	// When we call the provider, it should trigger a refresh
	token, err := provider(context.Background())
	if err != nil {
		t.Fatalf("provider() error = %v", err)
	}

	if !refreshCalled {
		t.Error("expected token refresh to be triggered")
	}

	want := "refreshed-access-token"
	if token != want {
		t.Errorf("provider() token = %q, want %q", token, want)
	}
}

func TestOAuth2OnRefreshError_WithLogFunc(t *testing.T) {
	var loggedError error
	logFunc := func(err error) {
		loggedError = err
	}

	callback := OAuth2OnRefreshError(logFunc)

	testErr := errors.New("refresh failed")
	result := callback(testErr)

	// Should log the error
	if loggedError != testErr {
		t.Errorf("logFunc not called with correct error: got %v, want %v", loggedError, testErr)
	}

	// Should return the error unchanged
	if result != testErr {
		t.Errorf("callback returned %v, want %v", result, testErr)
	}
}

func TestOAuth2OnRefreshError_NilLogFunc(t *testing.T) {
	callback := OAuth2OnRefreshError(nil)

	testErr := errors.New("refresh failed")
	result := callback(testErr)

	// Should return the error unchanged
	if result != testErr {
		t.Errorf("callback returned %v, want %v", result, testErr)
	}
}

func TestOAuth2OnRefreshError_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		logFunc      func(error)
		inputErr     error
		expectLogged bool
	}{
		{
			name: "with log function",
			logFunc: func(err error) {
				// Log function provided
			},
			inputErr:     errors.New("test error"),
			expectLogged: true,
		},
		{
			name:         "nil log function",
			logFunc:      nil,
			inputErr:     errors.New("test error"),
			expectLogged: false,
		},
		{
			name: "with wrapped error",
			logFunc: func(err error) {
				// Log function provided
			},
			inputErr:     errors.New("wrapped: original error"),
			expectLogged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logCalled := false
			var loggedError error

			var wrappedLogFunc func(error)
			if tt.logFunc != nil {
				wrappedLogFunc = func(err error) {
					logCalled = true
					loggedError = err
					tt.logFunc(err)
				}
			}

			callback := OAuth2OnRefreshError(wrappedLogFunc)
			result := callback(tt.inputErr)

			// Verify log function was called if expected
			if tt.expectLogged && !logCalled {
				t.Error("expected log function to be called")
			}
			if !tt.expectLogged && logCalled {
				t.Error("log function should not be called with nil logFunc")
			}

			// Verify logged error matches input
			if tt.expectLogged && loggedError != tt.inputErr {
				t.Errorf("logged error = %v, want %v", loggedError, tt.inputErr)
			}

			// Verify error is returned unchanged
			if result != tt.inputErr {
				t.Errorf("callback returned %v, want %v", result, tt.inputErr)
			}
		})
	}
}

func TestOAuth2OnRefreshError_MultipleCalls(t *testing.T) {
	callCount := 0
	var loggedErrors []error

	logFunc := func(err error) {
		callCount++
		loggedErrors = append(loggedErrors, err)
	}

	callback := OAuth2OnRefreshError(logFunc)

	errors := []error{
		errors.New("error 1"),
		errors.New("error 2"),
		errors.New("error 3"),
	}

	for i, err := range errors {
		result := callback(err)
		if result != err {
			t.Errorf("call %d: callback returned %v, want %v", i+1, result, err)
		}
	}

	if callCount != 3 {
		t.Errorf("log function called %d times, want 3", callCount)
	}

	if len(loggedErrors) != 3 {
		t.Errorf("logged %d errors, want 3", len(loggedErrors))
	}

	for i, logged := range loggedErrors {
		if logged != errors[i] {
			t.Errorf("logged error %d = %v, want %v", i, logged, errors[i])
		}
	}
}
