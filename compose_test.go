package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ── helpers ─────────────────────────────────────────────────────────

// upperHandler converts input bytes to uppercase.
func upperHandler(_ context.Context, input []byte) ([]byte, error) {
	return []byte(strings.ToUpper(string(input))), nil
}

// reverseHandler reverses a byte slice.
func reverseHandler(_ context.Context, input []byte) ([]byte, error) {
	b := make([]byte, len(input))
	for i, c := range input {
		b[len(input)-1-i] = c
	}
	return b, nil
}

// prefixHandler prefixes the input with "PREFIX:".
func prefixHandler(_ context.Context, input []byte) ([]byte, error) {
	return append([]byte("PREFIX:"), input...), nil
}

// failHandler always returns an error.
func failHandler(_ context.Context, _ []byte) ([]byte, error) {
	return nil, errors.New("intentional failure")
}

// echoHandler returns input unchanged.
func echoHandler(_ context.Context, input []byte) ([]byte, error) {
	return input, nil
}

// lenHandler returns the length of the input as a JSON number string.
func lenHandler(_ context.Context, input []byte) ([]byte, error) {
	return []byte(fmt.Sprintf("%d", len(input))), nil
}

// ── Pipeline tests ──────────────────────────────────────────────────

func TestPipeline_TwoHandlers(t *testing.T) {
	t.Parallel()
	h := Pipeline(upperHandler, reverseHandler)
	out, err := h(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "OLLEH" {
		t.Errorf("got %q, want %q", out, "OLLEH")
	}
}

func TestPipeline_ThreeHandlers(t *testing.T) {
	t.Parallel()
	h := Pipeline(upperHandler, reverseHandler, prefixHandler)
	out, err := h(context.Background(), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "PREFIX:CBA" {
		t.Errorf("got %q, want %q", out, "PREFIX:CBA")
	}
}

func TestPipeline_FailFast(t *testing.T) {
	t.Parallel()
	// failHandler in the middle should stop the pipeline.
	h := Pipeline(upperHandler, failHandler, prefixHandler)
	_, err := h(context.Background(), []byte("x"))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "intentional failure" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPipeline_ContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	h := Pipeline(upperHandler, reverseHandler)
	_, err := h(ctx, []byte("hello"))
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestPipeline_PreservesEmptyOutput(t *testing.T) {
	t.Parallel()
	// A handler that returns nil output should still work.
	nilHandler := func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, nil
	}
	h := Pipeline(nilHandler, echoHandler)
	out, err := h(context.Background(), []byte("anything"))
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("expected nil output, got %q", out)
	}
}

func TestPipeline_PanicOnNilHandler(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil handler")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "index 1") {
			t.Errorf("expected panic about index 1, got: %s", msg)
		}
	}()
	Pipeline(upperHandler, nil)
}

func TestPipeline_DataFlowsCorrectly(t *testing.T) {
	t.Parallel()
	// Pipeline: len("hello") = 5, then upper("5") = "5", then prefix → "PREFIX:5"
	h := Pipeline(lenHandler, upperHandler, prefixHandler)
	out, err := h(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "PREFIX:5" {
		t.Errorf("got %q, want %q", out, "PREFIX:5")
	}
}

// ── Parallel tests ──────────────────────────────────────────────────

func TestParallel_TwoBranches(t *testing.T) {
	t.Parallel()
	h := Parallel(
		NamedHandler{Name: "upper", Handler: upperHandler},
		NamedHandler{Name: "reverse", Handler: reverseHandler},
	)
	out, err := h(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if string(results["upper"].Output) != `"HELLO"` {
		t.Errorf("upper: got %s, want \"HELLO\"", results["upper"].Output)
	}
	if string(results["reverse"].Output) != `"olleh"` {
		t.Errorf("reverse: got %s, want \"olleh\"", results["reverse"].Output)
	}
}

func TestParallel_PartialFailure(t *testing.T) {
	t.Parallel()
	h := Parallel(
		NamedHandler{Name: "ok", Handler: echoHandler},
		NamedHandler{Name: "fail", Handler: failHandler},
	)
	out, err := h(context.Background(), []byte("test"))
	if err != nil {
		t.Fatalf("partial failure should not return top-level error: %v", err)
	}

	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}

	if results["ok"].Error != "" {
		t.Errorf("ok branch should not have error: %s", results["ok"].Error)
	}
	if string(results["ok"].Output) != `"test"` {
		t.Errorf("ok: got %s, want \"test\"", results["ok"].Output)
	}

	if results["fail"].Error == "" {
		t.Error("fail branch should have error")
	}
	if results["fail"].Error != "intentional failure" {
		t.Errorf("fail: got %q, want %q", results["fail"].Error, "intentional failure")
	}
}

func TestParallel_AllFail(t *testing.T) {
	t.Parallel()
	h := Parallel(
		NamedHandler{Name: "a", Handler: failHandler},
		NamedHandler{Name: "b", Handler: failHandler},
	)
	_, err := h(context.Background(), []byte("x"))
	if err == nil {
		t.Fatal("expected error when all branches fail")
	}
	if err.Error() != "all parallel branches failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParallel_SingleBranch(t *testing.T) {
	t.Parallel()
	h := Parallel(
		NamedHandler{Name: "only", Handler: upperHandler},
	)
	out, err := h(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}

	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if string(results["only"].Output) != `"HELLO"` {
		t.Errorf("got %s, want \"HELLO\"", results["only"].Output)
	}
}

func TestParallel_PanicOnEmpty(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty handlers")
		}
	}()
	Parallel()
}

