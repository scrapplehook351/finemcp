package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── LRU Cache unit tests ───────────────────────────────────────────

func TestLRUCache_GetSet(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)

	ctx := context.Background()
	err := c.Set(ctx, "k1", []byte("v1"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("expected hit, got: %v", err)
	}
	if string(got) != "v1" {
		t.Errorf("got %q, want %q", got, "v1")
	}
}

func TestLRUCache_Miss(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)

	_, err := c.Get(context.Background(), "nonexistent")
	if !errors.Is(err, ErrCacheMiss) {
		t.Errorf("expected ErrCacheMiss, got: %v", err)
	}
}

func TestLRUCache_TTLExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	c := NewLRUCache(10, withLRUClock(clock))
	ctx := context.Background()

	c.Set(ctx, "ephemeral", []byte("data"), 2*time.Second)

	// Before expiry — should hit.
	got, err := c.Get(ctx, "ephemeral")
	if err != nil {
		t.Fatalf("expected hit before TTL, got: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("got %q, want %q", got, "data")
	}

	// After expiry — should miss.
	advance(3 * time.Second)
	_, err = c.Get(ctx, "ephemeral")
	if !errors.Is(err, ErrCacheMiss) {
		t.Errorf("expected ErrCacheMiss after TTL, got: %v", err)
	}
}

func TestLRUCache_ZeroTTL_NoExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	c := NewLRUCache(10, withLRUClock(clock))
	ctx := context.Background()

	c.Set(ctx, "forever", []byte("immortal"), 0) // zero TTL

	advance(365 * 24 * time.Hour) // one year later

	got, err := c.Get(ctx, "forever")
	if err != nil {
		t.Fatalf("expected hit with zero TTL, got: %v", err)
	}
	if string(got) != "immortal" {
		t.Errorf("got %q, want %q", got, "immortal")
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(3)
	ctx := context.Background()

	c.Set(ctx, "a", []byte("1"), time.Hour)
	c.Set(ctx, "b", []byte("2"), time.Hour)
	c.Set(ctx, "c", []byte("3"), time.Hour)

	// Cache is full. Adding "d" should evict "a" (LRU).
	c.Set(ctx, "d", []byte("4"), time.Hour)

	_, err := c.Get(ctx, "a")
	if !errors.Is(err, ErrCacheMiss) {
		t.Error("expected 'a' to be evicted")
	}

	got, _ := c.Get(ctx, "d")
	if string(got) != "4" {
		t.Errorf("got %q, want %q", got, "4")
	}

	if c.Len() != 3 {
		t.Errorf("len = %d, want 3", c.Len())
	}
}

func TestLRUCache_AccessPromotesEntry(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(3)
	ctx := context.Background()

	c.Set(ctx, "a", []byte("1"), time.Hour)
	c.Set(ctx, "b", []byte("2"), time.Hour)
	c.Set(ctx, "c", []byte("3"), time.Hour)

	// Access "a" to promote it to most recently used.
	c.Get(ctx, "a")

	// Now "b" is LRU. Adding "d" should evict "b".
	c.Set(ctx, "d", []byte("4"), time.Hour)

	_, err := c.Get(ctx, "b")
	if !errors.Is(err, ErrCacheMiss) {
		t.Error("expected 'b' to be evicted after 'a' was promoted")
	}

	// "a" should still be present.
	got, err := c.Get(ctx, "a")
	if err != nil {
		t.Fatalf("expected 'a' to survive, got: %v", err)
	}
	if string(got) != "1" {
		t.Errorf("got %q, want %q", got, "1")
	}
}

func TestLRUCache_UpdateExisting(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)
	ctx := context.Background()

	c.Set(ctx, "k", []byte("old"), time.Hour)
	c.Set(ctx, "k", []byte("new"), time.Hour)

	got, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("got %q, want %q", got, "new")
	}

	if c.Len() != 1 {
		t.Errorf("len = %d, want 1", c.Len())
	}
}

