package finemcp

import "context"

// contextKey is an unexported type for context keys defined in this package.
// This prevents collisions with keys defined in other packages.
type contextKey int

const (
	// ctxKeyRequestID stores the JSON-RPC request ID.
	ctxKeyRequestID contextKey = iota

	// ctxKeyToolName stores the name of the tool being called.
	ctxKeyToolName

	// ctxKeyRoles stores the caller's roles for RBAC checks.
	ctxKeyRoles

	// ctxKeyToolRoles stores the required roles for the tool being called.
	ctxKeyToolRoles

	// ctxKeyToolSchema stores the tool's InputSchema for validation middleware.
	ctxKeyToolSchema

	// ctxKeySkipValidation signals that input validation should be skipped.
	ctxKeySkipValidation

	// ctxKeyProgressReporter stores the progress reporting function.
	ctxKeyProgressReporter

	// ctxKeyNotificationSender stores the transport's notification send function.
	ctxKeyNotificationSender

	// ctxKeySubscriberID stores the stable connection/session ID for subscription tracking.
	ctxKeySubscriberID

	// ctxKeyMeta stores the request's _meta object from the client.
	ctxKeyMeta

	// ctxKeyResponseMeta stores a *responseMetaHolder so handlers can attach
	// metadata to the response without returning a new context.
	ctxKeyResponseMeta

	// ctxKeyRequestSender stores the transport's server-to-client request function.
	ctxKeyRequestSender

	// ctxKeyAuthInfo stores the verified caller identity set by authentication.
	ctxKeyAuthInfo

	// ctxKeyTenantID stores the resolved tenant identifier for multi-tenant isolation.
	ctxKeyTenantID

	// ctxKeyItemFilter stores the per-request visibility filter for multi-tenant isolation.
	ctxKeyItemFilter

	// ctxKeyToolSimulator stores the per-tool SimulatorFunc for dry-run mode.
	ctxKeyToolSimulator

	// ctxKeySimulated signals that the current call is a simulation (dry-run).
	ctxKeySimulated

	// ctxKeySimDepth tracks the current simulation nesting depth.
	ctxKeySimDepth

	// ctxKeyToolStream stores the *ToolStream for streaming tool responses.
	ctxKeyToolStream
)

// WithRequestID returns a copy of ctx with the JSON-RPC request ID attached.
func WithRequestID(ctx context.Context, id any) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestID extracts the JSON-RPC request ID from the context.
// Returns nil if not set.
func RequestID(ctx context.Context) any {
	return ctx.Value(ctxKeyRequestID)
}

// WithToolName returns a copy of ctx with the tool name attached.
func WithToolName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyToolName, name)
}

// ToolName extracts the tool name from the context.
// Returns "" if not set.
func ToolName(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyToolName).(string)
	return v
}

// WithRolesCtx returns a copy of ctx with the caller's roles attached.
func WithRolesCtx(ctx context.Context, roles []string) context.Context {
	// Defensive copy to prevent mutation.
	cp := make([]string, len(roles))
	copy(cp, roles)
	return context.WithValue(ctx, ctxKeyRoles, cp)
}

// RolesFromCtx extracts the caller's roles from the context.
// Returns nil if not set.
func RolesFromCtx(ctx context.Context) []string {
	v, _ := ctx.Value(ctxKeyRoles).([]string)
	return v
}

// withToolRoles returns a copy of ctx with the tool's required roles attached.
// This is set internally by Server.CallTool so middleware can inspect it.
func withToolRoles(ctx context.Context, roles []string) context.Context {
	return context.WithValue(ctx, ctxKeyToolRoles, roles)
}

// ToolRolesFromCtx extracts the tool's required roles from the context.
// Returns nil if the tool has no role requirements.
// This is set internally by Server.CallTool and used by middleware like RBAC.
func ToolRolesFromCtx(ctx context.Context) []string {
	v, _ := ctx.Value(ctxKeyToolRoles).([]string)
	return v
}

