package finemcp

import (
	"context"
	"encoding/json"
	"testing"
)

// ── negotiateVersion unit tests ─────────────────────────────────────

func TestNegotiateVersion_ExactMatch_Latest(t *testing.T) {
	s := NewServer("test", "1.0")
	got := s.negotiateVersion("2025-11-25")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q", got, "2025-11-25")
	}
}

func TestNegotiateVersion_ExactMatch_Older(t *testing.T) {
	s := NewServer("test", "1.0")
	got := s.negotiateVersion("2024-11-05")
	if got != "2024-11-05" {
		t.Errorf("negotiateVersion = %q, want %q", got, "2024-11-05")
	}
}

func TestNegotiateVersion_ExactMatch_Middle(t *testing.T) {
	s := NewServer("test", "1.0")
	got := s.negotiateVersion("2025-03-26")
	if got != "2025-03-26" {
		t.Errorf("negotiateVersion = %q, want %q", got, "2025-03-26")
	}
}

func TestNegotiateVersion_Unsupported_FallsBackToLatest(t *testing.T) {
	s := NewServer("test", "1.0")
	got := s.negotiateVersion("2020-01-01")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q (latest)", got, "2025-11-25")
	}
}

func TestNegotiateVersion_FutureVersion_FallsBackToLatest(t *testing.T) {
	s := NewServer("test", "1.0")
	got := s.negotiateVersion("2099-12-31")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q (latest)", got, "2025-11-25")
	}
}

func TestNegotiateVersion_EmptyString_FallsBackToLatest(t *testing.T) {
	// Defense-in-depth: handleInitialize rejects empty protocolVersion
	// before calling negotiateVersion, but the function itself should
	// still not break if called directly with an empty string.
	s := NewServer("test", "1.0")
	got := s.negotiateVersion("")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q (latest)", got, "2025-11-25")
	}
}

func TestNegotiateVersion_CustomVersions(t *testing.T) {
	s := NewServer("test", "1.0", WithSupportedVersions("2025-11-25", "2024-11-05"))

	// Supported
	got := s.negotiateVersion("2024-11-05")
	if got != "2024-11-05" {
		t.Errorf("negotiateVersion = %q, want %q", got, "2024-11-05")
	}

	// Not in the custom set (was in defaults but excluded)
	got = s.negotiateVersion("2025-03-26")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q (latest custom)", got, "2025-11-25")
	}
}

func TestNegotiateVersion_SingleVersion(t *testing.T) {
	s := NewServer("test", "1.0", WithSupportedVersions("2025-11-25"))

	got := s.negotiateVersion("2025-11-25")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q", got, "2025-11-25")
	}

	got = s.negotiateVersion("2024-11-05")
	if got != "2025-11-25" {
		t.Errorf("negotiateVersion = %q, want %q (only supported)", got, "2025-11-25")
	}
}

// ── WithSupportedVersions option tests ──────────────────────────────

func TestWithSupportedVersions_SetsField(t *testing.T) {
	s := NewServer("test", "1.0", WithSupportedVersions("v3", "v2", "v1"))
	if len(s.supportedVersions) != 3 {
		t.Fatalf("supportedVersions length = %d, want 3", len(s.supportedVersions))
	}
	if s.supportedVersions[0] != "v3" {
		t.Errorf("supportedVersions[0] = %q, want v3", s.supportedVersions[0])
	}
}

func TestWithSupportedVersions_DefensiveCopy(t *testing.T) {
	versions := []string{"2025-11-25", "2024-11-05"}
	s := NewServer("test", "1.0", WithSupportedVersions(versions...))

	// Mutate the original slice — should not affect the server.
	versions[0] = "MUTATED"
	if s.supportedVersions[0] != "2025-11-25" {
		t.Error("WithSupportedVersions did not make a defensive copy")
	}
}

