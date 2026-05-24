---
url: "/docs/concepts/server/"
title: "Server"
description: "Creating and configuring the MCP server"
weight: 1
---

The `Server` is the central object in finemcp. It holds all registered tools, resources, prompts, roots, and middleware — and handles the MCP JSON-RPC protocol.

## Creating a Server

```go
s := finemcp.NewServer("my-server", "1.0.0")
```

The constructor takes a server name and version, plus optional functional options:

```go
s := finemcp.NewServer("my-server", "1.0.0",
    finemcp.WithResourceSubscriptions(),
    finemcp.WithStreamBufferSize(64),
    finemcp.WithMaxSessions(5000),
)
```

## Server Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithInstructions(text)` | `""` | Human-readable instructions sent to the client on initialize |
| `WithServerTitle(title)` | `""` | Human-readable display title for the server |
| `WithServerDescription(desc)` | `""` | Human-readable description included in the initialize response |
| `WithWebsiteURL(url)` | `""` | Website URL included in the initialize response |
| `WithIcons(icons...)` | nil | Icons (logo/thumbnail/favicon) for the server |
| `WithLifespan(fn)` | nil | Hook called once the server is initialized; use for startup/shutdown logic |
| `WithResourceSubscriptions()` | disabled | Enable `resources/subscribe` and change notifications |
| `WithTaskStore(ts)` | nil | Attach a `TaskStore` for spec-compliant async task management |
| `WithSupportedVersions(v...)` | `2025-11-25`, `2025-03-26`, `2024-11-05` | Override supported protocol versions |
| `WithMaxSessionTools(n)` | 100 | Max session-scoped tools per session |
| `WithMaxSessions(n)` | 10000 | Max concurrent sessions |
| `WithStreamBufferSize(n)` | 16 | Channel buffer size for streaming chunks |
| `WithMaxNotificationMethods(n)` | 1000 | Max distinct notification methods that can be registered |
| `WithMaxHandlersPerNotification(n)` | 100 | Max handlers per notification method |
| `WithNotificationPanicHandler(h)` | nil | Custom handler for panics inside notification handlers |

## Registering Components

### Tools

```go
tool, _ := finemcp.NewTool("ping", handler, finemcp.WithDescription("Health check"))
s.RegisterTool(tool)

// Register multiple at once
s.RegisterTools(tool1, tool2, tool3)

// Remove a tool
s.RemoveTool("ping")

// List all tools
tools := s.ListTools()
```

### Resources

```go
res, _ := finemcp.NewResource("config://app", "Config", handler)
s.RegisterResource(res)

tmpl, _ := finemcp.NewResourceTemplate("user://{id}", "User", handler)
s.RegisterResourceTemplate(tmpl)
```

### Prompts

```go
prompt, _ := finemcp.NewPrompt("greet", handler)
s.RegisterPrompt(prompt)
```

### Roots

```go
root, _ := finemcp.NewRoot("file:///workspace", finemcp.WithRootName("Workspace"))
s.RegisterRoot(root)
```

## Middleware

Apply middleware that wraps all tool invocations:

```go
s.Use(middleware.Recovery())
s.Use(middleware.Logging(logger))
s.Use(middleware.RateLimit(10))
```

Middleware executes in registration order (first registered = outermost wrapper). See [Middleware]({{< relref "/middleware" >}}) for details.

## Authentication

Set a protocol-level auth checker that runs before any tool call:

```go
s.SetAuthChecker(middleware.RequireAuth())
```

For HTTP-level authentication (bearer tokens, API keys), wrap the transport handler:

```go
handler := middleware.HTTPAuth(verifier, transport.Handler(s))
```

See [Auth Middleware]({{< relref "/middleware/auth" >}}) for full details.

## Multi-Tenancy

Set a tenant resolver for per-tenant tool/resource filtering:

```go
s.SetTenantResolver(middleware.NewTenantResolver(extractor, store))
```

See [Multi-Tenant Middleware]({{< relref "/middleware/multitenant" >}}) for details.

## Notifications

Notify connected clients when server state changes:

```go
s.NotifyToolsListChanged()
s.NotifyResourcesListChanged()
s.NotifyPromptsListChanged()
s.NotifyRootsListChanged()
s.NotifyResourceUpdated("config://app/settings")
```

## Session Lifecycle

MCP follows a strict session lifecycle:

1. **Client sends `initialize`** — with `protocolVersion`, `clientInfo`, and `capabilities`
2. **Server responds** — with negotiated version and server capabilities
3. **Client sends `initialized`** notification — session is now active
4. **Normal operations** — `tools/call`, `resources/read`, `prompts/get`, etc.
5. **Shutdown** — `s.Shutdown(ctx)` sends close notifications

Each session allows exactly one `initialize` call. Subsequent initialize calls return an error.

## Accessors

```go
s.Name()               // "my-server"
s.Version()            // "1.0.0"
s.NegotiatedVersion()  // "2025-11-25" (after initialization)
```

## Shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
s.Shutdown(ctx)
```
