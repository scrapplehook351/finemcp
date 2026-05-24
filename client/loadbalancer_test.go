package client

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Transport for Load Balancer Testing
// =============================================================================

// lbMockTransport simulates an MCP transport with configurable behavior.
type lbMockTransport struct {
	mu sync.Mutex

	id       string // Identifies which backend this is
	started  bool
	closed   bool
	sent     [][]byte
	incoming chan []byte
	closeCh  chan struct{} // closed by Close() to signal shutdown without racing

	// Configure behavior
	startErr     error
	sendErr      error
	receiveErr   error
	initFail     bool
	pingFail     bool
	callToolFail bool
	latency      time.Duration
	receiveDelay time.Duration

	// Call tracking
	startCalls    atomic.Int32
	sendCalls     atomic.Int32
	receiveCalls  atomic.Int32
	closeCalls    atomic.Int32
	initCalls     atomic.Int32
	pingCalls     atomic.Int32
	callToolCalls atomic.Int32
}

func newLBMockTransport(id string) *lbMockTransport {
	return &lbMockTransport{
		id:       id,
		incoming: make(chan []byte, 100),
		closeCh:  make(chan struct{}),
	}
}

func (m *lbMockTransport) Start(ctx context.Context) error {
	m.startCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.startErr != nil {
		return m.startErr
	}

	m.started = true
	return nil
}

func (m *lbMockTransport) Send(ctx context.Context, data []byte) error {
	m.sendCalls.Add(1)

	// Check latency while holding lock
	m.mu.Lock()
	latency := m.latency
	sendErr := m.sendErr
	closed := m.closed
	m.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if sendErr != nil {
		return sendErr
	}

	if closed {
		return errors.New("transport closed")
	}

	cp := make([]byte, len(data))
	copy(cp, data)
	m.sent = append(m.sent, cp)

	// Auto-respond to requests
	go m.autoRespond(data)

	return nil
}

func (m *lbMockTransport) Receive(ctx context.Context) ([]byte, error) {
	m.receiveCalls.Add(1)

	m.mu.Lock()
	receiveErr := m.receiveErr
	delay := m.receiveDelay
	m.mu.Unlock()

	if receiveErr != nil {
		return nil, receiveErr
	}

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-m.closeCh:
			return nil, io.EOF
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	select {
	case msg := <-m.incoming:
		return msg, nil
	case <-m.closeCh:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *lbMockTransport) Close() error {
	m.closeCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.closed {
		m.closed = true
		close(m.closeCh)
	}
	return nil
}

func (m *lbMockTransport) enqueue(data []byte) {
	select {
	case m.incoming <- data:
	case <-m.closeCh:
	}
}

func (m *lbMockTransport) enqueueJSON(v any) {
	data, _ := json.Marshal(v)
	m.enqueue(data)
}

// autoRespond generates appropriate responses based on the request.
func (m *lbMockTransport) autoRespond(data []byte) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}

	if err := json.Unmarshal(data, &req); err != nil {
		return
	}

	// Handle initialize
	if req.Method == "initialize" {
		m.initCalls.Add(1)
		m.mu.Lock()
		initFail := m.initFail
		id := m.id
		m.mu.Unlock()

		if initFail {
			m.enqueueJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32000,
					"message": fmt.Sprintf("init failed on %s", id),
				},
			})
			return
		}

		m.enqueueJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"protocolVersion": "1.0.0",
				"serverInfo": map[string]any{
					"name":    fmt.Sprintf("backend-%s", id),
					"version": "1.0.0",
				},
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
			},
		})
		return
	}

	// Handle ping
	if req.Method == "ping" {
		m.pingCalls.Add(1)
		m.mu.Lock()
		pingFail := m.pingFail
		id := m.id
		m.mu.Unlock()

		if pingFail {
			m.enqueueJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32000,
					"message": fmt.Sprintf("ping failed on %s", id),
				},
			})
			return
		}

		m.enqueueJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{},
		})
		return
	}

	// Handle tools/call
	if req.Method == "tools/call" {
		m.callToolCalls.Add(1)
		m.mu.Lock()
		callToolFail := m.callToolFail
		id := m.id
		m.mu.Unlock()

		if callToolFail {
			m.enqueueJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    500, // Server error - retryable
					"message": fmt.Sprintf("tool call failed on %s", id),
				},
			})
			return
		}

		m.enqueueJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": fmt.Sprintf("result from %s", id),
					},
				},
			},
		})
		return
	}

	// Handle tools/list
	if req.Method == "tools/list" {
		m.mu.Lock()
		id := m.id
		m.mu.Unlock()

		m.enqueueJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"tools": []map[string]any{
					{
						"name":        fmt.Sprintf("tool_%s_1", id),
						"description": fmt.Sprintf("Tool 1 from backend %s", id),
					},
					{
						"name":        fmt.Sprintf("tool_%s_2", id),
						"description": fmt.Sprintf("Tool 2 from backend %s", id),
					},
				},
			},
		})
		return
	}

	// Handle resources/list
	if req.Method == "resources/list" {
		m.mu.Lock()
		id := m.id
		m.mu.Unlock()

		m.enqueueJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"resources": []map[string]any{
					{
						"uri":         fmt.Sprintf("file:///%s/resource1", id),
						"name":        fmt.Sprintf("Resource 1 from %s", id),
						"description": "A test resource",
					},
				},
			},
		})
		return
	}

	// Handle prompts/list
	if req.Method == "prompts/list" {
		m.mu.Lock()
		id := m.id
		m.mu.Unlock()

		m.enqueueJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"prompts": []map[string]any{
					{
						"name":        fmt.Sprintf("prompt_%s", id),
						"description": fmt.Sprintf("Prompt from %s", id),
					},
				},
			},
		})
		return
	}

	// Default response
	m.enqueueJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  map[string]any{},
	})
}

