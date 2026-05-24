package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
)

// ── Failable Mock Transport ─────────────────────────────────────────

// failableTransport is a mock transport that can simulate failures on demand.
type failableTransport struct {
	mu sync.Mutex

	// Control failure behavior
	injectErrorChan chan error // Channel to inject errors into Receive
	sendErr         error      // Error to return on failed Send
	shouldFailSend  bool       // If true, next Send will fail

	// Transport state
	started       atomic.Bool
	closed        atomic.Bool
	channelClosed bool  // Track if incoming channel was closed
	startErr      error // Error to return on Start
	closeErr      error // Error to return on Close
	startCall     atomic.Int32

	// Message handling
	incoming chan []byte
	sent     [][]byte

	// Callback for tracking Start calls
	onStart func()

	// AutoRespond lifecycle
	autoRespondCtx     context.Context
	autoRespondCancel  context.CancelFunc
	autoRespondActive  bool // Track if autoRespond should be running
	autoRespondLoopGen int  // Generation counter to track goroutine restarts
}

func newFailableTransport() *failableTransport {
	return &failableTransport{
		incoming:        make(chan []byte, 64),
		injectErrorChan: make(chan error, 1),
	}
}

func (f *failableTransport) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.startCall.Add(1)

	if f.onStart != nil {
		f.onStart()
	}

	if f.startErr != nil {
		return f.startErr
	}

	f.started.Store(true)
	f.closed.Store(false)

	// Recreate channels if they were closed
	if f.channelClosed {
		f.incoming = make(chan []byte, 64)
		f.injectErrorChan = make(chan error, 1)
		f.channelClosed = false
	}

	// Reinitialize sent slice if nil (after Close was called)
	if f.sent == nil {
		f.sent = make([][]byte, 0, 16)
	}

	// Restart autoRespond if it was active
	if f.autoRespondActive {
		// Increment generation to invalidate old goroutines
		f.autoRespondLoopGen++
		// Create new context for the new goroutine
		f.autoRespondCtx, f.autoRespondCancel = context.WithCancel(context.Background())
		go f.autoRespondLoop(f.autoRespondLoopGen)
	}

	return nil
}

func (f *failableTransport) Send(ctx context.Context, data []byte) error {
	f.mu.Lock()
	if f.closed.Load() {
		f.mu.Unlock()
		return errors.New("transport closed")
	}

	// Check if we should fail this send
	if f.shouldFailSend {
		f.shouldFailSend = false
		err := f.sendErr
		f.mu.Unlock()
		if err != nil {
			return err
		}
		return errors.New("injected send failure")
	}

	cp := make([]byte, len(data))
	copy(cp, data)
	f.sent = append(f.sent, cp)
	f.mu.Unlock()

	return nil
}