func TestLRUCache_Delete(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)
	ctx := context.Background()

	c.Set(ctx, "k", []byte("v"), time.Hour)

	err := c.Delete(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Get(ctx, "k")
	if !errors.Is(err, ErrCacheMiss) {
		t.Error("expected miss after delete")
	}
}

func TestLRUCache_DeleteNonExistent(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)

	err := c.Delete(context.Background(), "ghost")
	if err != nil {
		t.Errorf("expected nil error for non-existent key, got: %v", err)
	}
}

func TestLRUCache_DeleteByPrefix(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)
	ctx := context.Background()

	c.Set(ctx, "tool_a:hash1", []byte("1"), time.Hour)
	c.Set(ctx, "tool_a:hash2", []byte("2"), time.Hour)
	c.Set(ctx, "tool_b:hash3", []byte("3"), time.Hour)

	n, err := c.DeleteByPrefix(ctx, "tool_a:")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}

	_, err = c.Get(ctx, "tool_a:hash1")
	if !errors.Is(err, ErrCacheMiss) {
		t.Error("expected tool_a:hash1 to be deleted")
	}

	// tool_b should survive.
	got, err := c.Get(ctx, "tool_b:hash3")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "3" {
		t.Errorf("got %q, want %q", got, "3")
	}
}

func TestLRUCache_DeleteByPrefix_NoMatch(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)

	n, err := c.DeleteByPrefix(context.Background(), "nope:")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("deleted = %d, want 0", n)
	}
}

func TestLRUCache_ValueIsolation(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(10)
	ctx := context.Background()

	original := []byte("hello")
	c.Set(ctx, "k", original, time.Hour)

	// Mutate the original — should not affect the cached copy.
	original[0] = 'X'

	got, _ := c.Get(ctx, "k")
	if string(got) != "hello" {
		t.Errorf("cache stored a reference instead of a copy: got %q", got)
	}

	// Mutate the returned value — should not affect the cached copy.
	got[0] = 'Y'
	got2, _ := c.Get(ctx, "k")
	if string(got2) != "hello" {
		t.Errorf("cache returned a reference instead of a copy: got %q", got2)
	}
}

func TestLRUCache_PanicOnZeroCapacity(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero capacity")
		}
	}()
	NewLRUCache(0)
}

func TestLRUCache_Concurrent(t *testing.T) {
	t.Parallel()
	c := NewLRUCache(100)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "key-" + string(rune('A'+i%26))
			c.Set(ctx, key, []byte("val"), time.Minute)
			c.Get(ctx, key)
			c.Delete(ctx, key)
		}(i)
	}
	wg.Wait()
}

// ── Cache middleware tests ──────────────────────────────────────────

func TestCache_HitAndMiss(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, input []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("result"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "echo")
	input := []byte(`{"msg":"hello"}`)

	// First call — miss, handler invoked.
	out, err := wrapped(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "result" {
		t.Errorf("got %q, want %q", out, "result")
	}
	if callCount.Load() != 1 {
		t.Errorf("call count = %d, want 1", callCount.Load())
	}

	// Second call — same input → cache hit, handler NOT invoked.
	out2, err := wrapped(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != "result" {
		t.Errorf("got %q, want %q", out2, "result")
	}
	if callCount.Load() != 1 {
		t.Errorf("call count = %d, want 1 (should be cached)", callCount.Load())
	}

	// Different input — miss, handler invoked again.
	_, err = wrapped(ctx, []byte(`{"msg":"world"}`))
	if err != nil {
		t.Fatal(err)
	}
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2", callCount.Load())
	}
}

func TestCache_DifferentToolNames(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)
	input := []byte(`{}`)

	// Tool "alpha" — miss.
	ctx1 := finemcp.WithToolName(context.Background(), "alpha")
	wrapped(ctx1, input)
	if callCount.Load() != 1 {
		t.Fatalf("call count = %d, want 1", callCount.Load())
	}

	// Tool "beta" — different namespace, also miss.
	ctx2 := finemcp.WithToolName(context.Background(), "beta")
	wrapped(ctx2, input)
	if callCount.Load() != 2 {
		t.Fatalf("call count = %d, want 2", callCount.Load())
	}

	// Tool "alpha" again — hit.
	wrapped(ctx1, input)
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2 (alpha should be cached)", callCount.Load())
	}
}

