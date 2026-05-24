package finemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// HandleMessage is the single entry point for all incoming JSON-RPC messages.
// It parses the raw bytes, routes to the correct method handler, and returns
// a JSON-RPC response. For notifications it returns nil (no response required).
// Protocol-level errors (bad JSON, unknown method) are returned as error responses,
// never as Go errors. The returned error is reserved for truly unrecoverable issues.
func (s *Server) HandleMessage(ctx context.Context, data []byte) (*JSONRPCResponse, error) {
	s.inflight.Add(1)
	defer s.inflight.Done()

	var req JSONRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return NewErrorResponse(nil, ErrCodeParseError, "parse error"), nil
	}

	if req.JSONRPC != jsonrpcVersion {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest, "invalid jsonrpc version"), nil
	}

	if req.Method == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest, "method must not be empty"), nil
	}

	// Route notifications — no response expected.
	if req.IsNotification() {
		s.handleNotification(ctx, &req)
		return nil, nil
	}

	return s.handleRequest(ctx, &req)
}

// handleNotification processes JSON-RPC notifications (no response).
func (s *Server) handleNotification(ctx context.Context, req *JSONRPCRequest) {
	// Built-in notification handling.
	switch req.Method {
	case methodInitialized:
		// Client acknowledges initialization — no action needed.
	case methodCancelled:
		var params CancelledParams
		if req.Params != nil {
			// Use json.Decoder with UseNumber so numeric requestId values
			// are preserved as json.Number, matching the request ID type.
			dec := json.NewDecoder(bytes.NewReader(req.Params))
			dec.UseNumber()
			if err := dec.Decode(&params); err == nil {
				s.cancelRequest(params.RequestID)
			}
		}
	}

	// Dispatch to user-registered notification handlers.
	// Fast path: skip allocation when no handlers are registered.
	s.notifMu.RLock()
	orig := s.notificationHandlers[req.Method]
	if len(orig) == 0 {
		s.notifMu.RUnlock()
		return
	}
	// Copy the slice under the lock so handlers run outside the lock,
	// preventing deadlocks if a handler calls OnNotification.
	handlers := make([]NotificationHandlerFunc, len(orig))
	copy(handlers, orig)
	s.notifMu.RUnlock()

	for _, h := range handlers {
		select {
		case <-ctx.Done():
			return // context cancelled — skip remaining handlers
		default:
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					if ph := s.notifPanicHandler; ph != nil {
						func() {
							defer func() { _ = recover() }() // don't let a buggy panic handler escape
							ph(req.Method, r)
						}()
					}
				}
			}()
			h(ctx, req.Params)
		}()
	}
}

