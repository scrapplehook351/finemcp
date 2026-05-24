package middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/finemcp/finemcp"
)

// SimulatorFunc is an alias for finemcp.SimulatorFunc for convenience.
// See finemcp.SimulatorFunc for documentation.
type SimulatorFunc = finemcp.SimulatorFunc

// ErrSimulationDepthExceeded is returned when nested simulation calls exceed
// the configured maximum depth.
var ErrSimulationDepthExceeded = errors.New("simulation depth exceeded")

// defaultMaxSimulationDepth is the default maximum nesting depth for
// recursive simulation calls.
const defaultMaxSimulationDepth = 3

// maxAllowedSimulationDepth is the hard upper bound for WithMaxDepth to
// prevent accidental stack exhaustion from misconfiguration.
const maxAllowedSimulationDepth = 10

// SimulationOption configures the Simulation middleware.
type SimulationOption func(*simulationConfig)

type simulationConfig struct {
	maxDepth int // 0 means use defaultMaxSimulationDepth
}

// WithMaxDepth sets the maximum nesting depth for recursive simulation calls.
// When a simulator itself invokes a tool with dryRun: true, this limits how
// deep the recursion can go before returning ErrSimulationDepthExceeded.
//
// The default is 3. A value of 1 disallows nested simulation entirely.
// Panics if depth < 1 or depth > maxAllowedSimulationDepth (currently 10).
func WithMaxDepth(depth int) SimulationOption {
	if depth < 1 || depth > maxAllowedSimulationDepth {
		panic(fmt.Sprintf("middleware.WithMaxDepth: depth must be 1-%d, got %d", maxAllowedSimulationDepth, depth))
	}
	return func(c *simulationConfig) {
		c.maxDepth = depth
	}
}

