package middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/finemcp/finemcp"
)

// DeniedHandler is called when RBAC denies access.
// It receives the context, the tool's required roles, and the caller's roles.
// It returns the error message to include in the result.
type DeniedHandler func(ctx context.Context, required, actual []string) string

// RBAC returns a middleware that enforces role-based access control.
// It compares the tool's required roles (set via WithRoles) against the
// caller's roles (set via WithRolesCtx on the context).
//
// Behaviour:
//   - Tool has no required roles → allowed (open access).
//   - Caller has at least one role matching a required role → allowed.
//   - Otherwise → blocked with an error.
//
// Usage:
//
//	server.Use(middleware.RBAC())
func RBAC() finemcp.Middleware {
	return RBACWithDenied(nil)
}

// RBACWithDenied returns an RBAC middleware with a custom denied handler
// for customizing the error message or logging denials.
// If handler is nil, a default message is used.
func RBACWithDenied(handler DeniedHandler) finemcp.Middleware {
	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			required := finemcp.ToolRolesFromCtx(ctx)

			// No role requirements → open access.
			if len(required) == 0 {
				return next(ctx, input)
			}

			callerRoles := finemcp.RolesFromCtx(ctx)
			if hasAnyRole(callerRoles, required) {
				return next(ctx, input)
			}

			var msg string
			if handler != nil {
				msg = handler(ctx, required, callerRoles)
			} else {
				msg = fmt.Sprintf("forbidden: tool %q requires one of %v", finemcp.ToolName(ctx), required)
			}
			return nil, errors.New(msg)
		}
	}
}

// hasAnyRole returns true if any element in caller appears in required.
func hasAnyRole(caller, required []string) bool {
	// For small role sets (typical), linear scan is faster than a map.
	for _, c := range caller {
		for _, r := range required {
			if c == r {
				return true
			}
		}
	}
	return false
}