// handleRequest routes JSON-RPC requests to the correct method handler.
func (s *Server) handleRequest(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	// Let's create a cancellable context
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	// Track the request for cancellation support, but only when
	// the request has a non-nil ID to avoid key collisions.
	if req.ID != nil {
		s.trackRequest(req.ID, cancel)
		defer s.untrackRequest(req.ID)
	}

	// Attach the request ID so middleware and handlers can access it.
	ctx = WithRequestID(ctx, req.ID)

	// Extract _meta from the request params and inject into context.
	// Handlers can read it via MetaFromCtx(ctx).
	meta := extractMeta(req.Params)
	if meta != nil {
		ctx = WithMeta(ctx, meta)
	}

	// If the transport injected a notification sender, wire up a progress reporter
	// so tool handlers can call finemcp.ReportProgress(ctx, ...).
	// When _meta.progressToken is present, use it as the correlation token;
	// otherwise fall back to the JSON-RPC request ID.
	if sender := NotificationSenderFromCtx(ctx); sender != nil {
		token := progressTokenFromMeta(meta)
		if token == nil {
			token = req.ID
		}
		ctx = withProgressReporter(ctx, func(progress, total float64) {
			n := newProgressNotification(token, progress, total)
			sender(n)
		})
	}

	// initialize is the only method allowed before the handshake completes.
	if req.Method == methodInitialize {
		return s.handleInitialize(ctx, req)
	}

	// All other methods require initialization.
	if !s.initialized.Load() {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest, errNotInitialized.Error()), nil
	}

	// ping is exempt from authentication — it's used as a keepalive/health-check
	// and must remain accessible to monitoring probes.
	if req.Method == methodPing {
		return NewResponse(req.ID, struct{}{}), nil
	}

	// Protocol-level authentication check. Runs after initialization but
	// before method routing so that ALL MCP methods (tools/call, resources/read,
	// prompts/get, etc.) are covered uniformly.
	if checker, _ := s.authChecker.Load().(AuthChecker); checker != nil {
		if err := checker(ctx); err != nil {
			return NewErrorResponse(req.ID, ErrCodeUnauthorized, err.Error()), nil
		}
	}

	// Tenant resolution — after auth, before method routing.
	// Resolves the caller's tenant identity and produces an ItemFilter that
	// controls which tools, resources, and prompts are visible/accessible.
	// Error messages are scrubbed to prevent tenant ID enumeration.
	if resolver, _ := s.tenantResolver.Load().(TenantResolver); resolver != nil {
		filter, err := resolver(ctx)
		if err != nil {
			return NewErrorResponse(req.ID, ErrCodeTenantRequired,
				"tenant identification required"), nil
		}
		if filter != nil {
			if filter.TenantID != "" {
				ctx = WithTenantID(ctx, filter.TenantID)
			}
			ctx = withItemFilter(ctx, filter)
		}
	}

	switch req.Method {
	case methodToolsList:
		return s.handleToolsList(ctx, req)
	case methodToolsCall:
		return s.handleToolsCall(ctx, req)
	case methodResourcesList:
		return s.handleResourcesList(ctx, req)
	case methodResourcesRead:
		return s.handleResourcesRead(ctx, req)
	case methodResourcesTemplatesList:
		return s.handleResourcesTemplatesList(ctx, req)
	case methodRootsList:
		return s.handleRootsList(req)
	case methodResourcesSubscribe:
		return s.handleResourcesSubscribe(ctx, req)
	case methodResourcesUnsubscribe:
		return s.handleResourcesUnsubscribe(ctx, req)
	case methodPromptsList:
		return s.handlePromptsList(ctx, req)
	case methodPromptsGet:
		return s.handlePromptsGet(ctx, req)
	case methodCompletionComplete:
		return s.handleCompletionComplete(ctx, req)
	case methodLoggingSetLevel:
		return s.handleLoggingSetLevel(ctx, req)
	case methodPing:
		// Handled above (exempt from auth), but listed here for completeness
		// in case future code restructures the flow.
		return NewResponse(req.ID, struct{}{}), nil
	case methodTasksGet:
		return s.handleTasksGet(ctx, req)
	case methodTasksResult:
		return s.handleTasksResult(ctx, req)
	case methodTasksCancel:
		return s.handleTasksCancel(ctx, req)
	case methodTasksList:
		return s.handleTasksList(ctx, req)
	default:
		return NewErrorResponse(req.ID, ErrCodeMethodNotFound, "method not found: "+req.Method), nil
	}
}

// handleInitialize performs the MCP initialize handshake with protocol
// version negotiation.
//
// The negotiation follows the MCP specification:
//  1. The client sends its preferred protocolVersion in InitializeParams.
//  2. If the server supports that version, it echoes it back — the client
//     and server will communicate using that version.
//  3. If the server does not support the client's version, it responds with
//     the latest version it does support. The client may then choose to
//     proceed with the server's version or disconnect.
//
// An empty protocolVersion from the client is treated as invalid params.
func (s *Server) handleInitialize(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if s.initialized.Load() {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest, errAlreadyInitialized.Error()), nil
	}

	var params InitializeParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid initialize params"), nil
		}
	}

	if params.ProtocolVersion == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "protocolVersion is required"), nil
	}

	// Negotiate the protocol version.
	// If the client's requested version is in our supported set, use it.
	// Otherwise, offer our latest (preferred) version.
	negotiated := s.negotiateVersion(params.ProtocolVersion)

	s.mu.Lock()
	s.negotiatedVersion = negotiated
	s.clientCaps = params.Capabilities
	s.mu.Unlock()
	s.initialized.Store(true)

	// Only advertise unsolicited-notification capabilities when the transport
	// has injected both a NotificationSender and a stable SubscriberID.
	// A sender without a subscriber ID means resources/subscribe would always
	// be rejected (SubscriberIDFromCtx empty), so we must not advertise those
	// capabilities either.
	hasNotifChan := NotificationSenderFromCtx(ctx) != nil && SubscriberIDFromCtx(ctx) != ""

	result := InitializeResult{
		ProtocolVersion: negotiated,
		Capabilities: ServerCapabilities{
			Tools: &ToolCapability{
				ListChanged: hasNotifChan,
			},
			Resources: &ResourceCapability{
				Subscribe:   s.enableSubs && hasNotifChan,
				ListChanged: hasNotifChan,
			},
			Prompts: &PromptCapability{
				ListChanged: hasNotifChan,
			},
		},
		ServerInfo: ProcessInfo{
			Name:        s.name,
			Version:     s.version,
			Title:       s.title,
			Description: s.description,
			WebsiteURL:  s.websiteURL,
			Icons:       s.icons,
		},
		Instructions: s.instructions,
	}

	// Advertise logging capability when a log handler is registered.
	s.mu.RLock()
	hasLogHandler := s.logHandler != nil
	s.mu.RUnlock()
	if hasLogHandler {
		result.Capabilities.Logging = &LoggingCapability{}
	}

	// Advertise task capabilities when a TaskStore is configured.
	if s.taskStore != nil {
		result.Capabilities.Tasks = &TaskCapability{
			List:   struct{}{},
			Cancel: struct{}{},
			Requests: &TaskRequestsCapability{
				Tools: &TaskToolsCapability{
					Call: struct{}{},
				},
			},
		}
	}

	// Advertise completion capability when any completer is registered.
	if s.hasCompleters() {
		result.Capabilities.Completions = &CompletionCapability{}
	}

	return NewResponse(req.ID, result), nil
}

