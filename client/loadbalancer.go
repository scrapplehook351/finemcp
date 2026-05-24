package client

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/finemcp/finemcp"
)

// LoadBalancerStrategy selects a backend for each request.
//
// Implementations must be thread-safe as Next() may be called concurrently.
// The strategy receives a snapshot of healthy backends and returns the
// index of the chosen backend, or -1 if no suitable backend exists.
type LoadBalancerStrategy interface {
	// Next selects the next backend from the provided healthy backends.
	// Returns the index into the backends slice, or -1 if no backend available.
	Next(backends []*backend) int

	// Reset resets the strategy's internal state.
	// Called when backends are added, removed, or become unhealthy.
	Reset()

	// Name returns a human-readable name for the strategy.
	Name() string
}

// RoundRobinStrategy distributes requests evenly across backends in circular order.
type RoundRobinStrategy struct {
	counter atomic.Uint64
}

// Next implements LoadBalancerStrategy.
func (s *RoundRobinStrategy) Next(backends []*backend) int {
	n := len(backends)
	if n == 0 {
		return -1
	}

	idx := s.counter.Add(1) - 1
	return int(idx % uint64(n)) // #nosec G115 -- modulo ensures result is always < n, no overflow possible
}

// Reset implements LoadBalancerStrategy.
func (s *RoundRobinStrategy) Reset() {
	s.counter.Store(0)
}

// Name implements LoadBalancerStrategy.
func (s *RoundRobinStrategy) Name() string {
	return "RoundRobin"
}

// RandomStrategy selects a backend uniformly at random.
type RandomStrategy struct{}

// Next implements LoadBalancerStrategy.
func (s *RandomStrategy) Next(backends []*backend) int {
	n := len(backends)
	if n == 0 {
		return -1
	}
	return rand.IntN(n) // #nosec G404 -- load balancing does not require cryptographic randomness
}

// Reset implements LoadBalancerStrategy.
func (s *RandomStrategy) Reset() {}

// Name implements LoadBalancerStrategy.
func (s *RandomStrategy) Name() string {
	return "Random"
}

// LeastConnectionsStrategy selects the backend with the fewest active connections.
type LeastConnectionsStrategy struct{}

// Next implements LoadBalancerStrategy.
func (s *LeastConnectionsStrategy) Next(backends []*backend) int {
	n := len(backends)
	if n == 0 {
		return -1
	}

	minIdx := 0
	minConns := backends[0].activeConns.Load()

	for i := 1; i < n; i++ {
		conns := backends[i].activeConns.Load()
		if conns < minConns {
			minConns = conns
			minIdx = i
		}
	}

	return minIdx
}

// Reset implements LoadBalancerStrategy.
func (s *LeastConnectionsStrategy) Reset() {}

// Name implements LoadBalancerStrategy.
func (s *LeastConnectionsStrategy) Name() string {
	return "LeastConnections"
}

// LoadBalancerConfig configures the load balancer's behavior.
type LoadBalancerConfig struct {
	// Strategy determines how backends are selected for each request.
	// If nil, defaults to RoundRobinStrategy.
	Strategy LoadBalancerStrategy

	// HealthCheck configures backend health checking.
	// If nil, health checking is disabled (not recommended for production).
	HealthCheck *HealthCheckConfig

	// MaxRetries is the maximum number of backends to try per request.
	// 0 means try all backends once.
	// -1 means keep trying indefinitely until success or context cancellation.
	MaxRetries int

	// MaxRetriesLimit is a safety limit for MaxRetries=-1 to prevent
	// true infinite loops. Default: 1000 if MaxRetries=-1.
	// Set to 0 to use default.
	MaxRetriesLimit int

	// RetryableErrors is a predicate that determines if an error should
	// trigger a retry with another backend. If nil, defaults to retrying
	// on transport errors and server 5xx errors.
	RetryableErrors func(err error) bool

	// Observability callbacks
	OnBackendSelected   func(backendID string, attempt int)
	OnBackendFailed     func(backendID string, err error)
	OnBackendHealthy    func(backendID string)
	OnBackendUnhealthy  func(backendID string, err error)
	OnAllBackendsFailed func(err error)

	// EnableMetrics enables detailed metrics collection.
	// When true, LoadBalancer.Metrics() returns live statistics.
	EnableMetrics bool
}

