package client_test

// bench_test.go — L2 performance benchmark suite for the finemcp client SDK.
//
// All benchmarks use the in-process autoBenchTransport (defined in
// bench_helpers_test.go) so no real network is required.  This keeps
// benchmarks fast, deterministic, and free of port-conflict flakiness.
//
// Run with:
//
//	go test -bench=. -benchmem -run=^$ ./client/...

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// mustMarshal is a test-only helper that marshals v to JSON or fatals.
func mustMarshal(b *testing.B, v any) json.RawMessage {
	b.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		b.Fatalf("mustMarshal: %v", err)
	}
	return data
}

// ── 1. In-process round-trip benchmarks ──────────────────────────────

// BenchmarkCallTool_Small benchmarks a tools/call with a small (~100 B) payload.
func BenchmarkCallTool_Small(b *testing.B) {
	b.ReportAllocs()

	payload := makePayload(100)
	result := finemcp.CallToolResult{
		Content: []finemcp.Content{finemcp.TextContent{Text: payload}},
	}
	c, cleanup := newBenchClientWithResponse(b, result)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": payload})}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkCallTool_Medium benchmarks a tools/call with a medium (~10 KB) payload.
func BenchmarkCallTool_Medium(b *testing.B) {
	b.ReportAllocs()

	payload := makePayload(10 * 1024)
	result := finemcp.CallToolResult{
		Content: []finemcp.Content{finemcp.TextContent{Text: payload}},
	}
	c, cleanup := newBenchClientWithResponse(b, result)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": payload})}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkCallTool_Large benchmarks a tools/call with a large (~1 MB) payload.
func BenchmarkCallTool_Large(b *testing.B) {
	b.ReportAllocs()

	payload := makePayload(1024 * 1024)
	result := finemcp.CallToolResult{
		Content: []finemcp.Content{finemcp.TextContent{Text: payload}},
	}
	c, cleanup := newBenchClientWithResponse(b, result)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": payload})}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkListTools benchmarks a tools/list operation (no parameters).
func BenchmarkListTools(b *testing.B) {
	b.ReportAllocs()

	c, cleanup := newBenchClient(b)
	defer cleanup()

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
			b.Fatalf("ListTools: %v", err)
		}
	}
}

// BenchmarkInitialize benchmarks the full initialize handshake, including
// client creation and protocol negotiation.
func BenchmarkInitialize(b *testing.B) {
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		tr := newAutoBenchTransport()
		tr.setResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
			ProtocolVersion: finemcp.ProtocolVersion,
			Capabilities:    finemcp.ServerCapabilities{},
			ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0.0"},
		})
		c, err := client.New(tr, client.Options{
			ClientInfo: finemcp.ProcessInfo{Name: "bench-client", Version: "1.0.0"},
		})
		if err != nil {
			b.Fatalf("client.New: %v", err)
		}
		b.StartTimer()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, err := c.Initialize(ctx); err != nil {
			cancel()
			b.Fatalf("Initialize: %v", err)
		}
		cancel()
		_ = c.Close()
	}
}

// BenchmarkConcurrent_10 benchmarks 10 goroutines sharing a single client.
func BenchmarkConcurrent_10(b *testing.B) {
	benchmarkConcurrent(b, 10)
}

// BenchmarkConcurrent_100 benchmarks 100 goroutines sharing a single client.
func BenchmarkConcurrent_100(b *testing.B) {
	benchmarkConcurrent(b, 100)
}

// BenchmarkConcurrent_1000 benchmarks 1000 goroutines sharing a single client.
func BenchmarkConcurrent_1000(b *testing.B) {
	benchmarkConcurrent(b, 1000)
}

// benchmarkConcurrent is the shared driver for concurrency benchmarks.
func benchmarkConcurrent(b *testing.B, goroutines int) {
	b.Helper()
	b.ReportAllocs()

	c, cleanup := newBenchClient(b)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "ping"})}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()
				if _, err := c.CallTool(ctx, params); err != nil {
					// Record but do not fatalf from goroutine.
					b.Errorf("CallTool: %v", err)
				}
			}()
		}
		wg.Wait()
	}
}

// ── 2. Transport-level round-trip benchmarks (spec) ──────────────────

// BenchmarkStdioTransport benchmarks the in-process round-trip via an
// autoBenchTransport (simulating stdio transport overhead).
func BenchmarkStdioTransport(b *testing.B) {
	b.ReportAllocs()
	c, cleanup := newBenchClient(b)
	defer cleanup()

	ctx := context.Background()
	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "hi"})}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkHTTPTransport benchmarks the client at the HTTP transport layer
