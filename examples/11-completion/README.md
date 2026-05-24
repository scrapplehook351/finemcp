# 11 — Completion

Demonstrates the MCP auto-completion system for both **prompts** and **resource templates**.

## How It Works

The `completion/complete` method lets clients request suggestions as the user types. Completions can target:
- **Prompt arguments** — `ref/prompt` with a prompt name
- **Resource template variables** — `ref/resource` with a template URI

## Example

This example registers a `weather` prompt and a `city://{name}` resource template, both with completers that suggest city names:

```go
var cities = []string{
    "New York", "London", "Tokyo", "Paris", "Berlin",
    "Sydney", "Toronto", "Mumbai", "Beijing", "Cairo",
}

// Prompt with completion
prompt, _ := finemcp.NewPrompt("weather",
    func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
        return []finemcp.PromptMessage{
            finemcp.NewUserMessage(fmt.Sprintf("What's the weather like in %s?", args["city"])),
        }, nil
    },
    finemcp.WithPromptDescription("Get weather for a city"),
    finemcp.WithPromptArguments(finemcp.PromptArgument{
        Name: "city", Description: "City name", Required: true,
    }),
    finemcp.WithCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
        prefix := strings.ToLower(req.Argument.Value)
        var matches []string
        for _, c := range cities {
            if strings.HasPrefix(strings.ToLower(c), prefix) {
                matches = append(matches, c)
            }
        }
        return &finemcp.CompletionResult{Values: matches, Total: len(cities)}, nil
    }),
)

// Resource template with completion
tmpl, _ := finemcp.NewResourceTemplate("city://{name}", "City Data",
    func(ctx context.Context, uri string) ([]finemcp.ResourceContent, error) {
        return []finemcp.ResourceContent{
            finemcp.NewTextResourceContent(uri, `{"population": 1000000}`),
        }, nil
    },
    finemcp.WithTemplateCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
        prefix := strings.ToLower(req.Argument.Value)
        var matches []string
        for _, c := range cities {
            if strings.HasPrefix(strings.ToLower(c), prefix) {
                matches = append(matches, c)
            }
        }
        return &finemcp.CompletionResult{Values: matches}, nil
    }),
)
```

## CompletionResult

| Field | Type | Description |
|-------|------|-------------|
| `Values` | `[]string` | Suggested completions matching the prefix |
| `HasMore` | `bool` | Whether more results exist beyond this page |
| `Total` | `int` | Total number of matches (optional) |

## Ref Types

| Ref Type | Key Field | Description |
|----------|-----------|-------------|
| `ref/prompt` | `name` | Complete a prompt argument |
| `ref/resource` | `uri` | Complete a resource template variable |

## Testing with curl

```bash
go run ./11-completion

# Initialize
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# Complete a prompt argument (city starting with "Ne")
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 4, "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/prompt", "name": "weather" },
    "argument": { "name": "city", "value": "Ne" }
  }
}'
# → { "values": ["New York"] }

# Complete a resource template variable (city starting with "Pa")
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 5, "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/resource", "uri": "city://{name}" },
    "argument": { "name": "name", "value": "Pa" }
  }
}'
# → { "values": ["Paris"] }
```