// negotiateVersion returns the protocol version to use for this session.
// If the client's requested version is in the server's supported set, it
// is returned as-is (exact match). Otherwise the server's preferred version
// (first in supportedVersions) is returned.
//
// supportedVersions should never be empty given NewServer's default and
// WithSupportedVersions' panic guard, but we fall back to the ProtocolVersion
// constant as a safety net.
func (s *Server) negotiateVersion(clientVersion string) string {
	for _, v := range s.supportedVersions {
		if v == clientVersion {
			return v
		}
	}
	// Client requested an unsupported version — fall back to our latest.
	if len(s.supportedVersions) > 0 {
		return s.supportedVersions[0]
	}
	return ProtocolVersion
}

// handlePaginatedList is a generic helper that handles cursor-based pagination
// for any list method. It preserves backward compatibility: when params are nil
// or an explicit empty object (cursor=="" && limit==0), the full unpaginated
// list is returned.
//
// Parameters:
//   - req: the JSON-RPC request (may contain ListParams in Params)
//   - items: the full slice of domain objects to paginate
//   - toInfo: converts a domain object to its wire representation
//   - wrapResult: builds the final result struct from infos and nextCursor
func handlePaginatedList[T any, Info any](
	req *JSONRPCRequest,
	items []T,
	toInfo func(T) Info,
	wrapResult func([]Info, string) any,
) (*JSONRPCResponse, error) {
	buildAll := func() (*JSONRPCResponse, error) {
		infos := make([]Info, len(items))
		for i, item := range items {
			infos[i] = toInfo(item)
		}
		return NewResponse(req.ID, wrapResult(infos, "")), nil
	}

	// Backwards compatibility: no params means return the full list.
	if req.Params == nil {
		return buildAll()
	}

	var params ListParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid list params"), nil
	}

	// Both cursor and limit are zero-values: treat as "pagination not requested"
	// to preserve backward compatibility. Clients that want the default page
	// size should send {"limit": 50} explicitly.
	if params.Cursor == "" && params.Limit == 0 {
		return buildAll()
	}

	page, nextCursor, err := paginateSlice(items, params.Cursor, params.Limit)
	if err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
	}

	infos := make([]Info, len(page))
	for i, item := range page {
		infos[i] = toInfo(item)
	}

	return NewResponse(req.ID, wrapResult(infos, nextCursor)), nil
}

// toolToInfo converts a Tool to its wire representation.
func toolToInfo(t *Tool) ToolInfo {
	schema := t.InputSchema
	if schema == nil {
		schema = map[string]string{"type": "object"}
	}
	return ToolInfo{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: schema,
		Annotations: t.Annotations,
	}
}

// handleToolsList responds to tools/list with the registered tools.
// Global tools are merged with session-specific tools (session tools shadow
// global tools with the same name). When a TenantResolver is active, tools
// are filtered to those permitted for the resolved tenant before pagination.
func (s *Server) handleToolsList(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	allTools := s.getSortedTools()
	f := itemFilterFromCtx(ctx)

	// Merge session tools: session tools shadow global tools with the same name.
	sessionID := SubscriberIDFromCtx(ctx)
	sessionSorted := s.sessionTools.sortedTools(sessionID)

	if len(sessionSorted) == 0 {
		// Fast path: no session tools — filter global tools only.
		tools := make([]*Tool, 0, len(allTools))
		for _, t := range allTools {
			if f.AllowTool(t) {
				tools = append(tools, t)
			}
		}
		return handlePaginatedList(req, tools, toolToInfo, func(infos []ToolInfo, cursor string) any {
			return ListToolsResult{Tools: infos, NextCursor: cursor}
		})
	}

	// O(N+M) sorted merge with inline tenant filtering — a single allocation
	// instead of an intermediate merged slice + separate filter pass.
	tools := mergeSessionToolsFiltered(allTools, sessionSorted, f)

	return handlePaginatedList(req, tools, toolToInfo, func(infos []ToolInfo, cursor string) any {
		return ListToolsResult{Tools: infos, NextCursor: cursor}
	})
}

