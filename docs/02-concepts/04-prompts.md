---
url: "/docs/concepts/prompts/"
title: "Prompts"
description: "Reusable prompt templates with arguments and auto-completion"
weight: 4
---

Prompts are reusable message templates that clients fetch and pass to an LLM. Unlike tools (which execute code), prompts return pre-built conversation messages.

## Creating a Prompt

```go
prompt, err := finemcp.NewPrompt("greet",
    func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
        name := args["name"]
        return []finemcp.PromptMessage{
            finemcp.NewUserMessage(fmt.Sprintf("Please greet %s warmly.", name)),
            finemcp.NewAssistantMessage(fmt.Sprintf("Hello, %s! Welcome!", name)),
        }, nil
    },
    finemcp.WithPromptDescription("Generate a friendly greeting"),
    finemcp.WithPromptArguments(
        finemcp.PromptArgument{
            Name:        "name",
            Description: "Name of the person to greet",
            Required:    true,
        },
    ),
)
s.RegisterPrompt(prompt)
```

Clients call `prompts/list` to discover prompts and `prompts/get` to fetch rendered messages:

```json
{
  "jsonrpc": "2.0", "id": 2, "method": "prompts/get",
  "params": { "name": "greet", "arguments": { "name": "Alice" } }
}
```

Response:

```json
{
  "messages": [
    { "role": "user", "content": { "type": "text", "text": "Please greet Alice warmly." } },
    { "role": "assistant", "content": { "type": "text", "text": "Hello, Alice! Welcome!" } }
  ]
}
```

## Message Helpers

```go
finemcp.NewUserMessage("text")       // { role: "user", content: { type: "text", text: "..." } }
finemcp.NewAssistantMessage("text")  // { role: "assistant", content: { type: "text", text: "..." } }
```

## Arguments

Declare expected arguments with `PromptArgument`:

```go
finemcp.WithPromptArguments(
    finemcp.PromptArgument{
        Name:        "language",
        Description: "Target programming language",
        Required:    true,
    },
    finemcp.PromptArgument{
        Name:        "style",
        Description: "Code style preference",
        Required:    false,
    },
)
```

Arguments are delivered as `map[string]string` in the handler.

## Auto-Completion

Attach a completer to provide suggestions as the user types:

```go
languages := []string{"Go", "Python", "TypeScript", "Rust", "Java"}

prompt, _ := finemcp.NewPrompt("translate",
    handler,
    finemcp.WithPromptArguments(finemcp.PromptArgument{
        Name: "language", Required: true,
    }),
    finemcp.WithCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
        prefix := strings.ToLower(req.Argument.Value)
        var matches []string
        for _, lang := range languages {
            if strings.HasPrefix(strings.ToLower(lang), prefix) {
                matches = append(matches, lang)
            }
        }
        return &finemcp.CompletionResult{
            Values:  matches,
            HasMore: false,
            Total:   len(languages),
        }, nil
    }),
)
```

Clients request completions via `completion/complete`:

```json
{
  "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/prompt", "name": "translate" },
    "argument": { "name": "language", "value": "Ty" }
  }
}
```

## Prompt Options

| Option | Description |
|--------|-------------|
| `WithPromptDescription(d)` | Human-readable description |
| `WithPromptArguments(args...)` | Declare required/optional arguments |
| `WithCompleter(fn)` | Auto-completion handler |

## CompletionResult

| Field | Type | Description |
|-------|------|-------------|
| `Values` | `[]string` | Matching suggestions |
| `HasMore` | `bool` | Whether more results exist |
| `Total` | `int` | Total number of available completions |
