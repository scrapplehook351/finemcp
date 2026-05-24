# 06 — Sampling

Demonstrates the MCP sampling API for **server-initiated LLM requests** — where the server asks the *client* to perform LLM inference.

## How It Works

Sampling reverses the typical flow: instead of the client asking the server to do work, the server sends a `createMessage` request to the client, which runs the prompt through its LLM and returns the result.

```
Client → tools/call "smart-answer" { question: "What is MCP?" }
Server → sampling/createMessage { messages: [...], maxTokens: 256 }  (to client)
Client → runs LLM inference → returns result
Server → uses result to build tool response
```

This is useful when a tool needs LLM reasoning but the server doesn't have direct access to an LLM.

> **Note:** Sampling requires the client to declare `"sampling": {}` in its capabilities during initialization.

## Example

The `smart-answer` tool forwards questions to the client's LLM via sampling:

```go
s := finemcp.NewServer("sampling", "1.0.0")

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
        return []byte(fmt.Sprintf("Model %s responded", result.Model)), nil
    },
    finemcp.WithDescription("Answer a question using client-side LLM sampling"),
    finemcp.WithInputSchema(map[string]any{
        "type": "object",
        "properties": map[string]any{
            "question": map[string]any{"type": "string", "description": "The question to answer"},
        },
        "required": []string{"question"},
    }),
)
s.RegisterTool(tool)
```

## CreateMessageParams

| Field | Type | Description |
|-------|------|-------------|
| `Messages` | `[]SamplingMessage` | Conversation messages to send |
| `SystemPrompt` | `string` | System instruction for the LLM |
| `MaxTokens` | `int` | Maximum token count for the response |
| `Temperature` | `*float64` | Sampling temperature (0.0–1.0) |
| `ModelPreferences` | `*ModelPreferences` | Preferred model hints |
| `IncludeContext` | `string` | Context scope: `"thisServer"`, `"allServers"`, or `""` |

## Testing with curl

```bash
go run ./06-sampling

# Initialize (must declare sampling capability)
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": {
    "protocolVersion": "2025-03-26",
    "clientInfo": { "name": "curl", "version": "1.0.0" },
    "capabilities": { "sampling": {} }
  }
}'

# Call the sampling tool
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "tools/call",
  "params": { "name": "smart-answer", "arguments": { "question": "What is MCP?" } }
}'
```

> **Note:** curl cannot respond to the server's `createMessage` request, so the tool will fail in practice. Use a proper MCP client that supports sampling.
