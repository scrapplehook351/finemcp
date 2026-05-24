package transport

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// DocsOption configures the DocsHandler.
type DocsOption func(*docsConfig)

type docsConfig struct {
	baseURL            string // explicit base URL override (no trailing slash)
	title              string
	toolFilter         func(*http.Request, *finemcp.Tool) bool
	corsOrigin         string
	executeRateLimiter *fixedWindowLimiter
}

// WithDocsBaseURL sets the base URL embedded in the UI and used by the "Try it"
// button when making fetch calls. If not set the UI derives the base URL from
// the HTTP request (scheme + host). Useful when the server sits behind a
// reverse proxy.
//
// Example:
//
//	transport.DocsHandler(server, transport.WithDocsBaseURL("https://api.example.com/docs"))
func WithDocsBaseURL(u string) DocsOption {
	return func(c *docsConfig) { c.baseURL = u }
}

// WithDocsTitle sets the page title shown in the browser tab and page header.
// Defaults to "<server-name> MCP Tools".
func WithDocsTitle(t string) DocsOption {
	return func(c *docsConfig) { c.title = t }
}

// WithToolFilter registers a function that controls which tools are visible
// in the documentation UI. The filter receives the incoming HTTP request
// (carrying any auth context injected by upstream middleware) and a tool.
// Return true to include the tool, false to hide it.
//
// When no filter is set all registered tools are listed.
func WithToolFilter(fn func(*http.Request, *finemcp.Tool) bool) DocsOption {
	return func(c *docsConfig) { c.toolFilter = fn }
}

// WithCORS enables Cross-Origin Resource Sharing headers on every response.
// origin is the value for the Access-Control-Allow-Origin header (e.g. "*"
// or "https://dashboard.example.com"). An OPTIONS preflight handler is added
// automatically.
func WithCORS(origin string) DocsOption {
	return func(c *docsConfig) { c.corsOrigin = origin }
}

// WithExecuteRateLimit caps the number of POST /execute requests allowed per
// minute. Requests that exceed the limit receive 429 Too Many Requests.
// A value ≤ 0 disables the limiter (the default).
func WithExecuteRateLimit(reqPerMinute int) DocsOption {
	return func(c *docsConfig) {
		if reqPerMinute > 0 {
			c.executeRateLimiter = newFixedWindowLimiter(reqPerMinute, time.Minute)
		}
	}
}

// ── Rate limiter ──────────────────────────────────────────────────────────────

// fixedWindowLimiter is a minimal fixed-window rate limiter used by the
// docs execute endpoint. It counts requests in contiguous time windows of
// the configured period and rejects requests once the limit is reached.
type fixedWindowLimiter struct {
	mu     sync.Mutex
	count  int
	limit  int
	window time.Time
	period time.Duration
}

func newFixedWindowLimiter(limit int, period time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{limit: limit, period: period, window: time.Now()}
}

func (l *fixedWindowLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.window) >= l.period {
		l.window = now
		l.count = 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

// DocsHandler returns an http.Handler serving an interactive documentation UI
// for all tools registered on s. The handler is self-contained — it serves the
// full HTML/CSS/JS on GET /, exposes a JSON tool catalogue on GET /tools, and
// provides a live "Try it" execution endpoint on POST /execute.
//
// Mounting (use http.StripPrefix so the handler sees clean paths):
//
//	mux.Handle("/docs/", http.StripPrefix("/docs", transport.DocsHandler(server)))
//
// Or standalone:
//
//	http.ListenAndServe(":8081", transport.DocsHandler(server))
//
// The "Try it" feature calls [finemcp.Server.CallTool] directly, which means
// the full middleware chain (RBAC, validation, simulation, etc.) is applied.
func DocsHandler(s *finemcp.Server, opts ...DocsOption) http.Handler {
	cfg := &docsConfig{}
	for _, o := range opts {
		o(cfg)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		serveDocsUI(w, r, s, cfg, false)
	})
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, r *http.Request) {
		serveToolsJSON(w, r, s, cfg)
	})
	mux.HandleFunc("POST /execute", func(w http.ResponseWriter, r *http.Request) {
		serveExecute(w, r, s, cfg)
	})
	mux.HandleFunc("GET /export", func(w http.ResponseWriter, r *http.Request) {
		serveDocsUI(w, r, s, cfg, true)
	})
	if cfg.corsOrigin != "" {
		return corsHandler(cfg.corsOrigin, mux)
	}
	return mux
}

