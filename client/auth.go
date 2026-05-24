package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

// AuthConfig provides authentication configuration for the client.
// It supports multiple authentication methods that can be used simultaneously.
type AuthConfig struct {
	// BearerToken is a static bearer token to include in the Authorization header.
	BearerToken string

	// APIKey is a static API key. Used with APIKeyHeader.
	APIKey string

	// APIKeyHeader is the header name for APIKey. Defaults to "X-API-Key".
	APIKeyHeader string

	// CustomHeaders are additional headers to include in every request.
	CustomHeaders map[string]string

	// TokenProvider is a function that dynamically provides an auth token.
	// It's called for each request, enabling support for refreshable tokens
	// (e.g., OAuth2). If both TokenProvider and BearerToken are set,
	// TokenProvider takes precedence.
	TokenProvider func(ctx context.Context) (string, error)

	// OnAuthError is called when an authentication error occurs (e.g., 401/403).
	// It can attempt to refresh credentials and return nil to retry, or return
	// an error to propagate the failure.
	OnAuthError func(err error) error
}

// ApplyToRequest applies authentication headers to an HTTP request.
// This should be called by transport implementations before sending requests.
func (a *AuthConfig) ApplyToRequest(ctx context.Context, req *http.Request) error {
	if a == nil {
		return nil
	}

	// Apply custom headers first (can be overridden by specific auth methods)
	for k, v := range a.CustomHeaders {
		req.Header.Set(k, v)
	}

	// Apply API key if configured
	if a.APIKey != "" {
		header := a.APIKeyHeader
		if header == "" {
			header = "X-API-Key"
		}
		req.Header.Set(header, a.APIKey)
	}

	// Apply bearer token (TokenProvider takes precedence over static token)
	var token string
	if a.TokenProvider != nil {
		t, err := a.TokenProvider(ctx)
		if err != nil {
			return fmt.Errorf("auth: token provider failed: %w", err)
		}
		token = t
	} else if a.BearerToken != "" {
		token = a.BearerToken
	}

	if token != "" {
		// Support both "Bearer <token>" and raw token
		if !strings.HasPrefix(token, "Bearer ") {
			token = "Bearer " + token
		}
		req.Header.Set("Authorization", token)
	}

	return nil
}

// HandleAuthError processes an authentication error through the OnAuthError callback.
// Returns true if the error was handled and the operation should be retried.
func (a *AuthConfig) HandleAuthError(err error) (bool, error) {
	if a == nil || a.OnAuthError == nil {
		return false, err
	}

	if retryErr := a.OnAuthError(err); retryErr == nil {
		return true, nil // Retry
	} else {
		return false, retryErr // Propagate error
	}
}

// IsAuthError checks if an HTTP status code indicates an authentication error.
func IsAuthError(statusCode int) bool {
	return statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}

// OAuth2TokenProvider wraps an oauth2.TokenSource for use with AuthConfig.TokenProvider.
// It handles token retrieval and refresh automatically through the TokenSource.
//
// This is the low-level building block for OAuth2 integration. For common OAuth2 flows,
// consider using OAuth2ClientCredentials or OAuth2AuthCode instead.
//
// Thread-safety: The returned function is safe for concurrent use if the underlying
// TokenSource is thread-safe (standard oauth2.TokenSource implementations are).
//
// Example usage:
//
//	import "golang.org/x/oauth2"
//
//	// Create an oauth2.TokenSource (e.g., from oauth2.Config)
//	ts := config.TokenSource(ctx, token)
//
//	// Use with AuthConfig
//	provider, err := client.OAuth2TokenProvider(ts)
//	if err != nil {
//	    return err
//	}
//	auth := &client.AuthConfig{
//	    TokenProvider: provider,
//	}
//
//	client, err := client.NewClient(transport, auth)
func OAuth2TokenProvider(ts oauth2.TokenSource) (func(context.Context) (string, error), error) {
	if ts == nil {
		return nil, errors.New("oauth2: TokenSource cannot be nil")
	}

	return func(ctx context.Context) (string, error) {
		// Check for context cancellation before token fetch
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		token, err := ts.Token()
		if err != nil {
			return "", fmt.Errorf("oauth2: failed to get token: %w", err)
		}
		if token == nil {
			return "", errors.New("oauth2: token source returned nil token")
		}

		return token.AccessToken, nil
	}, nil
}

