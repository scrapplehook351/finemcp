//go:build !windows

package transport

import (
	"context"
	"os/signal"
	"syscall"
)

// setupSignals configures OS signal handling for graceful shutdown on Unix-like systems.
// Supports SIGINT (Ctrl+C) and SIGTERM (termination signal).
func setupSignals(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
}
