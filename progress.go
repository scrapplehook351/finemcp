package finemcp

import "context"

// ReportProgress emits a notifications/progress event for the current tool call.
// progress is the current value (e.g. bytes processed, items completed).
// total is the expected final value; pass 0 for indeterminate progress.
//
// If the transport does not support server-to-client notifications (e.g. plain HTTP),
// or if no reporter was injected into ctx, this call is a no-op.
//
// On the SSE transport, progress notifications are delivered on a best-effort
// basis: if the session's event buffer is full the notification is silently
// dropped. Tool authors should not rely on every notification being delivered.
//
// Example:
//
//	tool, _ := finemcp.NewTypedTool("process", func(ctx context.Context, in Input) (string, error) {
//	    for i, item := range in.Items {
//	        finemcp.ReportProgress(ctx, float64(i+1), float64(len(in.Items)))
//	        // ... process item ...
//	    }
//	    return "done", nil
//	})
func ReportProgress(ctx context.Context, progress, total float64) {
	if fn := ProgressReporterFromCtx(ctx); fn != nil {
		fn(progress, total)
	}
}

// newProgressNotification builds a JSONRPCNotification for notifications/progress.
func newProgressNotification(token any, progress, total float64) *JSONRPCNotification {
	return &JSONRPCNotification{
		JSONRPC: jsonrpcVersion,
		Method:  methodProgress,
		Params: ProgressParams{
			ProgressToken: token,
			Progress:      progress,
			Total:         total,
		},
	}
}
