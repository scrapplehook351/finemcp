package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// ── Resource creation ───────────────────────────────────────────────

func TestNewResource_Valid(t *testing.T) {
	handler := func(_ context.Context, uri string) ([]ResourceContent, error) {
		return []ResourceContent{NewTextResourceContent(uri, "hello")}, nil
	}

	r, err := NewResource("file:///etc/hosts", "hosts",
		handler,
		WithResourceDescription("hosts file"),
		WithResourceMimeType("text/plain"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.URI != "file:///etc/hosts" {
		t.Errorf("URI = %q, want %q", r.URI, "file:///etc/hosts")
	}
	if r.Name != "hosts" {
		t.Errorf("Name = %q, want %q", r.Name, "hosts")
	}
	if r.Description != "hosts file" {
		t.Errorf("Description = %q, want %q", r.Description, "hosts file")
	}
	if r.MimeType != "text/plain" {
		t.Errorf("MimeType = %q, want %q", r.MimeType, "text/plain")
	}
}

func TestNewResource_EmptyURI(t *testing.T) {
	_, err := NewResource("", "name", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	if !errors.Is(err, errResourceURIEmpty) {
		t.Errorf("err = %v, want %v", err, errResourceURIEmpty)
	}
}

func TestNewResource_EmptyName(t *testing.T) {
	_, err := NewResource("file:///x", "", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	if !errors.Is(err, errResourceNameEmpty) {
		t.Errorf("err = %v, want %v", err, errResourceNameEmpty)
	}
}

func TestNewResource_NilHandler(t *testing.T) {
	_, err := NewResource("file:///x", "name", nil)
	if !errors.Is(err, errResourceHandlerNil) {
		t.Errorf("err = %v, want %v", err, errResourceHandlerNil)
	}
}

// ── Resource template creation ──────────────────────────────────────

func TestNewResourceTemplate_Valid(t *testing.T) {
	handler := func(_ context.Context, uri string) ([]ResourceContent, error) {
		return []ResourceContent{NewTextResourceContent(uri, "data")}, nil
	}

	tmpl, err := NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		handler,
		WithTemplateDescription("Daily log"),
		WithTemplateMimeType("text/plain"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.URITemplate != "file:///logs/{date}.log" {
		t.Errorf("URITemplate = %q", tmpl.URITemplate)
	}
	if tmpl.Name != "daily-log" {
		t.Errorf("Name = %q", tmpl.Name)
	}
}

func TestNewResourceTemplate_EmptyURI(t *testing.T) {
	_, err := NewResourceTemplate("", "name", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	if !errors.Is(err, errTemplateURIEmpty) {
		t.Errorf("err = %v, want %v", err, errTemplateURIEmpty)
	}
}

func TestNewResourceTemplate_EmptyName(t *testing.T) {
	_, err := NewResourceTemplate("file:///x/{id}", "", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	if !errors.Is(err, errTemplateNameEmpty) {
		t.Errorf("err = %v, want %v", err, errTemplateNameEmpty)
	}
}

func TestNewResourceTemplate_NilHandler(t *testing.T) {
	_, err := NewResourceTemplate("file:///x/{id}", "name", nil)
	if !errors.Is(err, errTemplateHandlerNil) {
		t.Errorf("err = %v, want %v", err, errTemplateHandlerNil)
	}
}

// ── Server registration ────────────────────────────────────────────

func TestRegisterResource_OK(t *testing.T) {
	s := NewServer("test", "1.0")
	r, _ := NewResource("file:///a", "a", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	if err := s.RegisterResource(r); err != nil {
		t.Fatal(err)
	}
	if got := s.ListResources(); len(got) != 1 {
		t.Errorf("resource count = %d, want 1", len(got))
	}
}

func TestRegisterResource_Nil(t *testing.T) {
	s := NewServer("test", "1.0")
	if err := s.RegisterResource(nil); !errors.Is(err, errResourceNil) {
		t.Errorf("err = %v, want %v", err, errResourceNil)
	}
}

func TestRegisterResource_Duplicate(t *testing.T) {
	s := NewServer("test", "1.0")
	r, _ := NewResource("file:///a", "a", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	s.RegisterResource(r)
	if err := s.RegisterResource(r); !errors.Is(err, errResourceAlreadyExists) {
		t.Errorf("err = %v, want %v", err, errResourceAlreadyExists)
	}
}

func TestRegisterResource_EmptyURI(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &Resource{
		URI:  "",
		Name: "name",
		Handler: func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, nil
		},
	}
	if err := s.RegisterResource(r); !errors.Is(err, errResourceURIEmpty) {
		t.Errorf("err = %v, want %v", err, errResourceURIEmpty)
	}
}

func TestRegisterResource_EmptyName(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &Resource{
		URI:  "file:///x",
		Name: "",
		Handler: func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, nil
		},
	}
	if err := s.RegisterResource(r); !errors.Is(err, errResourceNameEmpty) {
		t.Errorf("err = %v, want %v", err, errResourceNameEmpty)
	}
}

func TestRegisterResource_NilHandler(t *testing.T) {
	s := NewServer("test", "1.0")
	r := &Resource{
		URI:     "file:///x",
		Name:    "name",
		Handler: nil,
	}
	if err := s.RegisterResource(r); !errors.Is(err, errResourceHandlerNil) {
		t.Errorf("err = %v, want %v", err, errResourceHandlerNil)
	}
}

func TestRegisterResourceTemplate_OK(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl, _ := NewResourceTemplate("file:///logs/{date}", "logs", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	if err := s.RegisterResourceTemplate(tmpl); err != nil {
		t.Fatal(err)
	}
	if got := s.ListResourceTemplates(); len(got) != 1 {
		t.Errorf("template count = %d, want 1", len(got))
	}
}

func TestRegisterResourceTemplate_Nil(t *testing.T) {
	s := NewServer("test", "1.0")
	if err := s.RegisterResourceTemplate(nil); !errors.Is(err, errTemplateNil) {
		t.Errorf("err = %v, want %v", err, errTemplateNil)
	}
}

func TestRegisterResourceTemplate_Duplicate(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl, _ := NewResourceTemplate("file:///logs/{date}", "logs", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	s.RegisterResourceTemplate(tmpl)
	if err := s.RegisterResourceTemplate(tmpl); !errors.Is(err, errTemplateAlreadyExists) {
		t.Errorf("err = %v, want %v", err, errTemplateAlreadyExists)
	}
}

func TestRegisterResourceTemplate_EmptyURI(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl := &ResourceTemplate{
		URITemplate: "",
		Name:        "logs",
		Handler: func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, nil
		},
	}
	if err := s.RegisterResourceTemplate(tmpl); !errors.Is(err, errTemplateURIEmpty) {
		t.Errorf("err = %v, want %v", err, errTemplateURIEmpty)
	}
}

func TestRegisterResourceTemplate_EmptyName(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl := &ResourceTemplate{
		URITemplate: "file:///logs/{date}",
		Name:        "",
		Handler: func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, nil
		},
	}
	if err := s.RegisterResourceTemplate(tmpl); !errors.Is(err, errTemplateNameEmpty) {
		t.Errorf("err = %v, want %v", err, errTemplateNameEmpty)
	}
}

func TestRegisterResourceTemplate_NilHandler(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl := &ResourceTemplate{
		URITemplate: "file:///logs/{date}",
		Name:        "logs",
		Handler:     nil,
	}
	if err := s.RegisterResourceTemplate(tmpl); !errors.Is(err, errTemplateHandlerNil) {
		t.Errorf("err = %v, want %v", err, errTemplateHandlerNil)
	}
}

// ── ReadResource ────────────────────────────────────────────────────

func TestReadResource_OK(t *testing.T) {
	s := NewServer("test", "1.0")
	r, _ := NewResource("file:///hello", "hello", func(_ context.Context, uri string) ([]ResourceContent, error) {
		return []ResourceContent{NewTextResourceContent(uri, "world")}, nil
	})
	s.RegisterResource(r)

	result, err := s.ReadResource(context.Background(), "file:///hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(result.Contents))
	}
	if result.Contents[0].Text == nil || *result.Contents[0].Text != "world" {
		t.Errorf("text = %v, want %q", result.Contents[0].Text, "world")
	}
}

func TestReadResource_NotFound(t *testing.T) {
	s := NewServer("test", "1.0")
	_, err := s.ReadResource(context.Background(), "file:///nope")
	if !errors.Is(err, errResourceNotFound) {
		t.Errorf("err = %v, want %v", err, errResourceNotFound)
	}
}

func TestReadResource_HandlerError(t *testing.T) {
	s := NewServer("test", "1.0")
	handlerErr := errors.New("read failure")
	r, _ := NewResource("file:///fail", "fail", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, handlerErr
	})
	s.RegisterResource(r)

	_, err := s.ReadResource(context.Background(), "file:///fail")
	if !errors.Is(err, handlerErr) {
		t.Errorf("err = %v, want %v", err, handlerErr)
	}
}

func TestReadResource_UsesTemplateWhenNoStaticResource(t *testing.T) {
	s := NewServer("test", "1.0")

	var calledWith string
	tmpl, err := NewResourceTemplate("file:///logs/{date}.log", "daily-log", func(_ context.Context, uri string) ([]ResourceContent, error) {
		calledWith = uri
		return []ResourceContent{NewTextResourceContent(uri, "log data")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResourceTemplate(tmpl); err != nil {
		t.Fatal(err)
	}

	uri := "file:///logs/2025-01-01.log"
	result, err := s.ReadResource(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	if calledWith != uri {
		t.Errorf("handler called with %q, want %q", calledWith, uri)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(result.Contents))
	}
	if result.Contents[0].Text == nil || *result.Contents[0].Text != "log data" {
		t.Errorf("text = %v, want %q", result.Contents[0].Text, "log data")
	}
	if result.Contents[0].URI != uri {
		t.Errorf("URI = %q, want %q", result.Contents[0].URI, uri)
	}
}

func TestReadResource_TemplateNoMatchStillNotFound(t *testing.T) {
	s := NewServer("test", "1.0")

	tmpl, err := NewResourceTemplate("file:///logs/{date}.log", "daily-log", func(_ context.Context, uri string) ([]ResourceContent, error) {
		return []ResourceContent{NewTextResourceContent(uri, "log data")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResourceTemplate(tmpl); err != nil {
		t.Fatal(err)
	}

	_, err = s.ReadResource(context.Background(), "file:///logs/")
	if !errors.Is(err, errResourceNotFound) {
		t.Errorf("err = %v, want %v", err, errResourceNotFound)
	}
}

func TestReadResource_TemplateMultipleSegments(t *testing.T) {
	s := NewServer("test", "1.0")

	var calledWith string
	tmpl, _ := NewResourceTemplate("https://api.example.com/repos/{owner}/{repo}", "repo",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			calledWith = uri
			return []ResourceContent{NewTextResourceContent(uri, "repo data")}, nil
		})
	s.RegisterResourceTemplate(tmpl)

	uri := "https://api.example.com/repos/alice/myapp"
	result, err := s.ReadResource(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	if calledWith != uri {
		t.Errorf("handler called with %q, want %q", calledWith, uri)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(result.Contents))
	}
}

func TestReadResource_StaticWinsOverTemplate(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register a template that could match our URI.
	tmpl, _ := NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "template")}, nil
		})
	s.RegisterResourceTemplate(tmpl)

	// Register a static resource with the exact URI.
	r, _ := NewResource("file:///logs/2025-01-01.log", "specific-log",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "static")}, nil
		})
	s.RegisterResource(r)

	// Static must win.
	result, err := s.ReadResource(context.Background(), "file:///logs/2025-01-01.log")
	if err != nil {
		t.Fatal(err)
	}
	if result.Contents[0].Text == nil || *result.Contents[0].Text != "static" {
		t.Errorf("expected static resource to win, got %v", result.Contents[0].Text)
	}
}

func TestReadResource_TemplateHandlerError(t *testing.T) {
	s := NewServer("test", "1.0")
	handlerErr := errors.New("template read failure")

	tmpl, _ := NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, handlerErr
		})
	s.RegisterResourceTemplate(tmpl)

	_, err := s.ReadResource(context.Background(), "file:///logs/2025-01-01.log")
	if !errors.Is(err, handlerErr) {
		t.Errorf("err = %v, want %v", err, handlerErr)
	}
}

func TestRegisterResourceTemplate_InvalidBraces(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl := &ResourceTemplate{
		URITemplate: "file:///logs/{date",
		Name:        "broken-template",
		Handler: func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, nil
		},
	}
	if err := s.RegisterResourceTemplate(tmpl); !errors.Is(err, errTemplateInvalid) {
		t.Errorf("err = %v, want %v", err, errTemplateInvalid)
	}
}

func TestRegisterResourceTemplate_EmptyPlaceholder(t *testing.T) {
	s := NewServer("test", "1.0")
	tmpl := &ResourceTemplate{
		URITemplate: "file:///logs/{}.log",
		Name:        "empty-placeholder",
		Handler: func(_ context.Context, _ string) ([]ResourceContent, error) {
			return nil, nil
		},
	}
	if err := s.RegisterResourceTemplate(tmpl); !errors.Is(err, errTemplateInvalid) {
		t.Errorf("err = %v, want %v", err, errTemplateInvalid)
	}
}

// ── Dispatch integration tests ──────────────────────────────────────

func TestHandleMessage_ResourcesList(t *testing.T) {
	s := initServerWithResource(t)

	resp := jsonrpcCall(t, s, 2, "resources/list", nil)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result ListResourcesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("resource count = %d, want 1", len(result.Resources))
	}
	if result.Resources[0].URI != "file:///hello" {
		t.Errorf("URI = %q, want %q", result.Resources[0].URI, "file:///hello")
	}
	if result.Resources[0].Name != "hello" {
		t.Errorf("Name = %q, want %q", result.Resources[0].Name, "hello")
	}
}