// handleToolsCall responds to tools/call by executing the named tool.
// When the request includes a "task" field and the server has a TaskStore,
// the call is dispatched asynchronously and a CreateTaskResult is returned
// immediately.
func (s *Server) handleToolsCall(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	var params CallToolParams
	if req.Params == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "missing params"), nil
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid call params"), nil
	}

	if params.Name == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "tool name must not be empty"), nil
	}

	// ── Task-augmented path ────────────────────────────────────────
	if params.Task != nil {
		return s.handleTaskAugmentedCall(ctx, req, &params)
	}

	// ── Normal (synchronous) path ──────────────────────────────────

	// Attach tool name so middleware and handlers can access it.
	ctx = WithToolName(ctx, params.Name)

	// Prepare a response-meta holder so tool handlers can call SetResponseMeta.
	ctx = withResponseMetaHolder(ctx)

	// Set up a ToolStream for streaming tool responses when the transport
	// supports server-to-client notifications. The stream is closed after
	// the handler returns, ensuring all buffered chunks are flushed before
	// the final JSON-RPC response is sent.
	if sender := NotificationSenderFromCtx(ctx); sender != nil {
		meta := MetaFromCtx(ctx)
		token := progressTokenFromMeta(meta)
		if token == nil {
			token = req.ID
		}
		stream := newToolStream(ctx, sender, token, s.streamBufSize)
		ctx = withToolStream(ctx, stream)
		defer stream.close()
	}

	// Arguments is already json.RawMessage — pass directly to the handler
	// without an unnecessary marshal/unmarshal round-trip.
	input := []byte(params.Arguments)

	result, err := s.CallTool(ctx, params.Name, input)
	if err != nil {
		// errToolNotFound is a protocol error, not a tool error.
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
	}

	// Attach any response metadata set by the tool handler via SetResponseMeta.
	if rm := responseMetaFromHolder(ctx); rm != nil {
		result.Meta = rm
	}

	return NewResponse(req.ID, result), nil
}

// handleResourcesList responds to resources/list with all registered resources.
// When a TenantResolver is active, resources are filtered to those permitted
// for the resolved tenant before pagination.
func (s *Server) handleResourcesList(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	allResources := s.getSortedResources()
	f := itemFilterFromCtx(ctx)
	resources := make([]*Resource, 0, len(allResources))
	for _, r := range allResources {
		if f.AllowResource(r) {
			resources = append(resources, r)
		}
	}
	return handlePaginatedList(req, resources, func(r *Resource) ResourceInfo {
		return ResourceInfo{URI: r.URI, Name: r.Name, Description: r.Description, MimeType: r.MimeType}
	}, func(infos []ResourceInfo, cursor string) any {
		return ListResourcesResult{Resources: infos, NextCursor: cursor}
	})
}

// handleResourcesTemplatesList responds to resources/templates/list.
// When a TenantResolver is active, templates are filtered to those permitted
// for the resolved tenant before pagination.
func (s *Server) handleResourcesTemplatesList(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	allTemplates := s.getSortedTemplates()
	f := itemFilterFromCtx(ctx)
	templates := make([]*ResourceTemplate, 0, len(allTemplates))
	for _, t := range allTemplates {
		if f.AllowResourceTemplate(t) {
			templates = append(templates, t)
		}
	}
	return handlePaginatedList(req, templates, func(t *ResourceTemplate) ResourceTemplateInfo {
		return ResourceTemplateInfo{URITemplate: t.URITemplate, Name: t.Name, Description: t.Description, MimeType: t.MimeType}
	}, func(infos []ResourceTemplateInfo, cursor string) any {
		return ListResourceTemplatesResult{ResourceTemplates: infos, NextCursor: cursor}
	})
}

// handleRootsList responds to roots/list with all registered roots.
func (s *Server) handleRootsList(req *JSONRPCRequest) (*JSONRPCResponse, error) {
	return handlePaginatedList(req, s.ListRoots(), func(r *Root) RootInfo {
		return RootInfo{URI: r.URI, Name: r.Name}
	}, func(infos []RootInfo, cursor string) any {
		return ListRootsResult{Roots: infos, NextCursor: cursor}
	})
}

