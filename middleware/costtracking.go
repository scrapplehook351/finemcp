package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Types ───────────────────────────────────────────────────────────

// CostRecord captures the resource usage for a single tool call.
type CostRecord struct {
	// Timestamp is the wall-clock time when the call started.
	Timestamp time.Time `json:"timestamp"`
	// ToolName is the name of the tool that was called.
	ToolName string `json:"tool_name"`
	// RequestID is the JSON-RPC request ID (may be nil).
	RequestID any `json:"request_id,omitempty"`
	// InputSize is the byte length of the raw input.
	InputSize int `json:"input_size"`
	// OutputSize is the byte length of the raw output (0 on error).
	OutputSize int `json:"output_size"`
	// Duration is how long the tool call took.
	Duration time.Duration `json:"duration"`
	// Success is true when the inner handler returned nil error.
	Success bool `json:"success"`
	// Cost is the computed cost for this call (tool-specific).
	// Uses float64 to support fractional units (e.g. $0.002 per 1K tokens).
	Cost float64 `json:"cost"`
	// CostUnit is an optional label for the cost (e.g. "USD", "tokens", "credits").
	CostUnit string `json:"cost_unit,omitempty"`
	// Metadata holds arbitrary key-value data attached by the cost function
	// or other observers.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// CostCollector receives cost records. Implementations must be safe for
// concurrent use from multiple goroutines.
type CostCollector interface {
	// Collect records a single cost record.
	Collect(ctx context.Context, record CostRecord)
}

// CostCollectorFunc adapts a plain function to the CostCollector interface.
type CostCollectorFunc func(ctx context.Context, record CostRecord)

// Collect implements CostCollector.
func (f CostCollectorFunc) Collect(ctx context.Context, record CostRecord) { f(ctx, record) }

// CostFunc computes the cost for a single tool call given the record.
// It may also populate Metadata, CostUnit, and Cost on the record.
// The returned record is what gets sent to the collector.
type CostFunc func(record CostRecord) CostRecord

// ── Configuration ───────────────────────────────────────────────────

// CostTrackingOption configures the cost tracking middleware.
type CostTrackingOption func(*costTrackingConfig)

type costTrackingConfig struct {
	// collector receives computed cost records (required).
	collector CostCollector
	// defaultCostFn is applied to all tools unless overridden per-tool.
	defaultCostFn CostFunc
	// perToolCostFn overrides the default cost function for specific tools.
	perToolCostFn map[string]CostFunc
	// includeTools, if non-empty, restricts tracking to listed tools.
	includeTools map[string]struct{}
	// excludeTools skips listed tools (ignored when includeTools is set).
	excludeTools map[string]struct{}
	// onError is called when the collector itself panics (optional).
	onError func(err error)
	// now provides the current time (overridable for tests).
	now func() time.Time
}

// WithCostCollector sets the cost collector. This is effectively required;
// if no collector is provided, the middleware is a no-op.
func WithCostCollector(c CostCollector) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		if c != nil {
			cfg.collector = c
		}
	}
}

// WithDefaultCostFunc sets the default cost computation function applied
// to all tools that don't have a per-tool override.
// A nil value removes any previously set default.
func WithDefaultCostFunc(fn CostFunc) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		cfg.defaultCostFn = fn
	}
}

// WithToolCostFunc sets a cost computation function for a specific tool.
// Overrides the default cost function for that tool.
func WithToolCostFunc(toolName string, fn CostFunc) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		if cfg.perToolCostFn == nil {
			cfg.perToolCostFn = make(map[string]CostFunc)
		}
		cfg.perToolCostFn[toolName] = fn
	}
}

// WithCostIncludeTools restricts cost tracking to the listed tool names.
func WithCostIncludeTools(names ...string) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		m := make(map[string]struct{}, len(names))
		for _, n := range names {
			m[n] = struct{}{}
		}
		cfg.includeTools = m
	}
}

// WithCostExcludeTools skips cost tracking for the listed tool names.
// Ignored when WithCostIncludeTools is also set.
func WithCostExcludeTools(names ...string) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		m := make(map[string]struct{}, len(names))
		for _, n := range names {
			m[n] = struct{}{}
		}
		cfg.excludeTools = m
	}
}

// WithCostOnError sets a callback for when the collector itself panics.
func WithCostOnError(fn func(error)) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		cfg.onError = fn
	}
}

