---
url: "/docs/concepts/resources/"
title: "Resources"
description: "Expose data through static resources, URI templates, and subscriptions"
weight: 3
---

Resources expose data that clients can read. They use URIs as identifiers and support static content, dynamic templates, and real-time change subscriptions.

## Static Resources

A static resource has a fixed URI:

```go
res, err := finemcp.NewResource(
    "config://app/settings",
    "App Settings",
    func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
        return []finemcp.ResourceContent{
            finemcp.NewTextResourceContent(uri, `{"theme": "dark"}`),
        }, nil
    },
    finemcp.WithResourceDescription("Application configuration"),
    finemcp.WithResourceMimeType("application/json"),
)
s.RegisterResource(res)
```

Clients discover resources with `resources/list` and read them with `resources/read`.

## Resource Templates

Templates use [RFC 6570](https://tools.ietf.org/html/rfc6570) URI templates with `{variable}` placeholders:

```go
tmpl, err := finemcp.NewResourceTemplate(
    "user://{id}/profile",
    "User Profile",
    func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
        // uri is the resolved URI, e.g., "user://123/profile"
        // Extract the ID from the URI as needed
        return []finemcp.ResourceContent{
            finemcp.NewTextResourceContent(uri, `{"name": "Alice"}`),
        }, nil
    },
    finemcp.WithTemplateDescription("User profile data"),
    finemcp.WithTemplateMimeType("application/json"),
)
s.RegisterResourceTemplate(tmpl)
```

Clients discover templates with `resourceTemplates/list` and can request `completion/complete` for variable auto-completion.

### Template Auto-Completion

Attach a completer to suggest values for template variables:

```go
tmpl, _ := finemcp.NewResourceTemplate(
    "city://{name}",
    "City Data",
    handler,
    finemcp.WithTemplateCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
        prefix := strings.ToLower(req.Argument.Value)
        var matches []string
        for _, city := range allCities {
            if strings.HasPrefix(strings.ToLower(city), prefix) {
                matches = append(matches, city)
            }
        }
        return &finemcp.CompletionResult{Values: matches}, nil
    }),
)
```

## Resource Content

Two content types are available:

```go
// Text content
finemcp.NewTextResourceContent(uri, "plain text or JSON string")

// Binary content (base64-encoded)
finemcp.NewBlobResourceContent(uri, binaryData)
```

A handler can return multiple content items:

```go
func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
    return []finemcp.ResourceContent{
        finemcp.NewTextResourceContent(uri, "main content"),
        finemcp.NewTextResourceContent(uri+"/meta", `{"version": 2}`),
    }, nil
}
```

## Resource Options

### Static Resource Options

| Option | Description |
|--------|-------------|
| `WithResourceDescription(d)` | Human-readable description |
| `WithResourceMimeType(m)` | MIME type (e.g., `"application/json"`) |

### Template Options

| Option | Description |
|--------|-------------|
| `WithTemplateDescription(d)` | Human-readable description |
| `WithTemplateMimeType(m)` | MIME type |
| `WithTemplateCompleter(fn)` | Auto-completion for template variables |

## Subscriptions

Enable clients to subscribe to resource changes. First, enable subscriptions on the server:

```go
s := finemcp.NewServer("my-server", "1.0.0",
    finemcp.WithResourceSubscriptions(),
)
```

When a resource changes, notify subscribers:

```go
// Trigger from a tool handler, timer, or any event
s.NotifyResourceUpdated("config://app/settings")
```

Clients subscribe with `resources/subscribe` and receive `notifications/resources/updated` when the resource changes. They can then re-read the resource to get the new content.

Example: a tool that modifies a resource and triggers a notification:

```go
counter := 0
tool, _ := finemcp.NewTool("increment",
    func(ctx context.Context, input []byte) ([]byte, error) {
        counter++
        s.NotifyResourceUpdated("counter://value")
        return []byte(fmt.Sprintf("Counter: %d", counter)), nil
    },
)
```

## Dynamic Resource Management

```go
s.RegisterResource(res)
s.RegisterResourceTemplate(tmpl)

// List registered resources
resources := s.ListResources()
templates := s.ListResourceTemplates()

// Notify clients when the resource list changes
s.NotifyResourcesListChanged()
```