// withToolSchema returns a copy of ctx with the tool's InputSchema attached.
// This is set internally by Server.CallTool so validation middleware can access it.
func withToolSchema(ctx context.Context, schema any) context.Context {
	return context.WithValue(ctx, ctxKeyToolSchema, schema)
}

// ToolSchemaFromCtx extracts the tool's InputSchema from the context.
// Returns nil if no schema is set. Used by validation middleware.
func ToolSchemaFromCtx(ctx context.Context) any {
	return ctx.Value(ctxKeyToolSchema)
}

// withSkipValidation returns a copy of ctx with the skip-validation flag set.
func withSkipValidation(ctx context.Context, skip bool) context.Context {
	return context.WithValue(ctx, ctxKeySkipValidation, skip)
}

// SkipValidationFromCtx reports whether input validation should be skipped.
// Returns false if not set.
func SkipValidationFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeySkipValidation).(bool)
	return v
}

// ProgressReporter is a function tool handlers call to emit a progress notification.
// progress is the current value; total is the expected final value (0 = indeterminate).
type ProgressReporter func(progress, total float64)

// withProgressReporter attaches a progress reporter to the context.
// This is set internally by the dispatch layer when a NotificationSender is available.
func withProgressReporter(ctx context.Context, fn ProgressReporter) context.Context {
	return context.WithValue(ctx, ctxKeyProgressReporter, fn)
}

// ProgressReporterFromCtx extracts the ProgressReporter from the context.
// Returns nil if no reporter is set (progress reporting is a no-op in that case).
func ProgressReporterFromCtx(ctx context.Context) ProgressReporter {
	v, _ := ctx.Value(ctxKeyProgressReporter).(ProgressReporter)
	return v
}

// NotificationSender is a function that transports implement to send server-side
// notifications (like notifications/progress) back to the client.
type NotificationSender func(n *JSONRPCNotification)

// WithNotificationSender attaches a notification sender to the context.
// Transports call this before invoking HandleMessage so that tool handlers
// can emit mid-execution notifications via ReportProgress.
func WithNotificationSender(ctx context.Context, fn NotificationSender) context.Context {
	return context.WithValue(ctx, ctxKeyNotificationSender, fn)
}

// NotificationSenderFromCtx extracts the NotificationSender from the context.
// Returns nil if the transport does not support server-side notifications.
func NotificationSenderFromCtx(ctx context.Context) NotificationSender {
	v, _ := ctx.Value(ctxKeyNotificationSender).(NotificationSender)
	return v
}

// WithRequestSender attaches a server-to-client request sender to the context.
// Transports call this before invoking HandleMessage so that server methods
// like CreateMessage can send requests to the client and await responses.
func WithRequestSender(ctx context.Context, fn RequestSender) context.Context {
	return context.WithValue(ctx, ctxKeyRequestSender, fn)
}

// RequestSenderFromCtx extracts the RequestSender from the context.
// Returns nil if the transport does not support server-to-client requests.
func RequestSenderFromCtx(ctx context.Context) RequestSender {
	v, _ := ctx.Value(ctxKeyRequestSender).(RequestSender)
	return v
}

// WithSubscriberID attaches a stable per-connection identifier to the context.
// Transports set this once per connection/session so the server can associate
// subscriptions and broadcast senders with a specific client.
func WithSubscriberID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeySubscriberID, id)
}

// SubscriberIDFromCtx extracts the subscriber/connection ID from the context.
// Returns "" if not set.
func SubscriberIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeySubscriberID).(string)
	return v
}

// WithMeta returns a copy of ctx with the request's _meta attached.
// This is set by the dispatch layer when the client includes _meta in the
// request params. Handlers and middleware can inspect it via MetaFromCtx.
func WithMeta(ctx context.Context, meta map[string]any) context.Context {
	return context.WithValue(ctx, ctxKeyMeta, meta)
}