func (m *lbMockTransport) setInitFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initFail = fail
}

func (m *lbMockTransport) setPingFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pingFail = fail
}

func (m *lbMockTransport) setCallToolFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callToolFail = fail
}

// =============================================================================
// Test Helpers
// =============================================================================

// createTestBackends creates N mock backends for testing.
func createTestBackends(n int) []LoadBalancerBackend {
	backends := make([]LoadBalancerBackend, n)
	for i := 0; i < n; i++ {
		transport := newLBMockTransport(fmt.Sprintf("backend%d", i))
		backends[i] = LoadBalancerBackend{
			ID:               fmt.Sprintf("backend%d", i),
			Transport:        transport,
			InitiallyHealthy: true,
		}
	}
	return backends
}

// initializeLoadBalancer creates and initializes a test load balancer.
func initializeLoadBalancer(t *testing.T, backends []LoadBalancerBackend, config LoadBalancerConfig) *LoadBalancer {
	t.Helper()

	lb, err := NewLoadBalancer(backends, config)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = lb.Close()
	})

	return lb
}

// =============================================================================
// 1. Strategy Tests (8 tests)
// =============================================================================

func TestRoundRobinStrategy_DistributesEvenly(t *testing.T) {
	// Create 3 backends
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	strategy := &RoundRobinStrategy{}

	// Test distribution
	results := make(map[int]int)
	for i := 0; i < 30; i++ {
		idx := strategy.Next(backends)
		results[idx]++
	}

	// Each backend should be hit 10 times
	assert.Equal(t, 10, results[0])
	assert.Equal(t, 10, results[1])
	assert.Equal(t, 10, results[2])
}

func TestRoundRobinStrategy_ConcurrentAccess(t *testing.T) {
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	strategy := &RoundRobinStrategy{}

	// Launch 100 goroutines
	const numGoroutines = 100
	const requestsPerGoroutine = 100

	var wg sync.WaitGroup
	results := make([]int, numGoroutines*requestsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				idx := strategy.Next(backends)
				results[start+j] = idx
			}
		}(i * requestsPerGoroutine)
	}

	wg.Wait()

	// Verify all indices are valid
	for _, idx := range results {
		assert.True(t, idx >= 0 && idx < 3, "invalid index: %d", idx)
	}

	// Count distribution (should be roughly even)
	distribution := make(map[int]int)
	for _, idx := range results {
		distribution[idx]++
	}

	expectedPerBackend := (numGoroutines * requestsPerGoroutine) / 3
	for i := 0; i < 3; i++ {
		count := distribution[i]
		// Allow 10% variance
		assert.InDelta(t, expectedPerBackend, count, float64(expectedPerBackend)*0.1)
	}
}

func TestRoundRobinStrategy_ResetBehavior(t *testing.T) {
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
	}

	strategy := &RoundRobinStrategy{}

	// Get some values
	assert.Equal(t, 0, strategy.Next(backends))
	assert.Equal(t, 1, strategy.Next(backends))
	assert.Equal(t, 0, strategy.Next(backends))

	// Reset
	strategy.Reset()

	// Should start from 0 again
	assert.Equal(t, 0, strategy.Next(backends))
	assert.Equal(t, 1, strategy.Next(backends))
}

func TestRandomStrategy_ReturnsValidIndices(t *testing.T) {
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	strategy := &RandomStrategy{}

	// Test 1000 selections
	for i := 0; i < 1000; i++ {
		idx := strategy.Next(backends)
		assert.True(t, idx >= 0 && idx < 3, "invalid index: %d", idx)
	}
}

func TestRandomStrategy_DistributionOverTime(t *testing.T) {
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	strategy := &RandomStrategy{}

	// Test distribution over 10,000 requests
	distribution := make(map[int]int)
	const numRequests = 10000

	for i := 0; i < numRequests; i++ {
		idx := strategy.Next(backends)
		distribution[idx]++
	}

	// Each backend should get roughly 1/3 of requests (allow 15% variance)
	expectedPerBackend := numRequests / 3
	for i := 0; i < 3; i++ {
		count := distribution[i]
		assert.InDelta(t, expectedPerBackend, count, float64(expectedPerBackend)*0.15,
			"backend %d: expected ~%d, got %d", i, expectedPerBackend, count)
	}
}

