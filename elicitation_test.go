package finemcp

import (
	"context"
	"errors"
	"testing"
)

func TestElicitUser_Success(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	s.clientCaps.Elicitation = &ElicitationCapability{}

	mockSender := RequestSender(func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		if method != "elicitation/create" {
			t.Errorf("expected method elicitation/create, got %s", method)
		}

		result := ElicitationResult{
			Value:     "user input",
			Cancelled: false,
		}

		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      "1",
			Result:  result,
		}, nil
	})

	ctx := WithRequestSender(context.Background(), mockSender)

	params := ElicitationParams{
		Prompt: "Enter your name:",
		Type:   "text",
	}

	result, err := s.ElicitUser(ctx, params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.Value != "user input" {
		t.Errorf("expected value 'user input', got %s", result.Value)
	}

	if result.Cancelled {
		t.Error("expected cancelled to be false")
	}
}

func TestElicitUser_Cancelled(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	s.clientCaps.Elicitation = &ElicitationCapability{}

	mockSender := RequestSender(func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      "1",
			Result: ElicitationResult{
				Value:     "",
				Cancelled: true,
			},
		}, nil
	})

	ctx := WithRequestSender(context.Background(), mockSender)

	result, err := s.ElicitUser(ctx, ElicitationParams{Prompt: "test"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !result.Cancelled {
		t.Error("expected cancelled to be true")
	}
}

func TestElicitUser_NotInitialized(t *testing.T) {
	s := NewServer("test", "1.0.0")
	// Don't initialize

	ctx := context.Background()
	_, err := s.ElicitUser(ctx, ElicitationParams{Prompt: "test"})
	if err != errNotInitializedYet {
		t.Errorf("expected errNotInitializedYet, got %v", err)
	}
}

func TestElicitUser_NotSupported(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	// Don't set Elicitation capability

	ctx := context.Background()
	_, err := s.ElicitUser(ctx, ElicitationParams{Prompt: "test"})
	if err != errElicitationNotSupported {
		t.Errorf("expected errElicitationNotSupported, got %v", err)
	}
}

func TestElicitUser_NoRequestSender(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	s.clientCaps.Elicitation = &ElicitationCapability{}

	ctx := context.Background()
	_, err := s.ElicitUser(ctx, ElicitationParams{Prompt: "test"})
	if err != errNoRequestSender {
		t.Errorf("expected errNoRequestSender, got %v", err)
	}
}

func TestElicitUser_EmptyPrompt(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	s.clientCaps.Elicitation = &ElicitationCapability{}

	mockSender := RequestSender(func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		t.Fatal("sender should not be called for empty prompt")
		return nil, nil
	})

	ctx := WithRequestSender(context.Background(), mockSender)

	_, err := s.ElicitUser(ctx, ElicitationParams{Prompt: ""})
	if err == nil || err.Error() != "prompt must not be empty" {
		t.Errorf("expected 'prompt must not be empty' error, got %v", err)
	}
}

func TestElicitUser_ClientError(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	s.clientCaps.Elicitation = &ElicitationCapability{}

	mockSender := RequestSender(func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      "1",
			Error: &JSONRPCError{
				Code:    -32000,
				Message: "user cancelled",
			},
		}, nil
	})

	ctx := WithRequestSender(context.Background(), mockSender)

	_, err := s.ElicitUser(ctx, ElicitationParams{Prompt: "test"})
	if err == nil {
		t.Fatal("expected error for client error response")
	}

	want := "client returned error: user cancelled (code -32000)"
	if err.Error() != want {
		t.Errorf("expected %q, got %q", want, err.Error())
	}
}

func TestElicitUser_TransportError(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.initialized.Store(true)
	s.clientCaps.Elicitation = &ElicitationCapability{}

	mockSender := RequestSender(func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		return nil, errors.New("transport failed")
	})

	ctx := WithRequestSender(context.Background(), mockSender)

	_, err := s.ElicitUser(ctx, ElicitationParams{Prompt: "test"})
	if err == nil {
		t.Fatal("expected error for transport failure")
	}
}