// MetaFromCtx extracts the request _meta from the context.
// Returns nil if the client did not send _meta.
func MetaFromCtx(ctx context.Context) map[string]any {
	v, _ := ctx.Value(ctxKeyMeta).(map[string]any)
	return v
}

// responseMetaHolder is stored in context by pointer so tool handlers can
// accumulate response metadata without modifying the context itself.
//
// Not safe for concurrent writes from multiple goroutines; handlers should
// call SetResponseMeta only from the main handler goroutine.
type responseMetaHolder struct {
	meta map[string]any
}

// withResponseMetaHolder attaches a fresh response-meta holder to the context.
// The dispatch layer calls this before invoking a handler.
func withResponseMetaHolder(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyResponseMeta, &responseMetaHolder{})
}

// responseMetaFromHolder extracts the accumulated response _meta.
// Returns nil if no metadata was set.
func responseMetaFromHolder(ctx context.Context) map[string]any {
	h, _ := ctx.Value(ctxKeyResponseMeta).(*responseMetaHolder)
	if h == nil {
		return nil
	}
	return h.meta
}

// SetResponseMeta attaches a key-value pair to the response _meta.
// Tool, resource, and prompt handlers can call this to include custom
// metadata in the response without modifying the result struct directly.
//
// This is only effective for tools/call, resources/read, and prompts/get
// handlers — the dispatch layer wires up the response meta holder for
// those methods. Calls from other contexts are a safe no-op.
//
// Example:
//
//	func myHandler(ctx context.Context, input []byte) ([]byte, error) {
//	    finemcp.SetResponseMeta(ctx, "processingTimeMs", 42)
//	    return []byte("done"), nil
//	}
func SetResponseMeta(ctx context.Context, key string, value any) {
	h, _ := ctx.Value(ctxKeyResponseMeta).(*responseMetaHolder)
	if h == nil {
		return
	}
	if h.meta == nil {
		h.meta = make(map[string]any)
	}
	h.meta[key] = value
}

// SimulatorFunc is the function signature for a custom dry-run handler.
// It receives the same (ctx, input) as the real tool handler and should
// return a representative output describing what the tool would do.
//
// A SimulatorFunc MUST NOT perform real side effects (e.g., database
// writes, HTTP calls to external services, file deletions). It must be
// safe for concurrent use — the server may invoke the same simulator
// from multiple goroutines simultaneously. Avoid mutable state or
// protect it with appropriate synchronization.
//
// See middleware.Simulation and WithSimulator.
type SimulatorFunc func(ctx context.Context, input []byte) ([]byte, error)

// withToolSimulator attaches the tool's SimulatorFunc to the context.
// This is set internally by Server.CallTool so Simulation middleware can access it.
func withToolSimulator(ctx context.Context, fn SimulatorFunc) context.Context {
	return context.WithValue(ctx, ctxKeyToolSimulator, fn)
}

// ToolSimulatorFromCtx extracts the tool's SimulatorFunc from the context.
// Returns nil if the tool has no custom simulator.
func ToolSimulatorFromCtx(ctx context.Context) SimulatorFunc {
	v, _ := ctx.Value(ctxKeyToolSimulator).(SimulatorFunc)
	return v
}

// WithSimulated marks the context as a simulation (dry-run) call.
// The Simulation middleware calls this before invoking the simulator so
// that the simulator and any code it calls can detect dry-run mode via
// IsSimulatedFromCtx.
//
// Note: because Go contexts are immutable, the simulated flag is only
// visible to the simulator and downstream calls — middleware earlier in
// the chain (outer wrappers) will not see it. Outer middleware that
// needs to detect dry-run mode should check MetaFromCtx for the
// "dryRun" key instead.
func WithSimulated(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySimulated, true)
}

// IsSimulatedFromCtx reports whether the current call is a simulation.
// This is only true inside the simulator and any code it calls; outer
// middleware should use MetaFromCtx to check for dry-run mode.
func IsSimulatedFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeySimulated).(bool)
	return v
}

// WithSimDepth attaches the current simulation nesting depth to the context.
func WithSimDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, ctxKeySimDepth, depth)
}