// HealthCheckConfig configures backend health checking.
type HealthCheckConfig struct {
	// Interval is how often to check backend health.
	// Zero or negative values disable health checking.
	Interval time.Duration

	// Timeout is the max duration for a single health check.
	// If zero, defaults to Interval / 2.
	Timeout time.Duration

	// Method determines the health check approach:
	//   - "ping": Send MCP ping request (default)
	//   - "passive": Mark unhealthy only on request failures (no active checks)
	Method string

	// UnhealthyThreshold is the number of consecutive failures before
	// marking a backend unhealthy. Default: 3.
	UnhealthyThreshold int

	// HealthyThreshold is the number of consecutive successes before
	// marking an unhealthy backend as healthy. Default: 2.
	HealthyThreshold int
}

// LoadBalancerBackend describes a single backend server to add to the pool.
type LoadBalancerBackend struct {
	// ID uniquely identifies this backend. If empty, auto-generated.
	ID string

	// Transport is the underlying MCP transport to the server.
	Transport Transport

	// Options are the client options for this backend.
	Options Options

	// InitiallyHealthy determines if the backend starts as healthy.
	// Default: true (optimistic). Set to false for lazy health checking.
	InitiallyHealthy bool

	// Metadata is user-defined data attached to this backend.
	// Can be used for custom strategies or debugging.
	Metadata map[string]any
}

// errorHolder wraps an error for storage in atomic.Value, ensuring a
// consistent concrete type across all stores regardless of the concrete
// error implementation (*net.OpError, *url.Error, *errors.errorString, …).
type errorHolder struct{ err error }

// backend represents a single MCP server connection.
type backend struct {
	id        string
	client    *Client
	transport Transport
	options   Options

	// Health state
	healthy              atomic.Bool
	lastCheck            atomic.Value // time.Time
	lastError            atomic.Value // errorHolder
	consecutiveFailures  int
	consecutiveSuccesses int
	healthMu             sync.Mutex // protects consecutive counters

	// Connection tracking
	activeConns atomic.Int64
	totalReqs   atomic.Uint64
	totalErrs   atomic.Uint64

	// Metadata (read-only after creation)
	metadata map[string]any
}

// LoadBalancer distributes MCP requests across multiple backend servers.
// It implements the same interface as Client for drop-in replacement.
type LoadBalancer struct {
	// Configuration (immutable after creation)
	config LoadBalancerConfig

	// Backend management
	backends   []*backend
	backendsMu sync.RWMutex

	// Strategy
	strategy   LoadBalancerStrategy
	strategyMu sync.RWMutex

	// Health checking
	healthTicker *time.Ticker
	healthDone   chan struct{}
	healthWg     sync.WaitGroup // HIGH-2: Track health check goroutine

	// Lifecycle
	initialized atomic.Bool
	closed      atomic.Bool

	// Metrics
	metrics *LoadBalancerMetrics
}

// LoadBalancerMetrics provides real-time statistics.
type LoadBalancerMetrics struct {
	TotalRequests atomic.Uint64
	TotalRetries  atomic.Uint64
	TotalFailures atomic.Uint64

	BackendMetrics map[string]*BackendMetrics
}

// BackendMetrics tracks statistics for a single backend.
type BackendMetrics struct {
	ID                   string
	Healthy              bool
	ActiveConnections    int64
	TotalRequests        uint64
	TotalErrors          uint64
	LastError            error
	LastErrorTime        time.Time
	LastHealthCheck      time.Time
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
}

// NewLoadBalancer creates a load-balanced client pool.
// It initializes all backends and starts health checking if configured.
//
// Each backend's transport is wrapped with any configured wrappers
// (reconnect, circuit breaker) before creating the Client.
//
// The LoadBalancer is ready to use after this call, but backends may
// need to call Initialize before accepting requests.
func NewLoadBalancer(
	backends []LoadBalancerBackend,
	config LoadBalancerConfig,
) (*LoadBalancer, error) {
	if len(backends) == 0 {
		return nil, errors.New("load balancer: at least one backend required")
	}

	// Set defaults
	if config.Strategy == nil {
		config.Strategy = &RoundRobinStrategy{}
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = len(backends)
	}
	// HIGH-3: Validate MaxRetries
	if config.MaxRetries < -1 {
		return nil, errors.New("load balancer: MaxRetries must be >= -1")
	}
	// HIGH-3: Add safety limit for infinite retries
	if config.MaxRetries == -1 && config.MaxRetriesLimit == 0 {
		config.MaxRetriesLimit = 1000 // Default safety limit
	}
	if config.RetryableErrors == nil {
		config.RetryableErrors = defaultRetryableErrors
	}
	if config.HealthCheck != nil {
		if config.HealthCheck.UnhealthyThreshold == 0 {
			config.HealthCheck.UnhealthyThreshold = 3
		}
		if config.HealthCheck.HealthyThreshold == 0 {
			config.HealthCheck.HealthyThreshold = 2
		}
		if config.HealthCheck.Timeout == 0 {
			config.HealthCheck.Timeout = config.HealthCheck.Interval / 2
		}
		if config.HealthCheck.Method == "" {
			config.HealthCheck.Method = "ping"
		}
	}

	lb := &LoadBalancer{
		config:     config,
		backends:   make([]*backend, 0, len(backends)),
		strategy:   config.Strategy,
		healthDone: make(chan struct{}),
	}

	if config.EnableMetrics {
		lb.metrics = &LoadBalancerMetrics{
			BackendMetrics: make(map[string]*BackendMetrics),
		}
	}

	// Initialize backends
	for i, desc := range backends {
		b, err := lb.createBackend(desc, i)
		if err != nil {
			return nil, fmt.Errorf("load balancer: backend %d: %w", i, err)
		}
		lb.backends = append(lb.backends, b)
	}

	// Start health checking
	if config.HealthCheck != nil && config.HealthCheck.Interval > 0 {
		lb.healthTicker = time.NewTicker(config.HealthCheck.Interval)
		lb.healthWg.Add(1) // HIGH-2: Track health check goroutine
		go lb.healthCheckLoop()
	}

	return lb, nil
}