// using the in-process mock (measures serialisation + dispatch overhead).
func BenchmarkHTTPTransport(b *testing.B) {
	b.ReportAllocs()
	c, cleanup := newBenchClient(b)
	defer cleanup()

	ctx := context.Background()
	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "hi"})}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkWebSocketTransport benchmarks the WebSocket transport path using
// the in-process mock (measures serialisation + dispatch overhead).
func BenchmarkWebSocketTransport(b *testing.B) {
	b.ReportAllocs()
	c, cleanup := newBenchClient(b)
	defer cleanup()

	ctx := context.Background()
	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "hi"})}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkStreamableHTTPTransport benchmarks the streamable-HTTP transport
// path using the in-process mock.
func BenchmarkStreamableHTTPTransport(b *testing.B) {
	b.ReportAllocs()
	c, cleanup := newBenchClient(b)
	defer cleanup()

	ctx := context.Background()
	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "hi"})}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// ── 3. Caching benchmarks ─────────────────────────────────────────────

// BenchmarkCallTool_WithCaching_Hit benchmarks the cache-hit path for
// tools/call. Because tools/call is not in DefaultCacheMethods, we use
// tools/list (which is cached by default) to measure the hit path.
func BenchmarkCallTool_WithCaching_Hit(b *testing.B) {
	b.ReportAllocs()

	tr := newAutoBenchTransport()
	tr.setResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		Capabilities:    finemcp.ServerCapabilities{},
		ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0.0"},
	})
	tr.setResponse(finemcp.MethodToolsList, finemcp.ListToolsResult{
		Tools: []finemcp.ToolInfo{{Name: "echo", Description: "echo tool"}},
	})
	tr.setResponse(finemcp.MethodToolsCall, finemcp.CallToolResult{
		Content: []finemcp.Content{finemcp.TextContent{Text: "cached"}},
	})

	cachedTr := client.WithCaching(tr, client.CacheConfig{
		Size: 128,
		TTL:  5 * time.Minute,
	})

	c, err := client.New(cachedTr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "bench-client", Version: "1.0.0"},
	})
	if err != nil {
		b.Fatalf("client.New: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		b.Fatalf("Initialize: %v", err)
	}

	// Warm up the cache with one call.
	ctx = context.Background()
	if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
		b.Fatalf("ListTools warm-up: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Subsequent calls hit the cache.
		if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
			b.Fatalf("ListTools: %v", err)
		}
	}
}

// BenchmarkCallTool_WithCaching_Miss benchmarks the cache-miss path.
// A very short TTL forces every call to miss the cache.
func BenchmarkCallTool_WithCaching_Miss(b *testing.B) {
	b.ReportAllocs()

	tr := newAutoBenchTransport()
	tr.setResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		Capabilities:    finemcp.ServerCapabilities{},
		ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0.0"},
	})
	tr.setResponse(finemcp.MethodToolsList, finemcp.ListToolsResult{
		Tools: []finemcp.ToolInfo{{Name: "echo", Description: "echo tool"}},
	})

	// TTL of 1 ns ensures every request is a cache miss.
	cachedTr := client.WithCaching(tr, client.CacheConfig{
		Size: 128,
		TTL:  time.Nanosecond,
	})

	c, err := client.New(cachedTr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "bench-client", Version: "1.0.0"},
	})
	if err != nil {
		b.Fatalf("client.New: %v", err)
	}
	defer func() { _ = c.Close() }()

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	if _, err := c.Initialize(initCtx); err != nil {
		b.Fatalf("Initialize: %v", err)
	}

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
			b.Fatalf("ListTools: %v", err)
		}
	}
}

// BenchmarkListTools_WithCaching_Hit benchmarks list operations that are
// served from the in-process cache (warm path).
func BenchmarkListTools_WithCaching_Hit(b *testing.B) {
	b.ReportAllocs()

	tr := newAutoBenchTransport()
	tr.setResponse(finemcp.MethodInitialize, finemcp.InitializeResult{
		ProtocolVersion: finemcp.ProtocolVersion,
		Capabilities:    finemcp.ServerCapabilities{},
		ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0.0"},
	})
	tr.setResponse(finemcp.MethodToolsList, finemcp.ListToolsResult{
		Tools: []finemcp.ToolInfo{
			{Name: "tool1"}, {Name: "tool2"}, {Name: "tool3"},
		},
	})

	cachedTr := client.WithCaching(tr, client.CacheConfig{
		Size: 128,
		TTL:  5 * time.Minute,
	})

	c, err := client.New(cachedTr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "bench-client", Version: "1.0.0"},
	})
	if err != nil {
		b.Fatalf("client.New: %v", err)
	}
	defer func() { _ = c.Close() }()

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	if _, err := c.Initialize(initCtx); err != nil {
		b.Fatalf("Initialize: %v", err)
	}

	ctx := context.Background()
	// Warm the cache.
	if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
		b.Fatalf("ListTools warm-up: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
			b.Fatalf("ListTools: %v", err)
		}
	}
}