func (s *Server) handleResourcesSubscribe(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if !s.enableSubs {
		return NewErrorResponse(req.ID, ErrCodeMethodNotFound, "resource subscriptions are disabled"), nil
	}

	if req.Params == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "missing params"), nil
	}
	var params SubscribeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid params"), nil
	}
	if params.URI == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "uri is required"), nil
	}

	subscriberID := SubscriberIDFromCtx(ctx)
	sender := NotificationSenderFromCtx(ctx)

	if subscriberID == "" || sender == nil {
		// The transport does not provide a stable subscriber ID or a
		// notification channel, so a subscription cannot be tracked and
		// the client would never receive notifications/resources/updated.
		// Reject with an explicit error rather than silently succeeding.
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest,
			"transport does not support server-to-client notifications; resource subscriptions require a bidirectional connection"), nil
	}

	// Tenant-level filter: reject subscriptions to resources hidden from
	// this tenant. Returns the same "resource not found" error as a genuinely
	// missing resource to prevent cross-tenant enumeration.
	if f := itemFilterFromCtx(ctx); f != nil {
		// Check exact resource match.
		s.mu.RLock()
		resource := s.resources[params.URI]
		s.mu.RUnlock()
		if resource != nil && !f.AllowResource(resource) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, errResourceNotFound.Error()), nil
		}
		// Check template match (resource may be dynamic via template).
		if resource == nil {
			tmpl := s.findTemplateForURI(params.URI)
			if tmpl != nil && !f.AllowResourceTemplate(tmpl) {
				return NewErrorResponse(req.ID, ErrCodeInvalidParams, errResourceNotFound.Error()), nil
			}
		}
	}

	if err := s.Subscribe(subscriberID, params.URI, sender); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
	}

	return NewResponse(req.ID, struct{}{}), nil
}

func (s *Server) handleResourcesUnsubscribe(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if !s.enableSubs {
		return NewErrorResponse(req.ID, ErrCodeMethodNotFound, "resource subscriptions are disabled"), nil
	}

	if req.Params == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "missing params"), nil
	}
	var params SubscribeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid params"), nil
	}
	if params.URI == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "uri is required"), nil
	}

	subscriberID := SubscriberIDFromCtx(ctx)
	if subscriberID == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest,
			"transport did not provide a subscriber/connection ID; server cannot identify which subscription to remove"), nil
	}
	s.Unsubscribe(subscriberID, params.URI)

	return NewResponse(req.ID, struct{}{}), nil
}

// handleResourcesRead responds to resources/read by invoking the resource handler.
func (s *Server) handleResourcesRead(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	var params ReadResourceParams
	if req.Params == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "missing params"), nil
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid read params"), nil
	}

	if params.URI == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "resource URI must not be empty"), nil
	}

	// Prepare a response-meta holder so resource handlers can call SetResponseMeta.
	ctx = withResponseMetaHolder(ctx)

	result, err := s.ReadResource(ctx, params.URI)
	if err != nil {
		if errors.Is(err, errResourceNotFound) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
		}
		return NewErrorResponse(req.ID, ErrCodeInternalError, "resource read failed"), nil
	}

	// Attach any response metadata set by the handler via SetResponseMeta.
	if rm := responseMetaFromHolder(ctx); rm != nil {
		result.Meta = rm
	}

	return NewResponse(req.ID, result), nil
}

// handlePromptsList responds to prompts/list with all registered prompts.
// When a TenantResolver is active, prompts are filtered to those permitted
// for the resolved tenant before pagination.
func (s *Server) handlePromptsList(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	allPrompts := s.getSortedPrompts()
	f := itemFilterFromCtx(ctx)
	prompts := make([]*Prompt, 0, len(allPrompts))
	for _, p := range allPrompts {
		if f.AllowPrompt(p) {
			prompts = append(prompts, p)
		}
	}
	return handlePaginatedList(req, prompts, func(p *Prompt) PromptInfo {
		return PromptInfo{Name: p.Name, Description: p.Description, Arguments: p.Arguments}
	}, func(infos []PromptInfo, cursor string) any {
		return ListPromptsResult{Prompts: infos, NextCursor: cursor}
	})
}

// paginateSlice returns a window of items and the nextCursor for the next page.
// Cursor is interpreted as a zero-based offset encoded as a base-10 string
// (implementation detail; clients must treat cursors as opaque).
//
// The default page size is 50 when limit <= 0; the maximum page size is 1000
// (larger values are clamped silently). Cursors in [0, len(items)] are valid;
// cursors > len(items) return an error. When start >= len(items) or the page
// reaches the end, nextCursor is "" (no more items).
//
// Note: Pagination is stateless. If the registry is modified between page
// requests (e.g., tools added/removed), cursors may become stale, causing
// items to appear twice or be skipped. For stronger consistency, consider
// keyset pagination (cursor = last item ID) or snapshot-based pagination.
//
// Example:
//
//	items := []string{"a", "b", "c", "d", "e"}
//	page, next, _ := paginateSlice(items, "", 2)      // ["a", "b"], "2"
//	page, next, _ = paginateSlice(items, "2", 2)      // ["c", "d"], "4"
//	page, next, _ = paginateSlice(items, "4", 2)      // ["e"], ""
//	page, next, _ = paginateSlice(items, "5", 2)      // [], ""
//	_, _, err := paginateSlice(items, "999", 2)       // error: cursor out of range
func paginateSlice[T any](items []T, cursor string, limit int) ([]T, string, error) {
	const (
		defaultPageSize = 50
		maxPageSize     = 1000
	)

	start := 0
	if cursor != "" {
		idx, err := strconv.Atoi(cursor)
		if err != nil || idx < 0 {
			return nil, "", fmt.Errorf("invalid cursor")
		}
		if idx > len(items) {
			return nil, "", fmt.Errorf("cursor out of range")
		}
		start = idx
	}

	if start >= len(items) {
		return nil, "", nil
	}

	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	end := start + limit
	if end > len(items) {
		end = len(items)
	}

	page := items[start:end]
	if end == len(items) {
		return page, "", nil
	}

	return page, strconv.Itoa(end), nil
}