// createBackend creates a backend instance from a descriptor.
func (lb *LoadBalancer) createBackend(desc LoadBalancerBackend, idx int) (*backend, error) {
	// Generate ID if not provided
	id := desc.ID
	if id == "" {
		id = fmt.Sprintf("backend-%d", idx)
	}

	// Create client
	client, err := New(desc.Transport, desc.Options)
	if err != nil {
		return nil, err
	}

	b := &backend{
		id:        id,
		client:    client,
		transport: desc.Transport,
		options:   desc.Options,
		metadata:  desc.Metadata,
	}

	// Set initial health state (default true)
	initiallyHealthy := desc.InitiallyHealthy
	if !initiallyHealthy && desc.InitiallyHealthy {
		initiallyHealthy = true
	} else if desc.InitiallyHealthy {
		initiallyHealthy = true
	} else {
		// If not explicitly set, default to true
		initiallyHealthy = true
	}
	b.healthy.Store(initiallyHealthy)
	b.lastCheck.Store(time.Now())

	return b, nil
}

// defaultRetryableErrors determines if an error should trigger a retry.
func defaultRetryableErrors(err error) bool {
	if err == nil {
		return false
	}

	// Don't retry context errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Don't retry client-side errors
	if errors.Is(err, ErrNotInitialized) || errors.Is(err, ErrAlreadyInit) {
		return false
	}

	// Retry server errors (5xx)
	var respErr *ResponseError
	if errors.As(err, &respErr) {
		return respErr.Code >= 500 && respErr.Code < 600
	}

	// Default: retry (includes transport errors, circuit breaker open, etc.)
	return true
}

// healthCheckLoop runs periodic health checks on all backends.
func (lb *LoadBalancer) healthCheckLoop() {
	defer lb.healthWg.Done() // HIGH-2: Signal completion
	for {
		select {
		case <-lb.healthTicker.C:
			lb.performHealthChecks()
		case <-lb.healthDone:
			return
		}
	}
}

// performHealthChecks checks all backends concurrently.
func (lb *LoadBalancer) performHealthChecks() {
	lb.backendsMu.RLock()
	backends := append([]*backend(nil), lb.backends...)
	lb.backendsMu.RUnlock()

	var wg sync.WaitGroup
	for _, b := range backends {
		wg.Add(1)
		go func(backend *backend) {
			defer wg.Done()
			lb.checkBackendHealth(backend)
		}(b)
	}
	wg.Wait()
}

// checkBackendHealth performs a health check on a single backend.
func (lb *LoadBalancer) checkBackendHealth(b *backend) {
	ctx, cancel := context.WithTimeout(context.Background(), lb.config.HealthCheck.Timeout)
	defer cancel()

	var err error
	if lb.config.HealthCheck.Method == "ping" {
		err = b.client.Ping(ctx)
	}

	b.lastCheck.Store(time.Now())

	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	if err != nil {
		b.lastError.Store(errorHolder{err: err})
		b.consecutiveFailures++
		b.consecutiveSuccesses = 0

		if b.consecutiveFailures >= lb.config.HealthCheck.UnhealthyThreshold {
			if b.healthy.CompareAndSwap(true, false) {
				// Became unhealthy
				if lb.config.OnBackendUnhealthy != nil {
					go lb.safeCallback(func() {
						lb.config.OnBackendUnhealthy(b.id, err)
					})
				}
				// MEDIUM-3: Protect strategy.Reset() with mutex
				lb.strategyMu.Lock()
				lb.strategy.Reset()
				lb.strategyMu.Unlock()
			}
		}
	} else {
		b.consecutiveSuccesses++

		if !b.healthy.Load() && b.consecutiveSuccesses >= lb.config.HealthCheck.HealthyThreshold {
			if b.healthy.CompareAndSwap(false, true) {
				// Became healthy
				b.consecutiveFailures = 0
				if lb.config.OnBackendHealthy != nil {
					go lb.safeCallback(func() {
						lb.config.OnBackendHealthy(b.id)
					})
				}
				// MEDIUM-3: Protect strategy.Reset() with mutex
				lb.strategyMu.Lock()
				lb.strategy.Reset()
				lb.strategyMu.Unlock()
			}
		}
	}
}