// SimDepthFromCtx returns the current simulation nesting depth.
// Returns 0 if not inside a simulation.
func SimDepthFromCtx(ctx context.Context) int {
	v, _ := ctx.Value(ctxKeySimDepth).(int)
	return v
}

// withToolStream attaches a [ToolStream] to the context. This is set by the
// dispatch layer in handleToolsCall when the transport supports notifications.
func withToolStream(ctx context.Context, stream *ToolStream) context.Context {
	return context.WithValue(ctx, ctxKeyToolStream, stream)
}

// StreamFromCtx extracts the [ToolStream] for the current tool call.
// Returns nil only for plain HTTP transport, which has no persistent
// server-to-client notification channel. Stdio, WebSocket, SSE, and
// Streamable HTTP all inject a NotificationSender and return a valid stream.
// Tool handlers should check for nil and fall back to returning the full
// result when streaming is unavailable.
func StreamFromCtx(ctx context.Context) *ToolStream {
	v, _ := ctx.Value(ctxKeyToolStream).(*ToolStream)
	return v
}

// WithTenantID returns a copy of ctx with the resolved tenant ID attached.
// This is set by the TenantResolver during request dispatch and can be read
// by downstream middleware (rate limiter, audit log, cost tracking) via
// TenantIDFromCtx.
func WithTenantID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyTenantID, id)
}

// TenantIDFromCtx extracts the resolved tenant ID from the context.
// Returns "" if no tenant ID is set (single-tenant mode or before resolution).
func TenantIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTenantID).(string)
	return v
}

// withItemFilter attaches a per-request visibility filter to the context.
// This is set by the dispatch layer after tenant resolution.
func withItemFilter(ctx context.Context, f *ItemFilter) context.Context {
	return context.WithValue(ctx, ctxKeyItemFilter, f)
}

// itemFilterFromCtx extracts the per-request visibility filter.
// Returns nil if no filter is set (no multi-tenancy or filter allows all).
func itemFilterFromCtx(ctx context.Context) *ItemFilter {
	v, _ := ctx.Value(ctxKeyItemFilter).(*ItemFilter)
	return v
}

// AuthInfo represents the verified identity of an authenticated caller.
// It is injected into context by the authentication layer (transport HTTP
// middleware) after successful credential verification.
//
// Consumers (RBAC, audit log, logging, handlers) read it via AuthInfoFromCtx.
type AuthInfo struct {
	// Subject identifies the authenticated caller (e.g. user ID, service name).
	Subject string

	// Roles contains the caller's authorization roles for RBAC.
	Roles []string

	// Meta holds additional claims from the token (e.g. JWT claims).
	// May be nil.
	Meta map[string]any
}

// WithAuthInfo returns a copy of ctx with the verified caller identity attached.
// The Roles slice and Meta map are defensively copied to prevent mutation.
func WithAuthInfo(ctx context.Context, info AuthInfo) context.Context {
	// Defensive copy of Roles.
	if info.Roles != nil {
		cp := make([]string, len(info.Roles))
		copy(cp, info.Roles)
		info.Roles = cp
	}
	// Defensive copy of Meta.
	if info.Meta != nil {
		cp := make(map[string]any, len(info.Meta))
		for k, v := range info.Meta {
			cp[k] = v
		}
		info.Meta = cp
	}
	// Always set roles in context so existing RBAC middleware works without changes.
	// Called unconditionally so that a second WithAuthInfo with empty Roles
	// correctly clears any previously-set RBAC roles.
	ctx = WithRolesCtx(ctx, info.Roles)
	return context.WithValue(ctx, ctxKeyAuthInfo, &info)
}

// AuthInfoFromCtx extracts the verified caller identity from the context.
// Returns nil if no identity is set (unauthenticated request).
func AuthInfoFromCtx(ctx context.Context) *AuthInfo {
	v, _ := ctx.Value(ctxKeyAuthInfo).(*AuthInfo)
	return v
}