func TestHandleMessage_ResourcesRead(t *testing.T) {
	s := initServerWithResource(t)

	resp := jsonrpcCall(t, s, 2, "resources/read", map[string]any{
		"uri": "file:///hello",
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result ReadResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(result.Contents))
	}
	if result.Contents[0].Text == nil || *result.Contents[0].Text != "world" {
		t.Errorf("text = %v, want %q", result.Contents[0].Text, "world")
	}
	if result.Contents[0].URI != "file:///hello" {
		t.Errorf("URI = %q, want %q", result.Contents[0].URI, "file:///hello")
	}
}

func TestHandleMessage_ResourcesRead_NotFound(t *testing.T) {
	s := initServerWithResource(t)

	resp := jsonrpcCall(t, s, 2, "resources/read", map[string]any{
		"uri": "file:///nope",
	})
	if resp.Error == nil {
		t.Fatal("expected error for unknown resource")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_ResourcesRead_EmptyURI(t *testing.T) {
	s := initServerWithResource(t)

	resp := jsonrpcCall(t, s, 2, "resources/read", map[string]any{
		"uri": "",
	})
	if resp.Error == nil {
		t.Fatal("expected error for empty URI")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_ResourcesRead_MissingParams(t *testing.T) {
	s := initServerWithResource(t)

	data := jsonrpcReq(2, "resources/read", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing params")
	}
}

func TestHandleMessage_ResourcesRead_HandlerError(t *testing.T) {
	s := initServer(t)

	r, _ := NewResource("file:///fail", "fail", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, errors.New("handler exploded")
	})
	s.RegisterResource(r)

	resp := jsonrpcCall(t, s, 2, "resources/read", map[string]any{
		"uri": "file:///fail",
	})
	if resp.Error == nil {
		t.Fatal("expected error from resource handler failure")
	}
	// Handler errors should be InternalError, not InvalidParams.
	if resp.Error.Code != ErrCodeInternalError {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrCodeInternalError)
	}
	// Error message should not leak internal details.
	if resp.Error.Message == "handler exploded" {
		t.Error("error message should not expose raw internal error")
	}
}

func TestHandleMessage_ResourcesRead_ViaTemplate(t *testing.T) {
	s := initServer(t)

	tmpl, _ := NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "log content for "+uri)}, nil
		},
	)
	s.RegisterResourceTemplate(tmpl)

	resp := jsonrpcCall(t, s, 3, "resources/read", map[string]any{
		"uri": "file:///logs/2025-01-15.log",
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result ReadResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(result.Contents))
	}
	if result.Contents[0].URI != "file:///logs/2025-01-15.log" {
		t.Errorf("URI = %q", result.Contents[0].URI)
	}
	wantText := "log content for file:///logs/2025-01-15.log"
	if result.Contents[0].Text == nil || *result.Contents[0].Text != wantText {
		t.Errorf("Text = %v, want %q", result.Contents[0].Text, wantText)
	}
}

