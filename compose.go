package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ── Pipeline (sequential) ───────────────────────────────────────────

// Pipeline returns a ToolHandler that chains handlers sequentially.
// The output of each handler becomes the input of the next. Execution
// stops at the first error (fail-fast). If a handler panics, the panic
// is recovered and returned as an error, preventing execution of
// subsequent handlers.
//
// At least two handlers are required; Pipeline panics otherwise.
//
// Example:
//
//	fetch := fetchHandler()
//	transform := transformHandler()
//	validate := validateHandler()
//
//	composed, _ := NewTool("etl",
//	    Pipeline(fetch, transform, validate),
//	    WithDescription("Fetch → Transform → Validate"),
//	)
func Pipeline(first, second ToolHandler, rest ...ToolHandler) ToolHandler {
	handlers := make([]ToolHandler, 0, 2+len(rest))
	handlers = append(handlers, first, second)
	handlers = append(handlers, rest...)

	// Validate — no nil handlers.
	for i, h := range handlers {
		if h == nil {
			panic(fmt.Sprintf("finemcp.Pipeline: handler at index %d is nil", i))
		}
	}

	return func(ctx context.Context, input []byte) (_ []byte, retErr error) {
		defer func() {
			if r := recover(); r != nil {
				retErr = fmt.Errorf("panic in pipeline handler: %v", r)
			}
		}()
		data := input
		for _, h := range handlers {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out, err := h(ctx, data)
			if err != nil {
				return nil, err
			}
			data = out
		}
		return data, nil
	}
}

// toJSONValue converts raw handler output to a value suitable for embedding
// in a JSON document. If the output is already valid JSON it is returned as-is;
// otherwise it is JSON-encoded as a string.
//
// Performance note: json.Valid scans the entire byte slice. For handlers
// returning very large outputs (>1 MB), this may add measurable overhead.
// Consider having such handlers return pre-validated JSON when possible.
func toJSONValue(raw []byte) json.RawMessage {
	if json.Valid(raw) {
		return json.RawMessage(raw)
	}
	encoded, _ := json.Marshal(string(raw))
	return json.RawMessage(encoded)
}

// ── Parallel (concurrent fan-out, keyed results) ────────────────────

// ParallelResult holds the output or error for a single branch of a
// parallel composition.
type ParallelResult struct {
	// Output holds the raw bytes returned by the branch handler.
	// If the output is valid JSON it is embedded verbatim; otherwise it is
	// encoded as a JSON string (via toJSONValue).
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// validateNamedHandlers checks that all handlers are non-nil and have unique
// non-empty names. Panics with a descriptive message if validation fails.
func validateNamedHandlers(handlers []NamedHandler, caller string) {
	seen := make(map[string]struct{}, len(handlers))
	for i, nh := range handlers {
		if nh.Handler == nil {
			panic(fmt.Sprintf("finemcp.%s: handler %q at index %d is nil", caller, nh.Name, i))
		}
		if nh.Name == "" {
			panic(fmt.Sprintf("finemcp.%s: handler at index %d has empty name", caller, i))
		}
		if _, dup := seen[nh.Name]; dup {
			panic(fmt.Sprintf("finemcp.%s: duplicate handler name %q", caller, nh.Name))
		}
		seen[nh.Name] = struct{}{}
	}
}

// NamedHandler pairs a handler with a name used as the key in the
// parallel result map. Names must be unique within a Parallel call.
type NamedHandler struct {
	Name    string
	Handler ToolHandler
}

// Parallel returns a ToolHandler that executes all named handlers
// concurrently on the same input. The result is a JSON object mapping
// each name to its ParallelResult (output or error).
//
// All branches run to completion — errors in one branch do not cancel
// others. If every branch fails, the composed handler returns an error.
// If a handler panics, the panic is recovered and converted to an error
// for that branch.
//
// All branches receive the same input slice. Handlers MUST NOT mutate
// the input bytes, as this would cause data races between goroutines.
//
// At least one handler is required; Parallel panics otherwise.
//
// Example:
//
//	composed, _ := NewTool("multi_check",
//	    Parallel(
//	        NamedHandler{Name: "spell", Handler: spellCheck},
//	        NamedHandler{Name: "grammar", Handler: grammarCheck},
//	    ),
//	    WithDescription("Run spell and grammar checks concurrently"),
//	)
func Parallel(handlers ...NamedHandler) ToolHandler {
	if len(handlers) == 0 {
		panic("finemcp.Parallel: at least one handler is required")
	}
	validateNamedHandlers(handlers, "Parallel")

	return func(ctx context.Context, input []byte) ([]byte, error) {
		type indexedResult struct {
			idx    int
			name   string
			output []byte
			err    error
		}

		results := make([]indexedResult, len(handlers))
		var wg sync.WaitGroup
		wg.Add(len(handlers))

		for i, nh := range handlers {
			go func(idx int, name string, h ToolHandler) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						results[idx] = indexedResult{
							idx:  idx,
							name: name,
							err:  fmt.Errorf("panic in handler %q: %v", name, r),
						}
					}
				}()
				out, err := h(ctx, input)
				results[idx] = indexedResult{idx: idx, name: name, output: out, err: err}
			}(i, nh.Name, nh.Handler)
		}
		wg.Wait()

		// Build the keyed result map, preserving registration order.
		out := make(map[string]ParallelResult, len(results))
		errCount := 0
		for _, r := range results {
			pr := ParallelResult{}
			if r.err != nil {
				pr.Error = r.err.Error()
				errCount++
			} else {
				pr.Output = toJSONValue(r.output)
			}
			out[r.name] = pr
		}

		if errCount == len(handlers) {
			return nil, errors.New("all parallel branches failed")
		}

		return json.Marshal(out)
	}
}