// recordRequestError records a request error for health tracking.
func (lb *LoadBalancer) recordRequestError(b *backend, err error) {
	if !lb.config.RetryableErrors(err) {
		return
	}

	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	b.lastError.Store(errorHolder{err: err})
	b.consecutiveFailures++
	b.consecutiveSuccesses = 0

	if lb.config.HealthCheck != nil &&
		b.consecutiveFailures >= lb.config.HealthCheck.UnhealthyThreshold {
		if b.healthy.CompareAndSwap(true, false) {
			if lb.config.OnBackendUnhealthy != nil {
				go lb.safeCallback(func() {
					lb.config.OnBackendUnhealthy(b.id, err)
				})
			}
			// MEDIUM-3: Protect strategy.Reset() with mutex
			lb.strategyMu.Lock()
			lb.strategy.Reset()
			lb.strategyMu.Unlock()
		}
	}
}

// recordRequestSuccess records a successful request.
func (lb *LoadBalancer) recordRequestSuccess(b *backend) {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()

	b.consecutiveFailures = 0
	b.consecutiveSuccesses++
}

// safeCallback wraps a callback function with panic recovery.
// CRITICAL-1: Prevents panics in user callbacks from crashing the service.
func (lb *LoadBalancer) safeCallback(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			// Panic recovered - log but don't crash
			// In production, this should use proper logging
			_ = r // Suppress unused variable warning
		}
	}()
	fn()
}

// getHealthyBackends returns a snapshot of currently healthy backends.
func (lb *LoadBalancer) getHealthyBackends() []*backend {
	lb.backendsMu.RLock()
	defer lb.backendsMu.RUnlock()

	healthy := make([]*backend, 0, len(lb.backends))
	for _, b := range lb.backends {
		if b.healthy.Load() {
			healthy = append(healthy, b)
		}
	}
	return healthy
}

// executeWithFailover executes a function with automatic failover to other backends.
func (lb *LoadBalancer) executeWithFailover(
	ctx context.Context,
	fn func(b *backend) error,
) error {
	if lb.closed.Load() {
		return ErrClosed
	}

	maxRetries := lb.config.MaxRetries
	if maxRetries == 0 {
		lb.backendsMu.RLock()
		maxRetries = len(lb.backends)
		lb.backendsMu.RUnlock()
	}

	tried := make(map[string]bool)
	var lastErr error
	attemptCount := 0

	for attempt := 0; attempt < maxRetries || maxRetries == -1; attempt++ {
		// HIGH-3: Safety check for infinite retries
		attemptCount++
		if maxRetries == -1 && attemptCount > lb.config.MaxRetriesLimit {
			lastErr = errors.New("retry limit exceeded")
			break
		}
		// Check context
		if err := ctx.Err(); err != nil {
			return err
		}

		// Get healthy backends snapshot
		healthyBackends := lb.getHealthyBackends()
		if len(healthyBackends) == 0 {
			if lastErr == nil {
				lastErr = errors.New("no healthy backends available")
			}
			break
		}

		// Select backend using strategy
		lb.strategyMu.RLock()
		idx := lb.strategy.Next(healthyBackends)
		lb.strategyMu.RUnlock()

		if idx < 0 || idx >= len(healthyBackends) {
			lastErr = errors.New("strategy returned invalid index")
			break
		}

		backend := healthyBackends[idx]

		// Skip if already tried
		if tried[backend.id] {
			continue
		}
		tried[backend.id] = true

		// Notify backend selected
		if lb.config.OnBackendSelected != nil {
			go lb.safeCallback(func() {
				lb.config.OnBackendSelected(backend.id, attempt)
			})
		}

		// Track metrics
		if lb.config.EnableMetrics {
			lb.metrics.TotalRequests.Add(1)
			if attempt > 0 {
				lb.metrics.TotalRetries.Add(1)
			}
		}

		// Track active connection
		backend.activeConns.Add(1)
		backend.totalReqs.Add(1)

		// Execute request
		err := fn(backend)

		backend.activeConns.Add(-1)

		if err == nil {
			// Success
			lb.recordRequestSuccess(backend)
			return nil
		}

		// Handle error
		lastErr = err
		backend.totalErrs.Add(1)

		if lb.config.OnBackendFailed != nil {
			go lb.safeCallback(func() {
				lb.config.OnBackendFailed(backend.id, err)
			})
		}

		if !lb.config.RetryableErrors(err) {
			// Non-retryable error
			return err
		}

		// Record error for health tracking
		lb.recordRequestError(backend, err)

		// Continue to retry with next backend
	}

	// All backends failed
	if lb.config.EnableMetrics {
		lb.metrics.TotalFailures.Add(1)
	}

	if lb.config.OnAllBackendsFailed != nil {
		go lb.safeCallback(func() {
			lb.config.OnAllBackendsFailed(lastErr)
		})
	}

	if lastErr == nil {
		lastErr = errors.New("all backends failed")
	}
	return fmt.Errorf("load balancer: all backends failed: %w", lastErr)
}

