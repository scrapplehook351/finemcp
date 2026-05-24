package middleware

import (
	"context"
	"time"

	"github.com/finemcp/finemcp"
)

// Logger is the logging abstraction used by FineMCP middleware.
// Implement this interface to plug in your preferred logging framework
// (slog, zap, zerolog, logrus, etc.).
type Logger interface {
	// Info logs an informational message with structured key-value pairs.
	Info(msg string, keysAndValues ...any)
	// Error logs an error message with structured key-value pairs.
	Error(msg string, keysAndValues ...any)
}

// NopLogger is a Logger that discards all output.
// Useful for testing or disabling logging.
var NopLogger Logger = nopLogger{}

// nopLogger discards all log output; used to back [NopLogger].
type nopLogger struct{}

// Info implements [Logger] by discarding the message.
func (nopLogger) Info(string, ...any) {}

// Error implements [Logger] by discarding the message.
func (nopLogger) Error(string, ...any) {}

// Logging returns a middleware that logs every tool call with:
//   - tool name
//   - request ID (from context, if present)
//   - execution duration
//   - success or failure status
//
// Usage:
//
//	server.Use(middleware.Logging(myLogger))
func Logging(logger Logger) finemcp.Middleware {
	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			toolName := finemcp.ToolName(ctx)
			requestID := finemcp.RequestID(ctx)
			start := time.Now()

			out, err := next(ctx, input)

			duration := time.Since(start)
			fields := []any{
				"tool", toolName,
				"duration", duration.String(),
			}
			if requestID != nil {
				fields = append(fields, "requestID", requestID)
			}

			if err != nil {
				fields = append(fields, "error", err.Error())
				logger.Error("tool call failed", fields...)
			} else {
				logger.Info("tool call completed", fields...)
			}

			return out, err
		}
	}
}