func TestHandleMessage_ResourcesRead_ViaTemplate_NotFound(t *testing.T) {
	s := initServer(t)

	// Register a template that won't match the requested URI.
	tmpl, _ := NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "data")}, nil
		},
	)
	s.RegisterResourceTemplate(tmpl)

	resp := jsonrpcCall(t, s, 3, "resources/read", map[string]any{
		"uri": "file:///other/path.txt",
	})
	if resp.Error == nil {
		t.Fatal("expected error for URI not matching any template")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("code = %d, want %d (InvalidParams)", resp.Error.Code, ErrCodeInvalidParams)
	}
}

func TestHandleMessage_ResourcesTemplatesList(t *testing.T) {
	s := initServer(t)

	tmpl, _ := NewResourceTemplate("file:///logs/{date}.log", "daily-log",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "log data")}, nil
		},
		WithTemplateDescription("Daily log file"),
		WithTemplateMimeType("text/plain"),
	)
	s.RegisterResourceTemplate(tmpl)

	resp := jsonrpcCall(t, s, 2, "resources/templates/list", nil)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result ListResourceTemplatesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.ResourceTemplates) != 1 {
		t.Fatalf("template count = %d, want 1", len(result.ResourceTemplates))
	}
	if result.ResourceTemplates[0].URITemplate != "file:///logs/{date}.log" {
		t.Errorf("URITemplate = %q", result.ResourceTemplates[0].URITemplate)
	}
	if result.ResourceTemplates[0].Name != "daily-log" {
		t.Errorf("Name = %q", result.ResourceTemplates[0].Name)
	}
}

