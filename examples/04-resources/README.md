# 04 — Resources

Demonstrates how to expose data as MCP resources that clients can read and subscribe to.

## How It Works

Resources are **read-only data endpoints** identified by URIs. Unlike tools (which perform actions), resources provide data the client can fetch. Use cases:

- Configuration files
- Database records
- Live metrics
- File contents

```
Client sends:  resources/read { uri: "config://app/settings" }
Server runs:   handler(ctx, uri) → []ResourceContent
Server returns: { contents: [{ uri: "...", text: "{\"theme\": \"dark\"}" }] }
```

### Resource Types

| Type | Description |
|------|-------------|
| **Static Resource** | Fixed URI like `config://app/settings` |
| **Resource Template** | URI pattern like `users://{id}` with variables (RFC 6570) |

### Content Types

```go
// Text content (JSON, YAML, plain text, etc.)
finemcp.NewTextResourceContent(uri, `{"theme": "dark"}`)

// Binary content (images, files, etc.)
finemcp.NewBlobResourceContent(uri, []byte{0x89, 0x50, 0x4E, 0x47})
```

## Examples

### static/

Fixed-URI resources:

```go
res, _ := finemcp.NewResource(
    "config://app/settings",         // URI
    "Application Settings",          // Display name
    func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
        return []finemcp.ResourceContent{
            finemcp.NewTextResourceContent(uri, `{"theme": "dark", "lang": "en"}`),
        }, nil
    },
    finemcp.WithResourceDescription("Current application settings"),
    finemcp.WithResourceMimeType("application/json"),
)
s.RegisterResource(res)
```

**Run:** `go run ./04-resources/static`

### template/

URI templates with variables and auto-completion:

```go
tmpl, _ := finemcp.NewResourceTemplate(
    "users://{id}",             // URI template (RFC 6570)
    "User Profile",
    func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
        return []finemcp.ResourceContent{
            finemcp.NewTextResourceContent(uri, fmt.Sprintf(`{"id": "%s"}`, uri)),
        }, nil
    },
    finemcp.WithTemplateDescription("Fetch a user profile by ID"),
    finemcp.WithTemplateMimeType("application/json"),
)
s.RegisterResourceTemplate(tmpl)
```

With auto-completion for template variables:

```go
finemcp.WithTemplateCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
    return &finemcp.CompletionResult{
        Values: []string{"README.md", "go.mod", "main.go"},
    }, nil
})
```

**Run:** `go run ./04-resources/template`

### subscriptions/

Resources that notify clients when data changes:

```go
// Enable subscriptions on the server
s := finemcp.NewServer("subs", "1.0.0",
    finemcp.WithResourceSubscriptions(),
)

// Register a resource
s.RegisterResource(counterResource)

// When data changes, notify subscribers
s.NotifyResourceUpdated("metrics://counter")
```

The `bump` tool increments the counter and triggers a notification:

```go
bumpTool, _ := finemcp.NewTool("bump",
    func(ctx context.Context, input []byte) ([]byte, error) {
        counter.Add(1)
        s.NotifyResourceUpdated("metrics://counter")
        return []byte("Counter bumped"), nil
    },
)
```

**Run:** `go run ./04-resources/subscriptions`

## Resource Options

| Option | Description |
|--------|-------------|
| `WithResourceDescription(d)` | Human-readable description |
| `WithResourceMimeType(m)` | MIME type (e.g. `application/json`, `image/png`) |
| `WithTemplateDescription(d)` | Description for templates |
| `WithTemplateMimeType(m)` | MIME type for templates |
| `WithTemplateCompleter(fn)` | Auto-completion for template URI variables |

## Testing with curl

```bash
go run ./04-resources/static

# List resources
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "resources/list"
}'

# Read a resource
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "resources/read",
  "params": { "uri": "config://app/settings" }
}'

# List resource templates
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 4, "method": "resources/templates/list"
}'
```