// ── 4. Streaming benchmarks ───────────────────────────────────────────

// BenchmarkCallToolStreaming_10Chunks benchmarks streaming a response with
// 10 content chunks (delivered via the final result).
func BenchmarkCallToolStreaming_10Chunks(b *testing.B) {
	benchmarkCallToolStreaming(b, 10)
}

// BenchmarkCallToolStreaming_100Chunks benchmarks streaming a response with
// 100 content chunks.
func BenchmarkCallToolStreaming_100Chunks(b *testing.B) {
	benchmarkCallToolStreaming(b, 100)
}

// benchmarkCallToolStreaming is the shared driver for streaming benchmarks.
// Each call returns a CallToolResult whose Content slice has chunkCount items.
func benchmarkCallToolStreaming(b *testing.B, chunkCount int) {
	b.Helper()
	b.ReportAllocs()

	// Build a result with chunkCount text blocks.
	chunks := make([]finemcp.Content, chunkCount)
	for i := range chunks {
		chunks[i] = finemcp.TextContent{Text: fmt.Sprintf("chunk-%d", i)}
	}
	result := finemcp.CallToolResult{Content: chunks}

	c, cleanup := newBenchClientWithResponse(b, result)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "stream"})}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		contentCh, errCh := c.CallToolStreaming(ctx, params)
		// Drain the content channel.
		for range contentCh {
		}
		if err := <-errCh; err != nil {
			b.Fatalf("CallToolStreaming: %v", err)
		}
	}
}

// ── 5. Allocation benchmarks ──────────────────────────────────────────

// BenchmarkCallTool_Allocs measures per-operation allocations for a small
// tools/call (the primary hot path in the client SDK).
func BenchmarkCallTool_Allocs(b *testing.B) {
	b.ReportAllocs()

	c, cleanup := newBenchClient(b)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "ping"})}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.CallTool(ctx, params); err != nil {
			b.Fatalf("CallTool: %v", err)
		}
	}
}

// BenchmarkListTools_Allocs measures per-operation allocations for tools/list.
func BenchmarkListTools_Allocs(b *testing.B) {
	b.ReportAllocs()

	c, cleanup := newBenchClient(b)
	defer cleanup()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.ListTools(ctx, finemcp.ListParams{}); err != nil {
			b.Fatalf("ListTools: %v", err)
		}
	}
}

// ── 6. Payload-size sub-benchmarks ───────────────────────────────────

// BenchmarkCallTool_PayloadSizes runs CallTool across a sweep of payload
// sizes so we can characterise how serialisation cost scales.
func BenchmarkCallTool_PayloadSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"100B", 100},
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, tc := range sizes {
		tc := tc // capture loop variable
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()

			payload := makePayload(tc.size)
			result := finemcp.CallToolResult{
				Content: []finemcp.Content{finemcp.TextContent{Text: payload}},
			}
			c, cleanup := newBenchClientWithResponse(b, result)
			defer cleanup()

			params := finemcp.CallToolParams{
				Name:      "echo",
				Arguments: mustMarshal(b, map[string]any{"input": payload}),
			}
			ctx := context.Background()

			b.SetBytes(int64(tc.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.CallTool(ctx, params); err != nil {
					b.Fatalf("CallTool: %v", err)
				}
			}
		})
	}
}

// ── 7. Parallel benchmark ─────────────────────────────────────────────

// BenchmarkCallTool_Parallel benchmarks tools/call with Go's built-in
// parallel runner (b.RunParallel).
func BenchmarkCallTool_Parallel(b *testing.B) {
	b.ReportAllocs()

	c, cleanup := newBenchClient(b)
	defer cleanup()

	params := finemcp.CallToolParams{Name: "echo", Arguments: mustMarshal(b, map[string]any{"input": "parallel"})}
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := c.CallTool(ctx, params); err != nil {
				b.Errorf("CallTool: %v", err)
			}
		}
	})
}