// handlePromptsGet responds to prompts/get by invoking the prompt handler.
func (s *Server) handlePromptsGet(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	var params GetPromptParams
	if req.Params == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "missing params"), nil
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid prompt params"), nil
	}

	if params.Name == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "prompt name must not be empty"), nil
	}

	// Prepare a response-meta holder so prompt handlers can call SetResponseMeta.
	ctx = withResponseMetaHolder(ctx)

	result, err := s.GetPrompt(ctx, params.Name, params.Arguments)
	if err != nil {
		if errors.Is(err, errPromptNotFound) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
		}
		return NewErrorResponse(req.ID, ErrCodeInternalError, "prompt get failed"), nil
	}

	// Attach any response metadata set by the handler via SetResponseMeta.
	if rm := responseMetaFromHolder(ctx); rm != nil {
		result.Meta = rm
	}

	return NewResponse(req.ID, result), nil
}

// handleTaskAugmentedCall dispatches a tools/call request asynchronously,
// creating a task and running the tool in a background goroutine.
func (s *Server) handleTaskAugmentedCall(ctx context.Context, req *JSONRPCRequest, params *CallToolParams) (*JSONRPCResponse, error) {
	if s.taskStore == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest, "task support is not enabled; configure the server with WithTaskStore"), nil
	}

	// Best-effort check: reject immediately if the tool doesn't exist.
	// Check session tools first — they shadow global tools for this session.
	// A narrow TOCTOU race (tool removed between check and goroutine execution)
	// is handled by the goroutine's error path, which transitions the task to failed.
	sessionID := SubscriberIDFromCtx(ctx)
	tool := s.sessionTools.lookup(sessionID, params.Name)
	if tool == nil {
		s.mu.RLock()
		tool = s.tools[params.Name]
		s.mu.RUnlock()
	}
	if tool == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, errToolNotFound.Error()), nil
	}

	// Tenant-level filter: reject task-augmented calls for tools hidden from
	// this tenant. Same indistinguishable "not found" error.
	if f := itemFilterFromCtx(ctx); f != nil && !f.AllowTool(tool) {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, errToolNotFound.Error()), nil
	}

	// Build task options from client metadata.
	opts := TaskOptions{}
	if params.Task != nil && params.Task.TTL != nil {
		opts.TTL = params.Task.TTL
	}
	if subID := SubscriberIDFromCtx(ctx); subID != "" {
		opts.OwnerID = subID
	}

	taskID := s.taskStore.GenerateID()
	task, err := s.taskStore.SubmitWithOptions(taskID, opts)
	if err != nil {
		return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
	}

	// Capture context values that the goroutine needs. The request ctx
	// will be cancelled when the HTTP response is sent, so we cannot use
	// it directly. Instead we snapshot the values and attach them to a
	// new background context.
	reqSessionID := sessionID
	reqItemFilter := itemFilterFromCtx(ctx)
	reqTenantID := TenantIDFromCtx(ctx)

	// Run the tool asynchronously — use a cancellable, time-bounded context
	// so the goroutine isn't tied to the request lifetime and cannot leak.
	go func() { // #nosec G118 -- intentionally detached from request context
		defer func() {
			if r := recover(); r != nil {
				_ = s.taskStore.Fail(taskID, fmt.Sprintf("panic: %v", r))
			}
		}()

		bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer bgCancel()

		// Store the cancel function so tasks/cancel can abort this goroutine.
		s.taskStore.storeCancelFunc(taskID, bgCancel)

		// Propagate session identity so CallTool can find session tools
		// and tenant filter so it enforces the same visibility rules.
		if reqSessionID != "" {
			bgCtx = WithSubscriberID(bgCtx, reqSessionID)
		}
		if reqItemFilter != nil {
			bgCtx = withItemFilter(bgCtx, reqItemFilter)
		}
		if reqTenantID != "" {
			bgCtx = WithTenantID(bgCtx, reqTenantID)
		}

		bgCtx = WithToolName(bgCtx, params.Name)
		bgCtx = withResponseMetaHolder(bgCtx)

		input := []byte(params.Arguments)
		result, callErr := s.CallTool(bgCtx, params.Name, input)
		if callErr != nil {
			// Attempt to fail; may silently no-op if already cancelled.
			_ = s.taskStore.Fail(taskID, callErr.Error())
			return
		}

		// If the tool returned an error result, mark task as failed.
		if result.IsError {
			msg := "tool returned error"
			if len(result.Content) > 0 {
				if tc, ok := result.Content[0].(TextContent); ok {
					msg = tc.Text
				}
			}
			_ = s.taskStore.Fail(taskID, msg)
			return
		}

		// Attach response metadata if set by the handler.
		if rm := responseMetaFromHolder(bgCtx); rm != nil {
			result.Meta = rm
		}

		// Attempt to complete; may silently no-op if already cancelled.
		_ = s.taskStore.Complete(taskID, result)
	}()

	return NewResponse(req.ID, CreateTaskResult{Task: *task}), nil
}

