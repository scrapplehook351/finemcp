# SmartWiki — Full-Featured MCP Knowledge Base

A production-quality MCP server that showcases **every finemcp feature** in one
cohesive application: a multi-tenant, AI-assisted wiki.

---

## Feature inventory

| Category | What's included |
|----------|----------------|
| **Server** | Stream buffer, task store, log handler, roots |
| **Middleware (14)** | Recovery, Logging, OTel, AuditLog, CostTracking, RBAC, RateLimit, CircuitBreaker, Retry, Validation, Cache, Simulation, Sandbox, Async |
| **Tools (13)** | article_create, article_get, article_update, article_delete, article_list, article_search, article_export, stats, pipeline_process, parallel_audit, smart_suggest, simulate_publish, list_roots |
| **Resources (5)** | wiki://status (static), wiki://schema (static), wiki://articles/{id} (template+completion), wiki://categories/{name} (template+completion), wiki://articles/updates (subscription) |
| **Prompts (3)** | summarize, suggest-tags, write-review (with completion) |
| **Special** | Streaming output, progress reporting, elicitation, sampling, Pipeline & Parallel composition, simulation-aware destructive ops |
| **Transport** | HTTP embedding alongside `/health` and `/metrics` endpoints |
| **Auth** | Bearer token at HTTP layer (3 tokens: admin, editor, viewer/globex) |
| **Multi-tenant** | acme (all tools) vs globex (read-only tools only) |

---

## Run

```bash
go run ./18-example-apps/smartwiki/
```

Server listens on `:8080`.

---

## Tokens

| Token | User | Tenant | Roles |
|-------|------|--------|-------|
| `admin-token` | alice | acme | admin, editor, viewer |
| `editor-token` | bob | acme | editor, viewer |
| `globex-token` | charlie | globex | viewer (read-only) |

All MCP calls go to `POST http://localhost:8080/mcp` with
`Authorization: Bearer <token>`.

---

## Curl examples

### Initialize session

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc": "2.0", "id": 1, "method": "initialize",
    "params": {
      "protocolVersion": "2025-03-26",
      "clientInfo": {"name": "curl", "version": "1.0.0"},
      "capabilities": {"elicitation": {}}
    }
  }'
```

### List tools

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

### article_create (editor+ only)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":3,"method":"tools/call",
    "params":{"name":"article_create","arguments":{
      "title":"Getting Started with MCP",
      "content":"MCP stands for Model Context Protocol...",
      "category":"Tutorials",
      "tags":["mcp","beginner","protocol"]
    }}
  }'
```

### article_get (all roles)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer globex-token" \
  -d '{
    "jsonrpc":"2.0","id":4,"method":"tools/call",
    "params":{"name":"article_get","arguments":{"id":"article_1"}}
  }'
```

### article_list with streaming

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer editor-token" \
  -d '{
    "jsonrpc":"2.0","id":5,"method":"tools/call",
    "params":{"name":"article_list","arguments":{"category":"Tutorials"}}
  }'
```

### article_search (full-text + tag filter)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":6,"method":"tools/call",
    "params":{"name":"article_search","arguments":{
      "query":"MCP","tags":["beginner"],"limit":5
    }}
  }'
```

### article_export with progress

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":7,"method":"tools/call",
    "params":{"name":"article_export"}
  }'
```

### article_update (elicits confirmation for title changes)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer editor-token" \
  -d '{
    "jsonrpc":"2.0","id":8,"method":"tools/call",
    "params":{"name":"article_update","arguments":{
      "id":"article_1",
      "title":"Getting Started with MCP (Updated)"
    }}
  }'
```

### article_delete (admin only — destructive, elicits confirmation)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":9,"method":"tools/call",
    "params":{"name":"article_delete","arguments":{"id":"article_1"}}
  }'
```

### stats

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer globex-token" \
  -d '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"stats"}}'
```

### pipeline_process (validate → process → enrich)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":11,"method":"tools/call",
    "params":{"name":"pipeline_process","arguments":{"data":"hello"}}
  }'
```

### parallel_audit (two concurrent checks)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":12,"method":"tools/call",
    "params":{"name":"parallel_audit","arguments":{"data":"check me"}}
  }'
```

### smart_suggest (AI sampling)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer editor-token" \
  -d '{
    "jsonrpc":"2.0","id":13,"method":"tools/call",
    "params":{"name":"smart_suggest","arguments":{
      "article_id":"article_1",
      "aspect":"content"
    }}
  }'
```

### simulate_publish (dry-run via X-Simulate header)

```bash
# Simulate (dry-run)
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer editor-token" \
  -H "X-Simulate: true" \
  -d '{
    "jsonrpc":"2.0","id":14,"method":"tools/call",
    "params":{"name":"simulate_publish","arguments":{"id":"article_1"}}
  }'

# Real publish (no simulation header)
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer editor-token" \
  -d '{
    "jsonrpc":"2.0","id":15,"method":"tools/call",
    "params":{"name":"simulate_publish","arguments":{"id":"article_1"}}
  }'
```

### list_roots

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"list_roots"}}'
```

### Unauthenticated request (should fail with 401)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

### Globex tenant blocked from write tool

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer globex-token" \
  -d '{
    "jsonrpc":"2.0","id":1,"method":"tools/call",
    "params":{"name":"article_create","arguments":{
      "title":"Denied","content":"...","category":"Test"
    }}
  }'
```

---

## Resources

### List resources

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{"jsonrpc":"2.0","id":1,"method":"resources/list"}'
```

### Read server status

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"wiki://status"}}'
```

### Read article by ID (template resource)

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"wiki://articles/article_1"}}'
```

### Auto-complete article ID

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":4,"method":"completion/complete",
    "params":{
      "ref":{"type":"ref/resource","uri":"wiki://articles/{id}"},
      "argument":{"name":"id","value":"article_"}
    }
  }'
```

### Subscribe to article updates

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":5,
    "method":"resources/subscribe",
    "params":{"uri":"wiki://articles/updates"}
  }'
```

---

## Prompts

### List prompts

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{"jsonrpc":"2.0","id":1,"method":"prompts/list"}'
```

### summarize prompt

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":2,"method":"prompts/get",
    "params":{"name":"summarize","arguments":{"id":"article_1"}}
  }'
```

### suggest-tags prompt

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":3,"method":"prompts/get",
    "params":{"name":"suggest-tags","arguments":{"content":"Model Context Protocol enables AI models to access external tools..."}}
  }'
```

### Auto-complete article ID for write-review prompt

```bash
curl -s -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer admin-token" \
  -d '{
    "jsonrpc":"2.0","id":4,"method":"completion/complete",
    "params":{
      "ref":{"type":"ref/prompt","name":"write-review"},
      "argument":{"name":"id","value":"art"}
    }
  }'
```

---

## Non-MCP endpoints

```bash
# Health check
curl http://localhost:8080/health

# Cost/metrics report
curl http://localhost:8080/metrics

# Welcome page
curl http://localhost:8080/
```