func TestParallel_PanicOnNilHandler(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil handler")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "nil") {
			t.Errorf("expected nil handler panic, got: %s", msg)
		}
	}()
	Parallel(NamedHandler{Name: "bad", Handler: nil})
}

func TestParallel_PanicOnEmptyName(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "empty name") {
			t.Errorf("expected empty name panic, got: %s", msg)
		}
	}()
	Parallel(NamedHandler{Name: "", Handler: echoHandler})
}

func TestParallel_PanicOnDuplicateName(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate name")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "duplicate") {
			t.Errorf("expected duplicate name panic, got: %s", msg)
		}
	}()
	Parallel(
		NamedHandler{Name: "dup", Handler: echoHandler},
		NamedHandler{Name: "dup", Handler: upperHandler},
	)
}

func TestParallel_ContextCancellation(t *testing.T) {
	t.Parallel()
	// A slow handler that respects context.
	slowHandler := func(ctx context.Context, input []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := Parallel(
		NamedHandler{Name: "slow", Handler: slowHandler},
		NamedHandler{Name: "fast", Handler: echoHandler},
	)
	out, err := h(ctx, []byte("test"))
	// The fast branch succeeds, so overall should not error.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	if results["slow"].Error == "" {
		t.Error("slow branch should have a cancellation error")
	}
	if string(results["fast"].Output) != `"test"` {
		t.Errorf("fast: got %s, want \"test\"", results["fast"].Output)
	}
}

// ── FanOutFanIn tests ───────────────────────────────────────────────

func TestFanOutFanIn_MergesResults(t *testing.T) {
	t.Parallel()

	merge := func(_ context.Context, results map[string]ParallelResult) ([]byte, error) {
		// Concatenate all outputs (JSON-encoded strings: "ABC", "cba").
		var parts []string
		for _, name := range []string{"upper", "reverse"} {
			r := results[name]
			if r.Error != "" {
				parts = append(parts, "ERR:"+r.Error)
			} else {
				// Unmarshal the JSON value to get the plain string.
				var s string
				if err := json.Unmarshal(r.Output, &s); err != nil {
					parts = append(parts, string(r.Output))
				} else {
					parts = append(parts, s)
				}
			}
		}
		return []byte(strings.Join(parts, "+")), nil
	}

	h := FanOutFanIn(merge,
		NamedHandler{Name: "upper", Handler: upperHandler},
		NamedHandler{Name: "reverse", Handler: reverseHandler},
	)
	out, err := h(context.Background(), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ABC+cba" {
		t.Errorf("got %q, want %q", out, "ABC+cba")
	}
}

func TestFanOutFanIn_MergeReceivesErrors(t *testing.T) {
	t.Parallel()

	merge := func(_ context.Context, results map[string]ParallelResult) ([]byte, error) {
		// Count errors.
		errs := 0
		for _, r := range results {
			if r.Error != "" {
				errs++
			}
		}
		return []byte(fmt.Sprintf("errors:%d", errs)), nil
	}

	h := FanOutFanIn(merge,
		NamedHandler{Name: "ok", Handler: echoHandler},
		NamedHandler{Name: "fail", Handler: failHandler},
	)
	out, err := h(context.Background(), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "errors:1" {
		t.Errorf("got %q, want %q", out, "errors:1")
	}
}

func TestFanOutFanIn_MergeCanFail(t *testing.T) {
	t.Parallel()

	merge := func(_ context.Context, _ map[string]ParallelResult) ([]byte, error) {
		return nil, errors.New("merge failed")
	}

	h := FanOutFanIn(merge,
		NamedHandler{Name: "a", Handler: echoHandler},
	)
	_, err := h(context.Background(), []byte("x"))
	if err == nil {
		t.Fatal("expected merge error")
	}
	if err.Error() != "merge failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFanOutFanIn_PanicOnNilMerge(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil merge")
		}
	}()
	FanOutFanIn(nil, NamedHandler{Name: "a", Handler: echoHandler})
}

