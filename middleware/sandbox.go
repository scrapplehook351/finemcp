package middleware

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/finemcp/finemcp"
)

// ErrSandboxTimeout is returned when a tool handler exceeds its execution deadline.
var ErrSandboxTimeout = errors.New("sandbox: execution timeout")

// SandboxOption configures the sandbox middleware.
type SandboxOption func(*sandboxConfig)

type sandboxConfig struct {
	timeout       time.Duration // 0 means no timeout
	maxOutputSize int           // 0 means unlimited
}

// WithTimeout sets the maximum execution time for a tool handler.
// If the handler does not return within this duration, its context is
// cancelled and ErrSandboxTimeout is returned.
//
// A zero value means no timeout is applied.
// Panics if timeout is negative.
func WithTimeout(d time.Duration) SandboxOption {
	if d < 0 {
		panic("middleware.WithTimeout: timeout must be >= 0")
	}
	return func(c *sandboxConfig) {
		c.timeout = d
	}
}

// WithMaxOutputSize limits the byte length of a tool's output.
// If the output exceeds this limit it is truncated and a "[truncated]"
// marker is appended; the resulting output is always at most size bytes
// long, including the marker.
//
// A zero value means no limit.
// Panics if size is negative.
func WithMaxOutputSize(size int) SandboxOption {
	if size < 0 {
		panic("middleware.WithMaxOutputSize: size must be >= 0")
	}
	return func(c *sandboxConfig) {
		c.maxOutputSize = size
	}
}

// Sandbox returns a middleware that constrains tool execution:
//   - Timeout: cancels handlers that exceed a deadline.
//   - Output size: truncates oversized results.
//
// Important: when a timeout fires, the handler goroutine keeps running
// until it returns. Handlers MUST respect ctx.Done() to avoid leaking
// goroutines. For workloads that may ignore cancellation, combine
// Sandbox with a concurrency limiter to cap in-flight executions.
//
// Usage:
//
//	server.Use(middleware.Sandbox(
//	    middleware.WithTimeout(5 * time.Second),
//	    middleware.WithMaxOutputSize(1 << 20), // 1 MiB
//	))
func Sandbox(opts ...SandboxOption) finemcp.Middleware {
	var cfg sandboxConfig
	for _, o := range opts {
		o(&cfg)
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			out, err := execute(ctx, next, input, cfg.timeout)
			if err != nil {
				return nil, err
			}
			out = truncate(out, cfg.maxOutputSize)
			return out, nil
		}
	}
}

// execute runs the handler, optionally wrapped with a timeout.
func execute(
	ctx context.Context,
	handler finemcp.ToolHandler,
	input []byte,
	timeout time.Duration,
) ([]byte, error) {
	if timeout <= 0 {
		// No timeout — call directly on the current goroutine.
		return handler(ctx, input)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)

	go func() {
		out, err := handler(ctx, input)
		ch <- result{out, err}
	}()

	select {
	case r := <-ch:
		// If the context expired due to a deadline, surface this as a sandbox timeout
		// regardless of whether the handler returned an error. For other types of
		// cancellation, preserve the original error semantics.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if r.err != nil {
				return nil, fmt.Errorf("%w: %v", ErrSandboxTimeout, r.err)
			}
			return nil, ErrSandboxTimeout
		}
		return r.out, r.err
	case <-ctx.Done():
		// Handler did not return before the deadline or the context was otherwise cancelled.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrSandboxTimeout
		}
		return nil, ctx.Err()
	}
}

// truncate shortens output to at most maxSize bytes when maxSize > 0.
func truncate(data []byte, maxSize int) []byte {
	if maxSize <= 0 || data == nil || len(data) <= maxSize {
		return data
	}
	const marker = "[truncated]"
	if maxSize <= len(marker) {
		return []byte(marker)[:maxSize]
	}
	truncated := make([]byte, 0, maxSize)
	truncated = append(truncated, data[:maxSize-len(marker)]...)
	truncated = append(truncated, marker...)
	return truncated
}
