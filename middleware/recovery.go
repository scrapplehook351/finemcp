package middleware

import (
	"context"
	"fmt"

	"github.com/finemcp/finemcp"
)

// PanicHandler is called when a panic is recovered.
// It receives the context and the recovered value, and returns
// the error message to include in the result.
type PanicHandler func(ctx context.Context, panicVal any) string

// Recovery returns a middleware that catches panics in downstream handlers
// and converts them to error results instead of crashing the server.
//
// Usage:
//
//	server.Use(middleware.Recovery())
func Recovery() finemcp.Middleware {
	return RecoveryWithHandler(nil)
}

// RecoveryWithHandler returns a recovery middleware that delegates to
// the given PanicHandler for custom panic formatting/reporting.
// If handler is nil, a default message is used.
func RecoveryWithHandler(handler PanicHandler) finemcp.Middleware {
	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) (output []byte, err error) {
			defer func() {
				if r := recover(); r != nil {
					var msg string
					if handler != nil {
						msg = handler(ctx, r)
					} else {
						msg = fmt.Sprintf("internal error: panic: %v", r)
					}
					output = nil
					err = fmt.Errorf("%s", msg)
				}
			}()
			return next(ctx, input)
		}
	}
}