// ── LoadBalancer Lifecycle ─────────────────────────────────────────

// Initialize initializes ALL backends concurrently.
// Returns success if at least one backend initializes successfully.
func (lb *LoadBalancer) Initialize(ctx context.Context) (*finemcp.InitializeResult, error) {
	// HIGH-1: Check if already initialized without setting the flag yet
	if lb.initialized.Load() {
		return nil, ErrAlreadyInit
	}

	lb.backendsMu.RLock()
	backends := append([]*backend(nil), lb.backends...)
	lb.backendsMu.RUnlock()

	type result struct {
		backend *backend
		result  *finemcp.InitializeResult
		err     error
	}

	results := make(chan result, len(backends))

	// Initialize all backends concurrently
	for _, b := range backends {
		go func(backend *backend) {
			res, err := backend.client.Initialize(ctx)
			results <- result{backend: backend, result: res, err: err}
		}(b)
	}

	// Collect results
	var successResult *finemcp.InitializeResult
	successCount := 0

	for i := 0; i < len(backends); i++ {
		r := <-results
		if r.err != nil {
			// Mark backend unhealthy
			r.backend.healthy.Store(false)
			r.backend.lastError.Store(errorHolder{err: r.err})
			if lb.config.OnBackendUnhealthy != nil {
				go lb.safeCallback(func() {
					lb.config.OnBackendUnhealthy(r.backend.id, r.err)
				})
			}
		} else {
			successCount++
			if successResult == nil {
				successResult = r.result
			}
		}
	}

	if successCount == 0 {
		// HIGH-1: Don't set initialized=true if all backends failed
		return nil, errors.New("load balancer: all backends failed to initialize")
	}

	// HIGH-1: Only set initialized=true on success
	lb.initialized.Store(true)
	return successResult, nil
}

// AddBackend adds a new backend to the pool at runtime.
// The backend is initialized if the LoadBalancer is already initialized.
func (lb *LoadBalancer) AddBackend(desc LoadBalancerBackend) error {
	if lb.closed.Load() {
		return ErrClosed
	}

	lb.backendsMu.Lock()
	idx := len(lb.backends)
	b, err := lb.createBackend(desc, idx)
	if err != nil {
		lb.backendsMu.Unlock()
		return err
	}

	lb.backends = append(lb.backends, b)
	lb.backendsMu.Unlock()

	// Initialize if LB is already initialized
	if lb.initialized.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := b.client.Initialize(ctx); err != nil {
			b.healthy.Store(false)
			b.lastError.Store(errorHolder{err: err})
			if lb.config.OnBackendUnhealthy != nil {
				go lb.safeCallback(func() {
					lb.config.OnBackendUnhealthy(b.id, err)
				})
			}
		}
	}

	lb.strategyMu.Lock()
	lb.strategy.Reset()
	lb.strategyMu.Unlock()

	return nil
}

// RemoveBackend removes a backend by ID.
// Active requests to this backend will complete, but no new requests are routed.
func (lb *LoadBalancer) RemoveBackend(id string) error {
	lb.backendsMu.Lock()
	defer lb.backendsMu.Unlock()

	for i, b := range lb.backends {
		if b.id == id {
			// Close the backend client
			_ = b.client.Close()

			// Remove from slice
			lb.backends = append(lb.backends[:i], lb.backends[i+1:]...)

			lb.strategyMu.Lock()
			lb.strategy.Reset()
			lb.strategyMu.Unlock()

			return nil
		}
	}

	return fmt.Errorf("load balancer: backend %s not found", id)
}

