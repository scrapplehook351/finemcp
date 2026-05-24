//go:build windows

package transport

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// setupSignals configures OS signal handling for graceful shutdown on Windows.
// Windows supports SIGINT (Ctrl+C) and os.Interrupt, but not SIGTERM.
func setupSignals(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGINT, os.Interrupt)
}