// Simulation returns a middleware that intercepts tool calls when the client
// sends _meta.dryRun: true in the request params. Instead of executing the
// real tool handler, it runs a per-tool simulator (registered via
// [finemcp.WithSimulator]) or a default simulator that returns a plain-text
// descriptive message. Custom simulators should return output in the same
// format the real handler uses (typically JSON) for consistent parsing.
//
// # Middleware ordering
//
// The full middleware chain still applies in dry-run mode. Place Simulation
// AFTER [RBAC] in the chain so that unauthorized dry-run calls are rejected
// before the simulator runs. Placing it before RBAC would allow callers to
// probe tool existence and behavior without proper authorization.
//
// Recommended ordering:
//
//	server.Use(middleware.Recovery())      // catch panics (outermost)
//	server.Use(middleware.RBAC())          // authorize first
//	server.Use(middleware.Simulation())    // then check dry-run
//	server.Use(middleware.Validation())    // validate even in dry-run
//
// # Registration
//
// Register this middleware exactly once per server. Simulators that need to
// invoke other tools MUST call them via [finemcp.Server.CallTool] to ensure
// depth tracking works correctly. Direct handler invocation bypasses
// recursion limits.
//
// # Trigger
//
// The middleware activates when _meta.dryRun is exactly the boolean value
// true. String "true", integer 1, or null are all treated as false to
// prevent accidental activation via type coercion or injection.
//
// # Response metadata
//
// The middleware calls [finemcp.SetResponseMeta](ctx, "simulated", true) to
// mark responses, and sets "simulationError": true when a simulator returns
// an error. This metadata is only visible to callers using the JSON-RPC
// dispatch layer (which installs the response meta holder). Direct calls to
// [finemcp.Server.CallTool] without dispatch will not include this metadata
// in the returned [finemcp.CallToolResult]; the simulated flag on context
// is always set regardless.
//
// # Rate limiting
//
// Dry-run calls consume the same rate-limit budget as real calls, which
// prevents abuse but may limit agent planning throughput.
//
// # Context
//
// The middleware sets the simulated flag on the context via
// [finemcp.WithSimulated]. Standard Go contexts are immutable, so this
// flag is only visible to the simulator and downstream code it calls —
// not to middleware earlier in the chain. Outer middleware that needs to
// detect dry-run mode should check [finemcp.MetaFromCtx] for the "dryRun" key.
//
// # Default simulator
//
// When no custom simulator is registered, the default returns a plain-text
// message without echoing any input data, preventing accidental leakage of
// sensitive information (API keys, passwords, etc.).
//
// # Observability
//
// Dry-run calls traverse the full middleware chain, so they appear in
// metrics and traces alongside real calls. Observability middleware should
// check [finemcp.IsSimulatedFromCtx] and tag metrics/spans appropriately
// to prevent dry-run traffic from skewing production telemetry:
//
//	func myOtelMiddleware(next finemcp.ToolHandler) finemcp.ToolHandler {
//	    return func(ctx context.Context, input []byte) ([]byte, error) {
//	        span := trace.SpanFromContext(ctx)
//	        if finemcp.IsSimulatedFromCtx(ctx) {
//	            span.SetAttributes(attribute.Bool("tool.simulated", true))
//	        }
//	        return next(ctx, input)
//	    }
//	}
//
// Usage:
//
//	server.Use(middleware.Simulation())
//
// Per-tool custom simulator:
//
//	finemcp.NewTool("deploy", deployHandler,
//	    finemcp.WithSimulator(func(ctx context.Context, input []byte) ([]byte, error) {
//	        return []byte(`{"status":"would deploy v2.1 to staging"}`), nil
//	    }),
//	)
func Simulation(opts ...SimulationOption) finemcp.Middleware {
	var cfg simulationConfig
	for _, o := range opts {
		o(&cfg)
	}

	maxDepth := cfg.maxDepth
	if maxDepth == 0 {
		maxDepth = defaultMaxSimulationDepth
	}

	return func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			if !isDryRun(ctx) {
				return next(ctx, input)
			}

			// Check nesting depth to prevent runaway recursion when a
			// simulator itself invokes tools with dryRun: true.
			depth := finemcp.SimDepthFromCtx(ctx)
			if depth >= maxDepth {
				return nil, fmt.Errorf("%w: depth %d exceeds limit %d", ErrSimulationDepthExceeded, depth, maxDepth)
			}
			ctx = finemcp.WithSimDepth(ctx, depth+1)

			// Mark context so downstream code can detect simulation.
			ctx = finemcp.WithSimulated(ctx)

			// Mark response metadata.
			finemcp.SetResponseMeta(ctx, "simulated", true)

			simulator := finemcp.ToolSimulatorFromCtx(ctx)
			if simulator == nil {
				// Default: descriptive message with no input echoing to prevent leakage.
				msg := fmt.Sprintf("Tool %q execution skipped (dry-run mode)", toolNameOrUnknown(ctx))
				return []byte(msg), nil
			}

			output, err := simulator(ctx, input)
			if err != nil {
				finemcp.SetResponseMeta(ctx, "simulationError", true)
				return nil, fmt.Errorf("simulation of %q failed: %w", toolNameOrUnknown(ctx), err)
			}
			return output, nil
		}
	}
}

// isDryRun checks whether the request _meta contains dryRun: true.
// Only a JSON boolean true triggers dry-run mode; string "true", number 1,
// and any other truthy representation are rejected to prevent accidental
// activation via type coercion or injection.
func isDryRun(ctx context.Context) bool {
	meta := finemcp.MetaFromCtx(ctx)
	if meta == nil {
		return false
	}
	v, ok := meta["dryRun"].(bool)
	return ok && v
}

// toolNameOrUnknown returns the tool name from ctx, falling back to
// "<unknown>" when the name is empty.
func toolNameOrUnknown(ctx context.Context) string {
	if name := finemcp.ToolName(ctx); name != "" {
		return name
	}
	return "<unknown>"
}
