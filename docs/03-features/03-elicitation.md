---
url: "/docs/features/elicitation/"
title: "Elicitation"
description: "Server-initiated user prompts for confirmation or input"
weight: 3
---

Elicitation lets a server prompt the user for input through the client's UI. This is useful for confirmations, additional data collection, or interactive workflows.

## How It Works

```
Client → tools/call "delete-file" { path: "/tmp/test.txt" }
Server → elicitation/create { prompt: "Are you sure?" }     ← to client UI
Client → shows dialog → user types "yes"                    ← user interaction
Server → proceeds with deletion                             ← to client
```

## Usage

```go
s := finemcp.NewServer("elicitation-server", "1.0.0")

tool, _ := finemcp.NewTool("delete-file",
    func(ctx context.Context, input []byte) ([]byte, error) {
        var req struct {
            Path string `json:"path"`
        }
        json.Unmarshal(input, &req)

        result, err := s.ElicitUser(ctx, finemcp.ElicitationParams{
            Prompt:  fmt.Sprintf("Delete %q? Type 'yes' to confirm.", req.Path),
            Type:    "text",
            Default: "no",
        })
        if err != nil {
            return nil, fmt.Errorf("elicitation failed: %w", err)
        }

        if result.Cancelled || result.Value != "yes" {
            return []byte("Deletion cancelled."), nil
        }
        return []byte(fmt.Sprintf("Deleted %s", req.Path)), nil
    },
    finemcp.WithDescription("Delete a file with confirmation"),
    finemcp.WithDestructive(),
)
```

## ElicitationParams

| Field | Type | Description |
|-------|------|-------------|
| `Prompt` | `string` | Message shown to the user |
| `Type` | `string` | Input type (e.g., `"text"`) |
| `Default` | `string` | Default value pre-filled |

## ElicitationResult

| Field | Type | Description |
|-------|------|-------------|
| `Value` | `string` | The user's response |
| `Cancelled` | `bool` | Whether the user dismissed the prompt |

## Client Requirements

The client must declare elicitation support:

```json
{
  "capabilities": {
    "elicitation": {}
  }
}
```

{{< callout type="warning" >}}
Elicitation requires a client with a UI. Tools like curl cannot respond to elicitation requests.
{{< /callout >}}