// withCostClock overrides the time source. Unexported; for tests.
func withCostClock(fn func() time.Time) CostTrackingOption {
	return func(cfg *costTrackingConfig) {
		cfg.now = fn
	}
}

// ── Middleware constructor ───────────────────────────────────────────

// CostTracking returns a middleware that tracks token/API usage and computes
// cost for every tool call. Each call produces a [CostRecord] sent to the
// configured [CostCollector].
//
// By default, if no cost function is set, InputSize and OutputSize are still
// recorded — the Cost field remains 0.
//
// Usage:
//
//	// Basic: track sizes only (no cost function)
//	server.Use(middleware.CostTracking(
//	    middleware.WithCostCollector(myCollector),
//	))
//
//	// With per-call cost computation
//	server.Use(middleware.CostTracking(
//	    middleware.WithCostCollector(myCollector),
//	    middleware.WithDefaultCostFunc(func(r middleware.CostRecord) middleware.CostRecord {
//	        r.Cost = float64(r.InputSize+r.OutputSize) * 0.001
//	        r.CostUnit = "USD"
//	        return r
//	    }),
//	))
func CostTracking(opts ...CostTrackingOption) finemcp.Middleware {
	cfg := costTrackingConfig{
		now: time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			// No collector configured → pass through.
			if cfg.collector == nil {
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

			record := CostRecord{
				Timestamp:  start,
				ToolName:   toolName,
				RequestID:  finemcp.RequestID(ctx),
				InputSize:  len(input),
				OutputSize: len(out),
				Duration:   duration,
				Success:    err == nil,
			}

			// Apply cost function safely: per-tool override > default.
			record = safeCostFunc(cfg.perToolCostFn, cfg.defaultCostFn, toolName, record, cfg.onError)

			// Collect the record, recovering from panics.
			safeCollect(cfg.collector, cfg.onError, ctx, record)

			return out, err
		}
	}
}

// safeCollect calls collector.Collect in a deferred-recover wrapper.
func safeCollect(collector CostCollector, onError func(error), ctx context.Context, record CostRecord) {
	defer func() {
		if r := recover(); r != nil {
			if onError != nil {
				switch v := r.(type) {
				case error:
					onError(v)
				default:
					onError(&collectorPanicError{value: v})
				}
			}
		}
	}()
	collector.Collect(ctx, record)
}

// collectorPanicError wraps a non-error panic value from a collector.
type collectorPanicError struct {
	value any
}

// Error implements the error interface for collectorPanicError.
func (e *collectorPanicError) Error() string {
	return "cost collector panic: " + formatPanicValue(e.value)
}

// safeCostFunc applies the cost function in a panic-safe wrapper.
// On panic, the record is returned unchanged and onError is called.
func safeCostFunc(perTool map[string]CostFunc, defaultFn CostFunc, toolName string, record CostRecord, onError func(error)) (result CostRecord) {
	result = record
	defer func() {
		if r := recover(); r != nil {
			result = record // return unchanged on panic
			if onError != nil {
				switch v := r.(type) {
				case error:
					onError(v)
				default:
					onError(&collectorPanicError{value: v})
				}
			}
		}
	}()
	if fn, ok := perTool[toolName]; ok && fn != nil {
		return fn(record)
	} else if defaultFn != nil {
		return defaultFn(record)
	}
	return record
}

// ── InMemoryCostCollector (for testing) ──────────────────────────────

// InMemoryCostCollector collects cost records in memory. Thread-safe.
// Intended primarily for testing.
type InMemoryCostCollector struct {
	mu      sync.Mutex
	records []CostRecord
}

// Collect implements CostCollector.
func (c *InMemoryCostCollector) Collect(_ context.Context, record CostRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, record)
}

// Records returns a deep copy of all collected cost records.
func (c *InMemoryCostCollector) Records() []CostRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]CostRecord, len(c.records))
	for i, r := range c.records {
		cp[i] = r
		if r.Metadata != nil {
			m := make(map[string]any, len(r.Metadata))
			for k, v := range r.Metadata {
				m[k] = v
			}
			cp[i].Metadata = m
		}
	}
	return cp
}

// Len returns the number of collected records.
func (c *InMemoryCostCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

// TotalCost sums the Cost field across all records.
func (c *InMemoryCostCollector) TotalCost() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total float64
	for _, r := range c.records {
		total += r.Cost
	}
	return total
}

// Reset clears all collected records.
func (c *InMemoryCostCollector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = c.records[:0]
}