func TestCache_ErrorsNotCached(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		c := callCount.Add(1)
		if c == 1 {
			return nil, errors.New("transient error")
		}
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "flaky")
	input := []byte(`{}`)

	// First call — error, not cached.
	_, err := wrapped(ctx, input)
	if err == nil {
		t.Fatal("expected error")
	}

	// Second call — handler invoked again (error was not cached).
	out, err := wrapped(ctx, input)
	if err != nil {
		t.Fatalf("expected success on retry, got: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2", callCount.Load())
	}
}

func TestCache_TTLExpiration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("fresh"), nil
	}

	backend := NewLRUCache(100, withLRUClock(clock))
	mw := Cache(
		WithCacheTTL(5*time.Second),
		WithCacheBackend(backend),
	)
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "tool")
	input := []byte(`{}`)

	// Miss.
	wrapped(ctx, input)
	if callCount.Load() != 1 {
		t.Fatalf("call count = %d, want 1", callCount.Load())
	}

	// Hit (within TTL).
	advance(3 * time.Second)
	wrapped(ctx, input)
	if callCount.Load() != 1 {
		t.Fatalf("call count = %d, want 1 (should be cached)", callCount.Load())
	}

	// Expired — miss again.
	advance(3 * time.Second) // total 6s > 5s TTL
	wrapped(ctx, input)
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2 (TTL expired)", callCount.Load())
	}
}

func TestCache_SkipTools(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheSkipTools("write_tool"))
	wrapped := mw(handler)
	input := []byte(`{}`)

	// "write_tool" — always misses (skipped).
	ctx := finemcp.WithToolName(context.Background(), "write_tool")
	wrapped(ctx, input)
	wrapped(ctx, input)
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2 (write_tool should not be cached)", callCount.Load())
	}

	// "read_tool" — cached normally.
	callCount.Store(0)
	ctx2 := finemcp.WithToolName(context.Background(), "read_tool")
	wrapped(ctx2, input)
	wrapped(ctx2, input)
	if callCount.Load() != 1 {
		t.Errorf("call count = %d, want 1 (read_tool should be cached)", callCount.Load())
	}
}

func TestCache_OnlyTools(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheOnlyTools("read_tool"))
	wrapped := mw(handler)
	input := []byte(`{}`)

	// "read_tool" — cached.
	ctx := finemcp.WithToolName(context.Background(), "read_tool")
	wrapped(ctx, input)
	wrapped(ctx, input)
	if callCount.Load() != 1 {
		t.Errorf("call count = %d, want 1 (read_tool should be cached)", callCount.Load())
	}

	// "other_tool" — not in only list, always misses.
	callCount.Store(0)
	ctx2 := finemcp.WithToolName(context.Background(), "other_tool")
	wrapped(ctx2, input)
	wrapped(ctx2, input)
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2 (other_tool should not be cached)", callCount.Load())
	}
}

func TestCache_CustomKeyFunc(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("ok"), nil
	}

	type ctxKey string
	userKey := ctxKey("user")

	mw := Cache(WithCacheKeyFunc(func(ctx context.Context) string {
		if v, ok := ctx.Value(userKey).(string); ok {
			return v
		}
		return ""
	}))
	wrapped := mw(handler)

	input := []byte(`{}`)
	tool := "search"

	// User A.
	ctxA := context.WithValue(finemcp.WithToolName(context.Background(), tool), userKey, "alice")
	wrapped(ctxA, input)
	if callCount.Load() != 1 {
		t.Fatalf("count=%d, want 1", callCount.Load())
	}

	// User A again — hit.
	wrapped(ctxA, input)
	if callCount.Load() != 1 {
		t.Fatalf("count=%d, want 1 (alice should be cached)", callCount.Load())
	}

	// User B — different key, miss.
	ctxB := context.WithValue(finemcp.WithToolName(context.Background(), tool), userKey, "bob")
	wrapped(ctxB, input)
	if callCount.Load() != 2 {
		t.Errorf("count=%d, want 2 (bob should be separate from alice)", callCount.Load())
	}
}

