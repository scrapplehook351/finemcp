package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
)

// ── Benchmark Transport (controllable mock) ─────────────────────────

// benchTransport is a mock transport for benchmarking reconnection.
type benchTransport struct {
	mu               sync.Mutex
	started          bool
	closed           bool
	failCount        int32 // Number of times to fail before succeeding
	receiveCallCount int32 // Number of Receive calls made
	incoming         chan []byte
	sent             [][]byte
	autoRespond      bool // Auto-respond to initialize requests
}

func newBenchTransport() *benchTransport {
	return &benchTransport{
		incoming:    make(chan []byte, 100),
		sent:        make([][]byte, 0),
		autoRespond: true,
	}
}

// Start simulates transport connection.
func (t *benchTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if atomic.LoadInt32(&t.failCount) > 0 {
		atomic.AddInt32(&t.failCount, -1)
		return fmt.Errorf("simulated connection failure")
	}

	t.started = true
	t.closed = false
	return nil
}

// Close simulates transport disconnection.
func (t *benchTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		t.started = false
		close(t.incoming)
		t.incoming = make(chan []byte, 100) // Recreate for reconnection
	}
	return nil
}

// Send records the sent message and auto-responds if enabled.
func (t *benchTransport) Send(ctx context.Context, data []byte) error {
	t.mu.Lock()
	cp := make([]byte, len(data))
	copy(cp, data)
	t.sent = append(t.sent, cp)
	closed := t.closed
	autoRespond := t.autoRespond
	t.mu.Unlock()

	if closed {
		return fmt.Errorf("transport closed")
	}

	atomic.AddInt32(&t.receiveCallCount, 1)

	// Auto-respond to requests
	if autoRespond {
		var msg struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(data, &msg); err == nil && msg.ID != nil {
			switch msg.Method {
			case "initialize":
				resp := finemcp.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result: finemcp.InitializeResult{
						ProtocolVersion: finemcp.ProtocolVersion,
						Capabilities:    finemcp.ServerCapabilities{},
						ServerInfo:      finemcp.ProcessInfo{Name: "bench-server", Version: "1.0"},
					},
				}
				respData, _ := json.Marshal(resp)
				t.enqueue(respData)
			}
		}
	}

	return nil
}

// Receive returns pre-configured responses or blocks until context is done.
func (t *benchTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, fmt.Errorf("transport closed")
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// enqueue adds a response to the incoming queue.
func (t *benchTransport) enqueue(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		select {
		case t.incoming <- data:
		default:
			// Queue full, skip
		}
	}
}

// AddResponse adds a pre-configured response for Receive.
func (t *benchTransport) AddResponse(resp []byte) {
	t.enqueue(resp)
}

// SetFailCount sets how many Start() calls should fail before succeeding.
func (t *benchTransport) SetFailCount(count int) {
	atomic.StoreInt32(&t.failCount, int32(count))
}

// ── Benchmark Helpers ───────────────────────────────────────────────

// setupBenchClient creates a client configured for benchmarking.
func setupBenchClient(b *testing.B, transport *benchTransport, reconnectConfig *ReconnectConfig) *Client {
	b.Helper()

	opts := Options{
		ClientInfo: finemcp.ProcessInfo{
			Name:    "bench-client",
			Version: "1.0.0",
		},
		ProtocolVersion: finemcp.ProtocolVersion,
		Reconnect:       reconnectConfig,
	}

	client, err := New(transport, opts)
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}

	return client
}

// initializeClient performs the initialize handshake.
func initializeClient(b *testing.B, client *Client, transport *benchTransport) {
	b.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Initialize(ctx)
	if err != nil {
		b.Fatalf("Failed to initialize client: %v", err)
	}
}

// ── Benchmarks ──────────────────────────────────────────────────────

// BenchmarkBaseline_NormalOperation benchmarks normal client operations without reconnection.
func BenchmarkBaseline_NormalOperation(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil) // No reconnection
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Capture state (typical operation during normal usage)
		state := client.captureState()
		_ = state
	}
}

// BenchmarkReconnect_SingleReconnection benchmarks the cost of a single reconnection.
// This measures state capture + pending request cleanup + state restore.
func BenchmarkReconnect_SingleReconnection(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Simulate reconnection operations
		state := client.captureState()
		client.restoreState(state)
	}
}

// BenchmarkReconnect_MultipleReconnections benchmarks multiple sequential reconnection operations.
func BenchmarkReconnect_MultipleReconnections(b *testing.B) {
	const reconnectCount = 10

	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Perform multiple reconnection cycles
		for j := 0; j < reconnectCount; j++ {
			state := client.captureState()
			client.restoreState(state)
		}
	}
}

