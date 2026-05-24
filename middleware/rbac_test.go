package middleware_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
)

func TestRBAC_OpenAccess_NoRolesRequired(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("public", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "public", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected success for tool with no role requirements")
	}
}

func TestRBAC_Allowed_CallerHasRequiredRole(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("admin-tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("secret"), nil
	}, finemcp.WithRoles("admin"))
	s.RegisterTool(tool)

	ctx := finemcp.WithRolesCtx(context.Background(), []string{"admin"})
	result, err := s.CallTool(ctx, "admin-tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected success for caller with matching role")
	}
}

func TestRBAC_Allowed_CallerHasOneOfMultipleRoles(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("staff-tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}, finemcp.WithRoles("admin", "editor"))
	s.RegisterTool(tool)

	ctx := finemcp.WithRolesCtx(context.Background(), []string{"editor"})
	result, err := s.CallTool(ctx, "staff-tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("expected success when caller has one of the required roles")
	}
}

func TestRBAC_Denied_NoCallerRoles(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("admin-tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("secret"), nil
	}, finemcp.WithRoles("admin"))
	s.RegisterTool(tool)

	result, err := s.CallTool(context.Background(), "admin-tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected denied for caller with no roles")
	}

	tc := result.Content[0].(finemcp.TextContent)
	if !strings.Contains(tc.Text, "forbidden") {
		t.Errorf("text = %q, should contain 'forbidden'", tc.Text)
	}
}

func TestRBAC_Denied_WrongRole(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("admin-tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("secret"), nil
	}, finemcp.WithRoles("admin"))
	s.RegisterTool(tool)

	ctx := finemcp.WithRolesCtx(context.Background(), []string{"viewer"})
	result, err := s.CallTool(ctx, "admin-tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected denied for caller with wrong role")
	}
}

func TestRBAC_Denied_HandlerNotCalled(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	called := false
	tool, _ := finemcp.NewTool("secret", func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return []byte("leaked"), nil
	}, finemcp.WithRoles("admin"))
	s.RegisterTool(tool)

	s.CallTool(context.Background(), "secret", nil)

	if called {
		t.Error("handler should not be called when access is denied")
	}
}

func TestRBAC_CaseSensitive(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithRoles("Admin"))
	s.RegisterTool(tool)

	ctx := finemcp.WithRolesCtx(context.Background(), []string{"admin"})
	result, _ := s.CallTool(ctx, "tool", nil)
	if !result.IsError {
		t.Error("expected denied - roles should be case-sensitive")
	}
}

func TestRBACWithDenied_CustomHandler(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	var capturedRequired, capturedActual []string

	s.Use(middleware.RBACWithDenied(func(_ context.Context, required, actual []string) string {
		capturedRequired = required
		capturedActual = actual
		return "custom denied"
	}))

	tool, _ := finemcp.NewTool("tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithRoles("admin"))
	s.RegisterTool(tool)

	ctx := finemcp.WithRolesCtx(context.Background(), []string{"viewer"})
	result, _ := s.CallTool(ctx, "tool", nil)

	if !result.IsError {
		t.Fatal("expected denied")
	}

	tc := result.Content[0].(finemcp.TextContent)
	if tc.Text != "custom denied" {
		t.Errorf("text = %q, want %q", tc.Text, "custom denied")
	}

	if len(capturedRequired) != 1 || capturedRequired[0] != "admin" {
		t.Errorf("required = %v, want [admin]", capturedRequired)
	}
	if len(capturedActual) != 1 || capturedActual[0] != "viewer" {
		t.Errorf("actual = %v, want [viewer]", capturedActual)
	}
}

func TestRBAC_DefaultDeniedMessageIncludesToolName(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("admin-tool", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}, finemcp.WithRoles("admin"))
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	callMsg := jsonrpcReq(2, "tools/call", map[string]any{"name": "admin-tool"})
	resp, _ := s.HandleMessage(context.Background(), callMsg)

	if resp.Error != nil {
		t.Fatalf("RBAC denial should be a tool error, not protocol error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "admin-tool") {
		t.Errorf("result = %s, should mention tool name", raw)
	}
}

func TestRBAC_EndToEnd_AllowedViaDispatch(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")
	s.Use(middleware.RBAC())

	tool, _ := finemcp.NewTool("protected", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("success"), nil
	}, finemcp.WithRoles("user"))
	s.RegisterTool(tool)

	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	ctx := finemcp.WithRolesCtx(context.Background(), []string{"user"})
	callMsg := jsonrpcReq(2, "tools/call", map[string]any{"name": "protected"})
	resp, _ := s.HandleMessage(ctx, callMsg)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), `"isError":true`) {
		t.Error("expected success, got error result")
	}
}