// OAuth2ClientCredentials creates a TokenProvider for OAuth2 client credentials flow.
// This flow is used for server-to-server authentication where the application itself
// is authenticated, not a user.
//
// The function creates a reusable TokenProvider that will automatically refresh tokens
// as needed using the oauth2 library's built-in token source.
//
// Example usage:
//
//	import "golang.org/x/oauth2"
//
//	config := &oauth2.Config{
//	    ClientID:     "your-client-id",
//	    ClientSecret: "your-client-secret",
//	    Endpoint:     oauth2.Endpoint{TokenURL: "https://oauth.example.com/token"},
//	    Scopes:       []string{"api.read", "api.write"},
//	}
//
//	tokenProvider, err := client.OAuth2ClientCredentials(config)
//	if err != nil {
//	    return err
//	}
//
//	auth := &client.AuthConfig{
//	    TokenProvider: tokenProvider,
//	}
//
//	mcpClient, err := client.NewClient(transport, auth)
func OAuth2ClientCredentials(config *oauth2.Config) (func(context.Context) (string, error), error) {
	if config == nil {
		return nil, errors.New("oauth2: config cannot be nil")
	}

	// Use client credentials flow by passing nil token
	// Use background context for HTTP client creation to avoid cancellation issues.
	// Token refresh operations will use their own context from the TokenProvider.
	httpClient := config.Client(context.Background(), nil)

	// Extract the TokenSource from the transport
	transport, ok := httpClient.Transport.(*oauth2.Transport)
	if !ok {
		return nil, errors.New("oauth2: failed to extract token source from client")
	}

	return OAuth2TokenProvider(transport.Source)
}

// OAuth2AuthCode creates a TokenProvider for OAuth2 authorization code flow.
// This flow is used when a user authorizes the application, typically through a web browser.
//
// The initial token should be obtained through the standard OAuth2 authorization code flow
// (redirect to auth URL, receive code, exchange for token). This function wraps that token
// in a TokenProvider that will automatically refresh it as needed.
//
// PKCE Support: For enhanced security, use oauth2.SetAuthURLParam with oauth2.S256ChallengeOption
// when generating the authorization URL. Example:
//
//	verifier := oauth2.GenerateVerifier()
//	authURL := config.AuthCodeURL("state", oauth2.S256ChallengeOption(verifier))
//
// Example usage:
//
//	import "golang.org/x/oauth2"
//
//	config := &oauth2.Config{
//	    ClientID:     "your-client-id",
//	    ClientSecret: "your-client-secret",
//	    RedirectURL:  "http://localhost:8080/callback",
//	    Scopes:       []string{"user.read"},
//	    Endpoint: oauth2.Endpoint{
//	        AuthURL:  "https://oauth.example.com/authorize",
//	        TokenURL: "https://oauth.example.com/token",
//	    },
//	}
//
//	// User completes auth flow and you receive the initial token
//	token, err := config.Exchange(ctx, authCode)
//	if err != nil {
//	    return err
//	}
//
//	// Create TokenProvider that will auto-refresh
//	tokenProvider, err := client.OAuth2AuthCode(config, token)
//	if err != nil {
//	    return err
//	}
//
//	auth := &client.AuthConfig{
//	    TokenProvider: tokenProvider,
//	    OnAuthError:   client.OAuth2OnRefreshError(func(err error) {
//	        log.Printf("Token refresh failed: %v", err)
//	    }),
//	}
//
//	mcpClient, err := client.NewClient(transport, auth)
func OAuth2AuthCode(config *oauth2.Config, token *oauth2.Token) (func(context.Context) (string, error), error) {
	if config == nil {
		return nil, errors.New("oauth2: config cannot be nil")
	}
	if token == nil {
		return nil, errors.New("oauth2: token cannot be nil")
	}

	// Create a TokenSource that will refresh the token as needed
	// Use background context to avoid cancellation issues with long-lived TokenSource.
	// Token refresh operations will use their own context from the TokenProvider.
	ts := config.TokenSource(context.Background(), token)

	return OAuth2TokenProvider(ts)
}

// OAuth2OnRefreshError creates an OnAuthError callback that logs OAuth2 refresh errors.
// This is useful for debugging authentication issues or implementing custom error handling.
//
// The logFunc is called with the error before it's returned. If logFunc is nil,
// this function simply returns the error unchanged.
//
// SECURITY WARNING: Ensure errors are not logged to untrusted outputs (e.g., user-facing logs)
// as they may contain sensitive information like tokens or credentials.
//
// Example usage:
//
//	import "log"
//
//	auth := &client.AuthConfig{
//	    TokenProvider: tokenProvider,
//	    OnAuthError: client.OAuth2OnRefreshError(func(err error) {
//	        log.Printf("OAuth2 refresh failed: %v", err)
//	        // Could also send to error tracking service
//	        errorTracker.Capture(err)
//	    }),
//	}
//
//	mcpClient, err := client.NewClient(transport, auth)
//
// For automatic retry on token refresh (without logging):
//
//	auth := &client.AuthConfig{
//	    TokenProvider: tokenProvider,
//	    OnAuthError: func(err error) error {
//	        return nil // Return nil to trigger retry
//	    },
//	}
func OAuth2OnRefreshError(logFunc func(error)) func(error) error {
	return func(err error) error {
		if logFunc != nil {
			logFunc(err)
		}
		return err
	}
}