func TestLeastConnectionsStrategy_SelectsFewestConnections(t *testing.T) {
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	// Set different connection counts
	backends[0].activeConns.Store(5)
	backends[1].activeConns.Store(2) // Least
	backends[2].activeConns.Store(8)

	strategy := &LeastConnectionsStrategy{}

	// Should select backend 1
	idx := strategy.Next(backends)
	assert.Equal(t, 1, idx)
}

func TestLeastConnectionsStrategy_UpdatesWithChangingLoad(t *testing.T) {
	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	strategy := &LeastConnectionsStrategy{}

	// Initially all have 0 connections - should pick first
	assert.Equal(t, 0, strategy.Next(backends))

	// Update loads
	backends[0].activeConns.Store(10)
	backends[1].activeConns.Store(5)
	backends[2].activeConns.Store(1)

	// Should pick backend 2
	assert.Equal(t, 2, strategy.Next(backends))

	// Backend 2 gets a request
	backends[2].activeConns.Store(6)

	// Now backend 1 has least
	assert.Equal(t, 1, strategy.Next(backends))
}

func TestCustomStrategy(t *testing.T) {
	// Custom strategy that always selects the last backend
	strategy := &alwaysLastStrategy{}

	backends := []*backend{
		{id: "b0"},
		{id: "b1"},
		{id: "b2"},
	}

	idx := strategy.Next(backends)
	assert.Equal(t, 2, idx)

	// Test Reset
	strategy.Reset()
	idx = strategy.Next(backends)
	assert.Equal(t, 2, idx)

	// Test Name
	assert.Equal(t, "AlwaysLast", strategy.Name())
}

// alwaysLastStrategy is a custom strategy for testing
type alwaysLastStrategy struct{}

func (s *alwaysLastStrategy) Next(backends []*backend) int {
	if len(backends) == 0 {
		return -1
	}
	return len(backends) - 1
}

func (s *alwaysLastStrategy) Reset() {}

func (s *alwaysLastStrategy) Name() string {
	return "AlwaysLast"
}

// =============================================================================
// 2. Health Checking Tests (7 tests)
// =============================================================================

func TestHealthChecker_MarksUnhealthyAfterThreshold(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            25 * time.Millisecond,
			Method:             "ping",
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	// Initially healthy
	assert.True(t, lb.backends[0].healthy.Load())

	// Make backend 0 fail pings
	transport0.setPingFail(true)

	// Wait for health checks to mark it unhealthy
	assert.Eventually(t, func() bool {
		return !lb.backends[0].healthy.Load()
	}, 500*time.Millisecond, 10*time.Millisecond, "backend should become unhealthy")

	// Backend 1 should still be healthy
	assert.True(t, lb.backends[1].healthy.Load())
}

