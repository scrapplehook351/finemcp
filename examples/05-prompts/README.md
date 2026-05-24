# 05 — Prompts

Demonstrates the MCP prompt system for reusable prompt templates with arguments and auto-completion.

## How It Works

Prompts are **reusable message templates** that clients can fetch and use to construct LLM conversations. Unlike tools (which execute code), prompts return **pre-built messages** that the client passes to the LLM.

```
Client sends:  prompts/get { name: "greet", arguments: { name: "Alice" } }
Server returns: { messages: [
    { role: "user", content: { type: "text", text: "Please greet Alice warmly." } },
    { role: "assistant", content: { type: "text", text: "Hello, Alice! Welcome!" } }
]}
```

Use cases:
- Standardized instruction templates for LLMs
- Multi-turn conversation starters
- Parameterized prompts with required/optional arguments

## Examples

### basic/

Prompt with required arguments:

```go
prompt, _ := finemcp.NewPrompt(
    "greet",
    func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
        name := args["name"]
        if name == "" {
            name = "World"
        }
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

**Run:** `go run ./05-prompts/basic`

### completion/

Prompts with auto-completion for arguments:

```go
prompt, _ := finemcp.NewPrompt(
    "translate",
    func(ctx context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
        return []finemcp.PromptMessage{
            finemcp.NewUserMessage(fmt.Sprintf("Translate this code to %s.", args["language"])),
        }, nil
    },
    finemcp.WithPromptDescription("Translate code to another language"),
    finemcp.WithPromptArguments(finemcp.PromptArgument{
        Name: "language", Description: "Target programming language", Required: true,
    }),
    finemcp.WithCompleter(func(ctx context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
        prefix := req.Argument.Value
        var matches []string
        for _, lang := range languages {
            if strings.HasPrefix(strings.ToLower(lang), strings.ToLower(prefix)) {
                matches = append(matches, lang)
            }
        }
        return &finemcp.CompletionResult{Values: matches, HasMore: false}, nil
    }),
)
```

**Run:** `go run ./05-prompts/completion`

## Prompt Message Helpers

```go
finemcp.NewUserMessage("User instruction text")
finemcp.NewAssistantMessage("Pre-filled assistant response")
```

## Prompt Options

| Option | Description |
|--------|-------------|
| `WithPromptDescription(d)` | Human-readable description |
| `WithPromptArguments(args...)` | Declare required/optional arguments |
| `WithCompleter(fn)` | Auto-completion function for arguments |

## Testing with curl

```bash
go run ./05-prompts/basic

# Initialize
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": { "protocolVersion": "2025-03-26", "clientInfo": { "name": "curl", "version": "1.0.0" }, "capabilities": {} }
}'

# List prompts
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 2, "method": "prompts/list"
}'

# Get prompt with arguments
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 3, "method": "prompts/get",
  "params": { "name": "greet", "arguments": { "name": "Alice" } }
}'

# Auto-complete (completion example)
curl -X POST http://localhost:8080 -H "Content-Type: application/json" -d '{
  "jsonrpc": "2.0", "id": 4, "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/prompt", "name": "translate" },
    "argument": { "name": "language", "value": "Ty" }
  }
}'
# → Returns: { values: ["TypeScript"] }
```