func TestHandleMessage_ResourcesList_BeforeInit(t *testing.T) {
	s := NewServer("test", "1.0")
	r, _ := NewResource("file:///a", "a", func(_ context.Context, _ string) ([]ResourceContent, error) {
		return nil, nil
	})
	s.RegisterResource(r)

	data := jsonrpcReq(1, "resources/list", nil)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error before init")
	}
}

func TestHandleMessage_Initialize_AdvertisesResources(t *testing.T) {
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

	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}

	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	if result.Capabilities.Resources == nil {
		t.Fatal("capabilities.resources should not be nil")
	}
}

// ── Resource content tests ──────────────────────────────────────────

func TestNewTextResourceContent(t *testing.T) {
	c := NewTextResourceContent("file:///a", "hello")
	if c.URI != "file:///a" {
		t.Errorf("URI = %q", c.URI)
	}
	if c.Text == nil || *c.Text != "hello" {
		t.Errorf("Text = %v, want %q", c.Text, "hello")
	}
	if c.Blob != nil {
		t.Error("Blob should be nil for text content")
	}
}

func TestNewBlobResourceContent(t *testing.T) {
	c := NewBlobResourceContent("file:///bin", []byte{0x00, 0xFF})
	if c.URI != "file:///bin" {
		t.Errorf("URI = %q", c.URI)
	}
	if c.Blob == nil || *c.Blob != "AP8=" { // base64 of 0x00 0xFF
		t.Errorf("Blob = %v, want %q", c.Blob, "AP8=")
	}
	if c.Text != nil {
		t.Error("Text should be nil for blob content")
	}
}