func TestDefaultSupportedVersions(t *testing.T) {
	s := NewServer("test", "1.0")
	defaults := DefaultSupportedVersions()
	if len(s.supportedVersions) != len(defaults) {
		t.Fatalf("default supportedVersions length = %d, want %d", len(s.supportedVersions), len(defaults))
	}
	for i, v := range defaults {
		if s.supportedVersions[i] != v {
			t.Errorf("supportedVersions[%d] = %q, want %q", i, s.supportedVersions[i], v)
		}
	}
}

func TestDefaultSupportedVersions_IsolatedCopy(t *testing.T) {
	// Verify that NewServer makes a defensive copy — mutating the
	// package-level default does not affect existing servers.
	s := NewServer("test", "1.0")
	orig := s.supportedVersions[0]

	// Get a copy and mutate it — should not affect the server.
	defaults := DefaultSupportedVersions()
	defaults[0] = "MUTATED"
	if s.supportedVersions[0] != orig {
		t.Error("mutating DefaultSupportedVersions() return value corrupted the server")
	}
}

// ── NegotiatedVersion accessor ──────────────────────────────────────

func TestNegotiatedVersion_BeforeInit(t *testing.T) {
	s := NewServer("test", "1.0")
	if got := s.NegotiatedVersion(); got != "" {
		t.Errorf("NegotiatedVersion before init = %q, want empty", got)
	}
}

func TestNegotiatedVersion_AfterInit(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if got := s.NegotiatedVersion(); got != "2025-03-26" {
		t.Errorf("NegotiatedVersion = %q, want 2025-03-26", got)
	}
}

// ── handleInitialize integration tests (version negotiation) ────────

func TestHandleMessage_Initialize_ExactMatch(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, ProtocolVersion)
	}
}

func TestHandleMessage_Initialize_OlderSupportedVersion(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	// Server should echo the client's version when it's supported.
	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05", result.ProtocolVersion)
	}
}

func TestHandleMessage_Initialize_UnsupportedVersion(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2020-01-01",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	// Server should respond with its latest version for unsupported requests.
	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q (latest)", result.ProtocolVersion, ProtocolVersion)
	}
}

func TestHandleMessage_Initialize_FutureVersion(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2099-12-31",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	// Future version not in the set — should fall back to latest.
	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q (latest)", result.ProtocolVersion, ProtocolVersion)
	}
}

func TestHandleMessage_Initialize_MissingProtocolVersion(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"capabilities": map[string]any{},
		"clientInfo":   map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)

	if resp.Error == nil {
		t.Fatal("expected error for missing protocolVersion")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_Initialize_EmptyProtocolVersion(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)

	if resp.Error == nil {
		t.Fatal("expected error for empty protocolVersion")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_Initialize_CustomVersions_Accepted(t *testing.T) {
	s := NewServer("test", "1.0", WithSupportedVersions("2025-11-25", "2024-11-05"))
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05", result.ProtocolVersion)
	}
}

func TestHandleMessage_Initialize_CustomVersions_Unsupported(t *testing.T) {
	s := NewServer("test", "1.0", WithSupportedVersions("2025-11-25", "2024-11-05"))
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26", // excluded from custom set
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	// 2025-03-26 is NOT in the custom set, so server responds with its preferred version.
	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocolVersion = %q, want 2025-11-25 (preferred)", result.ProtocolVersion)
	}
}

// ── Backward compatibility: existing tests should still work ────────

func TestHandleMessage_Initialize_StillSetsInitialized(t *testing.T) {
	s := NewServer("test", "1.0")

	// Before init, non-init methods should fail.
	toolReq := jsonrpcReq(2, "tools/list", nil)
	resp, _ := s.HandleMessage(context.Background(), toolReq)
	if resp.Error == nil {
		t.Fatal("expected error before initialize")
	}

	// Initialize.
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	resp, _ = s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", resp.Error.Message)
	}

	// After init, non-init methods should work.
	resp, _ = s.HandleMessage(context.Background(), toolReq)
	if resp.Error != nil {
		t.Fatalf("tools/list after initialize: %s", resp.Error.Message)
	}
}