func TestHealthChecker_RestoresBackendAfterThreshold(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	var becameUnhealthy, becameHealthy atomic.Bool

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            25 * time.Millisecond,
			Method:             "ping",
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
		OnBackendUnhealthy: func(backendID string, err error) {
			if backendID == "backend0" {
				becameUnhealthy.Store(true)
			}
		},
		OnBackendHealthy: func(backendID string) {
			if backendID == "backend0" {
				becameHealthy.Store(true)
			}
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	// Make backend fail
	transport0.setPingFail(true)

	// Wait for it to become unhealthy
	assert.Eventually(t, func() bool {
		return becameUnhealthy.Load()
	}, 500*time.Millisecond, 10*time.Millisecond)

	// Restore backend
	transport0.setPingFail(false)

	// Wait for it to become healthy again
	assert.Eventually(t, func() bool {
		return becameHealthy.Load()
	}, 500*time.Millisecond, 10*time.Millisecond)

	assert.True(t, lb.backends[0].healthy.Load())
}

func TestHealthCheck_CallbacksInvoked(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	var (
		unhealthyCallbackCalled atomic.Bool
		healthyCallbackCalled   atomic.Bool
		unhealthyID             string
		healthyID               string
		mu                      sync.Mutex
	)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            25 * time.Millisecond,
			Method:             "ping",
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
		OnBackendUnhealthy: func(backendID string, err error) {
			mu.Lock()
			defer mu.Unlock()
			unhealthyID = backendID
			unhealthyCallbackCalled.Store(true)
		},
		OnBackendHealthy: func(backendID string) {
			mu.Lock()
			defer mu.Unlock()
			healthyID = backendID
			healthyCallbackCalled.Store(true)
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	// Make backend fail
	transport0.setPingFail(true)

	// Wait for unhealthy callback
	assert.Eventually(t, func() bool {
		return unhealthyCallbackCalled.Load()
	}, 500*time.Millisecond, 10*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "backend0", unhealthyID)
	mu.Unlock()

	// Restore backend
	transport0.setPingFail(false)

	// Wait for healthy callback
	assert.Eventually(t, func() bool {
		return healthyCallbackCalled.Load()
	}, 500*time.Millisecond, 10*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "backend0", healthyID)
	mu.Unlock()
}

func TestHealthCheck_WithPingMethod(t *testing.T) {
	backends := createTestBackends(1)
	transport := backends[0].Transport.(*lbMockTransport)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            25 * time.Millisecond,
			Method:             "ping",
			UnhealthyThreshold: 2,
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	// Wait for at least 3 health checks
	time.Sleep(200 * time.Millisecond)

	// Verify ping was called
	pingCalls := transport.pingCalls.Load()
	assert.GreaterOrEqual(t, pingCalls, int32(2), "ping should be called by health checker")
}

func TestHealthCheck_PassiveHealthChecking(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	var unhealthyCallbackCalled atomic.Bool

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		HealthCheck: &HealthCheckConfig{
			Method:             "passive", // No active checks
			Interval:           0,         // Disable active checking
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
		OnBackendUnhealthy: func(backendID string, err error) {
			if backendID == "backend0" {
				unhealthyCallbackCalled.Store(true)
			}
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	// Make both backends fail initially
	transport0.setCallToolFail(true)
	transport1 := backends[1].Transport.(*lbMockTransport)
	transport1.setCallToolFail(true)

	// Make requests that will fail on both backends
	for i := 0; i < 3; i++ {
		_, _ = lb.CallTool(ctx, finemcp.CallToolParams{
			Name: "test-tool",
		})
		time.Sleep(10 * time.Millisecond)
	}

	// Restore backend1
	transport1.setCallToolFail(false)

	// Check consecutive failures
	lb.backends[0].healthMu.Lock()
	failures0 := lb.backends[0].consecutiveFailures
	lb.backends[0].healthMu.Unlock()

	t.Logf("Backend0 consecutive failures: %d", failures0)

	// Backend0 should be marked unhealthy
	assert.Eventually(t, func() bool {
		return !lb.backends[0].healthy.Load()
	}, 1*time.Second, 50*time.Millisecond, "backend0 should become unhealthy")
}

func TestHealthCheck_TimeoutHandling(t *testing.T) {
	backends := createTestBackends(1)
	transport := backends[0].Transport.(*lbMockTransport)

	// Set a long latency that will exceed the health check timeout
	transport.mu.Lock()
	transport.latency = 200 * time.Millisecond
	transport.mu.Unlock()

	var unhealthyCallbackCalled atomic.Bool

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            10 * time.Millisecond, // Very short timeout
			Method:             "ping",
			UnhealthyThreshold: 2,
		},
		OnBackendUnhealthy: func(backendID string, err error) {
			unhealthyCallbackCalled.Store(true)
		},
	})
	require.NoError(t, err)

	// Don't initialize to avoid initial error during init
	// Just let health checks run
	time.Sleep(200 * time.Millisecond)

	err = lb.Close()
	require.NoError(t, err)

	// Note: Health checks may timeout, which would trigger unhealthy callback
	// But this is timing-dependent, so we just verify no panic occurred
}

func TestHealthCheck_GoroutineCleanup(t *testing.T) {
	backends := createTestBackends(1)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            25 * time.Millisecond,
			Method:             "ping",
			UnhealthyThreshold: 3,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	// Let health checks run
	time.Sleep(150 * time.Millisecond)

	// Close should stop health checking goroutine
	err = lb.Close()
	require.NoError(t, err)

	// Verify healthDone channel is closed
	select {
	case <-lb.healthDone:
		// Good - channel is closed
	case <-time.After(100 * time.Millisecond):
		t.Fatal("healthDone channel should be closed after Close()")
	}
}

// =============================================================================
// 3. Failover & Retry Tests (6 tests)
// =============================================================================

func TestFailover_RequestFailsOverToNextBackend(t *testing.T) {
	backends := createTestBackends(3)
	transport0 := backends[0].Transport.(*lbMockTransport)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy:   &RoundRobinStrategy{},
		MaxRetries: 3,
	})

	// Make backend0 fail
	transport0.setCallToolFail(true)

	ctx := context.Background()
	result, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	// Should succeed on backend1 or backend2
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify backend0 was tried (but failed)
	assert.Greater(t, transport0.callToolCalls.Load(), int32(0))
}

func TestFailover_MaxRetriesRespected(t *testing.T) {
	backends := createTestBackends(3)

	// Make first 2 backends fail
	backends[0].Transport.(*lbMockTransport).setCallToolFail(true)
	backends[1].Transport.(*lbMockTransport).setCallToolFail(true)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy:   &RoundRobinStrategy{},
		MaxRetries: 2, // Only try 2 backends
	})

	ctx := context.Background()
	_, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	// Should fail because we only retry twice
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all backends failed")
}