func TestResourceContent_JSON(t *testing.T) {
	c := NewTextResourceContent("file:///a", "hello")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}

	var decoded ResourceContent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.URI != "file:///a" || decoded.Text == nil || *decoded.Text != "hello" {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestResourceContent_JSON_EmptyText(t *testing.T) {
	// An empty string is valid text content; it must not be omitted.
	c := NewTextResourceContent("file:///empty", "")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	// The JSON must contain "text":"", not omit the field.
	if !json.Valid(data) {
		t.Fatalf("invalid JSON: %s", data)
	}
	var decoded ResourceContent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Text == nil {
		t.Fatal("Text should be non-nil for empty text content")
	}
	if *decoded.Text != "" {
		t.Errorf("Text = %q, want empty string", *decoded.Text)
	}
}

func TestResourceContent_JSON_BothSetErrors(t *testing.T) {
	text := "hello"
	blob := "AAAA"
	c := ResourceContent{URI: "file:///bad", Text: &text, Blob: &blob}
	_, err := json.Marshal(c)
	if err == nil {
		t.Fatal("expected error when both Text and Blob are set")
	}
}

func TestResourceContent_JSON_NeitherSetErrors(t *testing.T) {
	c := ResourceContent{URI: "file:///bad"}
	_, err := json.Marshal(c)
	if err == nil {
		t.Fatal("expected error when neither Text nor Blob is set")
	}
}

// ── Test helpers ────────────────────────────────────────────────────

func initServerWithResource(t *testing.T) *Server {
	t.Helper()
	s := initServer(t)

	r, err := NewResource("file:///hello", "hello",
		func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{NewTextResourceContent(uri, "world")}, nil
		},
		WithResourceDescription("Hello resource"),
		WithResourceMimeType("text/plain"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(r); err != nil {
		t.Fatal(err)
	}

	return s
}

// jsonrpcCall is a helper that marshals params, sends a JSON-RPC request, and returns the response.
func jsonrpcCall(t *testing.T, s *Server, id int, method string, params any) *JSONRPCResponse {
	t.Helper()
	data := jsonrpcReq(id, method, params)
	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatalf("HandleMessage(%s): %v", method, err)
	}
	if resp == nil {
		t.Fatalf("HandleMessage(%s): nil response", method)
	}
	return resp
}

func TestReadResourceResultFromText(t *testing.T) {
	t.Parallel()
	result := ReadResourceResultFromText("file:///hello.txt", "hello world")
	if result == nil {
		t.Fatal("ReadResourceResultFromText returned nil")
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Contents))
	}
	if result.Contents[0].URI != "file:///hello.txt" {
		t.Errorf("URI = %q, want file:///hello.txt", result.Contents[0].URI)
	}
	if result.Contents[0].Text == nil || *result.Contents[0].Text != "hello world" {
		t.Errorf("Text = %v, want %q", result.Contents[0].Text, "hello world")
	}
}
