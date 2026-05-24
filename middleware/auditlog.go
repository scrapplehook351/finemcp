package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Types ───────────────────────────────────────────────────────────

// AuditEntry represents a single auditable event for a tool call.
type AuditEntry struct {
	// Timestamp is the wall-clock time when the call started.
	Timestamp time.Time `json:"timestamp"`
	// ToolName is the name of the tool that was called.
	ToolName string `json:"tool_name"`
	// RequestID is the JSON-RPC request ID (may be nil).
	RequestID any `json:"request_id,omitempty"`
	// InputHash is the SHA-256 hex digest of the raw input bytes.
	// Use this for compliance without storing sensitive data.
	InputHash string `json:"input_hash"`
	// InputSize is the byte length of the raw input.
	InputSize int `json:"input_size"`
	// OutputSize is the byte length of the raw output (0 on error).
	OutputSize int `json:"output_size"`
	// Duration is how long the tool call took.
	Duration time.Duration `json:"duration"`
	// Success is true when the inner handler returned a nil error.
	Success bool `json:"success"`
	// ErrorMessage is the error string when Success is false.
	// Empty when Success is true.
	ErrorMessage string `json:"error_message,omitempty"`
}

// AuditSink receives audit entries. Implementations must be safe for
// concurrent use from multiple goroutines.
type AuditSink interface {
	// Log records a single audit entry.
	// Implementations should not block excessively; consider buffering
	// or async writes for high-throughput scenarios.
	Log(ctx context.Context, entry AuditEntry)
}

// AuditSinkFunc adapts a plain function to the AuditSink interface.
type AuditSinkFunc func(ctx context.Context, entry AuditEntry)

// Log implements AuditSink.
func (f AuditSinkFunc) Log(ctx context.Context, entry AuditEntry) { f(ctx, entry) }

// ── Configuration ───────────────────────────────────────────────────

// AuditLogOption configures the audit log middleware.
type AuditLogOption func(*auditLogConfig)

type auditLogConfig struct {
	// sink is the destination for audit entries (required).
	sink AuditSink
	// includeTools, if non-empty, limits auditing to listed tools.
	includeTools map[string]struct{}
	// excludeTools skips listed tools (ignored when includeTools is set).
	excludeTools map[string]struct{}
	// hashInput controls whether the input is hashed (default: true).
	hashInput bool
	// onError is called when the sink itself fails (optional).
	onError func(err error)
	// now provides the current time (overridable for tests).
	now func() time.Time
}

// WithAuditSink sets the audit sink. This is effectively required;
// if no sink is provided, the middleware is a no-op.
func WithAuditSink(s AuditSink) AuditLogOption {
	return func(c *auditLogConfig) {
		if s != nil {
			c.sink = s
		}
	}
}

// WithAuditIncludeTools restricts auditing to the listed tool names.
// When set, only matching tool calls are audited.
func WithAuditIncludeTools(names ...string) AuditLogOption {
	return func(c *auditLogConfig) {
		m := make(map[string]struct{}, len(names))
		for _, n := range names {
			m[n] = struct{}{}
		}
		c.includeTools = m
	}
}

// WithAuditExcludeTools skips auditing for the listed tool names.
// Ignored when WithAuditIncludeTools is also set.
func WithAuditExcludeTools(names ...string) AuditLogOption {
	return func(c *auditLogConfig) {
		m := make(map[string]struct{}, len(names))
		for _, n := range names {
			m[n] = struct{}{}
		}
		c.excludeTools = m
	}
}

// WithAuditHashInput controls whether the raw input bytes are hashed
// in the audit entry. Defaults to true. Set to false to omit the hash.
func WithAuditHashInput(hash bool) AuditLogOption {
	return func(c *auditLogConfig) {
		c.hashInput = hash
	}
}

// WithAuditOnError sets a callback for when the audit sink itself panics.
// This prevents audit failures from crashing the server.
func WithAuditOnError(fn func(error)) AuditLogOption {
	return func(c *auditLogConfig) {
		c.onError = fn
	}
}

// withAuditClock overrides the time source. Unexported; for tests.
func withAuditClock(fn func() time.Time) AuditLogOption {
	return func(c *auditLogConfig) {
		c.now = fn
	}
}

// ── Middleware constructor ───────────────────────────────────────────

// AuditLog returns a middleware that produces a compliance-ready audit
// trail for every tool call. Each call generates an [AuditEntry] sent
// to the configured [AuditSink].
//
// The middleware hashes input data by default (SHA-256) so sensitive
// payloads are not stored in plain text. Tools can be selectively
// included or excluded from auditing.
//
// Usage:
//
//	server.Use(middleware.AuditLog(
//	    middleware.WithAuditSink(myAuditSink),
//	))
//
//	// Only audit specific tools:
//	server.Use(middleware.AuditLog(
//	    middleware.WithAuditSink(myAuditSink),
//	    middleware.WithAuditIncludeTools("sensitive-tool", "payment-tool"),
//	))
func AuditLog(opts ...AuditLogOption) finemcp.Middleware {
	cfg := auditLogConfig{
		hashInput: true,
		now:       time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			// No sink configured → pass through.
			if cfg.sink == nil {
				return next(ctx, input)
			}

			toolName := finemcp.ToolName(ctx)

			// Check inclusion/exclusion filters.
			if !shouldProcess(toolName, cfg.includeTools, cfg.excludeTools) {
				return next(ctx, input)
			}

			start := cfg.now()

			// Call the inner handler.
			out, err := next(ctx, input)

			duration := cfg.now().Sub(start)

			entry := AuditEntry{
				Timestamp:  start,
				ToolName:   toolName,
				RequestID:  finemcp.RequestID(ctx),
				InputSize:  len(input),
				OutputSize: len(out),
				Duration:   duration,
				Success:    err == nil,
			}

			if cfg.hashInput && len(input) > 0 {
				h := sha256.Sum256(input)
				entry.InputHash = hex.EncodeToString(h[:])
			}

			if err != nil {
				entry.ErrorMessage = err.Error()
			}

			// Log the audit entry, recovering from any sink panic.
			safeSinkLog(cfg.sink, cfg.onError, ctx, entry)

			return out, err
		}
	}
}

// safeSinkLog calls sink.Log in a deferred-recover wrapper so that a
// panicking sink never propagates to the caller.
func safeSinkLog(sink AuditSink, onError func(error), ctx context.Context, entry AuditEntry) {
	defer func() {
		if r := recover(); r != nil {
			if onError != nil {
				switch v := r.(type) {
				case error:
					onError(v)
				default:
					// Wrap the panic value.
					onError(&sinkPanicError{value: v})
				}
			}
		}
	}()
	sink.Log(ctx, entry)
}

// sinkPanicError wraps a non-error panic value.
type sinkPanicError struct {
	value any
}

// Error implements the error interface for sinkPanicError.
func (e *sinkPanicError) Error() string {
	return "audit sink panic: " + formatPanicValue(e.value)
}

// ── InMemoryAuditSink (for testing) ─────────────────────────────────

// InMemoryAuditSink collects audit entries in memory. Thread-safe.
// Intended primarily for testing.
type InMemoryAuditSink struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// Log implements AuditSink.
func (s *InMemoryAuditSink) Log(_ context.Context, entry AuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

// Entries returns a copy of all collected audit entries.
func (s *InMemoryAuditSink) Entries() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]AuditEntry, len(s.entries))
	copy(cp, s.entries)
	return cp
}

// Len returns the number of collected audit entries.
func (s *InMemoryAuditSink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Reset clears all collected entries.
func (s *InMemoryAuditSink) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
}