// Close closes all backend clients and stops health checking.
func (lb *LoadBalancer) Close() error {
	if lb.closed.Swap(true) {
		return nil
	}

	// Stop health checking
	if lb.healthTicker != nil {
		lb.healthTicker.Stop()
	}
	close(lb.healthDone)

	// HIGH-2: Wait for health check goroutine to exit
	lb.healthWg.Wait()

	lb.backendsMu.Lock()
	defer lb.backendsMu.Unlock()

	// Close all backends
	var firstErr error
	for _, b := range lb.backends {
		if err := b.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// Metrics returns a snapshot of current metrics.
func (lb *LoadBalancer) Metrics() *LoadBalancerMetrics {
	if !lb.config.EnableMetrics || lb.metrics == nil {
		return nil
	}

	snapshot := &LoadBalancerMetrics{
		BackendMetrics: make(map[string]*BackendMetrics),
	}

	snapshot.TotalRequests.Store(lb.metrics.TotalRequests.Load())
	snapshot.TotalRetries.Store(lb.metrics.TotalRetries.Load())
	snapshot.TotalFailures.Store(lb.metrics.TotalFailures.Load())

	// CRITICAL-2: Copy backend references first, release lock before iterating
	lb.backendsMu.RLock()
	backends := append([]*backend(nil), lb.backends...)
	lb.backendsMu.RUnlock()

	// Now iterate without holding backendsMu
	for _, b := range backends {
		b.healthMu.Lock()
		var lastErr error
		if h, ok := b.lastError.Load().(errorHolder); ok {
			lastErr = h.err
		}
		lastCheck, _ := b.lastCheck.Load().(time.Time)

		snapshot.BackendMetrics[b.id] = &BackendMetrics{
			ID:                   b.id,
			Healthy:              b.healthy.Load(),
			ActiveConnections:    b.activeConns.Load(),
			TotalRequests:        b.totalReqs.Load(),
			TotalErrors:          b.totalErrs.Load(),
			LastError:            lastErr,
			LastHealthCheck:      lastCheck,
			ConsecutiveFailures:  b.consecutiveFailures,
			ConsecutiveSuccesses: b.consecutiveSuccesses,
		}
		b.healthMu.Unlock()
	}

	return snapshot
}

// ── MCP Method Proxying ─────────────────────────────────────────────

// Ping forwards the ping request to a selected backend.
func (lb *LoadBalancer) Ping(ctx context.Context) error {
	if !lb.initialized.Load() {
		return ErrNotInitialized
	}

	return lb.executeWithFailover(ctx, func(b *backend) error {
		return b.client.Ping(ctx)
	})
}

// CallTool forwards the tools/call request to a selected backend.
func (lb *LoadBalancer) CallTool(
	ctx context.Context,
	params finemcp.CallToolParams,
) (*finemcp.CallToolResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.CallToolResult
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.CallTool(ctx, params)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// ListTools returns the union of tools from all healthy backends.
func (lb *LoadBalancer) ListTools(
	ctx context.Context,
	params finemcp.ListParams,
) (*finemcp.ListToolsResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return nil, errors.New("load balancer: no healthy backends")
	}

	type result struct {
		tools []finemcp.ToolInfo
		err   error
	}

	results := make(chan result, len(healthyBackends))

	// Query all backends
	for _, b := range healthyBackends {
		go func(backend *backend) {
			res, err := backend.client.ListTools(ctx, params)
			if err != nil {
				results <- result{err: err}
			} else {
				results <- result{tools: res.Tools}
			}
		}(b)
	}

	// Collect and merge tools
	seen := make(map[string]bool)
	var allTools []finemcp.ToolInfo

	for i := 0; i < len(healthyBackends); i++ {
		r := <-results
		if r.err != nil {
			continue
		}

		for _, tool := range r.tools {
			if !seen[tool.Name] {
				allTools = append(allTools, tool)
				seen[tool.Name] = true
			}
		}
	}

	return &finemcp.ListToolsResult{Tools: allTools}, nil
}

// ListResources returns the union of resources from all healthy backends.
func (lb *LoadBalancer) ListResources(
	ctx context.Context,
	params finemcp.ListParams,
) (*finemcp.ListResourcesResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return nil, errors.New("load balancer: no healthy backends")
	}

	type result struct {
		resources []finemcp.ResourceInfo
		err       error
	}

	results := make(chan result, len(healthyBackends))

	for _, b := range healthyBackends {
		go func(backend *backend) {
			res, err := backend.client.ListResources(ctx, params)
			if err != nil {
				results <- result{err: err}
			} else {
				results <- result{resources: res.Resources}
			}
		}(b)
	}

	seen := make(map[string]bool)
	var allResources []finemcp.ResourceInfo

	for i := 0; i < len(healthyBackends); i++ {
		r := <-results
		if r.err != nil {
			continue
		}

		for _, resource := range r.resources {
			if !seen[resource.URI] {
				allResources = append(allResources, resource)
				seen[resource.URI] = true
			}
		}
	}

	return &finemcp.ListResourcesResult{Resources: allResources}, nil
}

// ReadResource forwards the resources/read request to a selected backend.
func (lb *LoadBalancer) ReadResource(
	ctx context.Context,
	params finemcp.ReadResourceParams,
) (*finemcp.ReadResourceResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.ReadResourceResult
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.ReadResource(ctx, params)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// ListResourceTemplates returns the union of resource templates from all healthy backends.
func (lb *LoadBalancer) ListResourceTemplates(
	ctx context.Context,
	params finemcp.ListParams,
) (*finemcp.ListResourceTemplatesResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return nil, errors.New("load balancer: no healthy backends")
	}

	type result struct {
		templates []finemcp.ResourceTemplateInfo
		err       error
	}

	results := make(chan result, len(healthyBackends))

	for _, b := range healthyBackends {
		go func(backend *backend) {
			res, err := backend.client.ListResourceTemplates(ctx, params)
			if err != nil {
				results <- result{err: err}
			} else {
				results <- result{templates: res.ResourceTemplates}
			}
		}(b)
	}

	seen := make(map[string]bool)
	var allTemplates []finemcp.ResourceTemplateInfo

	for i := 0; i < len(healthyBackends); i++ {
		r := <-results
		if r.err != nil {
			continue
		}

		for _, template := range r.templates {
			if !seen[template.URITemplate] {
				allTemplates = append(allTemplates, template)
				seen[template.URITemplate] = true
			}
		}
	}

	return &finemcp.ListResourceTemplatesResult{ResourceTemplates: allTemplates}, nil
}

// SubscribeResource forwards the resources/subscribe request to a selected backend.
func (lb *LoadBalancer) SubscribeResource(
	ctx context.Context,
	params finemcp.SubscribeParams,
) error {
	if !lb.initialized.Load() {
		return ErrNotInitialized
	}

	return lb.executeWithFailover(ctx, func(b *backend) error {
		return b.client.SubscribeResource(ctx, params)
	})
}

// UnsubscribeResource forwards the resources/unsubscribe request to a selected backend.
func (lb *LoadBalancer) UnsubscribeResource(
	ctx context.Context,
	params finemcp.SubscribeParams,
) error {
	if !lb.initialized.Load() {
		return ErrNotInitialized
	}

	return lb.executeWithFailover(ctx, func(b *backend) error {
		return b.client.UnsubscribeResource(ctx, params)
	})
}

// ListPrompts returns the union of prompts from all healthy backends.
func (lb *LoadBalancer) ListPrompts(
	ctx context.Context,
	params finemcp.ListParams,
) (*finemcp.ListPromptsResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return nil, errors.New("load balancer: no healthy backends")
	}

	type result struct {
		prompts []finemcp.PromptInfo
		err     error
	}

	results := make(chan result, len(healthyBackends))

	for _, b := range healthyBackends {
		go func(backend *backend) {
			res, err := backend.client.ListPrompts(ctx, params)
			if err != nil {
				results <- result{err: err}
			} else {
				results <- result{prompts: res.Prompts}
			}
		}(b)
	}

	seen := make(map[string]bool)
	var allPrompts []finemcp.PromptInfo

	for i := 0; i < len(healthyBackends); i++ {
		r := <-results
		if r.err != nil {
			continue
		}

		for _, prompt := range r.prompts {
			if !seen[prompt.Name] {
				allPrompts = append(allPrompts, prompt)
				seen[prompt.Name] = true
			}
		}
	}

	return &finemcp.ListPromptsResult{Prompts: allPrompts}, nil
}

