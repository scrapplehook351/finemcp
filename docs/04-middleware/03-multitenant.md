---
url: "/docs/middleware/multitenant/"
title: "Multi-Tenant"
description: "Per-tenant tool and resource filtering"
weight: 3
---

The multi-tenant middleware isolates tenants by filtering which tools, resources, templates, and prompts each tenant can access.

## Usage

```go
import "github.com/finemcp/finemcp/middleware"

// Define how to extract tenant ID from context
extractor := middleware.TenantFromAuthMeta("tenant_id")

// Define per-tenant access rules
store := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
    "tenant-a": {
        ToolFilter: func(name string) bool { return name != "dangerous-tool" },
    },
    "tenant-b": {
        ToolFilter: func(name string) bool { return name == "safe-tool" },
    },
})

// Set on server
s.SetTenantResolver(middleware.NewTenantResolver(extractor, store))
```

## Tenant Extractors

| Function | Extracts From |
|----------|---------------|
| `TenantFromAuthSubject()` | `AuthInfo.Subject` |
| `TenantFromAuthMeta(key)` | `AuthInfo.Meta[key]` |

Custom extractors:

```go
extractor := func(ctx context.Context) string {
    // Custom logic to determine tenant
    return "tenant-id"
}
```

## TenantConfig

```go
type TenantConfig struct {
    ToolFilter             func(string) bool  // Filter tools by name
    ResourceFilter         func(string) bool  // Filter resources by URI
    ResourceTemplateFilter func(string) bool  // Filter templates by URI
    PromptFilter           func(string) bool  // Filter prompts by name
    Metadata               map[string]any     // Tenant metadata
}
```

Return `true` to allow, `false` to deny.

## Tenant Stores

### StaticTenantStore

In-memory static configuration:

```go
store := middleware.NewStaticTenantStore(configs)
```

### Custom Store

Implement the `TenantStore` interface:

```go
type TenantStore interface {
    Lookup(ctx context.Context, tenantID string) (*TenantConfig, error)
}
```

## Options

| Option | Description |
|--------|-------------|
| `WithFallbackTenant(id)` | Default tenant when extraction fails |

## Accessing Tenant in Handlers

```go
tenantID := finemcp.TenantIDFromCtx(ctx)
```
