<p align="center">
  <h1 align="center"><img src="logo.png" alt="FineMCP" width="200"></h1>
  <p align="center">
    <strong>Production-grade Model Context Protocol framework for Go</strong><br>
    <strong>Server, client, or both—your choice.</strong> Build with 16 production middleware, streaming, resilience patterns, and selective imports—no bloat.
  </p>
</p>

<p align="center">
  <a href="https://github.com/finemcp/finemcp/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/finemcp/finemcp/ci.yml?branch=main&label=CI" alt="CI"></a>
  <a href="https://modelcontextprotocol.io"><img src="https://img.shields.io/badge/MCP%20Spec-2025--11--25-blue" alt="MCP Spec"></a>
  <a href="https://pkg.go.dev/github.com/finemcp/finemcp"><img src="https://pkg.go.dev/badge/github.com/finemcp/finemcp.svg" alt="Go Reference"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.25%20%7C%201.26-00ADD8" alt="Go Version"></a>
  <a href="https://finemcp.dev/docs/"><img src="https://img.shields.io/badge/docs-finemcp.dev-blue" alt="Docs"></a>
  <a href="https://goreportcard.com/report/github.com/finemcp/finemcp"><img src="https://goreportcard.com/badge/github.com/finemcp/finemcp" alt="Go Report Card"></a>
  <a href="https://github.com/finemcp/finemcp/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-green" alt="License: MIT"></a>
</p>

---

## What is FineMCP?