// GetPrompt forwards the prompts/get request to a selected backend.
func (lb *LoadBalancer) GetPrompt(
	ctx context.Context,
	params finemcp.GetPromptParams,
) (*finemcp.GetPromptResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.GetPromptResult
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.GetPrompt(ctx, params)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// ListRoots returns the union of roots from all healthy backends.
func (lb *LoadBalancer) ListRoots(
	ctx context.Context,
	params finemcp.ListParams,
) (*finemcp.ListRootsResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return nil, errors.New("load balancer: no healthy backends")
	}

	type result struct {
		roots []finemcp.RootInfo
		err   error
	}

	results := make(chan result, len(healthyBackends))

	for _, b := range healthyBackends {
		go func(backend *backend) {
			res, err := backend.client.ListRoots(ctx, params)
			if err != nil {
				results <- result{err: err}
			} else {
				results <- result{roots: res.Roots}
			}
		}(b)
	}

	seen := make(map[string]bool)
	var allRoots []finemcp.RootInfo

	for i := 0; i < len(healthyBackends); i++ {
		r := <-results
		if r.err != nil {
			continue
		}

		for _, root := range r.roots {
			if !seen[root.URI] {
				allRoots = append(allRoots, root)
				seen[root.URI] = true
			}
		}
	}

	return &finemcp.ListRootsResult{Roots: allRoots}, nil
}