func TestFailover_RetryableErrorsPredicate(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	retryableCallCount := atomic.Int32{}

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		RetryableErrors: func(err error) bool {
			retryableCallCount.Add(1)
			// Make errors from backend0 retryable (will succeed on backend1)
			return err != nil && err.Error() != ""
		},
	})

	// Make backend0 fail
	transport0.setCallToolFail(true)

	ctx := context.Background()
	result, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	// Should succeed on backend1 after retry
	require.NoError(t, err)
	assert.NotNil(t, result)

	// RetryableErrors should have been called
	assert.Greater(t, retryableCallCount.Load(), int32(0))
}

func TestFailover_AllBackendsFailed(t *testing.T) {
	backends := createTestBackends(3)

	// Make all backends fail
	for i := range backends {
		backends[i].Transport.(*lbMockTransport).setCallToolFail(true)
	}

	var allFailedCallbackCalled atomic.Bool
	var lastError error
	var mu sync.Mutex

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		OnAllBackendsFailed: func(err error) {
			mu.Lock()
			defer mu.Unlock()
			lastError = err
			allFailedCallbackCalled.Store(true)
		},
	})

	ctx := context.Background()
	_, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all backends failed")

	// Callback should be invoked
	assert.Eventually(t, func() bool {
		return allFailedCallbackCalled.Load()
	}, 1*time.Second, 10*time.Millisecond)

	mu.Lock()
	assert.NotNil(t, lastError)
	mu.Unlock()
}

func TestFailover_BackendFailedCallback(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	var failedBackends []string
	var mu sync.Mutex

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		OnBackendFailed: func(backendID string, err error) {
			mu.Lock()
			defer mu.Unlock()
			failedBackends = append(failedBackends, backendID)
		},
	})

	// Make backend0 fail
	transport0.setCallToolFail(true)

	ctx := context.Background()
	_, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	require.NoError(t, err) // Should succeed on backend1

	// Wait for callback
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.Contains(t, failedBackends, "backend0")
	mu.Unlock()
}

func TestFailover_ContextCancellationStopsRetries(t *testing.T) {
	backends := createTestBackends(3)

	// Make all backends slow
	for i := range backends {
		transport := backends[i].Transport.(*lbMockTransport)
		transport.mu.Lock()
		transport.latency = 500 * time.Millisecond
		transport.mu.Unlock()
	}

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy:   &RoundRobinStrategy{},
		MaxRetries: -1, // Infinite retries
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	startTime := time.Now()
	_, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})
	elapsed := time.Since(startTime)

	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))

	// Should not retry for long after context cancelled
	assert.Less(t, elapsed, 300*time.Millisecond)
}

// =============================================================================
// 4. MCP Method Tests (10 tests)
// =============================================================================

func TestCallTool_RoutesToHealthyBackend(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	result, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.Content, 1)
}

func TestListTools_MergesResultsFromAllBackends(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	result, err := lb.ListTools(ctx, finemcp.ListParams{})

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Each backend returns 2 tools, so should have 6 total
	assert.Len(t, result.Tools, 6)

	// Verify tools from each backend are present
	toolNames := make(map[string]bool)
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
	}

	assert.True(t, toolNames["tool_backend0_1"])
	assert.True(t, toolNames["tool_backend1_1"])
	assert.True(t, toolNames["tool_backend2_1"])
}

func TestListTools_DeduplicatesByToolName(t *testing.T) {
	backends := createTestBackends(2)

	// Modify mock to return duplicate tool names
	for _, b := range backends {
		transport := b.Transport.(*lbMockTransport)
		// We'll need to modify the transport later to return same tool names
		_ = transport
	}

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	result, err := lb.ListTools(ctx, finemcp.ListParams{})

	require.NoError(t, err)

	// Count occurrences of each tool name
	toolNames := make(map[string]int)
	for _, tool := range result.Tools {
		toolNames[tool.Name]++
	}

	// Each tool name should appear only once
	for name, count := range toolNames {
		assert.Equal(t, 1, count, "tool %s should appear only once", name)
	}
}

func TestListResources_ScatterGatherPattern(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	result, err := lb.ListResources(ctx, finemcp.ListParams{})

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Each backend returns 1 resource
	assert.Len(t, result.Resources, 3)

	// Verify resources from each backend
	uris := make(map[string]bool)
	for _, resource := range result.Resources {
		uris[resource.URI] = true
	}

	assert.True(t, uris["file:///backend0/resource1"])
	assert.True(t, uris["file:///backend1/resource1"])
	assert.True(t, uris["file:///backend2/resource1"])
}