// ── Task method handlers ────────────────────────────────────────────

// requireTaskStore returns an error response if the server has no TaskStore
// configured. Shared guard for all tasks/* handlers.
func (s *Server) requireTaskStore(reqID any) *JSONRPCResponse {
	if s.taskStore == nil {
		return NewErrorResponse(reqID, ErrCodeInvalidRequest, "task support is not enabled")
	}
	return nil
}

// checkTaskOwnership verifies that the caller is allowed to access the task.
// When both the task's owner and the caller's subscriber ID are known,
// they must match. Returns nil when access is permitted.
func (s *Server) checkTaskOwnership(ctx context.Context, taskID string, reqID any) *JSONRPCResponse {
	ownerID := s.taskStore.ownerOf(taskID)
	callerID := SubscriberIDFromCtx(ctx)
	if ownerID != "" && callerID != "" && ownerID != callerID {
		return NewErrorResponse(reqID, ErrCodeInvalidRequest, "access denied: task belongs to another session")
	}
	return nil
}

// parseTaskIdParams is a shared helper for handlers that require a taskId parameter.
func parseTaskIdParams(req *JSONRPCRequest) (*TaskIdParams, *JSONRPCResponse) {
	if req.Params == nil {
		return nil, NewErrorResponse(req.ID, ErrCodeInvalidParams, "missing params")
	}
	var params TaskIdParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid params")
	}
	if params.TaskID == "" {
		return nil, NewErrorResponse(req.ID, ErrCodeInvalidParams, "taskId is required")
	}
	return &params, nil
}

// handleTasksGet responds to tasks/get by returning a single task.
func (s *Server) handleTasksGet(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if errResp := s.requireTaskStore(req.ID); errResp != nil {
		return errResp, nil
	}
	params, errResp := parseTaskIdParams(req)
	if errResp != nil {
		return errResp, nil
	}
	if errResp := s.checkTaskOwnership(ctx, params.TaskID, req.ID); errResp != nil {
		return errResp, nil
	}

	task, err := s.taskStore.Get(params.TaskID)
	if err != nil {
		if errors.Is(err, errTaskNotFound) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
		}
		return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
	}

	return NewResponse(req.ID, task), nil
}

// handleTasksResult responds to tasks/result by returning the completed task's result.
func (s *Server) handleTasksResult(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if errResp := s.requireTaskStore(req.ID); errResp != nil {
		return errResp, nil
	}
	params, errResp := parseTaskIdParams(req)
	if errResp != nil {
		return errResp, nil
	}
	if errResp := s.checkTaskOwnership(ctx, params.TaskID, req.ID); errResp != nil {
		return errResp, nil
	}

	result, err := s.taskStore.GetResult(params.TaskID)
	if err != nil {
		if errors.Is(err, errTaskNotFound) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
		}
		if errors.Is(err, errTaskNotCompleted) {
			return NewErrorResponse(req.ID, ErrCodeInvalidRequest, err.Error()), nil
		}
		return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
	}

	return NewResponse(req.ID, result), nil
}

// handleTasksCancel responds to tasks/cancel by cancelling a working task.
func (s *Server) handleTasksCancel(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if errResp := s.requireTaskStore(req.ID); errResp != nil {
		return errResp, nil
	}
	params, errResp := parseTaskIdParams(req)
	if errResp != nil {
		return errResp, nil
	}
	if errResp := s.checkTaskOwnership(ctx, params.TaskID, req.ID); errResp != nil {
		return errResp, nil
	}

	task, err := s.taskStore.Cancel(params.TaskID)
	if err != nil {
		if errors.Is(err, errTaskNotFound) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, err.Error()), nil
		}
		if errors.Is(err, errTaskInvalidTransit) {
			return NewErrorResponse(req.ID, ErrCodeInvalidRequest, err.Error()), nil
		}
		return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
	}

	return NewResponse(req.ID, task), nil
}

