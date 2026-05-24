package finemcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Defaults for notification handler limits.
const (
	// DefaultMaxNotificationMethods is the default maximum number of distinct
	// notification methods that can have handlers registered. Override via
	// [WithMaxNotificationMethods] if you expect more than 1000 custom methods.
	DefaultMaxNotificationMethods = 1000

	// DefaultMaxHandlersPerMethod is the default maximum number of handlers
	// that can be registered for a single notification method. Override via
	// [WithMaxHandlersPerNotification] if you need more than 100 handlers per
	// method (uncommon: most applications use 1-5 handlers per notification;
	// event-bus or plugin-system patterns may need more).
	DefaultMaxHandlersPerMethod = 100

	// maxNotifMethodNameLength is the maximum allowed length in bytes (not
	// runes) for a notification method name. Byte length is used because
	// the security goal is bounding memory, not character count.
	//
	// 512 bytes allows for deeply nested namespaces like
	// "tenant/org/dept/service/subsystem/event" while preventing
	// pathological strings.
	maxNotifMethodNameLength = 512
)

// NotificationPanicHandler is called when a notification handler panics.
// The method is the notification method name and recovered is the value
// returned by recover(). Implementations must not panic.
//
// The handler runs synchronously on the dispatch goroutine. Keep it
// fast — avoid blocking I/O. For async error reporting, launch a goroutine:
//
//	func asyncPanicHandler(method string, recovered any) {
//	    go uploadToErrorService(method, recovered)
//	}
//
// The recovered value may contain attacker-controlled data if the panicking
// handler used unsanitized input. Implementations should sanitize before
// logging to prevent log injection:
//
//	func safePanicHandler(method string, recovered any) {
//	    log.Printf("panic in %s: %T", method, recovered) // log type, not raw value
//	}
type NotificationPanicHandler func(method string, recovered any)

// NotificationHandlerFunc is called when a JSON-RPC notification with the
// registered method name is received. The params argument is the raw JSON
// from the notification's "params" field (nil when absent).
//
// Handlers are called synchronously in registration order on the dispatcher
// goroutine. A slow handler blocks processing of subsequent handlers and
// the dispatch goroutine itself. If your handler performs I/O or heavy
// computation, launch a goroutine:
//
//	s.OnNotification("heavy/work", func(ctx context.Context, params json.RawMessage) {
//	    go processAsync(ctx, params) // don't block the dispatch path
//	})
//
// The context may be cancelled during handler execution. Long-running
// handlers should check ctx.Done() periodically and return early.
// Cancellation is only enforced between handlers, not mid-execution.
//
// A panic in a handler is recovered — it does not crash the server or
// prevent subsequent handlers from running. If a [NotificationPanicHandler]
// is configured via [WithNotificationPanicHandler], it is called with the
// recovered value and method name.
//
// The handler function is retained for the lifetime of the server (or until
// [Server.RemoveNotificationHandlers] is called). Avoid capturing large
// objects in handler closures. If the handler needs access to large or
// changing state, fetch it on demand:
//
//	s.OnNotification("event", func(ctx context.Context, params json.RawMessage) {
//	    data := getCurrentData() // fetch on-demand, don't capture
//	    // ...
//	})
type NotificationHandlerFunc func(ctx context.Context, params json.RawMessage)