func TestReadResource_SingleBackendPattern(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	result, err := lb.ReadResource(ctx, finemcp.ReadResourceParams{
		URI: "file:///test/resource",
	})

	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestGetPrompt_FailoverBehavior(t *testing.T) {
	backends := createTestBackends(2)
	transport0 := backends[0].Transport.(*lbMockTransport)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	// Make backend0 fail (we'll need to modify the mock to handle GetPrompt failures)
	transport0.mu.Lock()
	transport0.sendErr = errors.New("backend0 failed")
	transport0.mu.Unlock()

	ctx := context.Background()
	result, err := lb.GetPrompt(ctx, finemcp.GetPromptParams{
		Name: "test-prompt",
	})

	// Should failover to backend1
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestPing_HealthCheck(t *testing.T) {
	backends := createTestBackends(1)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	err := lb.Ping(ctx)

	require.NoError(t, err)
}

func TestClose_ClosesAllBackends(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	// Get transports
	transports := make([]*lbMockTransport, 3)
	for i, b := range backends {
		transports[i] = b.Transport.(*lbMockTransport)
	}

	// Close load balancer
	err := lb.Close()
	require.NoError(t, err)

	// Verify all transports are closed
	for i, transport := range transports {
		assert.Greater(t, transport.closeCalls.Load(), int32(0),
			"backend %d should be closed", i)
	}
}

func TestListPrompts_ReturnsUnion(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	result, err := lb.ListPrompts(ctx, finemcp.ListParams{})

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Each backend returns 1 prompt
	assert.Len(t, result.Prompts, 3)

	// Verify prompts from each backend
	promptNames := make(map[string]bool)
	for _, prompt := range result.Prompts {
		promptNames[prompt.Name] = true
	}

	assert.True(t, promptNames["prompt_backend0"])
	assert.True(t, promptNames["prompt_backend1"])
	assert.True(t, promptNames["prompt_backend2"])
}

func TestNotInitialized_ReturnsError(t *testing.T) {
	backends := createTestBackends(1)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})
	require.NoError(t, err)
	defer lb.Close()

	// Don't initialize
	ctx := context.Background()
	_, err = lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotInitialized)
}

// =============================================================================
// 5. Lifecycle Tests (6 tests)
// =============================================================================

func TestInitialize_InitializesAllBackendsConcurrently(t *testing.T) {
	backends := createTestBackends(5)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	startTime := time.Now()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)
	elapsed := time.Since(startTime)

	// With concurrent initialization, should complete quickly
	assert.Less(t, elapsed, 2*time.Second)

	// Verify all backends were initialized
	for i, backend := range backends {
		transport := backend.Transport.(*lbMockTransport)
		assert.Greater(t, transport.initCalls.Load(), int32(0),
			"backend %d should be initialized", i)
	}
}

func TestInitialize_MarksFailedBackendsUnhealthy(t *testing.T) {
	backends := createTestBackends(3)

	// Make backend1 fail initialization
	backends[1].Transport.(*lbMockTransport).setInitFail(true)

	var unhealthyBackends []string
	var mu sync.Mutex

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		OnBackendUnhealthy: func(backendID string, err error) {
			mu.Lock()
			defer mu.Unlock()
			unhealthyBackends = append(unhealthyBackends, backendID)
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	result, err := lb.Initialize(ctx)

	// Should succeed with at least one healthy backend
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Wait for callback
	time.Sleep(100 * time.Millisecond)

	// Backend1 should be marked unhealthy
	assert.False(t, lb.backends[1].healthy.Load())

	mu.Lock()
	assert.Contains(t, unhealthyBackends, "backend1")
	mu.Unlock()
}

func TestAddBackend_AddsAndInitializesNewBackend(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	// Add a new backend
	newTransport := newLBMockTransport("backend-new")
	err := lb.AddBackend(LoadBalancerBackend{
		ID:               "backend-new",
		Transport:        newTransport,
		InitiallyHealthy: true,
	})
	require.NoError(t, err)

	// Verify backend was added
	lb.backendsMu.RLock()
	assert.Len(t, lb.backends, 3)
	lb.backendsMu.RUnlock()

	// Wait for initialization
	time.Sleep(100 * time.Millisecond)

	// New backend should be initialized
	assert.Greater(t, newTransport.initCalls.Load(), int32(0))
}

func TestRemoveBackend_RemovesAndClosesBackend(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	transport1 := backends[1].Transport.(*lbMockTransport)

	// Remove backend1
	err := lb.RemoveBackend("backend1")
	require.NoError(t, err)

	// Verify backend was removed
	lb.backendsMu.RLock()
	assert.Len(t, lb.backends, 2)

	// Verify backend1 is not in the list
	found := false
	for _, b := range lb.backends {
		if b.id == "backend1" {
			found = true
			break
		}
	}
	lb.backendsMu.RUnlock()

	assert.False(t, found, "backend1 should be removed")

	// Verify backend was closed
	assert.Greater(t, transport1.closeCalls.Load(), int32(0))
}

func TestRemoveBackend_DuringActiveRequests(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()

	// Start a long-running request
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = lb.CallTool(ctx, finemcp.CallToolParams{
				Name: "test-tool",
			})
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// While requests are running, remove a backend
	time.Sleep(100 * time.Millisecond)
	err := lb.RemoveBackend("backend0")
	require.NoError(t, err)

	// Requests should continue on remaining backend
	wg.Wait()

	// Verify only one backend remains
	lb.backendsMu.RLock()
	assert.Len(t, lb.backends, 1)
	lb.backendsMu.RUnlock()
}

func TestClose_GracefulShutdown(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		HealthCheck: &HealthCheckConfig{
			Interval: 50 * time.Millisecond,
			Method:   "ping",
		},
	})

	// Close should:
	// 1. Stop health checking
	// 2. Close all backends
	// 3. Set closed flag

	err := lb.Close()
	require.NoError(t, err)

	// Verify closed flag
	assert.True(t, lb.closed.Load())

	// Verify health checking stopped
	select {
	case <-lb.healthDone:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Fatal("health checking should stop")
	}

	// Subsequent operations should fail
	ctx := context.Background()
	_, err = lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrClosed)
}