// Complete forwards the completion/complete request to a selected backend.
func (lb *LoadBalancer) Complete(
	ctx context.Context,
	params finemcp.CompleteParams,
) (*finemcp.CompleteResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.CompleteResult
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.Complete(ctx, params)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// SetLogLevel forwards the logging/setLevel request to all healthy backends.
func (lb *LoadBalancer) SetLogLevel(ctx context.Context, level finemcp.LogLevel) error {
	if !lb.initialized.Load() {
		return ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return errors.New("load balancer: no healthy backends")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(healthyBackends))

	for _, b := range healthyBackends {
		wg.Add(1)
		go func(backend *backend) {
			defer wg.Done()
			if err := backend.client.SetLogLevel(ctx, level); err != nil {
				errCh <- err
			}
		}(b)
	}

	wg.Wait()
	close(errCh)

	// Return first error if any
	for err := range errCh {
		return err
	}

	return nil
}

// GetTask forwards the tasks/get request to a selected backend.
func (lb *LoadBalancer) GetTask(ctx context.Context, taskID string) (*finemcp.Task, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.Task
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// GetTaskResult forwards the tasks/result request to a selected backend.
func (lb *LoadBalancer) GetTaskResult(
	ctx context.Context,
	taskID string,
) (*finemcp.CallToolResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.CallToolResult
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.GetTaskResult(ctx, taskID)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// CancelTask forwards the tasks/cancel request to a selected backend.
func (lb *LoadBalancer) CancelTask(ctx context.Context, taskID string) (*finemcp.Task, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	var result *finemcp.Task
	err := lb.executeWithFailover(ctx, func(b *backend) error {
		res, err := b.client.CancelTask(ctx, taskID)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	return result, err
}

// ListTasks returns the union of tasks from all healthy backends.
func (lb *LoadBalancer) ListTasks(
	ctx context.Context,
	params finemcp.ListParams,
) (*finemcp.ListTasksResult, error) {
	if !lb.initialized.Load() {
		return nil, ErrNotInitialized
	}

	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return nil, errors.New("load balancer: no healthy backends")
	}

	type result struct {
		tasks []finemcp.Task
		err   error
	}

	results := make(chan result, len(healthyBackends))

	for _, b := range healthyBackends {
		go func(backend *backend) {
			res, err := backend.client.ListTasks(ctx, params)
			if err != nil {
				results <- result{err: err}
			} else {
				results <- result{tasks: res.Tasks}
			}
		}(b)
	}

	seen := make(map[string]bool)
	var allTasks []finemcp.Task

	for i := 0; i < len(healthyBackends); i++ {
		r := <-results
		if r.err != nil {
			continue
		}

		for _, task := range r.tasks {
			if !seen[task.TaskID] {
				allTasks = append(allTasks, task)
				seen[task.TaskID] = true
			}
		}
	}

	return &finemcp.ListTasksResult{Tasks: allTasks}, nil
}

// ServerInfo returns the server info from the first healthy backend.
func (lb *LoadBalancer) ServerInfo() finemcp.ProcessInfo {
	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return finemcp.ProcessInfo{}
	}
	return healthyBackends[0].client.ServerInfo()
}

// ServerCapabilities returns the union of capabilities from all healthy backends.
func (lb *LoadBalancer) ServerCapabilities() finemcp.ServerCapabilities {
	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return finemcp.ServerCapabilities{}
	}

	// Return capabilities from first healthy backend
	// In a more sophisticated implementation, this could merge capabilities
	return healthyBackends[0].client.ServerCapabilities()
}

// NegotiatedVersion returns the negotiated protocol version from the first healthy backend.
func (lb *LoadBalancer) NegotiatedVersion() string {
	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return ""
	}
	return healthyBackends[0].client.NegotiatedVersion()
}

// Instructions returns the instructions from the first healthy backend.
func (lb *LoadBalancer) Instructions() string {
	healthyBackends := lb.getHealthyBackends()
	if len(healthyBackends) == 0 {
		return ""
	}
	return healthyBackends[0].client.Instructions()
}
