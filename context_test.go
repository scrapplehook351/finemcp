package finemcp

import (
	"context"
	"testing"
)

func TestRequestID_SetAndGet(t *testing.T) {
	ctx := WithRequestID(context.Background(), 42)

	got := RequestID(ctx)
	if got != 42 {
		t.Errorf("RequestID = %v, want 42", got)
	}
}

func TestRequestID_StringID(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-abc")

	got := RequestID(ctx)
	if got != "req-abc" {
		t.Errorf("RequestID = %v, want %q", got, "req-abc")
	}
}

func TestRequestID_NilWhenUnset(t *testing.T) {
	got := RequestID(context.Background())
	if got != nil {
		t.Errorf("RequestID = %v, want nil", got)
	}
}

func TestToolName_SetAndGet(t *testing.T) {
	ctx := WithToolName(context.Background(), "echo")

	got := ToolName(ctx)
	if got != "echo" {
		t.Errorf("ToolName = %q, want %q", got, "echo")
	}
}

func TestToolName_EmptyWhenUnset(t *testing.T) {
	got := ToolName(context.Background())
	if got != "" {
		t.Errorf("ToolName = %q, want empty", got)
	}
}

func TestRolesFromCtx_SetAndGet(t *testing.T) {
	ctx := WithRolesCtx(context.Background(), []string{"admin", "user"})

	got := RolesFromCtx(ctx)
	if len(got) != 2 || got[0] != "admin" || got[1] != "user" {
		t.Errorf("Roles = %v, want [admin user]", got)
	}
}

func TestRolesFromCtx_NilWhenUnset(t *testing.T) {
	got := RolesFromCtx(context.Background())
	if got != nil {
		t.Errorf("Roles = %v, want nil", got)
	}
}

func TestRolesFromCtx_DefensiveCopy(t *testing.T) {
	original := []string{"admin", "user"}
	ctx := WithRolesCtx(context.Background(), original)

	// Mutate the original — should not affect the stored value.
	original[0] = "hacked"

	got := RolesFromCtx(ctx)
	if got[0] != "admin" {
		t.Errorf("Roles[0] = %q, want %q — defensive copy failed", got[0], "admin")
	}
}

func TestContextKeys_NoCollision(t *testing.T) {
	ctx := context.Background()
	ctx = WithRequestID(ctx, "id-1")
	ctx = WithToolName(ctx, "myTool")
	ctx = WithRolesCtx(ctx, []string{"admin"})

	// All three values should coexist.
	if RequestID(ctx) != "id-1" {
		t.Error("RequestID clobbered")
	}
	if ToolName(ctx) != "myTool" {
		t.Error("ToolName clobbered")
	}
	roles := RolesFromCtx(ctx)
	if len(roles) != 1 || roles[0] != "admin" {
		t.Error("Roles clobbered")
	}
}

// Integration: verify context values are available inside middleware during tools/call.
func TestContext_AvailableInMiddleware(t *testing.T) {
	s := NewServer("test", "1.0")

	var gotRequestID any
	var gotToolName string

	s.Use(func(next ToolHandler) ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			gotRequestID = RequestID(ctx)
			gotToolName = ToolName(ctx)
			return next(ctx, input)
		}
	})

	echo, _ := NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(echo)

	// Initialize first.
	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	// Call tool.
	callMsg := jsonrpcReq("req-42", "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hi"},
	})
	resp, err := s.HandleMessage(context.Background(), callMsg)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	if gotRequestID != "req-42" {
		t.Errorf("RequestID in middleware = %v, want %q", gotRequestID, "req-42")
	}
	if gotToolName != "echo" {
		t.Errorf("ToolName in middleware = %q, want %q", gotToolName, "echo")
	}
}

func TestProgressReporterFromCtx_SetAndGet(t *testing.T) {
	var called bool
	reporter := ProgressReporter(func(progress, total float64) {
		called = true
	})
	ctx := withProgressReporter(context.Background(), reporter)

	got := ProgressReporterFromCtx(ctx)
	if got == nil {
		t.Fatal("ProgressReporterFromCtx returned nil, want non-nil")
	}
	got(1, 10)
	if !called {
		t.Error("reporter was not invoked")
	}
}

func TestProgressReporterFromCtx_NilWhenUnset(t *testing.T) {
	got := ProgressReporterFromCtx(context.Background())
	if got != nil {
		t.Error("ProgressReporterFromCtx = non-nil, want nil")
	}
}

func TestNotificationSenderFromCtx_SetAndGet(t *testing.T) {
	var called bool
	sender := NotificationSender(func(n *JSONRPCNotification) {
		called = true
	})
	ctx := WithNotificationSender(context.Background(), sender)

	got := NotificationSenderFromCtx(ctx)
	if got == nil {
		t.Fatal("NotificationSenderFromCtx returned nil, want non-nil")
	}
	got(&JSONRPCNotification{})
	if !called {
		t.Error("sender was not invoked")
	}
}

func TestNotificationSenderFromCtx_NilWhenUnset(t *testing.T) {
	got := NotificationSenderFromCtx(context.Background())
	if got != nil {
		t.Error("NotificationSenderFromCtx = non-nil, want nil")
	}
}

func TestSubscriberID_SetAndGet(t *testing.T) {
	ctx := WithSubscriberID(context.Background(), "session-abc")
	got := SubscriberIDFromCtx(ctx)
	if got != "session-abc" {
		t.Errorf("SubscriberIDFromCtx = %q, want %q", got, "session-abc")
	}
}