func TestHandleMessage_Initialize_ResponseSetsNegotiatedVersion(t *testing.T) {
	s := NewServer("test", "1.0")
	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	resp, _ := s.HandleMessage(context.Background(), data)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Verify the response and stored negotiated version match.
	raw, _ := json.Marshal(resp.Result)
	var result InitializeResult
	json.Unmarshal(raw, &result)

	if result.ProtocolVersion != "2025-06-18" {
		t.Errorf("response protocolVersion = %q, want 2025-06-18", result.ProtocolVersion)
	}
	if s.NegotiatedVersion() != "2025-06-18" {
		t.Errorf("NegotiatedVersion = %q, want 2025-06-18", s.NegotiatedVersion())
	}
}

// ── SupportedVersions package variable ──────────────────────────────

// ── DefaultSupportedVersions function tests ────────────────────────

func TestDefaultSupportedVersions_ContainsLatest(t *testing.T) {
	versions := DefaultSupportedVersions()
	if len(versions) == 0 {
		t.Fatal("DefaultSupportedVersions is empty")
	}
	if versions[0] != ProtocolVersion {
		t.Errorf("DefaultSupportedVersions()[0] = %q, want %q (ProtocolVersion)", versions[0], ProtocolVersion)
	}
}

func TestDefaultSupportedVersions_HasAllKnownVersions(t *testing.T) {
	expected := []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
	versions := DefaultSupportedVersions()
	if len(versions) != len(expected) {
		t.Fatalf("DefaultSupportedVersions length = %d, want %d", len(versions), len(expected))
	}
	for i, v := range expected {
		if versions[i] != v {
			t.Errorf("DefaultSupportedVersions()[%d] = %q, want %q", i, versions[i], v)
		}
	}
}

func TestDefaultSupportedVersions_ReturnsFreshCopy(t *testing.T) {
	a := DefaultSupportedVersions()
	b := DefaultSupportedVersions()
	a[0] = "MUTATED"
	if b[0] == "MUTATED" {
		t.Error("DefaultSupportedVersions did not return independent copies")
	}
}

// ── WithSupportedVersions panic guards ───────────────────────────────

func TestWithSupportedVersions_PanicsOnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from WithSupportedVersions with no args")
		}
	}()
	WithSupportedVersions() // should panic
}

func TestWithSupportedVersions_PanicsOnAscendingOrder(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from WithSupportedVersions with ascending order")
		}
	}()
	// "2024-11-05" < "2025-11-25" — wrong order (should be descending)
	WithSupportedVersions("2024-11-05", "2025-11-25")
}

func TestWithSupportedVersions_AcceptsDescendingOrder(t *testing.T) {
	// Should not panic.
	s := NewServer("test", "1.0", WithSupportedVersions("2025-11-25", "2024-11-05"))
	if len(s.supportedVersions) != 2 {
		t.Fatalf("supportedVersions length = %d, want 2", len(s.supportedVersions))
	}
}

func TestWithSupportedVersions_AcceptsSingleVersion(t *testing.T) {
	// Single version has no ordering to validate — should not panic.
	s := NewServer("test", "1.0", WithSupportedVersions("2025-11-25"))
	if len(s.supportedVersions) != 1 {
		t.Fatalf("supportedVersions length = %d, want 1", len(s.supportedVersions))
	}
}

// ── Edge case: all supported versions in DefaultSupportedVersions ───

func TestHandleMessage_Initialize_AllSupportedVersions(t *testing.T) {
	for _, version := range DefaultSupportedVersions() {
		t.Run(version, func(t *testing.T) {
			s := NewServer("test", "1.0")
			data := jsonrpcReq(1, "initialize", map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
			})

			resp, _ := s.HandleMessage(context.Background(), data)
			if resp.Error != nil {
				t.Fatalf("initialize with %s: %s", version, resp.Error.Message)
			}

			raw, _ := json.Marshal(resp.Result)
			var result InitializeResult
			json.Unmarshal(raw, &result)

			if result.ProtocolVersion != version {
				t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, version)
			}
			if s.NegotiatedVersion() != version {
				t.Errorf("NegotiatedVersion = %q, want %q", s.NegotiatedVersion(), version)
			}
		})
	}
}
