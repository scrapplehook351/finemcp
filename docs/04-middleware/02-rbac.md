---
url: "/docs/middleware/rbac/"
title: "RBAC"
description: "Role-based access control for tools"
weight: 2
---

The RBAC middleware restricts tool access based on user roles from the auth context.

## Usage

```go
s.Use(middleware.RBAC())
```

Tools declare required roles:

```go
adminTool, _ := finemcp.NewTool("delete-user", handler,
    finemcp.WithRoles("admin"),
)

editorTool, _ := finemcp.NewTool("edit-post", handler,
    finemcp.WithRoles("admin", "editor"), // Either role grants access
)

publicTool, _ := finemcp.NewTool("search", handler)
// No roles = public access
```

## How It Works

1. User authenticates → `AuthInfo` with roles is attached to context
2. User calls a tool → RBAC checks if any of the user's roles match the tool's required roles
3. **Match** → tool executes normally
4. **No match** → returns an error message
5. **No roles on tool** → tool is public, always accessible

## Custom Denied Handler

Customize the error message when access is denied:

```go
s.Use(middleware.RBACWithDenied(func(ctx context.Context, required, actual []string) string {
    return fmt.Sprintf("Access denied. Required: %v, your roles: %v", required, actual)
}))
```

## Accessing Roles in Handlers

```go
func handler(ctx context.Context, input []byte) ([]byte, error) {
    // Roles from auth context
    auth := finemcp.AuthInfoFromCtx(ctx)
    if auth != nil {
        fmt.Println("User roles:", auth.Roles)
    }

    // Roles required by this tool
    toolRoles := finemcp.ToolRolesFromCtx(ctx)
    fmt.Println("Tool requires:", toolRoles)

    return []byte("ok"), nil
}
```

## Combining with Auth

RBAC requires authentication to provide roles. Typical setup:

```go
s := finemcp.NewServer("secure", "1.0.0")
s.SetAuthChecker(middleware.RequireAuth())
s.Use(middleware.RBAC())

handler := middleware.HTTPAuth(verifier, transport.Handler(s))
```
