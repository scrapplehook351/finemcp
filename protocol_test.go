package finemcp

import (
	"encoding/json"
	"testing"
)

func TestInitializeResult_MarshalJSON(t *testing.T) {
	r := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools: &ToolCapability{},
		},
		ServerInfo: ProcessInfo{Name: "test", Version: "1.0"},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["protocolVersion"]; !ok {
		t.Error("missing protocolVersion")
	}
	if _, ok := raw["capabilities"]; !ok {
		t.Error("missing capabilities")
	}
	if _, ok := raw["serverInfo"]; !ok {
		t.Error("missing serverInfo")
	}
}

func TestToolInfo_EmptyInputSchema(t *testing.T) {
	info := ToolInfo{
		Name:        "myTool",
		InputSchema: map[string]string{"type": "object"},
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if string(raw["inputSchema"]) != `{"type":"object"}` {
		t.Errorf("unexpected inputSchema: %s", raw["inputSchema"])
	}
}

func TestToolInfo_OmitsEmptyDescription(t *testing.T) {
	info := ToolInfo{
		Name:        "myTool",
		InputSchema: map[string]string{"type": "object"},
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["description"]; ok {
		t.Error("empty description should be omitted")
	}
}

func TestCallToolParams_UnmarshalJSON(t *testing.T) {
	input := `{"name":"echo","arguments":{"text":"hello"}}`

	var p CallToolParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatal(err)
	}

	if p.Name != "echo" {
		t.Errorf("name = %q, want %q", p.Name, "echo")
	}

	if p.Arguments == nil {
		t.Fatal("arguments should not be nil")
	}
}

func TestCallToolParams_NoArguments(t *testing.T) {
	input := `{"name":"noop"}`

	var p CallToolParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatal(err)
	}

	if p.Name != "noop" {
		t.Errorf("name = %q, want %q", p.Name, "noop")
	}

	if p.Arguments != nil {
		t.Error("arguments should be nil when omitted")
	}
}

func TestListToolsResult_EmptyTools(t *testing.T) {
	r := ListToolsResult{Tools: []ToolInfo{}}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"tools":[]}`
	if string(data) != want {
		t.Errorf("got %s, want %s", data, want)
	}
}

func TestProcessInfo_RoundTrip(t *testing.T) {
	orig := ProcessInfo{Name: "finemcp", Version: "0.1.0"}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}

	var got ProcessInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.Name != orig.Name || got.Version != orig.Version {
		t.Errorf("round-trip: got %+v, want %+v", got, orig)
	}
}

func TestIcon_MarshalJSON(t *testing.T) {
	t.Parallel()

	icon := Icon{
		Src:      "https://example.com/icon.png",
		MimeType: "image/png",
		Sizes:    []string{"64x64", "128x128"},
	}

	data, err := json.Marshal(icon)
	if err != nil {
		t.Fatal(err)
	}

	var got Icon
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.Src != icon.Src {
		t.Errorf("src = %q, want %q", got.Src, icon.Src)
	}
	if got.MimeType != icon.MimeType {
		t.Errorf("mimeType = %q, want %q", got.MimeType, icon.MimeType)
	}
	if len(got.Sizes) != 2 || got.Sizes[0] != "64x64" || got.Sizes[1] != "128x128" {
		t.Errorf("sizes = %v, want [64x64 128x128]", got.Sizes)
	}
}

func TestIcon_OmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	icon := Icon{Src: "https://example.com/icon.svg"}

	data, err := json.Marshal(icon)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["mimeType"]; ok {
		t.Error("empty mimeType should be omitted")
	}
	if _, ok := raw["sizes"]; ok {
		t.Error("nil sizes should be omitted")
	}
}

func TestProcessInfo_ExtendedFields_RoundTrip(t *testing.T) {
	t.Parallel()

	orig := ProcessInfo{
		Name:        "finemcp",
		Version:     "0.1.0",
		Title:       "Fine MCP Server",
		Description: "A production-grade MCP server",
		WebsiteURL:  "https://finemcp.dev",
		Icons: []Icon{
			{Src: "https://finemcp.dev/icon.png", MimeType: "image/png"},
		},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}

	var got ProcessInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.Name != orig.Name {
		t.Errorf("name = %q, want %q", got.Name, orig.Name)
	}
	if got.Title != orig.Title {
		t.Errorf("title = %q, want %q", got.Title, orig.Title)
	}
	if got.Description != orig.Description {
		t.Errorf("description = %q, want %q", got.Description, orig.Description)
	}
	if got.WebsiteURL != orig.WebsiteURL {
		t.Errorf("websiteUrl = %q, want %q", got.WebsiteURL, orig.WebsiteURL)
	}
	if len(got.Icons) != 1 || got.Icons[0].Src != orig.Icons[0].Src {
		t.Errorf("icons = %+v, want %+v", got.Icons, orig.Icons)
	}
}

func TestProcessInfo_OmitsEmptyExtendedFields(t *testing.T) {
	t.Parallel()

	info := ProcessInfo{Name: "test", Version: "1.0"}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"title", "description", "websiteUrl", "icons"} {
		if _, ok := raw[field]; ok {
			t.Errorf("empty %s should be omitted", field)
		}
	}
}

func TestInitializeResult_WithInstructions(t *testing.T) {
	t.Parallel()

	r := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ServerCapabilities{Tools: &ToolCapability{}},
		ServerInfo:      ProcessInfo{Name: "test", Version: "1.0"},
		Instructions:    "Use the echo tool to test connectivity.",
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var got InitializeResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.Instructions != r.Instructions {
		t.Errorf("instructions = %q, want %q", got.Instructions, r.Instructions)
	}
}

func TestInitializeResult_OmitsEmptyInstructions(t *testing.T) {
	t.Parallel()

	r := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ServerCapabilities{},
		ServerInfo:      ProcessInfo{Name: "test", Version: "1.0"},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["instructions"]; ok {
		t.Error("empty instructions should be omitted")
	}
}

func TestServerCapabilities_WithLogging(t *testing.T) {
	t.Parallel()

	caps := ServerCapabilities{
		Tools:   &ToolCapability{},
		Logging: &LoggingCapability{},
	}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["logging"]; !ok {
		t.Error("logging capability should be present")
	}
	// Empty struct should marshal as {}
	if string(raw["logging"]) != "{}" {
		t.Errorf("logging = %s, want {}", raw["logging"])
	}
}

func TestServerCapabilities_OmitsNilLogging(t *testing.T) {
	t.Parallel()

	caps := ServerCapabilities{
		Tools: &ToolCapability{},
	}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["logging"]; ok {
		t.Error("nil logging should be omitted")
	}
}