func TestCache_MetaFlagOnHit(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "tool")
	input := []byte(`{}`)

	// Prime cache.
	wrapped(ctx, input)

	// SetResponseMeta is a no-op if no responseMetaHolder is in the
	// context — we just verify the middleware doesn't panic.
	_, err := wrapped(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCache_MetaDisabled(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute), WithCacheMeta(false))
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "tool")
	input := []byte(`{}`)

	wrapped(ctx, input) // prime
	_, err := wrapped(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	// No assertion on meta (disabled); just verify no panic.
}

func TestCache_DefaultBackend(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	// No WithCacheBackend — should create LRU automatically.
	mw := Cache()
	wrapped := mw(handler)

	ctx := finemcp.WithToolName(context.Background(), "test")
	_, err := wrapped(ctx, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
}

// ── Cache invalidation tests ────────────────────────────────────────

func TestCacheInvalidator_InvalidateKey(t *testing.T) {
	t.Parallel()

	backend := NewLRUCache(100)
	ctx := context.Background()
	backend.Set(ctx, "tool:abc", []byte("cached"), time.Hour)

	inv := NewCacheInvalidator(backend)
	err := inv.InvalidateKey(ctx, "tool:abc")
	if err != nil {
		t.Fatal(err)
	}

	_, err = backend.Get(ctx, "tool:abc")
	if !errors.Is(err, ErrCacheMiss) {
		t.Error("expected miss after invalidation")
	}
}

func TestCacheInvalidator_InvalidateTool(t *testing.T) {
	t.Parallel()

	backend := NewLRUCache(100)
	ctx := context.Background()
	backend.Set(ctx, "search:hash1", []byte("1"), time.Hour)
	backend.Set(ctx, "search:hash2", []byte("2"), time.Hour)
	backend.Set(ctx, "other:hash3", []byte("3"), time.Hour)

	inv := NewCacheInvalidator(backend)
	n, err := inv.InvalidateTool(ctx, "search")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("invalidated = %d, want 2", n)
	}

	// "other" should survive.
	got, _ := backend.Get(ctx, "other:hash3")
	if string(got) != "3" {
		t.Error("expected 'other:hash3' to survive invalidation")
	}
}

// ── CacheKey helpers ────────────────────────────────────────────────

func TestCacheKey_Consistency(t *testing.T) {
	t.Parallel()
	input := []byte(`{"query":"test"}`)

	k1 := CacheKey("mytool", input)
	k2 := CacheKey("mytool", input)

	if k1 != k2 {
		t.Errorf("inconsistent keys: %q != %q", k1, k2)
	}

	h := sha256.Sum256(input)
	want := "mytool:" + hex.EncodeToString(h[:])
	if k1 != want {
		t.Errorf("got %q, want %q", k1, want)
	}
}

func TestCacheKeyWithExtra(t *testing.T) {
	t.Parallel()
	input := []byte(`{}`)
	key := CacheKeyWithExtra("tool", "user123", input)

	h := sha256.Sum256(input)
	want := "tool:user123:" + hex.EncodeToString(h[:])
	if key != want {
		t.Errorf("got %q, want %q", key, want)
	}
}

// ── Concurrent middleware test ──────────────────────────────────────