func (f *failableTransport) Receive(ctx context.Context) ([]byte, error) {
	if f.closed.Load() {
		return nil, io.EOF
	}

	// Get channel references under lock to avoid races
	f.mu.Lock()
	injectErrorChan := f.injectErrorChan
	incoming := f.incoming
	f.mu.Unlock()

	select {
	case err := <-injectErrorChan:
		// Injected error takes priority
		return nil, err
	case msg, ok := <-incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *failableTransport) Close() error {
	f.mu.Lock()
	// Cancel autoRespond goroutine if running
	if f.autoRespondCancel != nil {
		f.autoRespondCancel()
	}

	if f.closeErr != nil {
		f.mu.Unlock()
		return f.closeErr
	}

	// Only close if not already closed
	if !f.closed.Load() {
		f.closed.Store(true)
		if !f.channelClosed {
			close(f.incoming)
			f.channelClosed = true
		}
	}

	// Clear sent messages so reconnection starts fresh
	f.sent = nil
	f.mu.Unlock()

	// Give goroutine a moment to stop
	time.Sleep(10 * time.Millisecond)

	return nil
}

func (f *failableTransport) enqueueJSON(v any) {
	f.mu.Lock()
	closed := f.channelClosed
	incoming := f.incoming
	started := f.started.Load()
	f.mu.Unlock()

	if closed || !started {
		return
	}

	data, _ := json.Marshal(v)
	select {
	case incoming <- data:
	default:
		// Channel full or closed, ignore
	}
}

// simulateDisconnect simulates a connection failure by injecting an error.
func (f *failableTransport) simulateDisconnect(err error) {
	if err == nil {
		err = errors.New("simulated disconnect")
	}

	// Non-blocking send of error
	select {
	case f.injectErrorChan <- err:
	default:
		// Error channel full, that's OK
	}
}

func (f *failableTransport) autoRespond(t *testing.T) {
	t.Helper()

	f.mu.Lock()
	f.autoRespondLoopGen++
	f.autoRespondCtx, f.autoRespondCancel = context.WithCancel(context.Background())
	f.autoRespondActive = true
	gen := f.autoRespondLoopGen
	f.mu.Unlock()

	go f.autoRespondLoop(gen)
}

// autoRespondLoop is the actual goroutine that responds to requests
func (f *failableTransport) autoRespondLoop(generation int) {
	f.mu.Lock()
	ctx := f.autoRespondCtx
	f.mu.Unlock()

	seen := 0
	for {
		// Check if we're still the current generation
		f.mu.Lock()
		if f.autoRespondLoopGen != generation {
			f.mu.Unlock()
			return // Newer goroutine has started, stop this one
		}
		count := len(f.sent)
		sentSlice := f.sent // Get reference for checking nil
		f.mu.Unlock()

		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check if sent is nil (shouldn't happen after Start reinitializes it)
		if sentSlice == nil {
			select {
			case <-time.After(time.Millisecond):
			case <-ctx.Done():
				return
			}
			continue
		}

		if count <= seen {
			// Use context-aware sleep
			select {
			case <-time.After(time.Millisecond):
			case <-ctx.Done():
				return
			}
			continue
		}

		for ; seen < count; seen++ {
			f.mu.Lock()
			// Check bounds before accessing (f.sent might have changed)
			if f.sent == nil || seen >= len(f.sent) {
				// fmt.Printf("[autoRespondLoop gen=%d] Bounds check failed: sent=nil or seen=%d >= len=%d\n", generation, seen, len(f.sent))
				f.mu.Unlock()
				break
			}
			data := f.sent[seen]
			f.mu.Unlock()

			var msg struct {
				ID     any    `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				fmt.Printf("[autoRespondLoop gen=%d] Unmarshal error: %v\n", generation, err)
				continue
			}
			if msg.ID == nil {
				fmt.Printf("[autoRespondLoop gen=%d] Skipping notification\n", generation)
				continue // notification
			}

			fmt.Printf("[autoRespondLoop gen=%d] Processing method=%s, id=%v\n", generation, msg.Method, msg.ID)

			switch msg.Method {
			case "initialize":
				fmt.Printf("[autoRespondLoop gen=%d] Sending initialize response\n", generation)
				f.enqueueJSON(finemcp.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result: finemcp.InitializeResult{
						ProtocolVersion: finemcp.ProtocolVersion,
						Capabilities:    finemcp.ServerCapabilities{},
						ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
					},
				})

			case "ping":
				f.enqueueJSON(finemcp.JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  struct{}{},
				})
			}
		}
	}
}

// ── Integration Tests ────────────────────────────────────────────────

func TestReconnect_DisabledByDefault(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		// Reconnect is nil (disabled by default)
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Inject a receive failure
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait longer for read loop to exit
	time.Sleep(300 * time.Millisecond)

	// New requests should fail since reconnection is disabled
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = c.Ping(ctx)
	// Either ErrClosed or timeout is acceptable since the read loop exited
	if err == nil {
		t.Error("expected error after transport failure with reconnection disabled, got nil")
	}
}

func TestReconnect_SuccessfulReconnection(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	var reconnectingCalled atomic.Bool
	var reconnectedCalled atomic.Bool
	var reconnectingAttempt atomic.Int32

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 3,
			Strategy:   client.NoBackoff(), // No delay for fast testing
			OnReconnecting: func(attempt int, err error) {
				reconnectingCalled.Store(true)
				reconnectingAttempt.Store(int32(attempt))
			},
			OnReconnected: func() {
				reconnectedCalled.Store(true)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Ping to ensure connection is working
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Initial ping failed: %v", err)
	}

	// Simulate a disconnect
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait for reconnection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reconnectedCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !reconnectingCalled.Load() {
		t.Error("OnReconnecting was never called")
	}

	if !reconnectedCalled.Load() {
		t.Error("OnReconnected was never called")
	}

	// Verify client works after reconnection
	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping failed after reconnection: %v", err)
	}
}

func TestReconnect_MaxRetriesEnforced(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	const maxRetries = 3
	var reconnectingCount atomic.Int32
	var failedCalled atomic.Bool

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: maxRetries,
			Strategy:   client.NoBackoff(),
			OnReconnecting: func(attempt int, err error) {
				reconnectingCount.Add(1)
			},
			OnReconnected: func() {
				t.Error("OnReconnected should not be called when Start fails")
			},
			OnFailed: func(err error) {
				failedCalled.Store(true)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Make Start fail on next reconnection
	tr.mu.Lock()
	tr.startErr = fmt.Errorf("start failure")
	tr.mu.Unlock()

	// Simulate a disconnect
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait for reconnection attempts to exhaust
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if failedCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	count := reconnectingCount.Load()
	if count != maxRetries {
		t.Errorf("expected %d reconnection attempts, got %d", maxRetries, count)
	}

	if !failedCalled.Load() {
		t.Error("OnFailed was never called")
	}
}

func TestReconnect_MultipleDisconnects(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	var reconnectedCount atomic.Int32

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 5,
			Strategy:   client.NoBackoff(),
			OnReconnected: func() {
				reconnectedCount.Add(1)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Simulate multiple disconnects
	for i := 0; i < 3; i++ {
		tr.simulateDisconnect(fmt.Errorf("disconnect %d", i))

		// Wait for reconnection
		deadline := time.Now().Add(2 * time.Second)
		targetCount := int32(i + 1)
		for time.Now().Before(deadline) {
			if reconnectedCount.Load() >= targetCount {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		if reconnectedCount.Load() < targetCount {
			t.Errorf("Reconnection %d failed", i+1)
		}

		// Verify ping works
		if err := c.Ping(ctx); err != nil {
			t.Errorf("Ping %d failed after reconnection: %v", i+1, err)
		}
	}

	finalCount := reconnectedCount.Load()
	if finalCount != 3 {
		t.Errorf("expected 3 reconnections, got %d", finalCount)
	}
}

func TestReconnect_ExponentialBackoff(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	const initialDelay = 50 * time.Millisecond
	const maxRetries = 4

	var attemptTimestamps []time.Time
	var mu sync.Mutex

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: maxRetries,
			Strategy:   client.ExponentialBackoff(initialDelay, 2*time.Second),
			OnReconnecting: func(attempt int, err error) {
				mu.Lock()
				attemptTimestamps = append(attemptTimestamps, time.Now())
				mu.Unlock()
			},
			OnReconnected: func() {
				t.Error("OnReconnected should not be called when reconnection fails")
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Make Start fail to force multiple retries
	tr.mu.Lock()
	tr.startErr = fmt.Errorf("start failure")
	tr.mu.Unlock()

	// Simulate disconnect
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait for all retry attempts
	time.Sleep(5 * time.Second)

	mu.Lock()
	timestamps := make([]time.Time, len(attemptTimestamps))
	copy(timestamps, attemptTimestamps)
	mu.Unlock()

	if len(timestamps) != maxRetries {
		t.Fatalf("expected %d retry attempts, got %d", maxRetries, len(timestamps))
	}

	// Verify exponential backoff delays
	// Attempt 1: immediate (0ms)
	// Attempt 2: ~50ms after attempt 1
	// Attempt 3: ~100ms after attempt 2 (50ms * 2)
	// Attempt 4: ~200ms after attempt 3 (100ms * 2)
	expectedDelays := []time.Duration{
		0,                      // Attempt 1: immediate
		50 * time.Millisecond,  // Attempt 2: initial delay
		100 * time.Millisecond, // Attempt 3: doubled
		200 * time.Millisecond, // Attempt 4: doubled again
	}

	for i := 1; i < len(timestamps); i++ {
		actualDelay := timestamps[i].Sub(timestamps[i-1])
		expectedDelay := expectedDelays[i]

		// Allow 100% tolerance due to scheduling jitter and reconnection overhead
		// (Measured time includes backoff + reconnection attempt + scheduling)
		minDelay := expectedDelay * 50 / 100
		maxDelay := expectedDelay * 250 / 100

		if actualDelay < minDelay || actualDelay > maxDelay {
			t.Errorf("Attempt %d: expected delay ~%v, got %v (outside tolerance %v to %v)",
				i+1, expectedDelay, actualDelay, minDelay, maxDelay)
		}
	}
}

func TestReconnect_StatePreservation(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	var reconnectedCalled atomic.Bool

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 3,
			Strategy:   client.NoBackoff(),
			OnReconnected: func() {
				reconnectedCalled.Store(true)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Capture ServerInfo and Capabilities before reconnection
	serverInfoBefore := c.ServerInfo()
	serverCapsBefore := c.ServerCapabilities()
	negotiatedVerBefore := c.NegotiatedVersion()

	if serverInfoBefore.Name == "" {
		t.Fatal("ServerInfo.Name should not be empty before reconnection")
	}
	if negotiatedVerBefore == "" {
		t.Fatal("NegotiatedVersion should not be empty before reconnection")
	}

	// Simulate disconnect
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait for reconnection
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reconnectedCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !reconnectedCalled.Load() {
		t.Fatal("Reconnection did not complete")
	}

	// Verify ServerInfo and Capabilities are preserved
	serverInfoAfter := c.ServerInfo()
	serverCapsAfter := c.ServerCapabilities()
	negotiatedVerAfter := c.NegotiatedVersion()

	if serverInfoAfter.Name != serverInfoBefore.Name {
		t.Errorf("ServerInfo.Name changed after reconnection: before=%q, after=%q",
			serverInfoBefore.Name, serverInfoAfter.Name)
	}
	if serverInfoAfter.Version != serverInfoBefore.Version {
		t.Errorf("ServerInfo.Version changed after reconnection: before=%q, after=%q",
			serverInfoBefore.Version, serverInfoAfter.Version)
	}

	// Note: We're comparing struct values directly. For more complex capabilities,
	// you might want to use reflect.DeepEqual or specific field comparisons
	if serverCapsAfter != serverCapsBefore {
		t.Errorf("ServerCapabilities changed after reconnection")
	}

	if negotiatedVerAfter != negotiatedVerBefore {
		t.Errorf("NegotiatedVersion changed after reconnection: before=%q, after=%q",
			negotiatedVerBefore, negotiatedVerAfter)
	}

	// Verify client still works
	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping failed after reconnection: %v", err)
	}
}

func TestReconnect_PendingRequestHandling(t *testing.T) {
	tr := newFailableTransport()

	// Manually respond to initialize, but not to ping
	go func() {
		time.Sleep(10 * time.Millisecond) // Wait for client to start

		for {
			tr.mu.Lock()
			count := len(tr.sent)
			tr.mu.Unlock()

			if count == 0 {
				time.Sleep(10 * time.Millisecond)
				continue
			}

			tr.mu.Lock()
			if len(tr.sent) > 0 {
				data := tr.sent[0]
				tr.mu.Unlock()

				var msg struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.Unmarshal(data, &msg); err == nil {
					if msg.Method == "initialize" {
						tr.enqueueJSON(finemcp.JSONRPCResponse{
							JSONRPC: "2.0",
							ID:      msg.ID,
							Result: finemcp.InitializeResult{
								ProtocolVersion: finemcp.ProtocolVersion,
								Capabilities:    finemcp.ServerCapabilities{},
								ServerInfo:      finemcp.ProcessInfo{Name: "test-server", Version: "1.0"},
							},
						})
						return // Only respond to initialize, ignore other requests
					}
				}
			} else {
				tr.mu.Unlock()
			}

			time.Sleep(10 * time.Millisecond)
		}
	}()

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 3,
			Strategy:   client.NoBackoff(),
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Start a Ping request that will not get a response
	errChan := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		errChan <- c.Ping(ctx)
	}()

	// Give the request time to be sent
	time.Sleep(50 * time.Millisecond)

	// Simulate disconnect while request is pending
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait for the pending request to complete
	select {
	case err := <-errChan:
		// The pending request should fail with ErrClosed (connection lost)
		if err == nil {
			t.Error("expected pending request to fail, got nil")
		}
		// We accept either ErrClosed or context timeout as valid outcomes
		// since the exact behavior depends on reconnection timing
	case <-time.After(3 * time.Second):
		t.Error("pending request did not complete within timeout")
	}
}

func TestReconnect_ConcurrentRequestSafety(t *testing.T) {
	tr := newFailableTransport()
	tr.autoRespond(t)

	var reconnectingCalled atomic.Bool
	var reconnectedCalled atomic.Bool

	c, err := client.New(tr, client.Options{
		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
		Reconnect: &client.ReconnectConfig{
			Enabled:    true,
			MaxRetries: 5,
			Strategy:   client.LinearBackoff(20 * time.Millisecond), // Small delay to create window for concurrent requests
			OnReconnecting: func(attempt int, err error) {
				reconnectingCalled.Store(true)
			},
			OnReconnected: func() {
				reconnectedCalled.Store(true)
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Verify initial connection works
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Initial ping failed: %v", err)
	}

	// Simulate disconnect
	tr.simulateDisconnect(errors.New("simulated failure"))

	// Wait for reconnecting to be called
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reconnectingCalled.Load() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !reconnectingCalled.Load() {
		t.Fatal("OnReconnecting was not called")
	}

	// Send concurrent requests during reconnection
	const numConcurrent = 10
	errChan := make(chan error, numConcurrent)
	var wg sync.WaitGroup

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			err := c.Ping(ctx)
			errChan <- err
		}(i)

		// Stagger requests slightly
		time.Sleep(2 * time.Millisecond)
	}

	wg.Wait()
	close(errChan)

	// Wait for reconnection to complete
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if reconnectedCalled.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Count successful and failed requests
	var successCount, failCount int
	for err := range errChan {
		if err == nil {
			successCount++
		} else {
			failCount++
		}
	}

	t.Logf("Concurrent requests: %d successful, %d failed", successCount, failCount)

	// At least some requests should succeed after reconnection
	// (Some may fail if sent before reconnection completes, which is acceptable)
	if successCount == 0 {
		t.Error("expected at least some concurrent requests to succeed after reconnection")
	}

	// Verify client is still functional
	if err := c.Ping(ctx); err != nil {
		t.Errorf("Final ping failed: %v", err)
	}
}