func TestFanOutFanIn_PanicOnEmpty(t *testing.T) {
	t.Parallel()
	merge := func(_ context.Context, _ map[string]ParallelResult) ([]byte, error) {
		return nil, nil
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty handlers")
		}
	}()
	FanOutFanIn(merge)
}

func TestFanOutFanIn_PanicOnNilHandler(t *testing.T) {
	t.Parallel()
	merge := func(_ context.Context, _ map[string]ParallelResult) ([]byte, error) {
		return nil, nil
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil handler")
		}
	}()
	FanOutFanIn(merge, NamedHandler{Name: "bad", Handler: nil})
}

func TestFanOutFanIn_PanicOnDuplicateName(t *testing.T) {
	t.Parallel()
	merge := func(_ context.Context, _ map[string]ParallelResult) ([]byte, error) {
		return nil, nil
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate name")
		}
	}()
	FanOutFanIn(merge,
		NamedHandler{Name: "dup", Handler: echoHandler},
		NamedHandler{Name: "dup", Handler: echoHandler},
	)
}

func TestFanOutFanIn_ContextPassedToMerge(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "secret")

	merge := func(ctx context.Context, _ map[string]ParallelResult) ([]byte, error) {
		v, _ := ctx.Value(ctxKey{}).(string)
		return []byte(v), nil
	}

	h := FanOutFanIn(merge,
		NamedHandler{Name: "a", Handler: echoHandler},
	)
	out, err := h(ctx, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "secret" {
		t.Errorf("got %q, want %q", out, "secret")
	}
}

// ── Composition nesting tests ───────────────────────────────────────