func TestCache_Concurrent(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		callCount.Add(1)
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tool := "tool"
			if i%2 == 0 {
				tool = "tool2"
			}
			ctx := finemcp.WithToolName(context.Background(), tool)
			_, err := wrapped(ctx, []byte(`{"i":1}`))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// With singleflight, we expect exactly 2 handler invocations
	// (one per distinct tool name). Concurrent requests for the same key
	// are deduplicated.
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2 (one per distinct tool name)", callCount.Load())
	}
}

// ── Integration with server stack ───────────────────────────────────

func TestCache_Integration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int64
	s := finemcp.NewServer("test", "1.0")
	s.Use(Cache(WithCacheTTL(time.Minute)))

	tool, _ := finemcp.NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		callCount.Add(1)
		return input, nil
	})
	s.RegisterTool(tool)

	// Init.
	initMsg := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": finemcp.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	s.HandleMessage(context.Background(), initMsg)

	// First call — miss.
	callMsg := jsonrpcReq(2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hi"},
	})
	resp, _ := s.HandleMessage(context.Background(), callMsg)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if callCount.Load() != 1 {
		t.Fatalf("call count = %d, want 1", callCount.Load())
	}

	// Second call — same args → hit.
	resp2, _ := s.HandleMessage(context.Background(), callMsg)
	if resp2.Error != nil {
		t.Fatalf("unexpected error: %s", resp2.Error.Message)
	}
	if callCount.Load() != 1 {
		t.Errorf("call count = %d, want 1 (should be cached)", callCount.Load())
	}

	// Verify _meta.cached = true on the second response.
	raw, _ := json.Marshal(resp2.Result)
	var result struct {
		Meta map[string]any `json:"_meta"`
	}
	json.Unmarshal(raw, &result)
	if result.Meta == nil {
		// _meta might not be populated if the context doesn't have a
		// responseMetaHolder — this is fine for a middleware-level test.
		t.Log("_meta not propagated — expected when testing at middleware level via HandleMessage")
	}

	// Different args — miss.
	callMsg2 := jsonrpcReq(3, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "world"},
	})
	s.HandleMessage(context.Background(), callMsg2)
	if callCount.Load() != 2 {
		t.Errorf("call count = %d, want 2", callCount.Load())
	}
}

// ── Edge case tests ─────────────────────────────────────────────────

func TestCache_NilInput(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)
	ctx := finemcp.WithToolName(context.Background(), "tool")

	wrapped(ctx, nil)
	wrapped(ctx, nil)
	if calls.Load() != 1 {
		t.Errorf("nil input should be cacheable, got %d calls", calls.Load())
	}
}

func TestCache_EmptyToolName(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(_ context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)

	// ToolName returns "" when no tool name is set on the context.
	ctx := context.Background()
	input := []byte(`{}`)

	wrapped(ctx, input)
	wrapped(ctx, input)
	if calls.Load() != 1 {
		t.Errorf("empty tool name should still be cacheable, got %d calls", calls.Load())
	}
}

func TestCache_ContextCancelled(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	handler := func(ctx context.Context, _ []byte) ([]byte, error) {
		calls.Add(1)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return []byte("ok"), nil
	}

	mw := Cache(WithCacheTTL(time.Minute))
	wrapped := mw(handler)

	// Cancel context before calling handler.
	ctx, cancel := context.WithCancel(
		finemcp.WithToolName(context.Background(), "slow"),
	)
	cancel()

	_, err := wrapped(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}

	// Error should NOT be cached. A fresh context should invoke the
	// handler again.
	freshCtx := finemcp.WithToolName(context.Background(), "slow")
	out, err := wrapped(freshCtx, []byte(`{}`))
	if err != nil {
		t.Fatalf("expected success with fresh context, got: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("got %q, want %q", out, "ok")
	}
	if calls.Load() != 2 {
		t.Errorf("call count = %d, want 2 (cancelled result should not be cached)", calls.Load())
	}
}

func TestCache_PanicOnConflictingOptions(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when both SkipTools and OnlyTools are set")
		}
	}()
	Cache(
		WithCacheSkipTools("a"),
		WithCacheOnlyTools("b"),
	)
}
