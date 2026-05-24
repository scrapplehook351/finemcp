// Example: SmartWiki — Full-Featured Multi-Tenant Knowledge Base
//
// A production-quality MCP server that demonstrates every finemcp feature:
//   - All 14 middleware layers (recovery, logging, otel, auditlog, cost tracking,
//     rbac, rate limit, circuit breaker, retry, validation, cache, simulation,
//     sandbox, async)
//   - 13 tools (typed/raw/pipeline/parallel, streaming, progress, elicitation,
//     sampling, destructive ops, simulation-aware)
//   - 5 resources (static + template with completion + subscriptions)
//   - 3 prompts with argument completion
//   - Roots API
//   - MCP logging API
//   - Multi-tenant resolution
//   - Auth at the HTTP layer
//   - HTTP embedding alongside health + metrics endpoints
//
// ── Quick-start ──────────────────────────────────────────────────────────────
//
// Build and run:
//
//	go run ./18-example-apps/smartwiki/
//
// Tokens:
//
//	admin-token  → alice  / tenant:acme  / roles: admin,editor,viewer
//	editor-token → bob    / tenant:acme  / roles: editor,viewer
//	globex-token → charlie/ tenant:globex/ roles: viewer (read-only)
//
// All MCP calls go to POST http://localhost:8080/mcp with the bearer token.
//
// See README.md for full curl examples.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/middleware"
	"github.com/finemcp/finemcp/transport"
)

// ──────────────────────────────────────────────────────────────────────────────
// Data model
// ──────────────────────────────────────────────────────────────────────────────

