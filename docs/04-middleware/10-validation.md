---
url: "/docs/middleware/validation/"
title: "Validation"
description: "Validate tool inputs against JSON schemas"
weight: 8
---

The validation middleware validates tool inputs against their declared JSON schemas before invoking the handler.

## Usage

```go
s.Use(middleware.Validation())
```

Tools declare schemas with `WithInputSchema`:

```go
tool, _ := finemcp.NewTool("create-user", handler,
    finemcp.WithInputSchema(map[string]any{
        "type": "object",
        "properties": map[string]any{
            "name":  map[string]any{"type": "string", "minLength": 1},
            "email": map[string]any{"type": "string", "format": "email"},
            "age":   map[string]any{"type": "integer", "minimum": 0},
        },
        "required": []string{"name", "email"},
    }),
)
```

If the input doesn't match the schema, the middleware returns an error before the handler runs.

## Typed Tools

Typed tools (`NewTypedTool`) auto-generate schemas from struct tags. Validation works automatically:

```go
type Input struct {
    Name  string `json:"name" description:"User name"`
    Email string `json:"email" description:"Email address"`
}

tool, _ := finemcp.NewTypedTool("create-user",
    func(ctx context.Context, input Input) (string, error) {
        return "created", nil
    },
)
```

## Skipping Validation

Disable validation for specific tools:

```go
tool, _ := finemcp.NewTool("flexible", handler,
    finemcp.WithValidation(false), // Skip validation for this tool
)
```

In handlers:

```go
skip := finemcp.SkipValidationFromCtx(ctx) // true if validation is skipped
```