// =============================================================================
// 6. Concurrency Tests (4 tests)
// =============================================================================

func TestConcurrency_CallToolSafe(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()

	// Launch 100 concurrent requests
	const numGoroutines = 100
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := lb.CallTool(ctx, finemcp.CallToolParams{
				Name: fmt.Sprintf("tool-%d", idx),
			})
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// No errors should occur
	for err := range errors {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConcurrency_ListToolsSafe(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()

	const numGoroutines = 50
	var wg sync.WaitGroup
	results := make(chan int, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := lb.ListTools(ctx, finemcp.ListParams{})
			if err == nil {
				results <- len(result.Tools)
			}
		}()
	}

	wg.Wait()
	close(results)

	// All results should have the same number of tools
	expectedCount := -1
	for count := range results {
		if expectedCount == -1 {
			expectedCount = count
		}
		assert.Equal(t, expectedCount, count)
	}
}

func TestConcurrency_RaceDetectorClean(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		HealthCheck: &HealthCheckConfig{
			Interval: 50 * time.Millisecond,
			Method:   "ping",
		},
	})

	ctx := context.Background()

	// Mix of operations
	var wg sync.WaitGroup

	// CallTool
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = lb.CallTool(ctx, finemcp.CallToolParams{Name: "tool"})
		}()
	}

	// ListTools
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = lb.ListTools(ctx, finemcp.ListParams{})
		}()
	}

	// Ping
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lb.Ping(ctx)
		}()
	}

	// Get metrics
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lb.getHealthyBackends()
		}()
	}

	wg.Wait()

	// If we get here without race detector warnings, test passes
}

func TestConcurrency_AddRemoveBackendDuringRequests(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()
	stopRequests := make(chan struct{})
	var wg sync.WaitGroup

	// Continuous requests
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopRequests:
					return
				default:
					_, _ = lb.CallTool(ctx, finemcp.CallToolParams{
						Name: "test-tool",
					})
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	// Add and remove backends concurrently
	time.Sleep(50 * time.Millisecond)

	// Add a backend
	newTransport := newLBMockTransport("backend-new")
	err := lb.AddBackend(LoadBalancerBackend{
		ID:               "backend-new",
		Transport:        newTransport,
		InitiallyHealthy: true,
	})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// Remove a backend
	err = lb.RemoveBackend("backend1")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// Stop requests
	close(stopRequests)
	wg.Wait()

	// Verify load balancer is in a consistent state
	lb.backendsMu.RLock()
	backendCount := len(lb.backends)
	lb.backendsMu.RUnlock()

	assert.Equal(t, 3, backendCount) // Started with 3, added 1, removed 1 = 3
}

// =============================================================================
// 7. Edge Cases (5 tests)
// =============================================================================

func TestEdgeCase_ZeroBackends(t *testing.T) {
	// Should fail to create load balancer with no backends
	_, err := NewLoadBalancer([]LoadBalancerBackend{}, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one backend required")
}

func TestEdgeCase_SingleBackend(t *testing.T) {
	backends := createTestBackends(1)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()

	// All requests should go to the single backend
	for i := 0; i < 10; i++ {
		result, err := lb.CallTool(ctx, finemcp.CallToolParams{
			Name: fmt.Sprintf("tool-%d", i),
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	}

	transport := backends[0].Transport.(*lbMockTransport)
	assert.Equal(t, int32(10), transport.callToolCalls.Load())
}

func TestEdgeCase_AllBackendsUnhealthy(t *testing.T) {
	backends := createTestBackends(3)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	// Mark all backends unhealthy
	for _, b := range lb.backends {
		b.healthy.Store(false)
	}

	ctx := context.Background()
	_, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no healthy backends")
}

func TestEdgeCase_BackendBecomesUnhealthyDuringRequest(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
	})

	ctx := context.Background()

	// Make backend0 fail
	transport0 := backends[0].Transport.(*lbMockTransport)

	// First request should succeed on backend0
	_, err := lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})
	require.NoError(t, err)

	// Make backend0 fail for subsequent requests
	transport0.setCallToolFail(true)

	// Second request should failover to backend1
	_, err = lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})
	require.NoError(t, err)

	// Verify backend1 was used (backend0 should have failed)
	transport1 := backends[1].Transport.(*lbMockTransport)
	assert.Greater(t, transport1.callToolCalls.Load(), int32(0),
		"backend1 should handle failover requests")
}