func TestPipeline_NestedComposition(t *testing.T) {
	t.Parallel()
	// Pipeline of Pipeline + simple handler.
	inner := Pipeline(upperHandler, reverseHandler) // "hello" → "HELLO" → "OLLEH"
	outer := Pipeline(inner, prefixHandler)         // "OLLEH" → "PREFIX:OLLEH"

	out, err := outer(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "PREFIX:OLLEH" {
		t.Errorf("got %q, want %q", out, "PREFIX:OLLEH")
	}
}

func TestPipeline_ParallelThenPipeline(t *testing.T) {
	t.Parallel()
	// Parallel → Pipeline: run parallel, then prefix the JSON output.
	par := Parallel(
		NamedHandler{Name: "upper", Handler: upperHandler},
		NamedHandler{Name: "len", Handler: lenHandler},
	)
	composed := Pipeline(par, prefixHandler)

	out, err := composed(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "PREFIX:") {
		t.Errorf("expected PREFIX: prefix, got %q", s)
	}
	// The prefixed part should be valid JSON.
	jsonPart := s[len("PREFIX:"):]
	var results map[string]ParallelResult
	if err := json.Unmarshal([]byte(jsonPart), &results); err != nil {
		t.Fatalf("expected valid JSON after prefix, got %q: %v", jsonPart, err)
	}
	if string(results["upper"].Output) != `"HELLO"` {
		t.Errorf("upper: got %s", results["upper"].Output)
	}
	// lenHandler returns "5" which is valid JSON (number), so it's embedded as-is.
	if string(results["len"].Output) != `5` {
		t.Errorf("len: got %s", results["len"].Output)
	}
}

// ── Registration as MCP tool ────────────────────────────────────────

func TestPipeline_RegisterAsNewTool(t *testing.T) {
	t.Parallel()
	composed := Pipeline(upperHandler, reverseHandler)
	tool, err := NewTool("upper_reverse", composed,
		WithDescription("Uppercases then reverses"),
		WithReadOnly(),
		WithIdempotent(),
	)
	if err != nil {
		t.Fatalf("failed to create composed tool: %v", err)
	}
	if tool.Name != "upper_reverse" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.Description != "Uppercases then reverses" {
		t.Errorf("description = %q", tool.Description)
	}
	// The handler should work.
	out, err := tool.Handler(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "OLLEH" {
		t.Errorf("got %q, want %q", out, "OLLEH")
	}
}

func TestParallel_RegisterAsNewTool(t *testing.T) {
	t.Parallel()
	composed := Parallel(
		NamedHandler{Name: "upper", Handler: upperHandler},
		NamedHandler{Name: "reverse", Handler: reverseHandler},
	)
	tool, err := NewTool("multi_transform", composed,
		WithDescription("Upper and reverse in parallel"),
	)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Handler(context.Background(), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	if string(results["upper"].Output) != `"ABC"` {
		t.Errorf("upper: got %s", results["upper"].Output)
	}
	if string(results["reverse"].Output) != `"cba"` {
		t.Errorf("reverse: got %s", results["reverse"].Output)
	}
}

func TestFanOutFanIn_RegisterAsNewTool(t *testing.T) {
	t.Parallel()
	merge := func(_ context.Context, results map[string]ParallelResult) ([]byte, error) {
		combined := make(map[string]string)
		for name, r := range results {
			if r.Error != "" {
				combined[name] = "ERROR"
			} else {
				var s string
				if err := json.Unmarshal(r.Output, &s); err != nil {
					combined[name] = string(r.Output)
				} else {
					combined[name] = s
				}
			}
		}
		return json.Marshal(combined)
	}

	composed := FanOutFanIn(merge,
		NamedHandler{Name: "up", Handler: upperHandler},
		NamedHandler{Name: "rev", Handler: reverseHandler},
	)
	tool, err := NewTool("fan_merge", composed,
		WithDescription("Fan-out and merge"),
	)
	if err != nil {
		t.Fatal(err)
	}

	out, err := tool.Handler(context.Background(), []byte("xy"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if result["up"] != "XY" {
		t.Errorf("up: got %q", result["up"])
	}
	if result["rev"] != "yx" {
		t.Errorf("rev: got %q", result["rev"])
	}
}

// ── Panic recovery tests ────────────────────────────────────────────

// panicHandler always panics.
func panicHandler(_ context.Context, _ []byte) ([]byte, error) {
	panic("intentional panic")
}

func TestParallel_HandlerPanic(t *testing.T) {
	t.Parallel()
	h := Parallel(
		NamedHandler{Name: "ok", Handler: echoHandler},
		NamedHandler{Name: "boom", Handler: panicHandler},
	)
	out, err := h(context.Background(), []byte("test"))
	// One branch succeeds, so no top-level error.
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}

	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	if string(results["ok"].Output) != `"test"` {
		t.Errorf("ok: got %s", results["ok"].Output)
	}
	if results["boom"].Error == "" {
		t.Fatal("boom branch should have a panic error")
	}
	if !strings.Contains(results["boom"].Error, "panic") {
		t.Errorf("expected panic mention in error, got: %s", results["boom"].Error)
	}
	if !strings.Contains(results["boom"].Error, "intentional panic") {
		t.Errorf("expected panic value in error, got: %s", results["boom"].Error)
	}
}

func TestParallel_AllPanic(t *testing.T) {
	t.Parallel()
	h := Parallel(
		NamedHandler{Name: "a", Handler: panicHandler},
		NamedHandler{Name: "b", Handler: panicHandler},
	)
	_, err := h(context.Background(), []byte("x"))
	if err == nil {
		t.Fatal("expected error when all branches panic")
	}
	if err.Error() != "all parallel branches failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFanOutFanIn_HandlerPanic(t *testing.T) {
	t.Parallel()
	merge := func(_ context.Context, results map[string]ParallelResult) ([]byte, error) {
		errs := 0
		for _, r := range results {
			if r.Error != "" {
				errs++
			}
		}
		return []byte(fmt.Sprintf("errors:%d", errs)), nil
	}

	h := FanOutFanIn(merge,
		NamedHandler{Name: "ok", Handler: echoHandler},
		NamedHandler{Name: "boom", Handler: panicHandler},
	)
	out, err := h(context.Background(), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "errors:1" {
		t.Errorf("got %q, want %q", out, "errors:1")
	}
}

// ── High concurrency stress test ────────────────────────────────────

func TestParallel_HighConcurrency(t *testing.T) {
	t.Parallel()
	const n = 100
	handlers := make([]NamedHandler, n)
	for i := range handlers {
		handlers[i] = NamedHandler{
			Name:    fmt.Sprintf("handler_%d", i),
			Handler: echoHandler,
		}
	}
	h := Parallel(handlers...)
	out, err := h(context.Background(), []byte("data"))
	if err != nil {
		t.Fatal(err)
	}

	var results map[string]ParallelResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != n {
		t.Errorf("expected %d results, got %d", n, len(results))
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("handler_%d", i)
		r, ok := results[name]
		if !ok {
			t.Errorf("missing result for %s", name)
			continue
		}
		if r.Error != "" {
			t.Errorf("%s: unexpected error: %s", name, r.Error)
		}
	}
}

// ── toJSONValue edge case tests ─────────────────────────────────────

func TestToJSONValue_ValidJSON(t *testing.T) {
	t.Parallel()
	// Valid JSON object — should be embedded as-is.
	input := []byte(`{"key":"value"}`)
	got := toJSONValue(input)
	if string(got) != `{"key":"value"}` {
		t.Errorf("got %s, want %s", got, input)
	}
}

func TestToJSONValue_ValidJSONNumber(t *testing.T) {
	t.Parallel()
	// Valid JSON number — should be embedded as-is.
	got := toJSONValue([]byte("42"))
	if string(got) != "42" {
		t.Errorf("got %s, want 42", got)
	}
}

func TestToJSONValue_InvalidJSON(t *testing.T) {
	t.Parallel()
	// Not valid JSON — should be string-encoded.
	got := toJSONValue([]byte("HELLO"))
	if string(got) != `"HELLO"` {
		t.Errorf("got %s, want \"HELLO\"", got)
	}
}

func TestToJSONValue_EmptySlice(t *testing.T) {
	t.Parallel()
	// Empty slice is not valid JSON — should be encoded as empty string.
	got := toJSONValue([]byte{})
	if string(got) != `""` {
		t.Errorf("got %s, want \"\"", got)
	}
}

func TestToJSONValue_NilSlice(t *testing.T) {
	t.Parallel()
	// nil is not valid JSON — should be encoded as empty string.
	got := toJSONValue(nil)
	if string(got) != `""` {
		t.Errorf("got %s, want \"\"", got)
	}
}

func TestToJSONValue_ValidJSONArray(t *testing.T) {
	t.Parallel()
	input := []byte(`[1,2,3]`)
	got := toJSONValue(input)
	if string(got) != `[1,2,3]` {
		t.Errorf("got %s, want %s", got, input)
	}
}

func TestToJSONValue_ValidJSONString(t *testing.T) {
	t.Parallel()
	// A JSON-encoded string is valid JSON — should be embedded as-is.
	input := []byte(`"already quoted"`)
	got := toJSONValue(input)
	if string(got) != `"already quoted"` {
		t.Errorf("got %s, want %s", got, input)
	}
}

// ── Pipeline panic recovery tests ───────────────────────────────────

func TestPipeline_HandlerPanic(t *testing.T) {
	t.Parallel()
	h := Pipeline(echoHandler, panicHandler)
	_, err := h(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected error from panicking handler")
	}
	if !strings.Contains(err.Error(), "panic in pipeline handler") {
		t.Errorf("expected pipeline panic error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "intentional panic") {
		t.Errorf("expected panic value in error, got: %v", err)
	}
}

func TestPipeline_PanicInMiddle(t *testing.T) {
	t.Parallel()
	// Panic in the middle should not run subsequent handlers.
	called := false
	tracker := func(_ context.Context, input []byte) ([]byte, error) {
		called = true
		return input, nil
	}
	h := Pipeline(echoHandler, panicHandler, tracker)
	_, err := h(context.Background(), []byte("x"))
	if err == nil {
		t.Fatal("expected error")
	}
	if called {
		t.Error("handler after panic should not have been called")
	}
}

// ── FanOutFanIn merge panic test ────────────────────────────────────

func TestFanOutFanIn_MergePanic(t *testing.T) {
	t.Parallel()
	merge := func(_ context.Context, _ map[string]ParallelResult) ([]byte, error) {
		panic("merge exploded")
	}
	h := FanOutFanIn(merge,
		NamedHandler{Name: "a", Handler: echoHandler},
	)
	_, err := h(context.Background(), []byte("test"))
	if err == nil {
		t.Fatal("expected error from panicking merge")
	}
	if !strings.Contains(err.Error(), "panic in merge function") {
		t.Errorf("expected merge panic error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "merge exploded") {
		t.Errorf("expected panic value in error, got: %v", err)
	}
}
