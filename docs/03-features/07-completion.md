---
url: "/docs/features/completion/"
title: "Completion"
description: "Auto-completion for prompt arguments and resource template variables"
weight: 7
---

The completion system provides real-time suggestions as users type values for prompt arguments or resource template variables.

## Prompt Completion

Attach a completer when creating a prompt:

```go
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
        return &finemcp.CompletionResult{Values: matches, Total: len(languages)}, nil
    }),
)
```

Client request:

```json
{
  "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/prompt", "name": "translate" },
    "argument": { "name": "language", "value": "Ty" }
  }
}
```

## Resource Template Completion

Attach a completer to a resource template:

```go
tmpl, _ := finemcp.NewResourceTemplate("city://{name}", "City",
    handler,
    finemcp.WithTemplateCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
        // req.Argument.Name is "name", req.Argument.Value is the typed prefix
        return &finemcp.CompletionResult{Values: matchingCities}, nil
    }),
)
```

Client request:

```json
{
  "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/resource", "uri": "city://{name}" },
    "argument": { "name": "name", "value": "Pa" }
  }
}
```

## CompleteRequest

| Field | Type | Description |
|-------|------|-------------|
| `Ref` | `CompletionRef` | Reference to the prompt or template |
| `Argument` | `CompletionArgument` | Argument being completed |

### CompletionRef

| Field | Type | Values |
|-------|------|--------|
| `Type` | `string` | `"ref/prompt"` or `"ref/resource"` |
| `Name` | `string` | Prompt name (for `ref/prompt`) |
| `URI` | `string` | Template URI (for `ref/resource`) |

### CompletionArgument

| Field | Type | Description |
|-------|------|-------------|
| `Name` | `string` | Argument name |
| `Value` | `string` | Current typed value (prefix) |

## CompletionResult

| Field | Type | Description |
|-------|------|-------------|
| `Values` | `[]string` | Matching suggestions |
| `HasMore` | `bool` | Whether more results exist |
| `Total` | `int` | Total number of matches (optional) |