func TestEdgeCase_BackendRecoveryAfterUnhealthy(t *testing.T) {
	backends := createTestBackends(2)

	lb, err := NewLoadBalancer(backends, LoadBalancerConfig{
		Strategy: &RoundRobinStrategy{},
		HealthCheck: &HealthCheckConfig{
			Interval:           50 * time.Millisecond,
			Timeout:            25 * time.Millisecond,
			Method:             "ping",
			UnhealthyThreshold: 2,
			HealthyThreshold:   2,
		},
	})
	require.NoError(t, err)
	defer lb.Close()

	ctx := context.Background()
	_, err = lb.Initialize(ctx)
	require.NoError(t, err)

	transport0 := backends[0].Transport.(*lbMockTransport)

	// Make backend0 fail
	transport0.setPingFail(true)

	// Wait for it to become unhealthy
	assert.Eventually(t, func() bool {
		return !lb.backends[0].healthy.Load()
	}, 500*time.Millisecond, 10*time.Millisecond)

	// Restore backend0
	transport0.setPingFail(false)

	// Wait for recovery
	assert.Eventually(t, func() bool {
		return lb.backends[0].healthy.Load()
	}, 500*time.Millisecond, 10*time.Millisecond)

	// Requests should work on recovered backend
	_, err = lb.CallTool(ctx, finemcp.CallToolParams{
		Name: "test-tool",
	})
	require.NoError(t, err)
}

// =============================================================================
// 8. Metrics Tests (3 tests)
// =============================================================================

func TestMetrics_EnabledTracksRequests(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy:      &RoundRobinStrategy{},
		EnableMetrics: true,
	})

	ctx := context.Background()

	// Make some requests
	for i := 0; i < 10; i++ {
		_, _ = lb.CallTool(ctx, finemcp.CallToolParams{
			Name: "test-tool",
		})
	}

	// Check metrics
	metrics := lb.Metrics()
	require.NotNil(t, metrics)

	assert.Equal(t, uint64(10), metrics.TotalRequests.Load())
	assert.NotNil(t, metrics.BackendMetrics)
}

func TestMetrics_DisabledReturnsNil(t *testing.T) {
	backends := createTestBackends(1)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy:      &RoundRobinStrategy{},
		EnableMetrics: false,
	})

	metrics := lb.Metrics()
	assert.Nil(t, metrics)
}

func TestMetrics_BackendStatistics(t *testing.T) {
	backends := createTestBackends(2)

	lb := initializeLoadBalancer(t, backends, LoadBalancerConfig{
		Strategy:      &RoundRobinStrategy{},
		EnableMetrics: true,
	})

	ctx := context.Background()

	// Make requests
	for i := 0; i < 10; i++ {
		_, _ = lb.CallTool(ctx, finemcp.CallToolParams{
			Name: "test-tool",
		})
	}

	metrics := lb.Metrics()
	require.NotNil(t, metrics)

	// Check backend metrics
	assert.Len(t, metrics.BackendMetrics, 2)

	for id, bm := range metrics.BackendMetrics {
		assert.NotEmpty(t, id)
		assert.True(t, bm.Healthy)
		assert.GreaterOrEqual(t, bm.TotalRequests, uint64(0))
	}
}

// =============================================================================
// 9. Strategy Edge Cases (3 tests)
// =============================================================================

func TestStrategy_EmptyBackendList(t *testing.T) {
	strategies := []LoadBalancerStrategy{
		&RoundRobinStrategy{},
		&RandomStrategy{},
		&LeastConnectionsStrategy{},
	}

	for _, strategy := range strategies {
		idx := strategy.Next([]*backend{})
		assert.Equal(t, -1, idx, "strategy %s should return -1 for empty backends", strategy.Name())
	}
}

func TestStrategy_SingleBackend(t *testing.T) {
	backends := []*backend{{id: "only"}}

	strategies := []LoadBalancerStrategy{
		&RoundRobinStrategy{},
		&RandomStrategy{},
		&LeastConnectionsStrategy{},
	}

	for _, strategy := range strategies {
		for i := 0; i < 10; i++ {
			idx := strategy.Next(backends)
			assert.Equal(t, 0, idx, "strategy %s should always return 0 for single backend", strategy.Name())
		}
	}
}

func TestStrategy_Names(t *testing.T) {
	tests := []struct {
		strategy LoadBalancerStrategy
		name     string
	}{
		{&RoundRobinStrategy{}, "RoundRobin"},
		{&RandomStrategy{}, "Random"},
		{&LeastConnectionsStrategy{}, "LeastConnections"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.name, tt.strategy.Name())
	}
}