func TestSubscriberID_EmptyWhenUnset(t *testing.T) {
	got := SubscriberIDFromCtx(context.Background())
	if got != "" {
		t.Errorf("SubscriberIDFromCtx = %q, want empty string", got)
	}
}

// ── ToolRoles ───────────────────────────────────────────────────────

func TestToolRolesFromCtx_SetAndGet(t *testing.T) {
	ctx := withToolRoles(context.Background(), []string{"admin", "editor"})
	got := ToolRolesFromCtx(ctx)
	if len(got) != 2 || got[0] != "admin" || got[1] != "editor" {
		t.Errorf("ToolRolesFromCtx = %v, want [admin editor]", got)
	}
}

func TestToolRolesFromCtx_NilWhenUnset(t *testing.T) {
	got := ToolRolesFromCtx(context.Background())
	if got != nil {
		t.Errorf("ToolRolesFromCtx = %v, want nil", got)
	}
}

// ── ToolSchema ──────────────────────────────────────────────────────

func TestToolSchemaFromCtx_SetAndGet(t *testing.T) {
	schema := map[string]any{"type": "object"}
	ctx := withToolSchema(context.Background(), schema)
	got := ToolSchemaFromCtx(ctx)
	if got == nil {
		t.Error("ToolSchemaFromCtx = nil, want schema")
	}
}

func TestToolSchemaFromCtx_NilWhenUnset(t *testing.T) {
	got := ToolSchemaFromCtx(context.Background())
	if got != nil {
		t.Errorf("ToolSchemaFromCtx = %v, want nil", got)
	}
}

// ── SkipValidation ──────────────────────────────────────────────────

func TestSkipValidationFromCtx_SetAndGet(t *testing.T) {
	ctx := withSkipValidation(context.Background(), true)
	if !SkipValidationFromCtx(ctx) {
		t.Error("SkipValidationFromCtx = false, want true")
	}
}

func TestSkipValidationFromCtx_FalseWhenUnset(t *testing.T) {
	if SkipValidationFromCtx(context.Background()) {
		t.Error("SkipValidationFromCtx = true, want false")
	}
}

// ── ToolSimulator ───────────────────────────────────────────────────

func TestToolSimulatorFromCtx_SetAndGet(t *testing.T) {
	called := false
	sim := SimulatorFunc(func(_ context.Context, input []byte) ([]byte, error) {
		called = true
		return input, nil
	})
	ctx := withToolSimulator(context.Background(), sim)
	got := ToolSimulatorFromCtx(ctx)
	if got == nil {
		t.Fatal("ToolSimulatorFromCtx = nil, want simulator")
	}
	_, _ = got(context.Background(), []byte("test"))
	if !called {
		t.Error("simulator was not invoked")
	}
}

func TestToolSimulatorFromCtx_NilWhenUnset(t *testing.T) {
	got := ToolSimulatorFromCtx(context.Background())
	if got != nil {
		t.Errorf("ToolSimulatorFromCtx = non-nil, want nil")
	}
}

// ── Simulated ───────────────────────────────────────────────────────

func TestWithSimulated_SetAndGet(t *testing.T) {
	ctx := WithSimulated(context.Background())
	if !IsSimulatedFromCtx(ctx) {
		t.Error("IsSimulatedFromCtx = false, want true")
	}
}

func TestIsSimulatedFromCtx_FalseWhenUnset(t *testing.T) {
	if IsSimulatedFromCtx(context.Background()) {
		t.Error("IsSimulatedFromCtx = true, want false")
	}
}

// ── SimDepth ────────────────────────────────────────────────────────

func TestWithSimDepth_SetAndGet(t *testing.T) {
	ctx := WithSimDepth(context.Background(), 3)
	got := SimDepthFromCtx(ctx)
	if got != 3 {
		t.Errorf("SimDepthFromCtx = %d, want 3", got)
	}
}

func TestSimDepthFromCtx_ZeroWhenUnset(t *testing.T) {
	got := SimDepthFromCtx(context.Background())
	if got != 0 {
		t.Errorf("SimDepthFromCtx = %d, want 0", got)
	}
}

// ── TenantID ────────────────────────────────────────────────────────

func TestWithTenantID_SetAndGet(t *testing.T) {
	ctx := WithTenantID(context.Background(), "tenant-42")
	got := TenantIDFromCtx(ctx)
	if got != "tenant-42" {
		t.Errorf("TenantIDFromCtx = %q, want tenant-42", got)
	}
}

// ── AuthInfo ────────────────────────────────────────────────────────

func TestWithAuthInfo_SetAndGet(t *testing.T) {
	info := AuthInfo{
		Subject: "user-1",
		Roles:   []string{"admin"},
		Meta:    map[string]any{"email": "user@example.com"},
	}
	ctx := WithAuthInfo(context.Background(), info)
	got := AuthInfoFromCtx(ctx)
	if got == nil {
		t.Fatal("AuthInfoFromCtx = nil, want AuthInfo")
	}
	if got.Subject != "user-1" {
		t.Errorf("Subject = %q, want user-1", got.Subject)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "admin" {
		t.Errorf("Roles = %v, want [admin]", got.Roles)
	}
}

func TestAuthInfoFromCtx_NilWhenUnset(t *testing.T) {
	got := AuthInfoFromCtx(context.Background())
	if got != nil {
		t.Errorf("AuthInfoFromCtx = non-nil, want nil")
	}
}