// corsHandler wraps next with CORS response headers and an OPTIONS preflight handler.
func corsHandler(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── toolDoc — JSON view of a tool ─────────────────────────────────────────────

// toolDoc is the serialisable summary of a single tool sent to the UI.
type toolDoc struct {
	Name         string                   `json:"name"`
	Description  string                   `json:"description"`
	InputSchema  json.RawMessage          `json:"inputSchema"`
	Annotations  *finemcp.ToolAnnotations `json:"annotations,omitempty"`
	HasSimulator bool                     `json:"hasSimulator"`
	Roles        []string                 `json:"roles,omitempty"`
}

// buildToolDocs converts the server's live tool list into JSON-safe toolDocs.
// If a tool's InputSchema cannot be JSON-marshalled, it falls back to {}.
func buildToolDocs(tools []*finemcp.Tool) []toolDoc {
	docs := make([]toolDoc, 0, len(tools))
	for _, t := range tools {
		schema := marshalSchema(t.InputSchema)
		docs = append(docs, toolDoc{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  schema,
			Annotations:  t.Annotations,
			HasSimulator: t.Simulator != nil,
			Roles:        t.Roles,
		})
	}
	return docs
}

// marshalSchema marshals an arbitrary InputSchema value to raw JSON.
// Returns {} on nil or marshalling failure so the UI always gets valid JSON.
func marshalSchema(schema any) json.RawMessage {
	if schema == nil {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// ── Sub-handlers ──────────────────────────────────────────────────────────────

// filteredTools returns the tools visible for the given request.
// When cfg.toolFilter is nil all tools are returned.
// Panics in the user-provided filter are recovered and treated as deny
// (fail-secure), consistent with [finemcp.ItemFilter.AllowTool].
func filteredTools(r *http.Request, s *finemcp.Server, cfg *docsConfig) []*finemcp.Tool {
	tools := s.ListTools()
	if cfg.toolFilter == nil {
		return tools
	}
	filtered := make([]*finemcp.Tool, 0, len(tools))
	for _, t := range tools {
		if allowTool(cfg.toolFilter, r, t) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// allowTool invokes fn and recovers from panics, treating them as deny.
func allowTool(fn func(*http.Request, *finemcp.Tool) bool, r *http.Request, t *finemcp.Tool) (allowed bool) {
	defer func() { _ = recover() }()
	return fn(r, t)
}

// serveToolsJSON returns the tool catalogue as a JSON array, filtered
// according to any configured [WithToolFilter].
func serveToolsJSON(w http.ResponseWriter, r *http.Request, s *finemcp.Server, cfg *docsConfig) {
	docs := buildToolDocs(filteredTools(r, s, cfg))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(docs)
}

// executeRequest is the body accepted by /execute.
type executeRequest struct {
	Tool   string `json:"tool"`
	Input  any    `json:"input"`
	DryRun bool   `json:"dryRun"`
}

// serveExecute handles POST /execute — calls the tool through the full chain.
func serveExecute(w http.ResponseWriter, r *http.Request, s *finemcp.Server, cfg *docsConfig) {
	// Require application/json to mitigate cross-origin form POST (CSRF).
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	// Apply rate limiting when configured.
	if cfg.executeRateLimiter != nil && !cfg.executeRateLimiter.allow() {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	const maxBodySize = 1 << 20 // 1 MB
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	var req executeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Tool == "" {
		http.Error(w, `"tool" field is required`, http.StatusBadRequest)
		return
	}

	// Re-encode the input sub-object as raw bytes for CallTool.
	inputBytes, err := json.Marshal(req.Input)
	if err != nil {
		http.Error(w, "invalid input format", http.StatusBadRequest)
		return
	}

	// If dryRun was requested, embed it in the request context via _meta.
	ctx := r.Context()
	if req.DryRun {
		ctx = finemcp.WithMeta(ctx, map[string]any{"dryRun": true})
	}

	result, err := s.CallTool(ctx, req.Tool, inputBytes)
	if err != nil {
		if isToolNotFoundErr(err) {
			http.Error(w, "tool not found", http.StatusNotFound)
		} else {
			http.Error(w, "execution failed", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// serveDocsUI renders the full HTML page. When export=true it sets a
// Content-Disposition header causing the browser to download the file.
func serveDocsUI(w http.ResponseWriter, r *http.Request, s *finemcp.Server, cfg *docsConfig, export bool) {
	docs := buildToolDocs(filteredTools(r, s, cfg))
	toolsJSON, err := json.Marshal(docs)
	if err != nil {
		http.Error(w, "failed to build tool data", http.StatusInternalServerError)
		return
	}

	// Guard against excessively large payloads being embedded in the HTML page.
	const maxToolsJSONSize = 10 << 20 // 10 MB
	if len(toolsJSON) > maxToolsJSONSize {
		http.Error(w, "tool metadata too large for documentation UI", http.StatusInternalServerError)
		return
	}

	baseURL := cfg.baseURL
	if baseURL == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		baseURL = scheme + "://" + r.Host
	}

	title := cfg.title
	if title == "" {
		title = s.Name() + " MCP Tools"
	}

	data := struct {
		Title     string
		ToolsJSON template.JS
		BaseURL   string
	}{
		Title:     title,
		ToolsJSON: template.JS(toolsJSON), // #nosec G203 -- controlled server data, marshaled from internal Tool structs
		BaseURL:   baseURL,
	}

	var buf bytes.Buffer
	if err := docsTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if export {
		w.Header().Set("Content-Disposition", `attachment; filename="docs.html"`)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// ── Template ──────────────────────────────────────────────────────────────────

// docsBaseURL returns a JS expression string for the baseURL runtime fallback
// used in template rendering. Kept as a plain func to keep the template clean.
func docsBaseURL() string { return "" }

var docsTmpl = template.Must(template.New("docs").Funcs(template.FuncMap{
	"docsBaseURL": docsBaseURL,
}).Parse(docsHTML))

const docsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;font-size:14px;color:#1a1a1a;display:flex;height:100vh;overflow:hidden}
/* sidebar */
#sidebar{width:260px;min-width:200px;background:#f0f2f5;border-right:1px solid #dde1e7;display:flex;flex-direction:column;overflow:hidden}
#sidebar-header{padding:16px;border-bottom:1px solid #dde1e7}
#sidebar-header h1{font-size:15px;font-weight:700;color:#1a1a1a;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
#sidebar-header p{font-size:11px;color:#6b7280;margin-top:2px}
#sidebar-search{padding:8px 12px;border-bottom:1px solid #dde1e7}
#sidebar-search input{width:100%;padding:5px 8px;border:1px solid #cdd2da;border-radius:5px;font-size:12px;outline:none}
#sidebar-search input:focus{border-color:#4f7ef8}
#tool-nav{flex:1;overflow-y:auto;padding:6px 0}
.nav-item{display:flex;align-items:center;gap:6px;padding:7px 14px;cursor:pointer;border-left:3px solid transparent;font-size:13px;color:#374151;transition:background .12s}
.nav-item:hover{background:#e5e7eb}
.nav-item.active{background:#e0e7ff;border-left-color:#4f7ef8;color:#3b4fc8;font-weight:500}
.nav-badge{font-size:10px;padding:1px 5px;border-radius:10px;font-weight:600;margin-left:auto}
.badge-ro{background:#dcfce7;color:#166534}
.badge-dest{background:#fee2e2;color:#991b1b}
.badge-idem{background:#fef9c3;color:#854d0e}
.badge-sim{background:#ede9fe;color:#5b21b6}
/* main */
#main{flex:1;overflow-y:auto;padding:24px;background:#fff}
#welcome{color:#6b7280;padding:60px 0;text-align:center}
#welcome h2{font-size:22px;margin-bottom:8px;color:#374151}
.tool-card{border:1px solid #e5e7eb;border-radius:10px;padding:22px;margin-bottom:28px;scroll-margin-top:16px}
.tool-card h2{font-size:18px;font-weight:700;color:#111}
.tool-title-row{display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-bottom:8px}
.badge{font-size:11px;padding:2px 8px;border-radius:10px;font-weight:600}
.tool-desc{color:#374151;line-height:1.6;margin-bottom:16px}
.tool-desc h1,.tool-desc h2,.tool-desc h3{font-size:14px;font-weight:700;margin:10px 0 4px}
.tool-desc ul{margin-left:18px;margin-bottom:8px}
.tool-desc code{background:#f3f4f6;padding:1px 5px;border-radius:4px;font-size:12px;font-family:monospace}
.tool-desc pre{background:#1e1e1e;color:#d4d4d4;padding:12px;border-radius:6px;overflow-x:auto;margin:8px 0}
.tool-desc pre code{background:none;padding:0;color:inherit}
.try-section{border-top:1px solid #e5e7eb;padding-top:16px;margin-top:16px}
.try-section h3{font-size:13px;font-weight:700;color:#374151;margin-bottom:12px}
.form-field{margin-bottom:12px}
.form-field label{display:block;font-size:12px;font-weight:600;color:#374151;margin-bottom:3px}
.form-field label .req{color:#ef4444;margin-left:2px}
.form-field input[type=text],.form-field input[type=number],.form-field textarea,.form-field select{width:100%;padding:6px 9px;border:1px solid #d1d5db;border-radius:6px;font-size:13px;font-family:inherit;outline:none}
.form-field input:focus,.form-field textarea:focus,.form-field select:focus{border-color:#4f7ef8}
.form-field textarea{min-height:80px;resize:vertical}
.form-field .hint{font-size:11px;color:#6b7280;margin-top:2px}
fieldset.nested{border:1px solid #e5e7eb;border-radius:6px;padding:10px 12px;margin-bottom:10px}
fieldset.nested legend{font-size:11px;font-weight:700;color:#6b7280;padding:0 4px}
.try-actions{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:12px}
.btn{padding:6px 14px;border:none;border-radius:6px;font-size:13px;font-weight:600;cursor:pointer}
.btn-primary{background:#4f7ef8;color:#fff}
.btn-primary:hover{background:#3b6ef1}
.btn-secondary{background:#f3f4f6;color:#374151;border:1px solid #d1d5db}
.btn-secondary:hover{background:#e5e7eb}
.dry-run-row{display:flex;align-items:center;gap:7px;margin-bottom:12px;font-size:13px}
.response-panel{background:#0f172a;color:#e2e8f0;border-radius:8px;padding:14px;font-family:monospace;font-size:12px;white-space:pre-wrap;word-break:break-word;min-height:40px;max-height:320px;overflow-y:auto;display:none}
.response-panel.visible{display:block}
.response-panel.error{border:1px solid #ef4444}
.roles-row{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:10px}
.role-chip{background:#f3f4f6;border:1px solid #d1d5db;color:#374151;font-size:11px;padding:2px 8px;border-radius:10px}
/* export bar */
#export-bar{position:fixed;bottom:0;right:0;padding:8px 16px;background:#fff;border-top:1px solid #e5e7eb;border-left:1px solid #e5e7eb;border-radius:8px 0 0 0;display:flex;gap:8px;z-index:100}
</style>
</head>
<body>
<nav id="sidebar">
  <div id="sidebar-header">
    <h1 id="page-title">{{.Title}}</h1>
    <p id="tool-count"></p>
  </div>
  <div id="sidebar-search"><input type="search" id="search-input" placeholder="Search tools…" /></div>
  <div id="tool-nav"></div>
</nav>
<main id="main">
  <div id="welcome">
    <h2>Select a tool from the sidebar</h2>
    <p>Or use the search box to find a specific tool.</p>
  </div>
  <div id="tool-cards"></div>
</main>
<div id="export-bar">
  <button class="btn btn-secondary" id="btn-export">Export as HTML</button>
</div>

<script>
(function(){
'use strict';

var BASE_URL = {{.BaseURL | printf "%q" | printf "%s"}};
var TOOLS;
try { TOOLS = {{.ToolsJSON}}; } catch(e) { console.error('Failed to parse tools data:', e); TOOLS = []; }

// ── Minimal Markdown renderer ───────────────────────────────────────────────
function renderMarkdown(src){
  if(!src) return '';
  src = escapeHtml(src);
  // fenced code blocks
  src = src.replace(/` + "`" + `` + "`" + `` + "`" + `[^\n]*\n([\s\S]*?)` + "`" + `` + "`" + `` + "`" + `/g, function(_,c){
    return '<pre><code>'+c+'</code></pre>';
  });
  // inline code
  src = src.replace(/` + "`" + `([^` + "`" + `]+)` + "`" + `/g,'<code>$1</code>');
  var lines = src.split('\n');
  var out = [];
  var inUL = false;
  for(var i=0;i<lines.length;i++){
    var l = lines[i];
    if(/^###\s/.test(l)){ if(inUL){out.push('</ul>');inUL=false;} out.push('<h3>'+l.replace(/^###\s/,'')+'</h3>'); continue; }
    if(/^##\s/.test(l)){ if(inUL){out.push('</ul>');inUL=false;} out.push('<h2>'+l.replace(/^##\s/,'')+'</h2>'); continue; }
    if(/^#\s/.test(l)){ if(inUL){out.push('</ul>');inUL=false;} out.push('<h1>'+l.replace(/^#\s/,'')+'</h1>'); continue; }
    if(/^[-*]\s/.test(l)){ if(!inUL){out.push('<ul>');inUL=true;} out.push('<li>'+inlineMarkdown(l.replace(/^[-*]\s/,''))+'</li>'); continue; }
    if(inUL){out.push('</ul>');inUL=false;}
    if(l.trim()===''){out.push('<br>'); continue;}
    out.push('<p>'+inlineMarkdown(l)+'</p>');
  }
  if(inUL) out.push('</ul>');
  return out.join('');
}
function inlineMarkdown(s){
  s = s.replace(/\*\*(.+?)\*\*/g,'<strong>$1</strong>');
  s = s.replace(/_(.+?)_/g,'<em>$1</em>');
  s = s.replace(/\[([^\]]+)\]\(([^)]+)\)/g,'<a href="$2" target="_blank">$1</a>');
  return s;
}
function escapeHtml(s){
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ── JSON Schema → Form ───────────────────────────────────────────────────────
function schemaToForm(schema, path){
  if(!schema || typeof schema !== 'object') return document.createTextNode('');
  var type = schema.type || 'object';
  if(type === 'object' && schema.properties){
    var frag = document.createDocumentFragment();
    var required = schema.required || [];
    for(var key in schema.properties){
      var prop = schema.properties[key];
      var isReq = required.indexOf(key) !== -1;
      var wrapper = document.createElement('div');
      wrapper.className = 'form-field';
      var lbl = document.createElement('label');
      lbl.textContent = key;
      if(isReq){ var req=document.createElement('span'); req.className='req'; req.textContent='*'; lbl.appendChild(req); }
      wrapper.appendChild(lbl);
      var inner = schemaToFormField(prop, path ? path+'.'+key : key);
      wrapper.appendChild(inner);
      if(prop.description){ var hint=document.createElement('div'); hint.className='hint'; hint.textContent=prop.description; wrapper.appendChild(hint); }
      frag.appendChild(wrapper);
    }
    return frag;
  }
  // top-level non-object: single textarea for raw JSON
  return schemaToFormField(schema, path || 'input');
}

function schemaToFormField(prop, name){
  var type = prop.type || 'string';
  if(type === 'object'){
    var fs = document.createElement('fieldset');
    fs.className = 'nested';
    var leg = document.createElement('legend');
    leg.textContent = name;
    fs.appendChild(leg);
    fs.appendChild(schemaToForm(prop, name));
    return fs;
  }
  if(type === 'array'){
    var ta = document.createElement('textarea');
    ta.name = name; ta.placeholder = '["item1","item2"]'; ta.rows = 3;
    return ta;
  }
  if(type === 'boolean'){
    var cb = document.createElement('input');
    cb.type='checkbox'; cb.name=name;
    return cb;
  }
  if(type === 'integer' || type === 'number'){
    var ni = document.createElement('input');
    ni.type='number'; ni.name=name;
    if(prop.minimum != null) ni.min=prop.minimum;
    if(prop.maximum != null) ni.max=prop.maximum;
    return ni;
  }
  if(prop.enum){
    var sel = document.createElement('select');
    sel.name = name;
    prop.enum.forEach(function(v){ var o=document.createElement('option'); o.value=v; o.textContent=v; sel.appendChild(o); });
    return sel;
  }
  // default: string text input
  var inp = document.createElement('input');
  inp.type='text'; inp.name=name;
  if(prop.pattern){ inp.pattern=prop.pattern; }
  return inp;
}

// ── Form → value object ─────────────────────────────────────────────────────
function collectFormValues(form, schema){
  var result = {};
  if(!schema || !schema.properties) return result;
  var required = schema.required || [];
  for(var key in schema.properties){
    var prop = schema.properties[key];
    var val = extractValue(form, key, prop);
    if(val === '' && required.indexOf(key) === -1) continue;
    result[key] = val;
  }
  return result;
}

function extractValue(form, name, prop){
  var type = prop ? prop.type : 'string';
  if(type === 'object' && prop.properties){
    var sub = {};
    for(var k in prop.properties){
      sub[k] = extractValue(form, name+'.'+k, prop.properties[k]);
    }
    return sub;
  }
  var el = form.querySelector('[name="'+name+'"]');
  if(!el) return undefined;
  if(type === 'boolean') return el.checked;
  if(type === 'integer') return parseInt(el.value, 10) || 0;
  if(type === 'number') return parseFloat(el.value) || 0;
  if(type === 'array'){
    try{ return JSON.parse(el.value || '[]'); }catch(e){ return []; }
  }
  return el.value;
}

// ── Build UI ─────────────────────────────────────────────────────────────────
function buildBadge(text, cls){
  var b = document.createElement('span');
  b.className = 'badge nav-badge '+cls;
  b.textContent = text;
  return b;
}

function annotationBadges(ann){
  if(!ann) return [];
  var badges = [];
  if(ann.readOnlyHint === true) badges.push(buildBadge('read-only','badge-ro'));
  if(ann.destructiveHint === true) badges.push(buildBadge('destructive','badge-dest'));
  if(ann.idempotentHint === true) badges.push(buildBadge('idempotent','badge-idem'));
  return badges;
}

function renderTool(tool){
  var card = document.createElement('div');
  card.className = 'tool-card';
  card.id = 'tool-'+tool.name;

  // title row
  var titleRow = document.createElement('div');
  titleRow.className = 'tool-title-row';
  var h2 = document.createElement('h2');
  h2.textContent = (tool.annotations && tool.annotations.title) ? tool.annotations.title : tool.name;
  titleRow.appendChild(h2);
  if((tool.annotations && tool.annotations.title) && tool.annotations.title !== tool.name){
    var nameSpan = document.createElement('code');
    nameSpan.style.cssText='font-size:12px;background:#f3f4f6;padding:2px 7px;border-radius:4px;color:#6b7280';
    nameSpan.textContent = tool.name;
    titleRow.appendChild(nameSpan);
  }
  annotationBadges(tool.annotations).forEach(function(b){ titleRow.appendChild(b); });
  if(tool.hasSimulator){ titleRow.appendChild(buildBadge('simulator','badge-sim')); }
  card.appendChild(titleRow);

  // required roles
  if(tool.roles && tool.roles.length > 0){
    var rolesRow = document.createElement('div');
    rolesRow.className = 'roles-row';
    tool.roles.forEach(function(r){
      var ch = document.createElement('span');
      ch.className='role-chip';
      ch.textContent='🔐 '+r;
      rolesRow.appendChild(ch);
    });
    card.appendChild(rolesRow);
  }

  // description (markdown)
  if(tool.description){
    var descDiv = document.createElement('div');
    descDiv.className = 'tool-desc';
    descDiv.innerHTML = renderMarkdown(tool.description);
    card.appendChild(descDiv);
  }

  // ── Try it ────────────────────────────────────────────────────────────────
  var trySection = document.createElement('div');
  trySection.className = 'try-section';
  var tryH = document.createElement('h3');
  tryH.textContent = 'Try it';
  trySection.appendChild(tryH);

  // form
  var form = document.createElement('form');
  form.noValidate = true;
  var schema = tool.inputSchema || {};
  form.appendChild(schemaToForm(schema, ''));
  trySection.appendChild(form);

  // dry-run row (only if simulator registered)
  var dryRunCb = null;
  if(tool.hasSimulator){
    var drRow = document.createElement('div');
    drRow.className = 'dry-run-row';
    dryRunCb = document.createElement('input');
    dryRunCb.type = 'checkbox';
    dryRunCb.id = 'dry-'+tool.name;
    var drLbl = document.createElement('label');
    drLbl.htmlFor = 'dry-'+tool.name;
    drLbl.textContent = 'Dry run (use simulator)';
    drRow.appendChild(dryRunCb);
    drRow.appendChild(drLbl);
    trySection.appendChild(drRow);
  }

  // action buttons
  var actions = document.createElement('div');
  actions.className = 'try-actions';

  var btnRun = document.createElement('button');
  btnRun.type = 'button';
  btnRun.className = 'btn btn-primary';
  btnRun.textContent = 'Execute';
  actions.appendChild(btnRun);

  var btnCurl = document.createElement('button');
  btnCurl.type = 'button';
  btnCurl.className = 'btn btn-secondary';
  btnCurl.textContent = 'Copy curl';
  actions.appendChild(btnCurl);

  trySection.appendChild(actions);

  // response panel
  var resp = document.createElement('pre');
  resp.className = 'response-panel';
  trySection.appendChild(resp);

  card.appendChild(trySection);

  // ── Execute handler ──────────────────────────────────────────────────────
  btnRun.addEventListener('click', function(){
    var input = (schema && schema.properties) ? collectFormValues(form, schema) : {};
    var payload = {tool: tool.name, input: input, dryRun: dryRunCb ? dryRunCb.checked : false};
    btnRun.disabled = true;
    btnRun.textContent = 'Running…';
    resp.className = 'response-panel visible';
    resp.textContent = '';

    fetch(BASE_URL+'/execute', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify(payload)
    })
    .then(function(r){ return r.json().then(function(d){ return {ok:r.ok, data:d, status:r.status}; }); })
    .then(function(r){
      if(!r.ok){ resp.className='response-panel visible error'; resp.textContent='HTTP '+r.status+'\n'+JSON.stringify(r.data,null,2); return; }
      var isErr = r.data.isError;
      resp.className = 'response-panel visible'+(isErr?' error':'');
      resp.textContent = JSON.stringify(r.data, null, 2);
    })
    .catch(function(e){ resp.className='response-panel visible error'; resp.textContent='Network error: '+e.message; })
    .finally(function(){ btnRun.disabled=false; btnRun.textContent='Execute'; });
  });

  // ── curl handler ─────────────────────────────────────────────────────────
  btnCurl.addEventListener('click', function(){
    var input = (schema && schema.properties) ? collectFormValues(form, schema) : {};
    var payload = {tool: tool.name, input: input};
    var curlStr = "curl -s -X POST '"+BASE_URL+"/execute' \\\n  -H 'Content-Type: application/json' \\\n  -d '"+JSON.stringify(payload).replace(/'/g,"'\\''")+"'";
    navigator.clipboard.writeText(curlStr).then(function(){
      var t = btnCurl.textContent; btnCurl.textContent='Copied!';
      setTimeout(function(){ btnCurl.textContent=t; }, 1500);
    });
  });

  return card;
}

// ── Sidebar ──────────────────────────────────────────────────────────────────
function buildSidebar(tools){
  var nav = document.getElementById('tool-nav');
  nav.innerHTML = '';
  tools.forEach(function(tool){
    var item = document.createElement('div');
    item.className = 'nav-item';
    item.dataset.name = tool.name;
    item.textContent = (tool.annotations && tool.annotations.title) ? tool.annotations.title : tool.name;
    if(tool.annotations && tool.annotations.readOnlyHint === true) item.appendChild(buildBadge('RO','nav-badge badge-ro'));
    else if(tool.annotations && tool.annotations.destructiveHint === true) item.appendChild(buildBadge('DEL','nav-badge badge-dest'));
    item.addEventListener('click', function(){
      var el = document.getElementById('tool-'+tool.name);
      if(el){ el.scrollIntoView({behavior:'smooth',block:'start'}); }
      highlightNav(tool.name);
    });
    nav.appendChild(item);
  });
}

function highlightNav(name){
  document.querySelectorAll('.nav-item').forEach(function(el){
    el.classList.toggle('active', el.dataset.name === name);
  });
}

// ── Main init ────────────────────────────────────────────────────────────────
function init(){
  var tools = TOOLS || [];
  document.getElementById('tool-count').textContent = tools.length + ' tool' + (tools.length===1?'':'s');

  buildSidebar(tools);

  var cards = document.getElementById('tool-cards');
  var welcome = document.getElementById('welcome');
  if(tools.length > 0){ welcome.style.display='none'; }
  tools.forEach(function(t){ cards.appendChild(renderTool(t)); });

  // search
  document.getElementById('search-input').addEventListener('input', function(e){
    var q = e.target.value.toLowerCase();
    document.querySelectorAll('.nav-item').forEach(function(el){
      el.style.display = el.dataset.name.toLowerCase().includes(q) ? '' : 'none';
    });
    document.querySelectorAll('.tool-card').forEach(function(el){
      var name = el.id.replace('tool-','');
      el.style.display = name.toLowerCase().includes(q) ? '' : 'none';
    });
  });

  // export button
  document.getElementById('btn-export').addEventListener('click', function(){
    window.location.href = BASE_URL+'/export';
  });

  // IntersectionObserver for nav highlighting on scroll
  var io = new IntersectionObserver(function(entries){
    entries.forEach(function(entry){
      if(entry.isIntersecting){
        highlightNav(entry.target.id.replace('tool-',''));
      }
    });
  },{rootMargin:'-20% 0px -70% 0px'});
  document.querySelectorAll('.tool-card').forEach(function(el){ io.observe(el); });

  // deep link: ?tool=name or #tool-name
  var hash = location.hash.replace('#','');
  var params = new URLSearchParams(location.search);
  var linkTo = params.get('tool') || hash;
  if(linkTo){
    var el = document.getElementById('tool-'+linkTo) || document.getElementById(linkTo);
    if(el){ setTimeout(function(){ el.scrollIntoView({block:'start'}); highlightNav(linkTo); }, 100); }
  } else if(tools.length > 0){
    highlightNav(tools[0].name);
  }
}

document.addEventListener('DOMContentLoaded', init);
})();
</script>
</body>
</html>`

// isToolNotFoundErr reports whether err is the sentinel error returned by
// [finemcp.Server.CallTool] when no tool with the given name is registered.
// The error is unexported from the finemcp package so we match textually.
func isToolNotFoundErr(err error) bool {
	return err != nil && err.Error() == "tool not found"
}
