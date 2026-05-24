package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp/client"
)

func TestHTTPTransport_StartValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "missing BaseURL",
			cfg:     Config{},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: Config{
				BaseURL: "https://example.com",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := New(tt.cfg)
			err := tr.Start(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("Start() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHTTPTransport_SendReceive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "success",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tr := New(Config{
		BaseURL: server.URL,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	response, err := tr.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(response, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["result"] != "success" {
		t.Errorf("expected result=success, got %v", resp["result"])
	}
}

func TestHTTPTransport_AuthBearerToken(t *testing.T) {
	var capturedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		BearerToken: "test-token-123",
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	want := "Bearer test-token-123"
	if capturedAuth != want {
		t.Errorf("Authorization header = %q, want %q", capturedAuth, want)
	}
}

func TestHTTPTransport_AuthAPIKey(t *testing.T) {
	var capturedAPIKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPIKey = r.Header.Get("X-API-Key")
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		APIKey: "key-456",
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	want := "key-456"
	if capturedAPIKey != want {
		t.Errorf("X-API-Key header = %q, want %q", capturedAPIKey, want)
	}
}

func TestHTTPTransport_AuthCustomHeaders(t *testing.T) {
	var capturedHeaders map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = map[string]string{
			"X-Tenant-ID":  r.Header.Get("X-Tenant-ID"),
			"X-Request-ID": r.Header.Get("X-Request-ID"),
		}
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		CustomHeaders: map[string]string{
			"X-Tenant-ID":  "tenant-123",
			"X-Request-ID": "req-456",
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if capturedHeaders["X-Tenant-ID"] != "tenant-123" {
		t.Errorf("X-Tenant-ID = %q, want %q", capturedHeaders["X-Tenant-ID"], "tenant-123")
	}
	if capturedHeaders["X-Request-ID"] != "req-456" {
		t.Errorf("X-Request-ID = %q, want %q", capturedHeaders["X-Request-ID"], "req-456")
	}
}

func TestHTTPTransport_AuthTokenProvider(t *testing.T) {
	callCount := 0
	var capturedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			callCount++
			return "dynamic-token", nil
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if callCount != 1 {
		t.Errorf("TokenProvider called %d times, want 1", callCount)
	}

	want := "Bearer dynamic-token"
	if capturedAuth != want {
		t.Errorf("Authorization header = %q, want %q", capturedAuth, want)
	}
}

func TestHTTPTransport_AuthTokenProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called")
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			return "", errors.New("token refresh failed")
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHTTPTransport_Auth401Callback(t *testing.T) {
	callbackInvoked := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		BearerToken: "invalid-token",
		OnAuthError: func(err error) error {
			callbackInvoked = true
			return errors.New("permanent auth failure")
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !callbackInvoked {
		t.Error("OnAuthError callback was not invoked")
	}
}

func TestHTTPTransport_NoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected Authorization header")
		}
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tr := New(Config{
		BaseURL: server.URL,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(context.Background(), request); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestHTTPTransport_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	tr := New(Config{
		BaseURL: server.URL,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHTTPTransport_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	tr := New(Config{
		BaseURL: server.URL,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHTTPTransport_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tr := New(Config{
		BaseURL: server.URL,
		Timeout: 50 * time.Millisecond,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestHTTPTransport_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	tr := New(Config{
		BaseURL: server.URL,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(ctx, request)
	if err == nil {
		t.Fatal("expected context canceled error, got nil")
	}
}

func TestHTTPTransport_CloseIdempotent(t *testing.T) {
	tr := New(Config{
		BaseURL: "https://example.com",
	})

	if err := tr.Close(); err != nil {
		t.Errorf("first Close() error = %v", err)
	}

	if err := tr.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestHTTPTransport_SendAfterClose(t *testing.T) {
	tr := New(Config{
		BaseURL: "https://example.com",
	})

	tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected error after close, got nil")
	}
}

// ── Advanced Auth Edge Case Tests ────────────────────────────────────

func TestHTTPTransport_Auth401RetryWithTokenRefresh(t *testing.T) {
	attemptCount := 0
	refreshCount := 0
	var currentToken string = "expired-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		auth := r.Header.Get("Authorization")

		// First request with expired token → 401
		if auth == "Bearer expired-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("token expired"))
			return
		}

		// Second request with refreshed token → success
		if auth == "Bearer fresh-token" {
			response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "success"}
			json.NewEncoder(w).Encode(response)
			return
		}

		// Unexpected token
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid token"))
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			return currentToken, nil
		},
		OnAuthError: func(err error) error {
			refreshCount++
			currentToken = "fresh-token" // Simulate token refresh
			return nil                   // Signal retry
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// First attempt with expired token (will fail with 401)
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	firstErr := tr.Send(context.Background(), request)
	if firstErr == nil {
		t.Fatal("expected first attempt to fail with 401")
	}

	// Verify OnAuthError was called
	if refreshCount != 1 {
		t.Errorf("expected 1 token refresh, got %d", refreshCount)
	}

	// Second attempt with refreshed token (should succeed)
	secondErr := tr.Send(context.Background(), request)
	if secondErr != nil {
		t.Fatalf("expected second attempt to succeed, got: %v", secondErr)
	}

	// Verify server received both requests
	if attemptCount != 2 {
		t.Errorf("expected 2 server requests, got %d", attemptCount)
	}
}

func TestHTTPTransport_OnAuthErrorCallbackModifiesState(t *testing.T) {
	callbackInvokedCount := 0
	serverReceivedNewToken := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer refreshed-token" {
			serverReceivedNewToken = true
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("always fail to test callback"))
	}))
	defer server.Close()

	var currentToken string = "initial-token"

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			return currentToken, nil
		},
		OnAuthError: func(err error) error {
			callbackInvokedCount++
			if callbackInvokedCount == 1 {
				currentToken = "refreshed-token"
				return nil // Allow retry
			}
			return errors.New("max retries exceeded") // Give up
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)

	// First attempt - callback refreshes token
	tr.Send(context.Background(), request)

	// Second attempt - callback gives up
	err := tr.Send(context.Background(), request)
	if err == nil {
		t.Fatal("expected error after max retries")
	}

	if callbackInvokedCount != 2 {
		t.Errorf("expected OnAuthError called 2 times, got %d", callbackInvokedCount)
	}

	if !serverReceivedNewToken {
		t.Error("server did not receive refreshed token on retry")
	}
}

func TestHTTPTransport_ConcurrentAuthProviderContention(t *testing.T) {
	tokenProviderCalls := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			mu.Lock()
			tokenProviderCalls++
			calls := tokenProviderCalls
			mu.Unlock()

			// Simulate token generation delay
			time.Sleep(10 * time.Millisecond)
			return fmt.Sprintf("token-%d", calls), nil
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// Send 10 concurrent requests
	const concurrentRequests = 10
	errChan := make(chan error, concurrentRequests)

	for i := 0; i < concurrentRequests; i++ {
		go func(id int) {
			request := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"test"}`, id))
			errChan <- tr.Send(context.Background(), request)
		}(i)
	}

	// Collect results
	for i := 0; i < concurrentRequests; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("request %d failed: %v", i, err)
		}
	}

	// Verify TokenProvider was called for each request
	mu.Lock()
	finalCalls := tokenProviderCalls
	mu.Unlock()

	if finalCalls != concurrentRequests {
		t.Errorf("expected %d token provider calls, got %d", concurrentRequests, finalCalls)
	}
}

func TestHTTPTransport_AuthProviderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be reached")
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			// Simulate slow token provider that respects context cancellation
			select {
			case <-time.After(5 * time.Second):
				return "token", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	err := tr.Send(ctx, request)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected context deadline error, got: %v", err)
	}
}

func TestHTTPTransport_TokenProviderErrorRecovery(t *testing.T) {
	attemptCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{"jsonrpc": "2.0", "id": 1, "result": "ok"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	auth := &client.AuthConfig{
		TokenProvider: func(ctx context.Context) (string, error) {
			attemptCount++
			// Fail first 2 attempts, succeed on 3rd
			if attemptCount < 3 {
				return "", fmt.Errorf("token provider error: attempt %d", attemptCount)
			}
			return "valid-token", nil
		},
	}

	tr := New(Config{
		BaseURL: server.URL,
		Auth:    auth,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)

	// First attempt - provider fails
	err1 := tr.Send(context.Background(), request)
	if err1 == nil {
		t.Fatal("expected first attempt to fail")
	}
	if !strings.Contains(err1.Error(), "token provider") {
		t.Errorf("expected token provider error, got: %v", err1)
	}

	// Second attempt - provider still fails
	err2 := tr.Send(context.Background(), request)
	if err2 == nil {
		t.Fatal("expected second attempt to fail")
	}

	// Third attempt - provider succeeds
	err3 := tr.Send(context.Background(), request)
	if err3 != nil {
		t.Fatalf("expected third attempt to succeed, got: %v", err3)
	}

	if attemptCount != 3 {
		t.Errorf("expected 3 token provider calls, got %d", attemptCount)
	}
}