[MCP (Model Context Protocol)](https://modelcontextprotocol.io) is an open protocol that lets LLM applications — Claude, Cursor, VS Code Copilot, and others — discover and invoke tools, resources, and prompts exposed by a server over JSON-RPC 2.0.

FineMCP is a **production-grade Go framework** for building MCP servers **and clients**. It goes beyond protocol plumbing: it provides 16 production middleware, typed tool handlers with automatic JSON Schema generation, streaming responses, client resilience patterns (circuit breaker, load balancer, retry), OpenTelemetry observability, and a zero-network test harness — capabilities that no other MCP implementation offers in any language.

**MCP Spec**: `2025-11-25` with backward compatibility for `2025-06-18`, `2025-03-26`, and `2024-11-05`.

---

## Why FineMCP?

Most MCP libraries hand you protocol primitives and leave the rest to you. FineMCP treats the server as a **system** — security, observability, resilience, and correctness are built in from day one.

- **Security** — RBAC per tool, token-bucket rate limiting, JSON Schema input validation, execution sandbox
- **Observability** — OpenTelemetry traces and metrics, structured logging, audit log, cost tracking
- **Resilience** — Circuit breaker, retry with exponential backoff, panic recovery
- **Correctness** — Auto JSON Schema generation, typed generic handlers, compile-time safety
- **Testability** — In-process test harness with assertion helpers and golden file diffing

### 🎯 Flexible Architecture

FineMCP is **one framework, three use cases** with zero bloat:

- **Build MCP servers** — Import `github.com/finemcp/finemcp` (no client code in your binary)
- **Build MCP clients** — Import `github.com/finemcp/finemcp/client` (no server overhead)
- **Build proxies/gateways** — Import both (shared dependencies optimized)

Go's package system ensures you **only compile what you import**. A server-only binary doesn't include any client code, and vice versa. This architectural separation means you get the power of a unified framework without the cost of unused dependencies.

---

## How FineMCP Compares

| Feature | FineMCP | [mcp-go](https://github.com/mark3labs/mcp-go) | [mcp-golang](https://github.com/metoro-io/mcp-golang) | [official go-sdk](https://github.com/modelcontextprotocol/go-sdk) |
|---|---|---|---|---|
| **Middleware pipeline** | ✅ 16 built-in | ❌ | ❌ | ❌ |
| **Circuit breaker** | ✅ | ❌ | ❌ | ❌ |
| **Rate limiting** | ✅ | ❌ | ❌ | ❌ |
| **Audit log** | ✅ | ❌ | ❌ | ❌ |
| **Cost tracking** | ✅ | ❌ | ❌ | ❌ |
| **Sandbox / dry-run mode** | ✅ | ❌ | ❌ | ❌ |
| **OpenTelemetry** | ✅ | ❌ | ❌ | ❌ |
| **Client SDK** | ✅ full | ❌ | ⚠️ partial | ✅ |
| **Client resilience** | ✅ retry, LB, backoff | ❌ | ❌ | ❌ |
| **Streaming tools** | ✅ all transports | ❌ | ❌ | ❌ |
| **Multi-tenancy** | ✅ | ❌ | ❌ | ❌ |
| **WebSocket transport** | ✅ | ❌ | ❌ | ❌ |
| **Streamable HTTP** | ✅ | ❌ | ❌ | ✅ |
| **Elicitation / Sampling** | ✅ | ❌ | ❌ | ✅ |
| **Async tasks** | ✅ | ❌ | ❌ | ❌ |
| **CLI tools** | ✅ repl, call, inspect | ❌ | ❌ | ❌ |
| **Zero-network test harness** | ✅ | ❌ | ❌ | ❌ |
| **Typed handlers + auto schema** | ✅ | ✅ | ✅ | ✅ |
| **MCP spec 2025-11-25** | ✅ | ✅ | ⚠️ partial | ✅ |

---

## When to Use FineMCP

**Choose FineMCP if you need:**
- Production-grade security (RBAC, rate limiting, multi-tenancy)
- High performance — see [Benchmarks](#benchmarks) for real numbers
- Enterprise features (audit logs, cost tracking, compliance)
- Type safety and compile-time guarantees
- Single binary deployment

---

## Features

### Full MCP Protocol Coverage

| Category | Methods |
|----------|---------|
| Core | `initialize`, `ping`, `notifications/initialized`, `notifications/cancelled` |
| Tools | `tools/list`, `tools/call`, `notifications/tools/list_changed` |
| Resources | `resources/list`, `resources/read`, `resources/templates/list`, `resources/subscribe`, `resources/unsubscribe`, `notifications/resources/list_changed`, `notifications/resources/updated` |
| Prompts | `prompts/list`, `prompts/get`, `notifications/prompts/list_changed` |
| Tasks | `tasks/get`, `tasks/result`, `tasks/cancel`, `tasks/list` |
| Completions | `completion/complete` |
| Sampling | `sampling/createMessage` |
| Elicitation | `elicitation/create` |
| Roots | `roots/list`, `notifications/roots/list_changed` |
| Logging | `logging/setLevel`, `notifications/message` |
| Progress | `notifications/progress` |

### Five Transports

| Transport | Description |
|-----------|-------------|
| **Stdio** | Newline-delimited JSON-RPC with signal handling |
| **HTTP** | Embeddable `http.Handler`, 204 for notifications |
| **SSE** | Session management, keepalive, backpressure, auto-cleanup |
| **Streamable HTTP** | MCP spec `2025-03-26+` primary transport with `Mcp-Session-Id` and resumability |
| **WebSocket** | Full-duplex with ping/pong and graceful close |

### 16 Built-in Middleware

Every tool call flows through an ordered, composable middleware chain:

| Middleware | Description |
|-----------|-------------|
| **Recovery** | Catches panics, returns clean JSON-RPC error |
| **Logging** | Structured logging — request ID, tool name, duration |
| **Auth** | Bearer token and API key validation with pluggable `TokenVerifier` |
| **RBAC** | Per-tool role-based access control |
| **RateLimit** | Token-bucket rate limiter, per-tool and per-user bucketing |
| **Validation** | JSON Schema validation — type, required, nested objects, arrays |
| **Sandbox** | Execution timeout and output size limits |
| **OTel** | OpenTelemetry spans, counters, and histograms per tool call |
| **Async** | Background job execution with state machine and polling |
| **Caching** | LRU result cache with TTL, input hash keying, per-tool control |
| **CircuitBreaker** | Three-state machine (closed/open/half-open) with configurable thresholds |
| **Retry** | Exponential backoff with jitter, context-aware sleep, custom retry classifier |
| **AuditLog** | SHA-256 input hashing, per-tool include/exclude, compliance-ready trail |
| **CostTracking** | Per-call cost tracking with custom cost functions and metadata |
| **MultiTenant** | Per-tenant tool visibility, rate limit buckets, and RBAC policies |
| **Simulation** | Dry-run mode via `_meta.dryRun: true`; result marked `simulated: true` |

Custom middleware is a single `func(ToolHandler) ToolHandler`.

**Authentication & Multi-tenancy:** Use `HTTPAuth` transport middleware + `RequireAuth` protocol checker for HTTP-layer authentication. Use `TenantResolver` for multi-tenant tool visibility and resource isolation. See [Transports Guide](https://finemcp.dev/docs/concepts/transports/).

### Tool System

- **Raw handlers** — `NewTool()` with functional options (`WithDescription`, `WithInputSchema`, `WithRoles`, `WithAnnotations`)
- **Typed generic handlers** — `NewTypedTool[In, Out]()` with automatic JSON marshal/unmarshal
- **Auto JSON Schema generation** — struct tags (`json`, `description`, `omitempty`) generate schemas automatically
- **Tool annotations** — `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`, `title`
- **Tool composition** — `Pipeline` (sequential), `Parallel` (concurrent fan-out), `FanOutFanIn` (parallel + merge)
- **Bitmap name validation** — O(1) per character, `[A-Za-z0-9_-.]`

## Client SDK

FineMCP includes a production-grade **MCP client SDK** with resilience patterns, observability, and multi-server capabilities:

### Core Client
- **Type-safe client** with automatic request/response handling
- **Transport abstraction** supporting stdio, HTTP, SSE, WebSocket
- **REPL** for interactive debugging and testing

### Resilience Patterns
- **Circuit breaker** — fail fast when servers are unhealthy
- **Retry with exponential backoff** — automatic retry on transient failures  
- **Reconnect logic** — automatic reconnection with configurable strategies
- **Load balancer** — distribute requests across multiple servers (round-robin, least-connections, random)
- **Request coalescing** — deduplicate identical in-flight requests

### Observability
- **OpenTelemetry integration** — traces, metrics, and logs for all client operations
- **Structured logging** — consistent log format across all client operations

### Multi-Server Patterns
- **Aggregator client** — query multiple MCP servers and merge results
- **Context propagation** — propagate trace context across server boundaries

### Caching & Optimization
- **Response caching** — cache tool/resource/prompt responses with TTL
- **Pagination helpers** — automatic pagination for large result sets

### Multi-Tenancy

FineMCP has first-class multi-tenancy built into the dispatch layer. A `TenantResolver` runs on every request to identify the tenant and apply per-tenant tool/resource visibility, rate-limit buckets, and RBAC policies — with zero application code changes.

```go
import (
    "github.com/finemcp/finemcp"
    "github.com/finemcp/finemcp/middleware"
)

// Define per-tenant configurations
configs := map[string]*middleware.TenantConfig{
    "tenant-free": {
        ToolFilter: func(t *finemcp.Tool) bool {
            return t.Annotations != nil && t.Annotations.Title == "basic"
        },
    },
    "tenant-pro": {
        // nil filters = all tools/resources visible
    },
}

store := middleware.NewStaticTenantStore(configs)

// Identify tenant from JWT subject (or any auth header)
resolver := middleware.NewTenantResolver(
    middleware.TenantFromAuthSubject(),
    store,
    middleware.WithFallbackTenant("tenant-free"),
)

server := finemcp.NewServer("my-server", "1.0.0")
server.SetTenantResolver(resolver)

// Each request now automatically scopes tools to the caller's tenant.
// Read tenant ID in any handler or middleware:
//   tenantID := finemcp.TenantIDFromCtx(ctx)
```

### Authentication
- **OAuth2 helpers** — built-in support for OAuth2 token refresh
- **Bearer token auth** — automatic token injection and refresh

**Example:**
```go
import (
    "context"
    "log"

    "github.com/finemcp/finemcp/client"
    "github.com/finemcp/finemcp/client/circuitbreaker"
    "github.com/finemcp/finemcp/client/retry"
    "github.com/finemcp/finemcp/transport"
)

// Create stdio transport to MCP server
stdioTransport, err := transport.NewStdioTransport("./server-binary")
if err != nil {
    log.Fatal(err)
}

// Client with circuit breaker and retry
c, err := client.NewClient(
    client.WithTransport(stdioTransport),
    circuitbreaker.WithCircuitBreaker(),
    retry.WithRetry(retry.DefaultConfig()),
)
if err != nil {
    log.Fatal(err)
}
defer c.Close()

// Call tool on remote server
ctx := context.Background()
result, err := c.CallTool(ctx, "calculator", map[string]any{
    "expression": "2 + 2",
})
if err != nil {
    log.Fatal(err)
}

log.Printf("Result: %v", result)
```

For complete examples, see [finemcp.dev/learn](https://finemcp.dev/learn).

### Streaming Tool Responses

Tool handlers can stream incremental content chunks to the client during execution via `notifications/progress`:

```go
stream := finemcp.StreamFromCtx(ctx)
if stream != nil {
    stream.SendText("processing row 42…")
    stream.Send(finemcp.ImageContent{Data: img, MimeType: "image/png"})
}
return finalResult, nil
```

- **Backpressure**: `Send` blocks when the buffer is full
- **Ordering**: sequence numbers reflect only chunks actually delivered
- **Flush on close**: all buffered chunks are flushed before close returns
- **Nil safety**: typed-nil pointers are rejected with a descriptive error

> **Transport note**: `StreamFromCtx` returns `nil` only for plain HTTP (`transport/http`), which has no persistent connection for notifications. Stdio, WebSocket, SSE, and Streamable HTTP all fully support streaming.

### Resources & Prompts

- Static resources (text + blob) and URI templates (RFC 6570)
- Per-client subscription tracking with update and list-changed notifications
- Prompt registration with named arguments, handler dispatch, and list-changed notifications

### Async Tasks

- Spec-compliant task lifecycle: `pending → running → complete / failed / cancelled`
- `tasks/get`, `tasks/result`, `tasks/cancel`, `tasks/list` with ownership-based access control
- Crypto-random task IDs, panic recovery, timeouts, cancellation, graceful shutdown drain

### Protocol Highlights

- JSON-RPC 2.0 with notification detection and request ID normalization
- Protocol version negotiation across four spec versions
- `_meta` extension on all request/result types with `_meta.progressToken` support
- Cursor-based pagination for all list methods (default page size 50, max 1000)
- Init gate (`atomic.Bool`) rejects pre-init requests
- Graceful shutdown with in-flight request draining

### Test Harness (`mcptest`)

- `NewServer(t, ...Option)` — zero-network, in-process server
- Request helpers: Initialize, CallTool, ListTools, ListResources, ReadResource, ListPrompts, GetPrompt, Notify, RawCall
- Assertion helpers: `AssertToolResult`, `AssertToolCount`, `AssertError`, `AssertResourceText`, `AssertPromptMessage`
- Fixture loading and golden file diffing

---

## Installation

Install the FineMCP framework to build MCP servers and clients:

```bash
go get github.com/finemcp/finemcp
```

**Go version**: Go 1.25+ required (latest two stable releases supported), following the [Go release policy](https://go.dev/doc/devel/release#policy).

### Packages

| Package | Import Path | Purpose |
|---------|-------------|----------|
| Core | `github.com/finemcp/finemcp` | Server, tools, protocol, middleware chain |
| Middleware | `github.com/finemcp/finemcp/middleware` | 16 built-in middleware (recovery, logging, RBAC, rate limiting, validation, sandbox, OpenTelemetry, async tasks, caching, circuit breaker, retry, audit logging, cost tracking, simulation, auth, multi-tenancy) |
| Client | `github.com/finemcp/finemcp/client` | Client SDK with 14 modules for resilience (circuit breaker, retry, load balancer, reconnect), observability (OpenTelemetry, logging), optimization (caching, coalescing, pagination), multi-server patterns (aggregator, context propagation), and authentication (OAuth2) |
| Transport | `github.com/finemcp/finemcp/transport` | Stdio, HTTP, SSE, Streamable HTTP, WebSocket |
| Test Harness | `github.com/finemcp/finemcp/mcptest` | In-process testing helpers |

---

## CLI Tools

For debugging, testing, and inspecting MCP servers, install the `finemcp` CLI:

**Go install (recommended for Go developers):**
```bash
go install github.com/finemcp/finemcp/cmd/finemcp@latest
```

**macOS:**
```bash
curl -L https://github.com/finemcp/finemcp/releases/latest/download/finemcp_darwin_amd64.tar.gz | tar xz
sudo mv finemcp /usr/local/bin/
```

**Linux:**
```bash
curl -L https://github.com/finemcp/finemcp/releases/latest/download/finemcp_linux_amd64.tar.gz | tar xz
sudo mv finemcp /usr/local/bin/
```

**Windows (PowerShell):**
```powershell
# Download and extract latest release (no admin required)
$url = "https://github.com/finemcp/finemcp/releases/latest/download/finemcp_windows_amd64.zip"
Invoke-WebRequest -Uri $url -OutFile finemcp.zip
Expand-Archive finemcp.zip -DestinationPath .

# Install to user-local bin (no admin needed)
$userBin = "$env:USERPROFILE\.local\bin"
New-Item -ItemType Directory -Force -Path $userBin | Out-Null
Move-Item finemcp.exe $userBin -Force

# Add to PATH permanently (current user only)
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$userBin*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$userBin", "User")
}
Write-Host "Installed to $userBin - restart your terminal to use 'finemcp'"
```

**Or system-wide (requires Administrator):**
```powershell
# After extracting finemcp.exe:
Move-Item finemcp.exe C:\Windows\System32\
```

**Or download manually:**  
Download the `.zip` file from [GitHub Releases](https://github.com/finemcp/finemcp/releases/latest), extract `finemcp.exe`, and add it to your PATH.

**Quick test:**
```bash
finemcp serve --http :8080                    # Start test server
finemcp inspect http http://localhost:8080    # Inspect server capabilities
finemcp repl http http://localhost:8080       # Interactive debugging REPL
```

**Windows (PowerShell):**
```powershell
finemcp serve --http :8080                    # Start test server
finemcp inspect http http://localhost:8080    # Inspect server capabilities
finemcp repl http http://localhost:8080       # Interactive debugging REPL
```

> 💡 **When to use CLI:** Debug tool visibility issues with Claude/Cursor, inspect server capabilities before deploying, test connectivity, and run interactive REPL sessions for development and troubleshooting.

---

## CLI Commands

The `finemcp` CLI provides essential debugging and testing utilities:

| Command | Description | Example |
|---------|-------------|----------|
| **serve** | Launch test ping server | `finemcp serve --http :8080` |
| **repl** | Interactive REPL session | `finemcp repl stdio ./server` |
| **list** | List tools/resources/prompts | `finemcp list http http://localhost:8080` |
| **call** | Call a tool and see result | `finemcp call http http://localhost:8080 toolName` |
| **inspect** | Full JSON capability dump | `finemcp inspect http http://localhost:8080` |
| **version** | Show version info | `finemcp version` |

**Common use cases:**
- 🐛 **Debug why Claude isn't seeing your tools** → `finemcp inspect`
- 🧪 **Test a server before deploying** → `finemcp serve` then connect Claude Desktop
- 📊 **Verify tool schemas** → `finemcp list` shows all registered capabilities
- ⚡ **Quick demos without code** → `finemcp serve --http :8080` gives you a working server instantly
- 🔍 **Troubleshoot stdio transport** → `finemcp repl stdio ./myserver` shows real-time communication

Run `finemcp <command> --help` for detailed usage.

---

## Quick Start

**Get a server running in under 10 lines:**

```go
package main

import (
	"context"
	"log"
	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

type Input struct{ Name string `json:"name"` }

func main() {
	s := finemcp.NewServer("myapp", "1.0")
	tool, _ := finemcp.NewTypedTool("greet", func(_ context.Context, in Input) (string, error) {
		return "Hello, " + in.Name + "!", nil
	})
	s.RegisterTool(tool)
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
```

<details>
<summary><b>📦 Add Middleware (Intermediate)</b></summary>

Add safety and observability with middleware:

```go
package main

import (
	"context"
	"log"
	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

type appLogger struct{}
func (appLogger) Info(msg string, kv ...any)  { log.Println(append([]any{msg}, kv...)...) }
func (appLogger) Error(msg string, kv ...any) { log.Println(append([]any{"ERROR", msg}, kv...)...) }

type Input struct {
	Name string `json:"name" description:"Name to greet"`
}

func main() {
	s := finemcp.NewServer("myapp", "1.0")
	
	// Add production middleware
	s.Use(middleware.Recovery())              // Catch panics
	s.Use(middleware.Logging(appLogger{}))    // Structured logs
	s.Use(middleware.Validation())            // JSON Schema validation
	
	tool, err := finemcp.NewTypedTool("greet",
		func(_ context.Context, in Input) (string, error) {
			return "Hello, " + in.Name + "!", nil
		},
		finemcp.WithDescription("Greets someone by name"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)
	
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
```

</details>

<details>
<summary><b>🚀 Production Ready (Advanced)</b></summary>

Full production setup with security, rate limiting, and observability:

```go
package main

import (
	"context"
	"log"
	"time"
	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

type appLogger struct{}
func (appLogger) Info(msg string, kv ...any)  { log.Println(append([]any{msg}, kv...)...) }
func (appLogger) Error(msg string, kv ...any) { log.Println(append([]any{"ERROR", msg}, kv...)...) }

type Input struct {
	Name string `json:"name" description:"Name to greet"`
}

func main() {
	s := finemcp.NewServer("myapp", "1.0")
	
	// Production middleware stack
	s.Use(middleware.Recovery())           // Panic recovery
	s.Use(middleware.Logging(appLogger{})) // Structured logging
	s.Use(middleware.RateLimit(100))       // 100 req/sec rate limit
	s.Use(middleware.RBAC())               // Role-based access (roles set per-tool with WithRoles)
	s.Use(middleware.Validation())         // Input validation
	s.Use(middleware.Sandbox(            // Timeout & output size limits
		middleware.WithTimeout(5*time.Second),
		middleware.WithMaxOutputSize(1<<20),
	))
	s.Use(middleware.OTel())   // OpenTelemetry (configure providers via WithTracerProvider/WithMeterProvider)
	s.Use(middleware.AuditLog( // Audit logging
		middleware.WithAuditSink(middleware.AuditSinkFunc(func(ctx context.Context, e middleware.AuditEntry) {
			log.Printf("[AUDIT] tool=%s duration=%v", e.ToolName, e.Duration)
		})),
	))
	
	// Register typed tool with full options
	tool, err := finemcp.NewTypedTool("greet",
		func(_ context.Context, in Input) (string, error) {
			return "Hello, " + in.Name + "!", nil
		},
		finemcp.WithDescription("Greets someone by name"),
		finemcp.WithRoles("user", "admin"),
		finemcp.WithReadOnly(),
		finemcp.WithIdempotent(),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(tool)
	
	log.Printf("Starting MCP server on :8080")
	log.Fatal(transport.StartHTTP(s, ":8080"))
}
```

</details>

> **💡 Tip**: Start simple and add middleware as you need it. Each middleware is opt-in and composable.

---

## Benchmarks

All numbers are from the bundled benchmark suite (`go test -bench=. -benchmem ./client/...`) using an **in-process mock transport** on Apple M1 — no real network, so actual HTTP/stdio/WebSocket throughput will vary by network and payload size.

| Benchmark | ns/op | ops/sec | Memory |
|---|---|---|---|
| `CallTool_Small` (100B, single goroutine) | 12,617 | ~79k | 5 KB / 98 allocs |
| `CallTool_Parallel` (GOMAXPROCS goroutines) | 4,391 | ~228k | 5 KB / 98 allocs |
| `ListTools` | 9,019 | ~111k | 4 KB / 94 allocs |
| `StdioTransport` round-trip | 8,477 | ~118k | 4 KB / 98 allocs |
| `HTTPTransport` round-trip | 8,790 | ~114k | 4 KB / 98 allocs |
| `WebSocketTransport` round-trip | 8,611 | ~116k | 4 KB / 98 allocs |
| `StreamableHTTP` round-trip | 8,540 | ~117k | 4 KB / 98 allocs |
| `CallTool_WithCaching` (cache hit) | 7,449 | ~134k | 3 KB / 78 allocs |
| `Streaming 10 chunks` | 43,991 | ~23k | 27 KB / 438 allocs |
| `Concurrent_100` (100 goroutines) | 677k total | — | 461 KB / 9.9k allocs |

Run the benchmarks yourself:
```bash
go test -bench=. -benchmem -run='^$' ./client/...
```

---

## MCP Spec Roadmap

FineMCP targets **MCP spec `2025-11-25`** — the current stable release. As the spec evolves we track changes here:

| Spec version | Status | Notes |
|---|---|---|
| `2024-11-05` | ✅ Supported | Legacy compat |
| `2025-03-26` | ✅ Supported | |
| `2025-06-18` | ✅ Supported | |
| `2025-11-25` | ✅ **Current** | Full compliance |

Protocol negotiation is handled automatically — the server advertises the highest version it supports and downgrades gracefully when a client requests an older version.

---

## Documentation

📚 **[Complete Documentation](https://finemcp.dev/docs/)** — Guides, API reference, architecture, middleware, transports, and best practices

🎓 **[Examples & Tutorials](https://finemcp.dev/learn)** — Working code samples and step-by-step tutorials

| Document | Description |
|----------|-------------|
| [Contributing](CONTRIBUTING.md) | Development workflow, commit guidelines, coding style |
| [Security](SECURITY.md) | Security policy and vulnerability reporting |

---

## Contributing

Contributions welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

---

## License

MIT License. See [LICENSE](LICENSE).