// Article is the core entity of the SmartWiki knowledge base.
type Article struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	Tags      []string  `json:"tags"`
	Author    string    `json:"author"`
	TenantID  string    `json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

func (a *Article) format() string {
	b, _ := json.MarshalIndent(a, "", "  ")
	return string(b)
}

// ──────────────────────────────────────────────────────────────────────────────
// In-memory store
// ──────────────────────────────────────────────────────────────────────────────

var (
	mu       sync.RWMutex
	articles = map[string]*Article{}
	idSeq    int64

	// cost report accumulated across all calls (capped at 1 000 entries)
	costMu     sync.Mutex
	costReport []string
)

func nextID() string {
	n := atomic.AddInt64(&idSeq, 1)
	return fmt.Sprintf("article_%d", n)
}

func getArticle(id string) (*Article, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := articles[id]
	return a, ok
}

func allArticles() []*Article {
	mu.RLock()
	defer mu.RUnlock()
	list := make([]*Article, 0, len(articles))
	for _, a := range articles {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	return list
}

func articleIDsList() []string {
	list := allArticles()
	ids := make([]string, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	return ids
}

func categoriesList() []string {
	seen := map[string]struct{}{}
	for _, a := range allArticles() {
		seen[a.Category] = struct{}{}
	}
	cats := make([]string, 0, len(seen))
	for c := range seen {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	return cats
}

// isStdioMode reports whether the server is being run as a subprocess
// (e.g. Claude Desktop) where stdin is a pipe rather than a terminal.
// In stdio mode the server uses the MCP stdio transport, skips HTTP auth,
// RBAC, multi-tenant resolution, and the async middleware (which returns
// task IDs that Claude cannot poll for).
func isStdioMode() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) == 0
}

// ──────────────────────────────────────────────────────────────────────────────
// Logger for middleware.Logging
// ──────────────────────────────────────────────────────────────────────────────

type stdLogger struct{}

func (stdLogger) Info(msg string, kv ...any)  { log.Printf("[INFO]  %s %v", msg, kv) }
func (stdLogger) Error(msg string, kv ...any) { log.Printf("[ERROR] %s %v", msg, kv) }

// ──────────────────────────────────────────────────────────────────────────────
// main
// ──────────────────────────────────────────────────────────────────────────────

func main() {
	stdio := isStdioMode()

	// ── Server construction ────────────────────────────────────────────────
	taskStore := finemcp.NewTaskStore()
	s := finemcp.NewServer("smartwiki", "1.0.0",
		finemcp.WithStreamBufferSize(64),
		finemcp.WithTaskStore(taskStore),
		finemcp.WithResourceSubscriptions(),
	)

	// ── MCP log handler: suppress debug, forward info+ ────────────────────
	s.SetLogHandler(func(_ context.Context, level finemcp.LogLevel) error {
		if level == finemcp.LogLevelDebug {
			return fmt.Errorf("debug suppressed")
		}
		return nil
	})

	// ── Roots ─────────────────────────────────────────────────────────────
	articlesRoot, err := finemcp.NewRoot("wiki://articles", finemcp.WithRootName("Articles Root"))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterRoot(articlesRoot)

	catsRoot, err := finemcp.NewRoot("wiki://categories", finemcp.WithRootName("Categories Root"))
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterRoot(catsRoot)

	// ── Auth & multi-tenant (HTTP mode only) ─────────────────────────────────
	// In stdio mode (e.g. Claude Desktop) there is no HTTP auth layer and no
	// per-request tenant context, so these are skipped.
	if !stdio {
		s.SetAuthChecker(middleware.RequireAuth())
		readOnlyTools := map[string]bool{
			"article_get": true, "article_list": true, "article_search": true,
			"stats": true, "list_roots": true, "smart_suggest": true,
		}
		tenantStore := middleware.NewStaticTenantStore(map[string]*middleware.TenantConfig{
			"acme": {
				ToolFilter: func(_ *finemcp.Tool) bool { return true },
			},
			"globex": {
				ToolFilter: func(t *finemcp.Tool) bool { return readOnlyTools[t.Name] },
			},
		})
		s.SetTenantResolver(middleware.NewTenantResolver(
			middleware.TenantFromAuthMeta("tenant_id"),
			tenantStore,
		))
	}

	// ── Middleware stack ───────────────────────────────────────────────────
	// 1. Recovery
	s.Use(middleware.Recovery())

	// 2. Logging
	s.Use(middleware.Logging(stdLogger{}))

	// 3. OpenTelemetry
	s.Use(middleware.OTel())

	// 4. Audit log
	s.Use(middleware.AuditLog(
		middleware.WithAuditSink(middleware.AuditSinkFunc(
			func(_ context.Context, e middleware.AuditEntry) {
				log.Printf("[AUDIT] tool=%s duration=%v success=%v",
					e.ToolName, e.Duration, e.Success)
			},
		)),
	))

	// 5. Cost tracking
	s.Use(middleware.CostTracking(
		middleware.WithCostCollector(middleware.CostCollectorFunc(
			func(_ context.Context, r middleware.CostRecord) {
				costMu.Lock()
				if len(costReport) < 1000 { // cap to bound memory usage
					costReport = append(costReport,
						fmt.Sprintf("tool=%s duration=%v", r.ToolName, r.Duration))
				}
				costMu.Unlock()
			},
		)),
	))

	// 6. RBAC – only meaningful when auth info is injected by the HTTP layer
	if !stdio {
		s.Use(middleware.RBAC())
	}

	// 7. Rate limiting – 10 req/s global with burst of 20
	s.Use(middleware.RateLimit(10, middleware.WithBurst(20)))

	// 8. Circuit breaker – open after 5 consecutive failures
	s.Use(middleware.CircuitBreaker(
		middleware.WithFailureThreshold(5),
		middleware.WithSuccessThreshold(3),
	))

	// 9. Retry – up to 3 attempts on transient errors
	s.Use(middleware.Retry(middleware.WithMaxAttempts(3)))

	// 10. Validation – validates inputs against JSON schema declared on the tool
	s.Use(middleware.Validation())

	// 11. Cache – 2-minute TTL for read operations
	s.Use(middleware.Cache(middleware.WithCacheTTL(2 * time.Minute)))

	// 12. Simulation – dry-run support via X-Simulate header
	s.Use(middleware.Simulation())

	// 13. Sandbox – hard limits on execution time and output size
	s.Use(middleware.Sandbox(
		middleware.WithTimeout(10*time.Second),
		middleware.WithMaxOutputSize(1<<20), // 1 MiB
	))

	// 14. Async – dispatches tool calls to background tasks (HTTP mode only).
	// In stdio mode this is skipped: clients like Claude Desktop expect
	// synchronous responses and have no mechanism to poll for task results.
	if !stdio {
		asyncMW, _ := middleware.Async()
		s.Use(asyncMW)
	}

	// ── Register resources ─────────────────────────────────────────────────
	registerResources(s)

	// ── Register prompts ───────────────────────────────────────────────────
	registerPrompts(s)

	// ── Register tools ─────────────────────────────────────────────────────
	registerTools(s)

	// ── Start server ──────────────────────────────────────────────────────
	if stdio {
		// Stdio mode: used by Claude Desktop and other subprocess-based clients.
		// ALL diagnostic text must go to stderr — stdout is the JSON-RPC stream.
		fmt.Fprintln(os.Stderr, "SmartWiki MCP server (stdio mode)")
		if err := transport.ServeStdio(context.Background(), s); err != nil {
			log.Fatal(err)
		}
		return
	}

	// HTTP mode: auth at the transport layer, health + metrics side-cars
	verifier := middleware.ChainVerifiers(
		middleware.StaticBearerTokenVerifier(map[string]finemcp.AuthInfo{
			"admin-token": {
				Subject: "alice",
				Roles:   []string{"admin", "editor", "viewer"},
				Meta:    map[string]any{"tenant_id": "acme"},
			},
			"editor-token": {
				Subject: "bob",
				Roles:   []string{"editor", "viewer"},
				Meta:    map[string]any{"tenant_id": "acme"},
			},
			"globex-token": {
				Subject: "charlie",
				Roles:   []string{"viewer"},
				Meta:    map[string]any{"tenant_id": "globex"},
			},
		}),
	)

	mcpHandler := transport.Handler(s)
	protected := middleware.HTTPAuth(verifier, mcpHandler)

	mux := http.NewServeMux()
	mux.Handle("/mcp", protected)

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.RLock()
		count := len(articles)
		mu.RUnlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"articles":  count,
			"timestamp": time.Now().UTC(),
		})
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Copy under lock so we don't hold costMu for the full HTTP write.
		costMu.Lock()
		report := make([]string, len(costReport))
		copy(report, costReport)
		costMu.Unlock()
		fmt.Fprintf(w, "# SmartWiki cost report (%d entries)\n", len(report))
		for _, line := range report {
			fmt.Fprintln(w, line)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "SmartWiki MCP server")
		fmt.Fprintln(w, "  MCP endpoint : POST http://localhost:8080/mcp")
		fmt.Fprintln(w, "  Health check : GET  http://localhost:8080/health")
		fmt.Fprintln(w, "  Cost metrics : GET  http://localhost:8080/metrics")
	})

	fmt.Fprintln(os.Stderr, "SmartWiki MCP server listening on :8080")
	fmt.Fprintln(os.Stderr, "  /mcp      → MCP (requires Bearer token)")
	fmt.Fprintln(os.Stderr, "  /health   → health check")
	fmt.Fprintln(os.Stderr, "  /metrics  → cost report")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// ──────────────────────────────────────────────────────────────────────────────
// Resources
// ──────────────────────────────────────────────────────────────────────────────

func registerResources(s *finemcp.Server) {
	// Static: server status
	status, err := finemcp.NewResource(
		"wiki://status",
		"Server Status",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			mu.RLock()
			count := len(articles)
			mu.RUnlock()
			payload, _ := json.Marshal(map[string]any{
				"status":    "healthy",
				"articles":  count,
				"timestamp": time.Now().UTC(),
			})
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, string(payload)),
			}, nil
		},
		finemcp.WithResourceDescription("Live server status and article count"),
		finemcp.WithResourceMimeType("application/json"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResource(status)

	// Static: article JSON schema
	schemaBody, _ := json.MarshalIndent(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"title":    map[string]any{"type": "string"},
			"content":  map[string]any{"type": "string"},
			"category": map[string]any{"type": "string"},
			"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"author":   map[string]any{"type": "string"},
		},
	}, "", "  ")
	schema, err := finemcp.NewResource(
		"wiki://schema",
		"Article Schema",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, string(schemaBody)),
			}, nil
		},
		finemcp.WithResourceDescription("JSON schema for the Article entity"),
		finemcp.WithResourceMimeType("application/json"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResource(schema)

	// Template: article by ID (with completion + subscriptions)
	articleTmpl, err := finemcp.NewResourceTemplate(
		"wiki://articles/{id}",
		"Article by ID",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			// Extract id from uri "wiki://articles/<id>"
			id := strings.TrimPrefix(uri, "wiki://articles/")
			a, ok := getArticle(id)
			if !ok {
				return nil, fmt.Errorf("article %q not found", id)
			}
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, a.format()),
			}, nil
		},
		finemcp.WithTemplateDescription("Read an article by its ID"),
		finemcp.WithTemplateCompleter(func(_ context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := strings.ToLower(req.Argument.Value)
			var matches []string
			ids := articleIDsList()
			for _, id := range ids {
				if strings.HasPrefix(strings.ToLower(id), prefix) {
					matches = append(matches, id)
				}
			}
			return &finemcp.CompletionResult{Values: matches, Total: len(ids)}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResourceTemplate(articleTmpl)

	// Template: articles by category (with completion)
	categoryTmpl, err := finemcp.NewResourceTemplate(
		"wiki://categories/{name}",
		"Articles by Category",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			cat := strings.TrimPrefix(uri, "wiki://categories/")
			var results []*Article
			for _, a := range allArticles() {
				if strings.EqualFold(a.Category, cat) {
					results = append(results, a)
				}
			}
			payload, _ := json.MarshalIndent(results, "", "  ")
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri, string(payload)),
			}, nil
		},
		finemcp.WithTemplateDescription("List all articles in a category"),
		finemcp.WithTemplateCompleter(func(_ context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := strings.ToLower(req.Argument.Value)
			cats := categoriesList()
			var matches []string
			for _, c := range cats {
				if strings.HasPrefix(strings.ToLower(c), prefix) {
					matches = append(matches, c)
				}
			}
			return &finemcp.CompletionResult{Values: matches, Total: len(cats)}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResourceTemplate(categoryTmpl)

	// Subscription resource: subscribable article URI
	subResource, err := finemcp.NewResource(
		"wiki://articles/updates",
		"Article Update Feed",
		func(_ context.Context, uri string) ([]finemcp.ResourceContent, error) {
			return []finemcp.ResourceContent{
				finemcp.NewTextResourceContent(uri,
					"Subscribe to this resource to receive article update notifications"),
			}, nil
		},
		finemcp.WithResourceDescription("Subscribe to receive notifications when articles change"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterResource(subResource)
}

// ──────────────────────────────────────────────────────────────────────────────
// Prompts
// ──────────────────────────────────────────────────────────────────────────────

func registerPrompts(s *finemcp.Server) {
	// 1. summarize — summarize article, id with completion
	summarize, err := finemcp.NewPrompt(
		"summarize",
		func(_ context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			id := args["id"]
			a, ok := getArticle(id)
			if !ok {
				return nil, fmt.Errorf("article %q not found", id)
			}
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage(fmt.Sprintf(
					"Please summarize the following article titled %q:\n\n%s",
					a.Title, a.Content)),
			}, nil
		},
		finemcp.WithPromptDescription("Generate a concise summary of an article"),
		finemcp.WithPromptArguments(finemcp.PromptArgument{
			Name: "id", Description: "Article ID to summarize", Required: true,
		}),
		finemcp.WithCompleter(func(_ context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := strings.ToLower(req.Argument.Value)
			var matches []string
			for _, id := range articleIDsList() {
				if strings.HasPrefix(strings.ToLower(id), prefix) {
					matches = append(matches, id)
				}
			}
			return &finemcp.CompletionResult{Values: matches}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterPrompt(summarize)

	// 2. suggest-tags — suggest tags for article content
	suggestTags, err := finemcp.NewPrompt(
		"suggest-tags",
		func(_ context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			content := args["content"]
			if content == "" {
				return nil, fmt.Errorf("content is required")
			}
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage(fmt.Sprintf(
					"Suggest 5 relevant tags for the following content. Return only a JSON array of lowercase strings.\n\n%s",
					content)),
			}, nil
		},
		finemcp.WithPromptDescription("Suggest relevant tags for article content"),
		finemcp.WithPromptArguments(finemcp.PromptArgument{
			Name: "content", Description: "Article content to tag", Required: true,
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterPrompt(suggestTags)

	// 3. write-review — generate review prompt, id with completion
	writeReview, err := finemcp.NewPrompt(
		"write-review",
		func(_ context.Context, args map[string]string) ([]finemcp.PromptMessage, error) {
			id := args["id"]
			a, ok := getArticle(id)
			if !ok {
				return nil, fmt.Errorf("article %q not found", id)
			}
			return []finemcp.PromptMessage{
				finemcp.NewUserMessage(fmt.Sprintf(
					"Write a thorough peer review for the article titled %q in category %q.\n\nContent:\n%s",
					a.Title, a.Category, a.Content)),
			}, nil
		},
		finemcp.WithPromptDescription("Generate a peer review for an article"),
		finemcp.WithPromptArguments(finemcp.PromptArgument{
			Name: "id", Description: "Article ID to review", Required: true,
		}),
		finemcp.WithCompleter(func(_ context.Context, req finemcp.CompleteRequest) (*finemcp.CompletionResult, error) {
			prefix := strings.ToLower(req.Argument.Value)
			var matches []string
			for _, id := range articleIDsList() {
				if strings.HasPrefix(strings.ToLower(id), prefix) {
					matches = append(matches, id)
				}
			}
			return &finemcp.CompletionResult{Values: matches}, nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterPrompt(writeReview)
}

// ──────────────────────────────────────────────────────────────────────────────
// Tools
// ──────────────────────────────────────────────────────────────────────────────

func registerTools(s *finemcp.Server) {
	// ── 1. article_create ──────────────────────────────────────────────────
	createTool, err := finemcp.NewTypedTool("article_create",
		func(ctx context.Context, in struct {
			Title    string   `json:"title"    description:"Article title (required, unique)"`
			Content  string   `json:"content"  description:"Article body text (required)"`
			Category string   `json:"category" description:"Category name (required)"`
			Tags     []string `json:"tags"     description:"Relevant tags"`
		}) (string, error) {
			if in.Title == "" || in.Content == "" || in.Category == "" {
				return "", fmt.Errorf("title, content, and category are required")
			}
			mu.Lock()
			for _, a := range articles {
				if strings.EqualFold(a.Title, in.Title) {
					mu.Unlock()
					return "", fmt.Errorf("article titled %q already exists (id: %s)", in.Title, a.ID)
				}
			}
			auth := finemcp.AuthInfoFromCtx(ctx)
			author := "unknown"
			tenantID := ""
			if auth != nil {
				author = auth.Subject
				if tid, ok := auth.Meta["tenant_id"].(string); ok {
					tenantID = tid
				}
			}
			now := time.Now()
			id := nextID()
			articles[id] = &Article{
				ID: id, Title: in.Title, Content: in.Content,
				Category: in.Category, Tags: in.Tags,
				Author: author, TenantID: tenantID,
				CreatedAt: now, UpdatedAt: now, Version: 1,
			}
			mu.Unlock()
			// MCP log message
			_ = s.SendLogMessage(ctx, finemcp.LogLevelInfo, "article_create",
				map[string]any{"id": id, "title": in.Title, "author": author})
			s.NotifyResourceUpdated("wiki://articles/updates")
			return fmt.Sprintf("Created article %s: %q (category: %s)", id, in.Title, in.Category), nil
		},
		finemcp.WithDescription("Create a new wiki article"),
		finemcp.WithRoles("editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(createTool)

	// ── 2. article_get ─────────────────────────────────────────────────────
	getTool, err := finemcp.NewTypedTool("article_get",
		func(_ context.Context, in struct {
			ID string `json:"id" description:"Article ID (e.g. article_1)"`
		}) (string, error) {
			a, ok := getArticle(in.ID)
			if !ok {
				return "", fmt.Errorf("article %q not found", in.ID)
			}
			return a.format(), nil
		},
		finemcp.WithDescription("Read an article by ID"),
		finemcp.WithRoles("viewer", "editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(getTool)

	// ── 3. article_update ──────────────────────────────────────────────────
	updateTool, err := finemcp.NewTypedTool("article_update",
		func(ctx context.Context, in struct {
			ID       string   `json:"id"       description:"Article ID to update"`
			Title    string   `json:"title"    description:"New title (omit to keep current)"`
			Content  string   `json:"content"  description:"New content (omit to keep current)"`
			Category string   `json:"category" description:"New category (omit to keep current)"`
			Tags     []string `json:"tags"     description:"New tags (omit to keep current)"`
		}) (string, error) {
			if in.Title == "" && in.Content == "" && in.Category == "" && len(in.Tags) == 0 {
				return "", fmt.Errorf("provide at least one field to update")
			}
			mu.Lock()
			a, ok := articles[in.ID]
			if !ok {
				mu.Unlock()
				return "", fmt.Errorf("article %q not found", in.ID)
			}
			if in.Title != "" {
				for _, other := range articles {
					if other.ID != in.ID && strings.EqualFold(other.Title, in.Title) {
						mu.Unlock()
						return "", fmt.Errorf("an article titled %q already exists (id: %s)", in.Title, other.ID)
					}
				}
				a.Title = in.Title
			}
			if in.Content != "" {
				a.Content = in.Content
			}
			if in.Category != "" {
				a.Category = in.Category
			}
			if len(in.Tags) > 0 {
				a.Tags = in.Tags
			}
			a.UpdatedAt = time.Now()
			a.Version++
			mu.Unlock()
			s.NotifyResourceUpdated("wiki://articles/updates")
			return fmt.Sprintf("Updated article %s (version %d)", in.ID, a.Version), nil
		},
		finemcp.WithDescription("Update an existing article"),
		finemcp.WithRoles("editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(updateTool)

	// ── 4. article_delete ──────────────────────────────────────────────────
	deleteTool, err := finemcp.NewTypedTool("article_delete",
		func(ctx context.Context, in struct {
			ID      string `json:"id"      description:"Article ID to delete"`
			Confirm bool   `json:"confirm" description:"Pass true to confirm permanent deletion"`
		}) (string, error) {
			a, ok := getArticle(in.ID)
			if !ok {
				return "", fmt.Errorf("article %q not found", in.ID)
			}
			if !in.Confirm {
				// Try interactive elicitation for clients that support it.
				result, err := s.ElicitUser(ctx, finemcp.ElicitationParams{
					Prompt:  fmt.Sprintf("Permanently delete article %q (%s)? Type 'yes' to confirm.", in.ID, a.Title),
					Type:    "text",
					Default: "no",
				})
				if err != nil || result.Cancelled {
					// Elicitation not supported by this client; require explicit confirm.
					return fmt.Sprintf("To delete article %s (%q), call again with confirm:true.", in.ID, a.Title), nil
				}
				if result.Value != "yes" {
					return "Deletion cancelled.", nil
				}
			}
			mu.Lock()
			delete(articles, in.ID)
			mu.Unlock()
			s.NotifyResourceUpdated("wiki://articles/updates")
			_ = s.SendLogMessage(ctx, finemcp.LogLevelWarning, "article_delete",
				map[string]any{"id": in.ID, "title": a.Title})
			return fmt.Sprintf("Deleted article %s (%q)", in.ID, a.Title), nil
		},
		finemcp.WithDescription("Permanently delete an article; pass confirm:true to proceed"),
		finemcp.WithRoles("admin"),
		finemcp.WithDestructive(),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(deleteTool)

	// ── 5. article_list (streaming) ────────────────────────────────────────
	listTool, err := finemcp.NewTypedTool("article_list",
		func(ctx context.Context, in struct {
			Category string `json:"category" description:"Filter by category (optional)"`
		}) (string, error) {
			list := allArticles()
			stream := finemcp.StreamFromCtx(ctx)
			var results []*Article
			for _, a := range list {
				if in.Category == "" || strings.EqualFold(a.Category, in.Category) {
					results = append(results, a)
					if stream != nil {
						_ = stream.SendText(fmt.Sprintf("[%s] %s  (%s)", a.ID, a.Title, a.Category))
					}
				}
			}
			if len(results) == 0 {
				return "No articles found.", nil
			}
			// Always return the full list; stream events above are a supplemental notification.
			_ = stream // stream may be nil; used above for event delivery
			out := fmt.Sprintf("%d article(s):\n", len(results))
			for _, a := range results {
				out += fmt.Sprintf("  [%s] %s  (category: %s, v%d)\n",
					a.ID, a.Title, a.Category, a.Version)
			}
			return out, nil
		},
		finemcp.WithDescription("List articles with optional category filter (streams results)"),
		finemcp.WithRoles("viewer", "editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(listTool)

	// ── 6. article_search (JSON schema input) ──────────────────────────────
	searchTool, err := finemcp.NewTool("article_search",
		func(_ context.Context, input []byte) ([]byte, error) {
			var req struct {
				Query    string   `json:"query"`
				Tags     []string `json:"tags"`
				Category string   `json:"category"`
				Limit    int      `json:"limit"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			if req.Limit <= 0 {
				req.Limit = 20
			}
			q := strings.ToLower(req.Query)
			var matches []*Article
			for _, a := range allArticles() {
				if len(matches) >= req.Limit {
					break
				}
				if req.Category != "" && !strings.EqualFold(a.Category, req.Category) {
					continue
				}
				if q != "" && !strings.Contains(strings.ToLower(a.Title), q) &&
					!strings.Contains(strings.ToLower(a.Content), q) {
					continue
				}
				if len(req.Tags) > 0 {
					found := false
					for _, wantTag := range req.Tags {
						for _, aTag := range a.Tags {
							if strings.EqualFold(aTag, wantTag) {
								found = true
								break
							}
						}
						if found {
							break
						}
					}
					if !found {
						continue
					}
				}
				matches = append(matches, a)
			}
			out, _ := json.MarshalIndent(matches, "", "  ")
			return out, nil
		},
		finemcp.WithDescription("Full-text + tag + category search over articles"),
		finemcp.WithRoles("viewer", "editor", "admin"),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":    map[string]any{"type": "string", "description": "Full-text search query"},
				"tags":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter by tags"},
				"category": map[string]any{"type": "string", "description": "Filter by category"},
				"limit":    map[string]any{"type": "integer", "description": "Max results (default 20)"},
			},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(searchTool)

	// ── 7. article_export (progress reporting) ─────────────────────────────
	exportTool, err := finemcp.NewTool("article_export",
		func(ctx context.Context, input []byte) ([]byte, error) {
			list := allArticles()
			total := float64(len(list))
			if total == 0 {
				return []byte("[]"), nil
			}
			var buf strings.Builder
			buf.WriteString("[\n")
			for i, a := range list {
				finemcp.ReportProgress(ctx, float64(i+1), total)
				time.Sleep(20 * time.Millisecond) // simulate serialisation work
				b, _ := json.MarshalIndent(a, "  ", "  ")
				buf.WriteString("  ")
				buf.Write(b)
				if i < len(list)-1 {
					buf.WriteString(",")
				}
				buf.WriteString("\n")
			}
			buf.WriteString("]")
			return []byte(buf.String()), nil
		},
		finemcp.WithDescription("Export all articles as JSON with progress updates"),
		finemcp.WithRoles("admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(exportTool)

	// ── 8. stats ───────────────────────────────────────────────────────────
	statsTool, err := finemcp.NewTypedTool("stats",
		func(_ context.Context, _ struct{}) (string, error) {
			list := allArticles()
			cats := map[string]int{}
			tags := map[string]int{}
			for _, a := range list {
				cats[a.Category]++
				for _, t := range a.Tags {
					tags[t]++
				}
			}
			out := fmt.Sprintf("Total articles: %d\n", len(list))
			out += fmt.Sprintf("Categories: %d\n", len(cats))
			out += fmt.Sprintf("Unique tags: %d\n", len(tags))
			if len(cats) > 0 {
				out += "Per category:\n"
				for c, n := range cats {
					out += fmt.Sprintf("  %s: %d\n", c, n)
				}
			}
			return out, nil
		},
		finemcp.WithDescription("Show knowledge-base statistics"),
		finemcp.WithRoles("viewer", "editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(statsTool)

	// ── 9. pipeline_process (composition: validate → process → enrich) ─────
	validateFn := func(_ context.Context, input []byte) ([]byte, error) {
		if len(input) == 0 {
			return nil, fmt.Errorf("empty input")
		}
		var m map[string]any
		if err := json.Unmarshal(input, &m); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return input, nil
	}
	processFn := func(_ context.Context, input []byte) ([]byte, error) {
		var m map[string]any
		_ = json.Unmarshal(input, &m)
		m["processed"] = true
		m["processed_at"] = time.Now().UTC()
		out, _ := json.Marshal(m)
		return out, nil
	}
	enrichFn := func(_ context.Context, input []byte) ([]byte, error) {
		var m map[string]any
		_ = json.Unmarshal(input, &m)
		m["enriched"] = true
		m["server"] = "smartwiki"
		out, _ := json.Marshal(m)
		return out, nil
	}

	pipelineTool, err := finemcp.NewTool("pipeline_process",
		finemcp.Pipeline(validateFn, processFn, enrichFn),
		finemcp.WithDescription("Validate → process → enrich a JSON payload in sequence"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(pipelineTool)

	// ── 10. parallel_audit (composition: two concurrent checks) ────────────
	parallelTool, err := finemcp.NewTool("parallel_audit",
		finemcp.Parallel(
			finemcp.NamedHandler{
				Name: "schema-check",
				Handler: func(_ context.Context, input []byte) ([]byte, error) {
					if len(input) == 0 {
						return nil, fmt.Errorf("empty input")
					}
					var m map[string]any
					if err := json.Unmarshal(input, &m); err != nil {
						return nil, fmt.Errorf("invalid JSON")
					}
					return []byte("schema-check: passed"), nil
				},
			},
			finemcp.NamedHandler{
				Name: "integrity-check",
				Handler: func(_ context.Context, _ []byte) ([]byte, error) {
					// Counts articles as a basic integrity probe
					mu.RLock()
					n := len(articles)
					mu.RUnlock()
					return []byte(fmt.Sprintf("integrity-check: %d articles indexed", n)), nil
				},
			},
		),
		finemcp.WithDescription("Run schema and integrity checks in parallel"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(parallelTool)

	// ── 11. smart_suggest (sampling via s.CreateMessage) ───────────────────
	suggestTool, err := finemcp.NewTypedTool("smart_suggest",
		func(ctx context.Context, in struct {
			ArticleID string `json:"article_id" description:"Article to generate a suggestion for"`
			Aspect    string `json:"aspect"     description:"What to improve: title|content|tags"`
		}) (string, error) {
			a, ok := getArticle(in.ArticleID)
			if !ok {
				return "", fmt.Errorf("article %q not found", in.ArticleID)
			}
			aspect := in.Aspect
			if aspect == "" {
				aspect = "content"
			}
			prompt := fmt.Sprintf(
				"You are a wiki editor. Article title: %q, category: %q.\nContent:\n%s\n\nSuggest improvements for the %s.",
				a.Title, a.Category, a.Content, aspect)
			temp := 0.7
			resp, err := s.CreateMessage(ctx, finemcp.CreateMessageParams{
				Messages: []finemcp.SamplingMessage{
					{Role: "user", Content: finemcp.TextContent{Text: prompt}},
				},
				SystemPrompt: "You are a concise and helpful wiki editor.",
				Temperature:  &temp,
				MaxTokens:    300,
			})
			if err != nil {
				return fmt.Sprintf("[sampling unavailable] Suggest improving the %s of article %s", aspect, in.ArticleID), nil
			}
			// Content is json.RawMessage; extract text field if present
			var contentObj struct {
				Text string `json:"text"`
			}
			if jerr := json.Unmarshal(resp.Content, &contentObj); jerr == nil && contentObj.Text != "" {
				return contentObj.Text, nil
			}
			return fmt.Sprintf("Model %s responded (raw: %s)", resp.Model, string(resp.Content)), nil
		},
		finemcp.WithDescription("Use AI sampling to suggest improvements for an article"),
		finemcp.WithRoles("viewer", "editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(suggestTool)

	// ── 12. simulate_publish (destructive + simulation-aware) ──────────────
	publishTool, err := finemcp.NewTool("simulate_publish",
		func(ctx context.Context, input []byte) ([]byte, error) {
			var req struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(input, &req); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			a, ok := getArticle(req.ID)
			if !ok {
				return nil, fmt.Errorf("article %q not found", req.ID)
			}
			if finemcp.IsSimulatedFromCtx(ctx) {
				return []byte(fmt.Sprintf("[SIMULATED] Would publish article %q to the public wiki", a.Title)), nil
			}
			return []byte(fmt.Sprintf("Published article %q (id: %s) to the public wiki", a.Title, a.ID)), nil
		},
		finemcp.WithDescription("Publish an article to the public wiki (supports simulation via X-Simulate header)"),
		finemcp.WithRoles("editor", "admin"),
		finemcp.WithDestructive(),
		finemcp.WithInputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Article ID to publish"},
			},
			"required": []string{"id"},
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(publishTool)

	// ── 13. list_roots ─────────────────────────────────────────────────────
	rootsTool, err := finemcp.NewTool("list_roots",
		func(_ context.Context, _ []byte) ([]byte, error) {
			roots := s.ListRoots()
			var lines string
			for _, r := range roots {
				lines += fmt.Sprintf("- %s  (%s)\n", r.URI, r.Name)
			}
			return []byte(lines), nil
		},
		finemcp.WithDescription("List all registered root URIs"),
		finemcp.WithRoles("viewer", "editor", "admin"),
	)
	if err != nil {
		log.Fatal(err)
	}
	s.RegisterTool(rootsTool)
}
