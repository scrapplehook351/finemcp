---
url: "/docs/features/sampling/"
title: "Sampling"
description: "Server-initiated LLM requests to the client"
weight: 2
---

Sampling lets a server ask the client to perform LLM inference. The server sends a `createMessage` request to the client, which runs the prompt through its LLM and returns the result.

## How It Works

```
Client → tools/call "smart-answer" { question: "What is MCP?" }
Server → sampling/createMessage { messages: [...] }         ← to client
Client → runs LLM inference → returns result                ← from LLM
Server → uses LLM result in tool response                   ← to client
```

This is useful when a tool handler needs LLM reasoning but the server doesn't have direct LLM access.

## Usage

```go
s := finemcp.NewServer("sampling-server", "1.0.0")

tool, _ := finemcp.NewTool("smart-answer",
    func(ctx context.Context, input []byte) ([]byte, error) {
        var req struct {
            Question string `json:"question"`
        }
        json.Unmarshal(input, &req)

        temp := 0.7
        result, err := s.CreateMessage(ctx, finemcp.CreateMessageParams{
            Messages: []finemcp.SamplingMessage{
                {Role: "user", Content: finemcp.TextContent{Text: req.Question}},
            },
            SystemPrompt: "You are a helpful assistant. Be concise.",
            MaxTokens:    256,
            Temperature:  &temp,
            ModelPreferences: &finemcp.ModelPreferences{
                Hints: []finemcp.ModelHint{{Name: "claude"}},
            },
            IncludeContext: "thisServer",
        })
        if err != nil {
            return nil, fmt.Errorf("sampling failed: %w", err)
        }
        return []byte(fmt.Sprintf("Model %s responded: %s", result.Model, result.Role)), nil
    },
)
```

## CreateMessageParams

| Field | Type | Description |
|-------|------|-------------|
| `Messages` | `[]SamplingMessage` | Conversation messages |
| `SystemPrompt` | `string` | System instruction |
| `MaxTokens` | `int` | Maximum response tokens |
| `Temperature` | `*float64` | Sampling temperature (0.0–1.0) |
| `StopSequences` | `[]string` | Stop generation sequences |
| `ModelPreferences` | `*ModelPreferences` | Preferred model configuration |
| `IncludeContext` | `string` | `"thisServer"`, `"allServers"`, or `""` |
| `Metadata` | `map[string]any` | Arbitrary metadata |

## ModelPreferences

```go
&finemcp.ModelPreferences{
    Hints: []finemcp.ModelHint{
        {Name: "claude"},
        {Name: "gpt-4"},
    },
    CostPriority:         floatPtr(0.3),
    SpeedPriority:        floatPtr(0.5),
    IntelligencePriority: floatPtr(0.8),
}
```

Hints are suggestions — the client may use any available model.

## CreateMessageResult

| Field | Type | Description |
|-------|------|-------------|
| `Role` | `string` | Response role (typically `"assistant"`) |
| `Content` | `json.RawMessage` | Response content |
| `Model` | `string` | Model that generated the response |
| `StopReason` | `string` | Why generation stopped |

## Client Requirements

The client must declare sampling support during initialization:

```json
{
  "capabilities": {
    "sampling": {}
  }
}
```

Without this capability, `CreateMessage` will fail.

{{< callout type="warning" >}}
Sampling requires a client that can perform LLM inference. Tools like curl cannot respond to `createMessage` requests — use a proper MCP client.
{{< /callout >}}
