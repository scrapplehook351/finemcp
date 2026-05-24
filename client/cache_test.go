package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Helpers
// =============================================================================

// buildRequest creates a minimal JSON-RPC 2.0 request payload.
func buildRequest(id any, method string, params any) []byte {
	type req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}
	data, err := json.Marshal(req{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		panic(fmt.Sprintf("buildRequest: %v", err))
	}
	return data
}

// buildResponse creates a minimal JSON-RPC 2.0 success response payload.
func buildResponse(id any, result any) []byte {
	type resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  any    `json:"result"`
	}
	data, err := json.Marshal(resp{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		panic(fmt.Sprintf("buildResponse: %v", err))
	}
	return data
}

// resultFromResponse extracts the raw "result" field from a JSON-RPC response.
func resultFromResponse(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &env))
	return env.Result
}

// idFromResponse extracts the raw "id" field from a JSON-RPC response.
func idFromResponse(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	var env struct {
		ID json.RawMessage `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &env))
	return env.ID
}

// =============================================================================
// stubTransport — a synchronous in-memory Transport for deterministic tests.
// =============================================================================

// stubTransport is a simple Transport that echoes back pre-programmed responses.
// The caller enqueues (requestID → response) pairs before the test. Send records
// the request ID; Receive returns the matching response.
type stubTransport struct {
	mu sync.Mutex

	// responses maps raw request-ID JSON to the response bytes to return.
	responses map[string][]byte

	// receiveCh is fed by Send and drained by Receive.
	receiveCh chan []byte

	sendCount    int32 // atomic
	receiveCount int32 // atomic
	closed       bool
}

func newStubTransport() *stubTransport {
	return &stubTransport{
		responses: make(map[string][]byte),
		receiveCh: make(chan []byte, 64),
	}
}

// addResponse registers a canned response for the given request ID.
// The response will be delivered the next time Receive is called after the
// matching Send.
func (s *stubTransport) addResponse(id any, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rawID, _ := json.Marshal(id)
	s.responses[string(rawID)] = buildResponse(id, result)
}

func (s *stubTransport) Start(_ context.Context) error { return nil }

func (s *stubTransport) Send(_ context.Context, data []byte) error {
	atomic.AddInt32(&s.sendCount, 1)

	var env struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil
	}

	s.mu.Lock()
	resp, ok := s.responses[string(env.ID)]
	s.mu.Unlock()

	if ok {
		s.receiveCh <- resp
	}
	return nil
}

func (s *stubTransport) Receive(ctx context.Context) ([]byte, error) {
	atomic.AddInt32(&s.receiveCount, 1)
	select {
	case msg := <-s.receiveCh:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *stubTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *stubTransport) Sends() int    { return int(atomic.LoadInt32(&s.sendCount)) }
func (s *stubTransport) Receives() int { return int(atomic.LoadInt32(&s.receiveCount)) }

// errorTransport is a Transport whose Send always returns the configured error.
type errorTransport struct {
	sendErr error
}

func (e *errorTransport) Start(_ context.Context) error          { return nil }
func (e *errorTransport) Send(_ context.Context, _ []byte) error { return e.sendErr }
func (e *errorTransport) Receive(_ context.Context) ([]byte, error) {
	select {} // blocks forever; never called in error-path tests
}
func (e *errorTransport) Close() error { return nil }

// =============================================================================
// Tests
// =============================================================================

// TestCacheMiss_ForwardsToInner verifies that a first request (cache miss) is
// forwarded to the inner transport and its response is returned to the caller.
func TestCacheMiss_ForwardsToInner(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	stub.addResponse("r1", map[string]string{"server": "response"})

	req := buildRequest("r1", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req))

	resp, err := tr.Receive(ctx)
	require.NoError(t, err)
	assert.Contains(t, string(resp), "server")

	assert.Equal(t, 1, stub.Sends(), "should have forwarded exactly one request to inner")
}

// TestCacheHit_ReturnsCache verifies that the second identical request is
// served from the cache without calling the inner transport again.
func TestCacheHit_ReturnsCache(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	// -- First request: cache miss → inner called.
	stub.addResponse("r1", map[string]string{"tools": "v1"})
	req1 := buildRequest("r1", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req1))
	resp1, err := tr.Receive(ctx)
	require.NoError(t, err)
	_ = resp1

	// Allow the receive loop to process and cache the response.
	time.Sleep(20 * time.Millisecond)

	sendsAfterFirst := stub.Sends()

	// -- Second request with same method+params: should hit cache.
	req2 := buildRequest("r2", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req2))
	resp2, err := tr.Receive(ctx)
	require.NoError(t, err)

	// The ID in the response must match the second request's ID.
	assert.Equal(t, `"r2"`, string(idFromResponse(t, resp2)))

	// Inner transport must NOT have been called a second time.
	assert.Equal(t, sendsAfterFirst, stub.Sends(), "cache hit must not call inner transport")
}

// TestCacheHit_PreservesResult verifies that the cached result returned is
// byte-for-byte identical to what the inner transport originally returned.
func TestCacheHit_PreservesResult(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	serverResult := map[string]any{
		"tools": []map[string]string{
			{"name": "echo", "description": "echoes input"},
		},
	}

	stub.addResponse("r1", serverResult)
	req1 := buildRequest("r1", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req1))
	resp1, err := tr.Receive(ctx)
	require.NoError(t, err)
	result1 := resultFromResponse(t, resp1)

	time.Sleep(20 * time.Millisecond)

	req2 := buildRequest("r2", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req2))
	resp2, err := tr.Receive(ctx)
	require.NoError(t, err)
	result2 := resultFromResponse(t, resp2)

	assert.JSONEq(t, string(result1), string(result2), "cached result must equal original")
}

// TestTTLExpiry verifies that a cache entry is not served after its TTL expires
// and instead a fresh request is forwarded to the inner transport.
func TestTTLExpiry(t *testing.T) {
	stub := newStubTransport()
	shortTTL := 50 * time.Millisecond
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: shortTTL})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	// First request — populates cache.
	stub.addResponse("r1", map[string]string{"version": "v1"})
	req1 := buildRequest("r1", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req1))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)

	// Wait for TTL to expire.
	time.Sleep(shortTTL + 30*time.Millisecond)

	sendsBeforeExpiry := stub.Sends()

	// Second request after expiry — should miss cache and hit inner.
	stub.addResponse("r2", map[string]string{"version": "v2"})
	req2 := buildRequest("r2", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req2))
	resp2, err := tr.Receive(ctx)
	require.NoError(t, err)

	assert.Greater(t, stub.Sends(), sendsBeforeExpiry, "expired entry must trigger inner transport call")
	assert.Contains(t, string(resp2), "v2")
}

// TestNonCacheable_AlwaysPassThrough verifies that requests for methods not in
// the cacheable set are always forwarded to the inner transport.
func TestNonCacheable_AlwaysPassThrough(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("r%d", i+1)
		stub.addResponse(id, map[string]string{"ok": "true"})
		req := buildRequest(id, "tools/call", map[string]string{"name": "echo"})
		require.NoError(t, tr.Send(ctx, req))
		_, err := tr.Receive(ctx)
		require.NoError(t, err)
	}

	assert.Equal(t, 3, stub.Sends(), "non-cacheable method must always reach inner transport")
}

// TestZeroTTL_Disabled verifies that WithCaching with TTL=0 acts as a pure
// pass-through with no caching whatsoever.
func TestZeroTTL_Disabled(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: 0})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("r%d", i+1)
		stub.addResponse(id, map[string]string{"ok": "true"})
		req := buildRequest(id, "tools/list", nil)
		require.NoError(t, tr.Send(ctx, req))
		_, err := tr.Receive(ctx)
		require.NoError(t, err)
	}

	assert.Equal(t, 3, stub.Sends(), "zero TTL must disable caching and always call inner transport")
}

// TestCustomMethods verifies that only the methods listed in CacheConfig.Methods
// are cached when an explicit list is provided.
func TestCustomMethods(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{
		Size:    10,
		TTL:     time.Minute,
		Methods: []string{"custom/list"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	// "tools/list" is NOT in the custom method list — must always forward.
	stub.addResponse("r1", map[string]string{"ok": "1"})
	req1 := buildRequest("r1", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req1))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)

	stub.addResponse("r2", map[string]string{"ok": "2"})
	req2 := buildRequest("r2", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req2))
	_, err = tr.Receive(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, stub.Sends(), "tools/list must not be cached when not in custom Methods list")

	// "custom/list" IS in the list — second call must be a cache hit.
	stub.addResponse("r3", map[string]string{"ok": "3"})
	req3 := buildRequest("r3", "custom/list", nil)
	require.NoError(t, tr.Send(ctx, req3))
	_, err = tr.Receive(ctx)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	sendsAfterCustom := stub.Sends()

	req4 := buildRequest("r4", "custom/list", nil)
	require.NoError(t, tr.Send(ctx, req4))
	_, err = tr.Receive(ctx)
	require.NoError(t, err)

	assert.Equal(t, sendsAfterCustom, stub.Sends(), "custom/list cache hit must not call inner transport")
}

// TestDifferentParams_SeparateCacheEntries verifies that requests with the same
// method but different params result in separate cache entries.
func TestDifferentParams_SeparateCacheEntries(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	// First request — cursor A.
	stub.addResponse("r1", map[string]string{"page": "A"})
	req1 := buildRequest("r1", "tools/list", map[string]string{"cursor": "A"})
	require.NoError(t, tr.Send(ctx, req1))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	sendsAfterA := stub.Sends()

	// Second request — cursor B (different params → different cache key).
	stub.addResponse("r2", map[string]string{"page": "B"})
	req2 := buildRequest("r2", "tools/list", map[string]string{"cursor": "B"})
	require.NoError(t, tr.Send(ctx, req2))
	_, err = tr.Receive(ctx)
	require.NoError(t, err)

	assert.Greater(t, stub.Sends(), sendsAfterA, "different params must use separate cache entries")
}

// TestCacheSize_Eviction verifies that when the cache is full the oldest entry
// is evicted so that the total cached item count does not exceed the configured
// size.
func TestCacheSize_Eviction(t *testing.T) {
	const maxSize = 3
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: maxSize, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	// Fill the cache with maxSize entries, each with a unique cursor.
	for i := 0; i < maxSize; i++ {
		id := fmt.Sprintf("r%d", i)
		params := map[string]int{"cursor": i}
		stub.addResponse(id, map[string]int{"i": i})
		req := buildRequest(id, "tools/list", params)
		require.NoError(t, tr.Send(ctx, req))
		_, err := tr.Receive(ctx)
		require.NoError(t, err)
		time.Sleep(5 * time.Millisecond) // ensure distinct expiresAt times
	}

	time.Sleep(20 * time.Millisecond)

	// Add one more entry — should evict the oldest.
	id := fmt.Sprintf("r%d", maxSize)
	params := map[string]int{"cursor": maxSize}
	stub.addResponse(id, map[string]int{"i": maxSize})
	req := buildRequest(id, "tools/list", params)
	require.NoError(t, tr.Send(ctx, req))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	// Check the internal cache does not exceed maxSize.
	ct := tr.(*cachedTransport)
	ct.mu.RLock()
	cacheLen := len(ct.entries)
	ct.mu.RUnlock()

	assert.LessOrEqual(t, cacheLen, maxSize, "cache must not exceed configured size")
}

// TestDefaultCacheSize_Applied verifies that a zero Size in CacheConfig
// defaults to the internal default (128).
func TestDefaultCacheSize_Applied(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{TTL: time.Minute}) // Size = 0

	ct := tr.(*cachedTransport)
	assert.Equal(t, 128, ct.cfg.Size, "zero Size must default to 128")
}

// TestDefaultMethods_Applied verifies that an empty Methods list defaults to
// DefaultCacheMethods.
func TestDefaultMethods_Applied(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{TTL: time.Minute}) // Methods = nil

	ct := tr.(*cachedTransport)
	for _, m := range DefaultCacheMethods {
		assert.True(t, ct.methods[m], "method %q must be in the default set", m)
	}
}

// TestConcurrentSafety exercises the cached transport from multiple goroutines
// simultaneously to confirm there are no data races.  Run with -race.
func TestConcurrentSafety(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 50, TTL: 200 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	const numGoroutines = 20
	const reqPerGoroutine = 10

	var wg sync.WaitGroup

	// Receiver goroutine: drains responses continuously.
	receiverDone := make(chan struct{})
	go func() {
		defer close(receiverDone)
		for {
			_, err := tr.Receive(ctx)
			if err != nil {
				return
			}
		}
	}()

	// Sender goroutines: fire requests concurrently.
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < reqPerGoroutine; i++ {
				id := fmt.Sprintf("g%d-r%d", g, i)
				stub.addResponse(id, map[string]int{"g": g, "i": i})
				req := buildRequest(id, "tools/list", nil)
				if err := tr.Send(ctx, req); err != nil {
					return
				}
			}
		}(g)
	}

	wg.Wait()
	cancel()
	<-receiverDone
}

// TestResourcesListCached verifies resources/list is cached by default.
func TestResourcesListCached(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	stub.addResponse("r1", map[string]string{"resources": "list"})
	req1 := buildRequest("r1", "resources/list", nil)
	require.NoError(t, tr.Send(ctx, req1))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	sendsAfterFirst := stub.Sends()

	req2 := buildRequest("r2", "resources/list", nil)
	require.NoError(t, tr.Send(ctx, req2))
	_, err = tr.Receive(ctx)
	require.NoError(t, err)

	assert.Equal(t, sendsAfterFirst, stub.Sends())
}

// TestPromptsListCached verifies prompts/list is cached by default.
func TestPromptsListCached(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	stub.addResponse("r1", map[string]string{"prompts": "list"})
	req1 := buildRequest("r1", "prompts/list", nil)
	require.NoError(t, tr.Send(ctx, req1))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	sendsAfterFirst := stub.Sends()

	req2 := buildRequest("r2", "prompts/list", nil)
	require.NoError(t, tr.Send(ctx, req2))
	_, err = tr.Receive(ctx)
	require.NoError(t, err)

	assert.Equal(t, sendsAfterFirst, stub.Sends())
}

// TestClose_Idempotent verifies that calling Close multiple times does not
// panic or return an error.
func TestClose_Idempotent(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx := context.Background()
	require.NoError(t, tr.Start(ctx))

	assert.NoError(t, tr.Close())
	assert.NoError(t, tr.Close(), "second Close must not panic or error")
}

// TestCacheHit_CorrectIDMapping verifies that when multiple concurrent cache
// hits are in flight, each synthetic response carries the correct request ID.
func TestCacheHit_CorrectIDMapping(t *testing.T) {
	stub := newStubTransport()
	tr := WithCaching(stub, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	// Seed the cache with one tools/list entry.
	stub.addResponse("seed", map[string]string{"seeded": "true"})
	req0 := buildRequest("seed", "tools/list", nil)
	require.NoError(t, tr.Send(ctx, req0))
	_, err := tr.Receive(ctx)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	// Send several cache-hit requests.
	ids := []string{"id-alpha", "id-beta", "id-gamma"}
	for _, id := range ids {
		req := buildRequest(id, "tools/list", nil)
		require.NoError(t, tr.Send(ctx, req))
	}

	// Collect responses and verify each carries the matching ID.
	receivedIDs := make(map[string]bool)
	for range ids {
		resp, err := tr.Receive(ctx)
		require.NoError(t, err)
		var env struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(resp, &env))
		receivedIDs[env.ID] = true
	}

	for _, id := range ids {
		assert.True(t, receivedIDs[id], "response for id %q was not received", id)
	}
}

// TestSendError_CleansUpInFlight verifies that when the inner transport's Send
// returns an error on a cache-miss request, the in-flight tracking entry is
// cleaned up so that a subsequent successful request can be cached normally.
func TestSendError_CleansUpInFlight(t *testing.T) {
	sendErr := errors.New("inner send failed")
	inner := &errorTransport{sendErr: sendErr}
	tr := WithCaching(inner, CacheConfig{Size: 10, TTL: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, tr.Start(ctx))
	defer tr.Close()

	req := buildRequest("e1", "tools/list", nil)
	err := tr.Send(ctx, req)
	assert.ErrorIs(t, err, sendErr, "Send must propagate inner transport error")
}

// TestDefaultCacheMethods_Exported verifies that DefaultCacheMethods is
// exported and contains the expected set of list operations.
func TestDefaultCacheMethods_Exported(t *testing.T) {
	expected := map[string]bool{
		"tools/list":     true,
		"resources/list": true,
		"prompts/list":   true,
	}
	for _, m := range DefaultCacheMethods {
		assert.True(t, expected[m], "unexpected method in DefaultCacheMethods: %q", m)
	}
	assert.Len(t, DefaultCacheMethods, len(expected))
}
