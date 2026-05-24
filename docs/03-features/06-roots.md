---
url: "/docs/features/roots/"
title: "Roots"
description: "Root URIs that define the content boundaries visible to the server"
weight: 6
---

Roots are URI-based entry points that tell clients what content namespaces the server operates on.

## Creating Roots

```go
root, err := finemcp.NewRoot("file:///workspace/project",
    finemcp.WithRootName("Project Files"),
)
s.RegisterRoot(root)
```

## Root Options

| Option | Description |
|--------|-------------|
| `WithRootName(name)` | Human-readable display name |

## Listing Roots

```go
for _, r := range s.ListRoots() {
    fmt.Printf("URI: %s, Name: %s\n", r.URI, r.Name)
}
```

## Example: Tool That Lists Roots

```go
tool, _ := finemcp.NewTool("list-roots",
    func(ctx context.Context, input []byte) ([]byte, error) {
        roots := s.ListRoots()
        var lines string
        for _, r := range roots {
            lines += fmt.Sprintf("- %s (%s)\n", r.URI, r.Name)
        }
        return []byte(lines), nil
    },
)
```

## Change Notifications

Notify clients when roots change:

```go
s.RegisterRoot(newRoot)
s.NotifyRootsListChanged()
```
