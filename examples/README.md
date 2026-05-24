# FineMCP Examples

Comprehensive, runnable examples for every feature of the [FineMCP](https://github.com/finemcp/finemcp) Go framework.

## Quick Start

```bash
git clone https://github.com/finemcp/finemcp.git
cd finemcp/examples
go mod tidy
go run ./01-server/basic
```

## Examples

| # | Category | Example | Description |
|---|----------|---------|-------------|
| 01 | **Server** | [basic](01-server/basic) | Minimal server with a single tool on HTTP |
| 01 | **Server** | [options](01-server/options) | All ServerOption variants (subscriptions, task store, stream buffer, sessions, versions) |
| 02 | **Tools** | [raw](02-tools/raw) | Raw ToolHandler with manual JSON parsing and WithInputSchema |
| 02 | **Tools** | [typed](02-tools/typed) | NewTypedTool with generics, struct tags, complex types |
| 02 | **Tools** | [annotations](02-tools/annotations) | Tool annotations: ReadOnly, Destructive, Idempotent, Roles, Title |
| 02 | **Tools** | [session-tools](02-tools/session-tools) | Per-session tool overlays that shadow global tools |
| 03 | **Streaming** | [streaming](03-streaming) | Stream incremental content from tools via ToolStream |
| 04 | **Resources** | [static](04-resources/static) | Static resources with fixed URIs (text and blob) |
| 04 | **Resources** | [template](04-resources/template) | Resource templates with URI variables (RFC 6570) and completion |
| 04 | **Resources** | [subscriptions](04-resources/subscriptions) | Subscribe to resource changes and receive update notifications |
| 05 | **Prompts** | [basic](05-prompts/basic) | Reusable prompt templates with arguments |
| 05 | **Prompts** | [completion](05-prompts/completion) | Auto-completion for prompt argument values |
| 06 | **Sampling** | [sampling](06-sampling) | Server-initiated LLM sampling via createMessage |
| 07 | **Roots** | [roots](07-roots) | Register root URIs defining server content boundaries |
| 08 | **Elicitation** | [elicitation](08-elicitation) | Server-initiated user prompts for confirmation/input |
| 09 | **Progress** | [progress](09-progress) | Report incremental progress from long-running tools |
| 10 | **Logging** | [logging](10-logging) | Structured server logging with level filtering |
| 11 | **Completion** | [completion](11-completion) | Auto-completion for prompts and resource templates |
| 12 | **Transports** | [stdio](12-transports/stdio) | Stdio transport (stdin/stdout JSON-RPC) |
| 12 | **Transports** | [http](12-transports/http) | Simple HTTP transport with custom mux |
| 12 | **Transports** | [sse](12-transports/sse) | SSE transport with keepalive and configurable paths |
| 12 | **Transports** | [streamable](12-transports/streamable) | Streamable HTTP transport (MCP 2025-11-25) with sessions |
| 12 | **Transports** | [websocket](12-transports/websocket) | WebSocket transport with origin validation |
| 13 | **Middleware** | [recovery](13-middleware/recovery) | Catch panics and convert to error results |
| 13 | **Middleware** | [logging](13-middleware/logging) | Log every tool invocation with timing |
| 13 | **Middleware** | [validation](13-middleware/validation) | Validate inputs against JSON Schema |
| 13 | **Middleware** | [rbac](13-middleware/rbac) | Role-based access control for tools |
| 13 | **Middleware** | [ratelimit](13-middleware/ratelimit) | Global and per-key rate limiting |
| 13 | **Middleware** | [retry](13-middleware/retry) | Automatic retry with exponential backoff |
| 13 | **Middleware** | [circuitbreaker](13-middleware/circuitbreaker) | Circuit breaker to prevent cascading failures |
| 13 | **Middleware** | [cache](13-middleware/cache) | Cache tool results with TTL and tool filtering |
| 13 | **Middleware** | [sandbox](13-middleware/sandbox) | Enforce timeout and output size limits |
| 13 | **Middleware** | [auditlog](13-middleware/auditlog) | Record tool invocations for compliance |
| 13 | **Middleware** | [costtracking](13-middleware/costtracking) | Track invocation costs with custom cost functions |
| 13 | **Middleware** | [otel](13-middleware/otel) | OpenTelemetry tracing and metrics |
| 13 | **Middleware** | [async](13-middleware/async) | Convert tools to async jobs with polling |
| 13 | **Middleware** | [auth](13-middleware/auth) | HTTP-layer auth (Bearer/API key) + MCP auth checker |
| 13 | **Middleware** | [multitenant](13-middleware/multitenant) | Tenant isolation with per-tenant tool/resource filtering |
| 13 | **Middleware** | [simulation](13-middleware/simulation) | Dry-run / simulation mode for destructive tools |
| 14 | **Testing** | [testing](14-testing) | Unit tests with mcptest (no real server needed) |
| 15 | **Content** | [content-types](15-content-types) | All content types: text, image, audio, embedded resource |
| 16 | **Composition** | [composition](16-composition) | Pipeline and Parallel tool composition patterns |
| 17 | **Embedding** | [embedding](17-embedding) | Mount MCP handlers alongside existing HTTP routes |
| 18 | **Example Apps** | [smartwiki](18-example-apps/smartwiki) | Full-featured multi-tenant knowledge base — all 14 middleware, 13 tools, resources, prompts, sampling, elicitation, streaming, progress, logging, and dual stdio/HTTP transport |

## Requirements

- Go 1.24 or later
- FineMCP framework (`go get github.com/finemcp/finemcp`)

## Running Examples

Most examples start an HTTP server on `:8080`:

```bash
go run ./09-progress
```

The stdio transport example reads from stdin:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}' | go run ./12-transports/stdio
```

Test examples run with `go test`:

```bash
go test ./14-testing/...
```

## SmartWiki (Example App)

[18-example-apps/smartwiki](18-example-apps/smartwiki) is a complete, production-style MCP server that combines every framework feature in one application. It supports two run modes detected automatically at startup:

**Claude Desktop / stdio clients** — add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "smartwiki": {
      "command": "/path/to/mcp-smartwiki"
    }
  }
}
```

Build the binary:

```bash
go build -o ~/bin/mcp-smartwiki ./18-example-apps/smartwiki/
```

**HTTP mode** — run directly; server starts on `:8080` with Bearer auth:

```bash
go run ./18-example-apps/smartwiki/
# POST http://localhost:8080/mcp  (Authorization: Bearer admin-token)
# GET  http://localhost:8080/health
# GET  http://localhost:8080/metrics
```

Built-in tokens: `admin-token` (admin/editor/viewer, tenant `acme`), `editor-token` (editor/viewer, tenant `acme`), `reader-token` (viewer, tenant `globex` — read-only tools only).

## License

Same as [FineMCP](https://github.com/finemcp/finemcp/blob/main/LICENSE).
