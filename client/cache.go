package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// DefaultCacheMethods are the MCP list-operation methods that are cached by
// default when no explicit Methods list is provided in CacheConfig.
// These methods return catalogue-style data that changes infrequently and
// is safe to serve from a short-lived in-process cache.
var DefaultCacheMethods = []string{
	"tools/list",
	"resources/list",
	"prompts/list",
}

// CacheConfig configures the client-side caching transport wrapper.
type CacheConfig struct {
	// Size is the maximum number of distinct (method+params) responses to hold
	// in the cache simultaneously.  When the cache is full the oldest entry is
	// evicted on the next write.  Zero or negative values default to 128.
	Size int

	// TTL is the lifetime of each cached entry.  Entries older than TTL are
	// considered expired and trigger a fresh server round-trip on the next
	// request.  A zero or negative TTL disables caching entirely so every
	// call is forwarded to the underlying transport unchanged.
	TTL time.Duration

	// Methods is the list of JSON-RPC method names whose responses should be
	// cached.  When nil or empty, DefaultCacheMethods is used.
	Methods []string
}

// WithCaching wraps a Transport with a client-side response cache.
//
// Responses for cacheable methods (tools/list, resources/list, prompts/list
// by default) are stored in an in-process cache keyed on method name +
// JSON-serialised params.  Subsequent calls return the cached result until
// the entry's TTL expires, avoiding unnecessary round-trips to the server.
//
// Non-cacheable methods, and any call made when TTL ≤ 0, are forwarded
// directly to the underlying transport unchanged.
//
// The returned Transport is safe for concurrent use.
//
// Example:
//
//	rawTransport := stdio.New(stdio.Config{Command: "mcp-server"})
//	tr := client.WithCaching(rawTransport, client.CacheConfig{
//	    Size: 100,
//	    TTL:  5 * time.Minute,
//	})
//	c, err := client.New(tr, client.Options{...})
//
// The caching transport can be layered with other wrappers:
//
//	tr := client.WithCaching(
//	    client.WithCircuitBreaker(rawTransport, cbCfg),
//	    client.CacheConfig{TTL: time.Minute},
//	)
func WithCaching(tr Transport, cfg CacheConfig) Transport {
	if cfg.Size <= 0 {
		cfg.Size = 128
	}

	methods := cfg.Methods
	if len(methods) == 0 {
		methods = DefaultCacheMethods
	}

	methodSet := make(map[string]bool, len(methods))
	for _, m := range methods {
		methodSet[m] = true
	}

	return &cachedTransport{
		inner:    tr,
		cfg:      cfg,
		methods:  methodSet,
		entries:  make(map[string]*cacheEntry),
		inFlight: make(map[string]string),
		// synthCh needs enough capacity so that Send never blocks under
		// normal concurrency — 256 far exceeds any realistic parallel call count.
		synthCh: make(chan []byte, 256),
		// innerCh carries responses forwarded from the inner transport's
		// receive loop goroutine.
		innerCh: make(chan recvResult, 16),
		stopCh:  make(chan struct{}),
	}
}

// cacheEntry holds a single cached JSON-RPC result and its expiry time.
type cacheEntry struct {
	result    json.RawMessage
	expiresAt time.Time
}

// recvResult is the result of a single inner-transport Receive call.
type recvResult struct {
	data []byte
	err  error
}

// rpcEnvelope is the minimal structure needed to read the id, method, params,
// and result fields from an incoming/outgoing raw JSON-RPC message.
type rpcEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// cachedTransport wraps a Transport and short-circuits repeated list-operation
// requests by serving results from an in-process TTL cache.
type cachedTransport struct {
	inner   Transport
	cfg     CacheConfig
	methods map[string]bool // set of cacheable method names

	// Cache storage — protected by mu.
	mu      sync.RWMutex
	entries map[string]*cacheEntry // cacheKey → entry

	// Tracks in-flight cache-miss requests so we can store their responses.
	// Key: raw JSON representation of the request ID (e.g. `"c-1"` or `1`).
	// Value: the cache key to store the result under when the response arrives.
	inFlightMu sync.Mutex
	inFlight   map[string]string // rawID → cacheKey

	// synthCh carries synthetic (cache-hit) responses back to Receive.
	synthCh chan []byte

	// innerCh carries real responses forwarded from the inner transport's
	// background receive loop.
	innerCh chan recvResult

	// stopCh is closed by Close to signal the background receive loop to exit.
	stopCh chan struct{}

	// started ensures the background receive goroutine is launched exactly once.
	started sync.Once
}

// Start establishes the connection on the underlying transport and starts the
// background goroutine that drains its receive stream.
func (ct *cachedTransport) Start(ctx context.Context) error {
	if err := ct.inner.Start(ctx); err != nil {
		return err
	}
	ct.started.Do(func() {
		go ct.receiveLoop()
	})
	return nil
}