// handleTasksList responds to tasks/list by returning tasks visible to the caller,
// with pagination support.
func (s *Server) handleTasksList(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if errResp := s.requireTaskStore(req.ID); errResp != nil {
		return errResp, nil
	}

	callerID := SubscriberIDFromCtx(ctx)
	tasks := s.taskStore.ListByOwner(callerID)
	return handlePaginatedList(req, tasks, func(t Task) Task { return t }, func(infos []Task, cursor string) any {
		return ListTasksResult{Tasks: infos, NextCursor: cursor}
	})
}

// extractMeta parses the _meta object from raw JSON-RPC params.
// Returns nil if params is nil or _meta is absent or unparseable.
func extractMeta(raw json.RawMessage) map[string]any {
	if raw == nil {
		return nil
	}
	var envelope struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	return envelope.Meta
}

// progressTokenFromMeta extracts the progressToken from a _meta map.
// Returns nil if meta is nil or progressToken is absent.
func progressTokenFromMeta(meta map[string]any) any {
	if meta == nil {
		return nil
	}
	if token, ok := meta["progressToken"]; ok {
		return token
	}
	return nil
}

// ── completion/complete ─────────────────────────────────────────────

// handleCompletionComplete processes a completion/complete request.
// It resolves the reference (prompt or resource template), looks up the
// registered CompleteHandler, and returns the auto-completion results.
func (s *Server) handleCompletionComplete(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if req.Params == nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "params required"), nil
	}

	var params CompleteParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "invalid completion params"), nil
	}

	if params.Argument.Name == "" {
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "argument name is required"), nil
	}

	var completer CompleteHandler

	switch params.Ref.Type {
	case RefTypePrompt:
		name := params.Ref.Name
		if name == "" {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "ref.name is required for ref/prompt"), nil
		}
		s.mu.RLock()
		p, ok := s.prompts[name]
		s.mu.RUnlock()
		if !ok {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "prompt not found: "+name), nil
		}
		// Tenant-level filter: reject completions for prompts hidden from
		// this tenant (returns the same "not found" error).
		if f := itemFilterFromCtx(ctx); !f.AllowPrompt(p) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "prompt not found: "+name), nil
		}
		completer = p.Completer

	case RefTypeResource:
		// For resource templates, clients may send the template URI in either
		// "name" or "uri". The MCP spec does not mandate which field to use
		// for ref/resource, so we support both for maximum interoperability.
		uri := params.Ref.URI
		if uri == "" {
			uri = params.Ref.Name
		}
		if uri == "" {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "ref.uri (or ref.name) is required for ref/resource"), nil
		}
		s.mu.RLock()
		t, ok := s.templates[uri]
		s.mu.RUnlock()
		if !ok {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "resource template not found: "+uri), nil
		}
		// Tenant-level filter: reject completions for resource templates
		// hidden from this tenant (same "not found" error).
		if f := itemFilterFromCtx(ctx); !f.AllowResourceTemplate(t) {
			return NewErrorResponse(req.ID, ErrCodeInvalidParams, "resource template not found: "+uri), nil
		}
		completer = t.Completer

	default:
		return NewErrorResponse(req.ID, ErrCodeInvalidParams, "unsupported ref type: "+params.Ref.Type), nil
	}

	// If no completer is registered, return an empty result.
	if completer == nil {
		return NewResponse(req.ID, CompleteResult{
			Completion: CompletionResult{Values: []string{}},
		}), nil
	}

	result, err := completer(ctx, CompleteRequest{
		Ref:      params.Ref,
		Argument: params.Argument,
	})
	if err != nil {
		return NewErrorResponse(req.ID, ErrCodeInternalError, err.Error()), nil
	}

	if result == nil {
		result = &CompletionResult{Values: []string{}}
	}
	if result.Values == nil {
		result.Values = []string{}
	}

	// Enforce the MCP-recommended 100-value limit. If the handler returned
	// more, truncate the slice and fix up Total / HasMore so the client
	// knows additional completions are available.
	if len(result.Values) > maxCompletionValues {
		if result.Total == 0 {
			result.Total = len(result.Values)
		}
		result.Values = result.Values[:maxCompletionValues]
		result.HasMore = true
	}

	return NewResponse(req.ID, CompleteResult{
		Completion: *result,
	}), nil
}

// hasCompleters reports whether any registered prompt or resource template
// has a non-nil CompleteHandler. This is called only during initialization
// to decide whether to advertise the completions capability. The O(n) scan
// is acceptable because it runs once per session, not per request.
func (s *Server) hasCompleters() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.prompts {
		if p.Completer != nil {
			return true
		}
	}
	for _, t := range s.templates {
		if t.Completer != nil {
			return true
		}
	}
	return false
}