// BenchmarkBackoff_Exponential benchmarks exponential backoff strategy.
func BenchmarkBackoff_Exponential(b *testing.B) {
	backoff := ExponentialBackoff(1*time.Millisecond, 100*time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for attempt := 0; attempt < 10; attempt++ {
			_ = backoff.NextBackoff(attempt)
		}
		backoff.Reset()
	}
}

// BenchmarkBackoff_Linear benchmarks linear backoff strategy.
func BenchmarkBackoff_Linear(b *testing.B) {
	backoff := LinearBackoff(10 * time.Millisecond)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for attempt := 0; attempt < 10; attempt++ {
			_ = backoff.NextBackoff(attempt)
		}
		backoff.Reset()
	}
}

// BenchmarkBackoff_NoBackoff benchmarks no-backoff strategy.
func BenchmarkBackoff_NoBackoff(b *testing.B) {
	backoff := NoBackoff()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for attempt := 0; attempt < 10; attempt++ {
			_ = backoff.NextBackoff(attempt)
		}
		backoff.Reset()
	}
}

// BenchmarkStateCapture benchmarks capturing session state.
func BenchmarkStateCapture(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		state := client.captureState()
		_ = state
	}
}

// BenchmarkStateRestore benchmarks restoring session state.
func BenchmarkStateRestore(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	state := client.captureState()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		client.restoreState(state)
	}
}

// BenchmarkConcurrentAccess_DuringReconnection benchmarks concurrent client access during reconnection simulation.
func BenchmarkConcurrentAccess_DuringReconnection(b *testing.B) {
	const goroutines = 10

	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		wg.Add(goroutines)

		// Concurrent goroutines accessing client state
		for j := 0; j < goroutines; j++ {
			go func() {
				defer wg.Done()
				for k := 0; k < 100; k++ {
					// Simulate concurrent read operations
					_ = client.ServerInfo()
					_ = client.ServerCapabilities()
					_ = client.NegotiatedVersion()
					_ = client.Instructions()
				}
			}()
		}

		wg.Wait()
	}
}

// BenchmarkSessionMutex_ReadLock benchmarks session mutex read lock overhead.
func BenchmarkSessionMutex_ReadLock(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = client.ServerInfo()
	}
}

// BenchmarkSessionMutex_WriteLock benchmarks session mutex write lock overhead.
func BenchmarkSessionMutex_WriteLock(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	state := client.captureState()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		client.restoreState(state)
	}
}

// BenchmarkFailPendingRequests benchmarks failing multiple pending requests.
func BenchmarkFailPendingRequests(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		transport := newBenchTransport()
		client := setupBenchClient(b, transport, nil)
		initializeClient(b, client, transport)

		// Create pending requests
		pendingCount := 100
		for j := 0; j < pendingCount; j++ {
			id := fmt.Sprintf("req-%d", j)
			ch := make(chan *finemcp.JSONRPCResponse, 1)
			client.mu.Lock()
			client.pending[id] = ch
			client.mu.Unlock()
		}

		b.StartTimer()

		// Fail all pending requests
		client.failPendingRequests(fmt.Errorf("benchmark error"))

		b.StopTimer()
		client.Close()
	}
}

// BenchmarkMemoryAllocation_ReconnectCycle benchmarks memory allocations during reconnection operations.
func BenchmarkMemoryAllocation_ReconnectCycle(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Full reconnection cycle operations
		state := client.captureState()
		client.restoreState(state)
	}
}

// BenchmarkBackoffCalculation benchmarks the getBackoffDuration method.
func BenchmarkBackoffCalculation(b *testing.B) {
	transport := newBenchTransport()
	reconnectConfig := &ReconnectConfig{
		Enabled:  true,
		Strategy: ExponentialBackoff(1*time.Millisecond, 100*time.Millisecond),
	}
	client := setupBenchClient(b, transport, reconnectConfig)
	defer client.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		for attempt := 0; attempt < 10; attempt++ {
			_ = client.getBackoffDuration(attempt)
		}
	}
}

// BenchmarkConcurrentStateAccess benchmarks concurrent state access under load.
func BenchmarkConcurrentStateAccess(b *testing.B) {
	transport := newBenchTransport()
	client := setupBenchClient(b, transport, nil)
	defer client.Close()

	initializeClient(b, client, transport)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				// Read operations
				_ = client.ServerInfo()
				_ = client.ServerCapabilities()
			} else {
				// Capture state (mixed read operations)
				state := client.captureState()
				_ = state
			}
			i++
		}
	})
}