// receiveLoop runs in a dedicated goroutine and forwards every message from
// the inner transport to innerCh, optionally storing cacheable responses along
// the way.  It exits when the inner transport returns an error or stopCh is
// closed.
func (ct *cachedTransport) receiveLoop() {
	for {
		data, err := ct.inner.Receive(context.Background())

		if err == nil {
			ct.maybeCacheResponse(data)
		}

		res := recvResult{data: data, err: err}

		select {
		case <-ct.stopCh:
			return
		case ct.innerCh <- res:
		}

		if err != nil {
			return
		}
	}
}

// maybeCacheResponse inspects a raw JSON-RPC response.  If it matches a
// pending cache-miss request, the result is stored in the cache.
func (ct *cachedTransport) maybeCacheResponse(data []byte) {
	var env rpcEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}

	// Only cache success responses (no "error" field, has "result").
	if len(env.Error) > 0 || len(env.Result) == 0 {
		return
	}

	rawID := string(env.ID)
	if rawID == "" {
		return
	}

	ct.inFlightMu.Lock()
	cacheKey, ok := ct.inFlight[rawID]
	if ok {
		delete(ct.inFlight, rawID)
	}
	ct.inFlightMu.Unlock()

	if !ok {
		return
	}

	ct.mu.Lock()
	ct.storeEntry(cacheKey, env.Result)
	ct.mu.Unlock()
}

// storeEntry adds or replaces a cache entry.  When the cache is at capacity
// the oldest entry is evicted first.  Must be called with ct.mu held (write).
func (ct *cachedTransport) storeEntry(key string, result json.RawMessage) {
	// Evict the oldest entry when we are at capacity and adding a new key.
	if _, exists := ct.entries[key]; !exists && len(ct.entries) >= ct.cfg.Size {
		var oldest string
		var oldestExp time.Time
		for k, e := range ct.entries {
			if oldest == "" || e.expiresAt.Before(oldestExp) {
				oldest = k
				oldestExp = e.expiresAt
			}
		}
		if oldest != "" {
			delete(ct.entries, oldest)
		}
	}

	ct.entries[key] = &cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(ct.cfg.TTL),
	}
}

// Send marshals the request, checks the cache, and either:
//   - returns a synthetic response immediately (cache hit), or
//   - forwards to the inner transport and records the request for later caching.
func (ct *cachedTransport) Send(ctx context.Context, data []byte) error {
	// Pass-through when caching is disabled.
	if ct.cfg.TTL <= 0 {
		return ct.inner.Send(ctx, data)
	}

	var env rpcEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		// Unparseable request — forward as-is and don't attempt caching.
		return ct.inner.Send(ctx, data)
	}

	// Only cache requests, not notifications (notifications have no id).
	if len(env.ID) == 0 || !ct.methods[env.Method] {
		return ct.inner.Send(ctx, data)
	}

	cacheKey := makeCacheKey(env.Method, env.Params)

	// --- Cache lookup ---
	ct.mu.RLock()
	entry, hit := ct.entries[cacheKey]
	if hit && time.Now().After(entry.expiresAt) {
		hit = false // expired
	}
	var cachedResult json.RawMessage
	if hit {
		cachedResult = entry.result
	}
	ct.mu.RUnlock()

	if hit {
		// Build a synthetic JSON-RPC response and queue it for Receive.
		synthetic, err := buildSyntheticResponse(env.ID, cachedResult)
		if err != nil {
			// Fallback to real transport on marshal failure.
			return ct.inner.Send(ctx, data)
		}
		select {
		case ct.synthCh <- synthetic:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Register the in-flight entry BEFORE forwarding to the inner transport so
	// that receiveLoop can never process the response before the entry is set.
	rawID := string(env.ID)
	ct.inFlightMu.Lock()
	ct.inFlight[rawID] = cacheKey
	ct.inFlightMu.Unlock()

	if err := ct.inner.Send(ctx, data); err != nil {
		ct.inFlightMu.Lock()
		delete(ct.inFlight, rawID)
		ct.inFlightMu.Unlock()
		return err
	}

	return nil
}

// Receive returns the next JSON-RPC message, selecting between synthetic
// (cache-hit) responses and real responses forwarded from the inner transport.
func (ct *cachedTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case data := <-ct.synthCh:
		return data, nil
	case res := <-ct.innerCh:
		return res.data, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close shuts down the background receive loop and the inner transport.
func (ct *cachedTransport) Close() error {
	// Signal the receive loop to stop (idempotent: close only once).
	select {
	case <-ct.stopCh:
		// already closed
	default:
		close(ct.stopCh)
	}
	return ct.inner.Close()
}

// makeCacheKey produces a stable string key from a method name and its raw
// JSON params.  When params is empty the key is just the method name.
func makeCacheKey(method string, params json.RawMessage) string {
	if len(params) == 0 {
		return method
	}
	return fmt.Sprintf("%s:%s", method, string(params))
}

// buildSyntheticResponse constructs a minimal JSON-RPC 2.0 success response
// carrying rawID as the id and cachedResult as the result.
func buildSyntheticResponse(rawID json.RawMessage, cachedResult json.RawMessage) ([]byte, error) {
	type syntheticResp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	return json.Marshal(syntheticResp{
		JSONRPC: "2.0",
		ID:      rawID,
		Result:  cachedResult,
	})
}