// ── Fan-out / Fan-in ────────────────────────────────────────────────

// MergeFunc receives the results of all parallel branches (keyed by
// handler name) and produces a single merged output. It is called in
// the goroutine that invoked the composed handler.
type MergeFunc func(ctx context.Context, results map[string]ParallelResult) ([]byte, error)

// FanOutFanIn returns a ToolHandler that fans the input out to all
// named handlers concurrently, then passes all results through a merge
// function that produces the final output.
//
// Like Parallel, all branches run to completion before the merge
// function is called. The merge function receives every branch's result
// (including errors) and decides how to combine them.
// If a handler panics, the panic is recovered and converted to an error
// for that branch. If the merge function panics, the panic is recovered
// and returned as an error.
//
// All branches receive the same input slice. Handlers MUST NOT mutate
// the input bytes, as this would cause data races between goroutines.
//
// At least one handler is required; FanOutFanIn panics otherwise.
//
// Example:
//
//	merge := func(ctx context.Context, results map[string]ParallelResult) ([]byte, error) {
//	    // combine results from all APIs
//	    return json.Marshal(combined)
//	}
//
//	composed, _ := NewTool("unified_search",
//	    FanOutFanIn(merge,
//	        NamedHandler{Name: "api_a", Handler: apiA},
//	        NamedHandler{Name: "api_b", Handler: apiB},
//	    ),
//	    WithDescription("Query multiple APIs and merge results"),
//	)
func FanOutFanIn(merge MergeFunc, handlers ...NamedHandler) ToolHandler {
	if merge == nil {
		panic("finemcp.FanOutFanIn: merge function must not be nil")
	}
	if len(handlers) == 0 {
		panic("finemcp.FanOutFanIn: at least one handler is required")
	}
	validateNamedHandlers(handlers, "FanOutFanIn")

	return func(ctx context.Context, input []byte) (_ []byte, retErr error) {
		type indexedResult struct {
			idx    int
			name   string
			output []byte
			err    error
		}

		results := make([]indexedResult, len(handlers))
		var wg sync.WaitGroup
		wg.Add(len(handlers))

		for i, nh := range handlers {
			go func(idx int, name string, h ToolHandler) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						results[idx] = indexedResult{
							idx:  idx,
							name: name,
							err:  fmt.Errorf("panic in handler %q: %v", name, r),
						}
					}
				}()
				out, err := h(ctx, input)
				results[idx] = indexedResult{idx: idx, name: name, output: out, err: err}
			}(i, nh.Name, nh.Handler)
		}
		wg.Wait()

		// Build the result map in registration order.
		merged := make(map[string]ParallelResult, len(results))
		for _, r := range results {
			pr := ParallelResult{}
			if r.err != nil {
				pr.Error = r.err.Error()
			} else {
				pr.Output = toJSONValue(r.output)
			}
			merged[r.name] = pr
		}

		defer func() {
			if r := recover(); r != nil {
				retErr = fmt.Errorf("panic in merge function: %v", r)
			}
		}()
		return merge(ctx, merged)
	}
}