// OnNotification registers a handler for a JSON-RPC notification method.
// Multiple handlers can be registered for the same method — they are called
// in registration order. Handlers registered for built-in methods
// (e.g. "notifications/initialized", "notifications/cancelled") run after
// the server's built-in processing for that method.
//
// Panics if method is empty, contains invalid characters, handler is nil,
// or the handler limit is exceeded.
//
// Method names are validated for MCP protocol compliance. Do not use method
// names in file paths, shell commands, or SQL queries without additional
// sanitization.
//
// Safe for concurrent use.
func (s *Server) OnNotification(method string, handler NotificationHandlerFunc) {
	if method == "" {
		panic("finemcp: OnNotification requires a non-empty method")
	}
	if len(method) > maxNotifMethodNameLength {
		panic(fmt.Sprintf("finemcp: notification method name too long (%d bytes, max %d)", len(method), maxNotifMethodNameLength))
	}
	if !isValidNotifMethodName(method) {
		panic(fmt.Sprintf("finemcp: invalid notification method name %q: must contain only ASCII [a-zA-Z0-9_\\-/.] with no leading, trailing, or consecutive slashes", method))
	}
	if handler == nil {
		panic("finemcp: OnNotification requires a non-nil handler")
	}

	maxMethods := s.maxNotifMethods
	if maxMethods <= 0 {
		maxMethods = DefaultMaxNotificationMethods
	}
	maxPerMethod := s.maxHandlersPerMethod
	if maxPerMethod <= 0 {
		maxPerMethod = DefaultMaxHandlersPerMethod
	}

	s.notifMu.Lock()
	defer s.notifMu.Unlock()

	if _, exists := s.notificationHandlers[method]; !exists {
		if len(s.notificationHandlers) >= maxMethods {
			panic(fmt.Sprintf("finemcp: notification method limit exceeded (%d)", maxMethods))
		}
	}
	if len(s.notificationHandlers[method]) >= maxPerMethod {
		panic(fmt.Sprintf("finemcp: handler limit for method %q exceeded (%d)", method, maxPerMethod))
	}

	s.notificationHandlers[method] = append(s.notificationHandlers[method], handler)
}

// RemoveNotificationHandlers removes all handlers registered for the given
// notification method. Returns the number of handlers that were removed.
// This is useful for per-session cleanup patterns where handlers are
// registered and later torn down.
//
// Removal is not retroactive: handlers for in-flight notifications that
// acquired the handler list before removal will still complete. For strict
// cleanup guarantees, cancel the session context before calling
// RemoveNotificationHandlers so that in-flight handlers observe
// cancellation.
//
// Returns 0 for empty or oversized method names (which could never have
// been registered via [Server.OnNotification]).
//
// Safe for concurrent use.
func (s *Server) RemoveNotificationHandlers(method string) int {
	if method == "" || len(method) > maxNotifMethodNameLength {
		return 0
	}

	s.notifMu.Lock()
	defer s.notifMu.Unlock()

	n := len(s.notificationHandlers[method])
	delete(s.notificationHandlers, method)
	return n
}

// NotificationStats returns a point-in-time snapshot of registered
// notification handler counts keyed by method name. The snapshot is
// consistent (all counts from the same instant) but may become stale
// if handlers are added or removed after the call returns. The returned
// map is a copy and safe to mutate. Useful for debugging and monitoring.
//
// Map iteration order is non-deterministic per Go semantics.
//
// This is a server-wide view: in multi-tenant deployments the result
// includes methods from all tenants. Restrict access to trusted operators.
//
// Allocates O(N) memory where N is the number of registered methods.
// Not intended for hot-path use.
func (s *Server) NotificationStats() map[string]int {
	s.notifMu.RLock()
	defer s.notifMu.RUnlock()

	stats := make(map[string]int, len(s.notificationHandlers))
	for method, handlers := range s.notificationHandlers {
		stats[method] = len(handlers)
	}
	return stats
}

// isValidNotifMethodName reports whether name is a non-empty string
// containing only ASCII characters allowed in MCP/JSON-RPC method
// names: [a-zA-Z0-9_\-/.]. Leading, trailing, and consecutive slashes
// are rejected per the MCP method name grammar (segment *("/" segment)).
func isValidNotifMethodName(name string) bool {
	if len(name) == 0 {
		return false
	}
	if name[0] == '/' || name[len(name)-1] == '/' {
		return false
	}
	prevSlash := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '/' {
			if prevSlash {
				return false // consecutive slashes
			}
			prevSlash = true
		} else {
			prevSlash = false
		}
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-' || c == '/' || c == '.') {
			return false
		}
	}
	return true
}
