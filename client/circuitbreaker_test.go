package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Transport for Testing
// =============================================================================

// mockTransport is a test double that simulates a Transport for circuit breaker testing.
type mockTransport struct {
	mu sync.Mutex

	// Behavior configuration
	sendErr    error
	receiveErr error
	receiveMsg []byte
	startErr   error

	// Call tracking
	startCalls   int
	sendCalls    int
	receiveCalls int
	closeCalls   int

	// Latency simulation
	sendDelay    time.Duration
	receiveDelay time.Duration
}

func (m *mockTransport) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls++
	return m.startErr
}

func (m *mockTransport) Send(ctx context.Context, data []byte) error {
	m.mu.Lock()
	m.sendCalls++
	delay := m.sendDelay
	err := m.sendErr
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return err
}

func (m *mockTransport) Receive(ctx context.Context) ([]byte, error) {
	m.mu.Lock()
	m.receiveCalls++
	delay := m.receiveDelay
	err := m.receiveErr
	msg := m.receiveMsg
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return msg, err
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	return nil
}

// setSendError configures the mock to return an error from Send.
func (m *mockTransport) setSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendErr = err
}

// setReceiveError configures the mock to return an error from Receive.
func (m *mockTransport) setReceiveError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receiveErr = err
}

// getCalls returns the number of Send/Receive calls.
func (m *mockTransport) getCalls() (send, receive int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sendCalls, m.receiveCalls
}

// =============================================================================
// 1. State Transition Tests (8 tests)
// =============================================================================

func TestCircuitBreaker_InitialState(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 3,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport, ok := cb.(*circuitBreakerTransport)
	require.True(t, ok)

	// Assert
	assert.Equal(t, StateClosed, cbTransport.State())
	metrics := cbTransport.Metrics()
	assert.Equal(t, StateClosed, metrics.State)
	assert.Equal(t, 0, metrics.ConsecutiveFailures)
	assert.Equal(t, 0, metrics.HalfOpenRequests)
}

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	tests := []struct {
		name        string
		maxFailures int
		failures    int
		wantState   CircuitState
	}{
		{
			name:        "below threshold stays closed",
			maxFailures: 5,
			failures:    4,
			wantState:   StateClosed,
		},
		{
			name:        "at threshold opens circuit",
			maxFailures: 5,
			failures:    5,
			wantState:   StateOpen,
		},
		{
			name:        "above threshold stays open",
			maxFailures: 3,
			failures:    10,
			wantState:   StateOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mock := &mockTransport{}
			transportErr := errors.New("transport error")
			mock.setSendError(transportErr)

			var stateChanges []CircuitState
			var stateChangeMu sync.Mutex

			cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
				MaxFailures: tt.maxFailures,
				OpenTimeout: 100 * time.Millisecond,
				OnStateChange: func(from, to CircuitState) {
					stateChangeMu.Lock()
					stateChanges = append(stateChanges, to)
					stateChangeMu.Unlock()
				},
			})

			cbTransport := cb.(*circuitBreakerTransport)

			// Act - trigger failures
			for i := 0; i < tt.failures; i++ {
				err := cb.Send(context.Background(), []byte("test"))
				// After circuit opens, subsequent calls return ErrCircuitOpen
				if i < tt.maxFailures {
					assert.Equal(t, transportErr, err)
				} else {
					assert.Error(t, err) // ErrCircuitOpen or transportErr
				}
			}

			// Give callbacks time to execute
			time.Sleep(50 * time.Millisecond)

			// Assert
			assert.Equal(t, tt.wantState, cbTransport.State())

			if tt.wantState == StateOpen {
				stateChangeMu.Lock()
				assert.Contains(t, stateChanges, StateOpen)
				stateChangeMu.Unlock()
			}
		})
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - trip circuit
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	assert.Equal(t, StateOpen, cbTransport.State())

	// Wait for timeout to expire
	time.Sleep(150 * time.Millisecond)

	// Clear error for recovery test
	mock.setSendError(nil)

	// Attempt a request to trigger transition to Half-Open
	err := cb.Send(context.Background(), []byte("test"))

	// Assert
	assert.NoError(t, err)
	// After successful request in Half-Open, should be Closed
	assert.Equal(t, StateClosed, cbTransport.State())
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	var stateChanges []CircuitState
	var stateChangeMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			stateChangeMu.Lock()
			stateChanges = append(stateChanges, to)
			stateChangeMu.Unlock()
		},
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - trip circuit to Open
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateOpen, cbTransport.State())

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Clear error for successful recovery
	mock.setSendError(nil)

	// Send successful request (triggers Open → Half-Open → Closed)
	err := cb.Send(context.Background(), []byte("test"))

	// Give callbacks time to execute
	time.Sleep(50 * time.Millisecond)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, StateClosed, cbTransport.State())

	stateChangeMu.Lock()
	assert.Contains(t, stateChanges, StateHalfOpen)
	assert.Contains(t, stateChanges, StateClosed)
	stateChangeMu.Unlock()
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	var stateChanges []CircuitState
	var stateChangeMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			stateChangeMu.Lock()
			stateChanges = append(stateChanges, to)
			stateChangeMu.Unlock()
		},
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - trip circuit to Open
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateOpen, cbTransport.State())

	// Wait for timeout to transition to Half-Open
	time.Sleep(150 * time.Millisecond)

	// Send failing request (should transition Half-Open → Open)
	err := cb.Send(context.Background(), []byte("test"))

	// Give callbacks time to execute
	time.Sleep(50 * time.Millisecond)

	// Assert
	assert.Equal(t, transportErr, err)
	assert.Equal(t, StateOpen, cbTransport.State())

	stateChangeMu.Lock()
	// Should see: Open, then HalfOpen, then Open again
	// Note: callbacks are async (goroutines), so order may vary slightly
	require.GreaterOrEqual(t, len(stateChanges), 3)
	assert.Contains(t, stateChanges, StateOpen)
	assert.Contains(t, stateChanges, StateHalfOpen)
	// First transition should be to Open
	assert.Equal(t, StateOpen, stateChanges[0])
	stateChangeMu.Unlock()
}

func TestCircuitBreaker_StaysClosed(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 5,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - alternate success and failure to prevent opening
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			mock.setSendError(transportErr)
			cb.Send(context.Background(), []byte("test"))
		} else {
			mock.setSendError(nil)
			cb.Send(context.Background(), []byte("test"))
		}
	}

	// Assert - should stay closed because consecutive failures never hit threshold
	assert.Equal(t, StateClosed, cbTransport.State())
	metrics := cbTransport.Metrics()
	assert.Less(t, metrics.ConsecutiveFailures, 5)
}

func TestCircuitBreaker_StaysOpen(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 200 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - trip circuit
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateOpen, cbTransport.State())

	// Try to send within timeout period
	err := cb.Send(context.Background(), []byte("test"))

	// Assert - should be rejected immediately
	assert.Equal(t, ErrCircuitOpen, err)
	assert.Equal(t, StateOpen, cbTransport.State())

	// Verify mock wasn't called (circuit rejected before reaching transport)
	sendCalls, _ := mock.getCalls()
	assert.Equal(t, 2, sendCalls) // Only the first 2 calls that tripped the circuit
}

func TestCircuitBreaker_MultipleTransitions(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")

	var stateChanges []CircuitState
	var stateChangeMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			stateChangeMu.Lock()
			stateChanges = append(stateChanges, to)
			stateChangeMu.Unlock()
		},
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - full cycle test

	// 1. Closed → Open
	mock.setSendError(transportErr)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateOpen, cbTransport.State())

	// 2. Wait for timeout → Half-Open → Closed
	time.Sleep(150 * time.Millisecond)
	mock.setSendError(nil)
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateClosed, cbTransport.State())

	// 3. Closed → Open again
	mock.setSendError(transportErr)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateOpen, cbTransport.State())

	// 4. Wait for timeout → Half-Open → Open (test fails)
	time.Sleep(150 * time.Millisecond)
	cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, StateOpen, cbTransport.State())

	time.Sleep(50 * time.Millisecond)

	// Assert - verify state transition sequence
	// Note: callbacks are executed in goroutines, so order may not be strictly sequential
	stateChangeMu.Lock()
	require.GreaterOrEqual(t, len(stateChanges), 6, "Should have at least 6 transitions")
	// Verify all expected states appear
	assert.Contains(t, stateChanges, StateOpen)
	assert.Contains(t, stateChanges, StateHalfOpen)
	assert.Contains(t, stateChanges, StateClosed)
	// Count occurrences
	openCount := 0
	halfOpenCount := 0
	closedCount := 0
	for _, s := range stateChanges {
		switch s {
		case StateOpen:
			openCount++
		case StateHalfOpen:
			halfOpenCount++
		case StateClosed:
			closedCount++
		}
	}
	assert.Equal(t, 3, openCount, "Should have 3 Open transitions")
	assert.Equal(t, 2, halfOpenCount, "Should have 2 Half-Open transitions")
	assert.Equal(t, 1, closedCount, "Should have 1 Closed transition")
	stateChangeMu.Unlock()
}

// =============================================================================
// 2. Concurrent Access Tests (5 tests)
// =============================================================================

func TestCircuitBreaker_ConcurrentSend(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 50,
		OpenTimeout: 1 * time.Second,
	})

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Act - send from multiple goroutines concurrently
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			err := cb.Send(context.Background(), []byte("test"))
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// Assert - all calls should succeed
	sendCalls, _ := mock.getCalls()
	assert.Equal(t, numGoroutines, sendCalls)
}

func TestCircuitBreaker_ConcurrentReceive(t *testing.T) {
	// Arrange
	mock := &mockTransport{
		receiveMsg: []byte("response"),
	}

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 50,
		OpenTimeout: 1 * time.Second,
	})

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Act - receive from multiple goroutines concurrently
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			data, err := cb.Receive(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, []byte("response"), data)
		}()
	}

	wg.Wait()

	// Assert - all calls should succeed
	_, receiveCalls := mock.getCalls()
	assert.Equal(t, numGoroutines, receiveCalls)
}

func TestCircuitBreaker_RaceOnTransition(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 3,
		OpenTimeout: 50 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Act - trigger state transition while requests are in flight
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			// Some will succeed in opening, others will be rejected
			cb.Send(context.Background(), []byte("test"))
		}()
	}

	wg.Wait()

	// Assert - should end in Open state, no panics or races
	assert.Equal(t, StateOpen, cbTransport.State())
}

func TestCircuitBreaker_HalfOpenConcurrentRequests(t *testing.T) {
	// Arrange
	mock := &mockTransport{
		sendDelay: 50 * time.Millisecond, // Slow enough to allow concurrent attempts
	}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures:         2,
		OpenTimeout:         100 * time.Millisecond,
		HalfOpenMaxRequests: 1, // Only 1 test request allowed
	})

	// Trip circuit
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	// Wait for transition to Half-Open
	time.Sleep(150 * time.Millisecond)

	// Clear error for test request
	mock.setSendError(nil)

	// Act - try multiple concurrent requests in Half-Open
	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	var circuitOpenCount atomic.Int32
	var successCount atomic.Int32

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			err := cb.Send(context.Background(), []byte("test"))
			if errors.Is(err, ErrCircuitOpen) {
				circuitOpenCount.Add(1)
			} else if err == nil {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// Assert - only HalfOpenMaxRequests should succeed, rest rejected
	assert.Equal(t, int32(1), successCount.Load(), "Only 1 test request should succeed")
	assert.Greater(t, circuitOpenCount.Load(), int32(0), "Other requests should be rejected")
}

func TestCircuitBreaker_ThreadSafety(t *testing.T) {
	// This test is designed to be run with -race flag: go test -race
	// It stresses the circuit breaker with mixed operations to detect race conditions

	// Arrange
	mock := &mockTransport{
		receiveMsg: []byte("response"),
	}

	var failureCount atomic.Int32

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 10,
		OpenTimeout: 50 * time.Millisecond,
		OnFailure: func(err error) {
			failureCount.Add(1)
		},
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - mixed concurrent operations
	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // Send, Receive, and State checks

	// Concurrent sends
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			if idx%3 == 0 {
				mock.setSendError(errors.New("fail"))
			} else {
				mock.setSendError(nil)
			}
			cb.Send(context.Background(), []byte("test"))
		}(i)
	}

	// Concurrent receives
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			if idx%3 == 0 {
				mock.setReceiveError(errors.New("fail"))
			} else {
				mock.setReceiveError(nil)
			}
			cb.Receive(context.Background())
		}(i)
	}

	// Concurrent state/metrics reads
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = cbTransport.State()
			_ = cbTransport.Metrics()
		}()
	}

	wg.Wait()

	// Assert - no races detected (by -race flag), operations completed
	assert.True(t, true, "If we reach here without race detector panic, test passes")
}

// =============================================================================
// 3. Error Handling Tests (6 tests)
// =============================================================================

func TestCircuitBreaker_ShouldTrip_ContextCanceled(t *testing.T) {
	// Arrange
	mock := &mockTransport{}

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Configure mock to return context.Canceled
	mock.setSendError(context.Canceled)

	// Act - send with canceled context (should NOT trip circuit)
	for i := 0; i < 5; i++ {
		cb.Send(context.Background(), []byte("test"))
	}

	// Assert - circuit should remain closed (context errors don't count)
	assert.Equal(t, StateClosed, cbTransport.State())
	metrics := cbTransport.Metrics()
	assert.Equal(t, 0, metrics.ConsecutiveFailures)
}

func TestCircuitBreaker_ShouldTrip_TransportError(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("connection failed")

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Configure mock to return transport error
	mock.setSendError(transportErr)

	// Act - send with transport error (should trip circuit)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	// Assert - circuit should open
	assert.Equal(t, StateOpen, cbTransport.State())
}

func TestCircuitBreaker_CustomShouldTrip(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	validationErr := errors.New("validation error")
	networkErr := errors.New("network error")

	var trippingErrors []error
	var trippingMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
		ShouldTrip: func(err error) bool {
			// Custom logic: only network errors trip the circuit
			if errors.Is(err, networkErr) {
				return true
			}
			return false
		},
		OnFailure: func(err error) {
			trippingMu.Lock()
			trippingErrors = append(trippingErrors, err)
			trippingMu.Unlock()
		},
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - send validation errors (should NOT trip)
	mock.setSendError(validationErr)
	for i := 0; i < 5; i++ {
		cb.Send(context.Background(), []byte("test"))
	}
	assert.Equal(t, StateClosed, cbTransport.State())

	// Send network errors (should trip)
	mock.setSendError(networkErr)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	time.Sleep(50 * time.Millisecond)

	// Assert
	assert.Equal(t, StateOpen, cbTransport.State())
	trippingMu.Lock()
	assert.Len(t, trippingErrors, 2)
	trippingMu.Unlock()
}

func TestCircuitBreaker_ErrCircuitOpen(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 1 * time.Second,
	})

	// Act - trip circuit
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	// Try operations while circuit is open
	sendErr := cb.Send(context.Background(), []byte("test"))
	receiveData, receiveErr := cb.Receive(context.Background())

	// Assert - should return ErrCircuitOpen
	assert.Equal(t, ErrCircuitOpen, sendErr)
	assert.Equal(t, ErrCircuitOpen, receiveErr)
	assert.Nil(t, receiveData)

	// Verify error can be checked with errors.Is
	assert.True(t, errors.Is(sendErr, ErrCircuitOpen))
}

func TestCircuitBreaker_StartFailureNotCounted(t *testing.T) {
	// Arrange
	mock := &mockTransport{
		startErr: errors.New("start failed"),
	}

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - Start failures should not affect circuit breaker state
	for i := 0; i < 10; i++ {
		err := cb.Start(context.Background())
		assert.Error(t, err)
	}

	// Assert - circuit should remain closed (Start not monitored)
	assert.Equal(t, StateClosed, cbTransport.State())
	metrics := cbTransport.Metrics()
	assert.Equal(t, 0, metrics.ConsecutiveFailures)
}

func TestCircuitBreaker_NonTrippingErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "context.Canceled",
			err:  context.Canceled,
		},
		{
			name: "context.DeadlineExceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "ErrNotInitialized",
			err:  ErrNotInitialized,
		},
		{
			name: "ErrAlreadyInit",
			err:  ErrAlreadyInit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mock := &mockTransport{}
			mock.setSendError(tt.err)

			cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
				MaxFailures: 2,
				OpenTimeout: 100 * time.Millisecond,
			})

			cbTransport := cb.(*circuitBreakerTransport)

			// Act - send errors that shouldn't trip circuit
			for i := 0; i < 10; i++ {
				cb.Send(context.Background(), []byte("test"))
			}

			// Assert - circuit should remain closed
			assert.Equal(t, StateClosed, cbTransport.State())
			metrics := cbTransport.Metrics()
			assert.Equal(t, 0, metrics.ConsecutiveFailures)
		})
	}
}

// =============================================================================
// 4. Configuration Tests (3 tests)
// =============================================================================

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	// Arrange
	mock := &mockTransport{}

	// Act - create with zero/default config
	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{})
	cbTransport := cb.(*circuitBreakerTransport)

	// Assert - defaults applied
	assert.Equal(t, 5, cbTransport.cfg.MaxFailures)
	assert.Equal(t, 30*time.Second, cbTransport.cfg.OpenTimeout)
	assert.Equal(t, 1, cbTransport.cfg.HalfOpenMaxRequests)
	assert.NotNil(t, cbTransport.cfg.ShouldTrip)

	// Verify default works correctly
	mock.setSendError(errors.New("error"))
	for i := 0; i < 5; i++ {
		cb.Send(context.Background(), []byte("test"))
	}
	assert.Equal(t, StateOpen, cbTransport.State())
}

func TestCircuitBreaker_CustomConfig(t *testing.T) {
	// Arrange
	customShouldTrip := func(err error) bool {
		return err != nil && err.Error() == "critical"
	}

	var onStateChangeCalled atomic.Bool
	var onFailureCalled atomic.Bool

	cfg := CircuitBreakerConfig{
		MaxFailures:         10,
		OpenTimeout:         5 * time.Second,
		HalfOpenMaxRequests: 3,
		ShouldTrip:          customShouldTrip,
		OnStateChange: func(from, to CircuitState) {
			onStateChangeCalled.Store(true)
		},
		OnFailure: func(err error) {
			onFailureCalled.Store(true)
		},
	}

	mock := &mockTransport{}

	// Act
	cb := WithCircuitBreaker(mock, cfg)
	cbTransport := cb.(*circuitBreakerTransport)

	// Assert - custom values preserved
	assert.Equal(t, 10, cbTransport.cfg.MaxFailures)
	assert.Equal(t, 5*time.Second, cbTransport.cfg.OpenTimeout)
	assert.Equal(t, 3, cbTransport.cfg.HalfOpenMaxRequests)

	// Test custom ShouldTrip
	mock.setSendError(errors.New("non-critical"))
	for i := 0; i < 15; i++ {
		cb.Send(context.Background(), []byte("test"))
	}
	assert.Equal(t, StateClosed, cbTransport.State(), "Non-critical errors should not trip")

	mock.setSendError(errors.New("critical"))
	for i := 0; i < 10; i++ {
		cb.Send(context.Background(), []byte("test"))
	}
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, StateOpen, cbTransport.State(), "Critical errors should trip")

	// Verify callbacks were called
	assert.True(t, onStateChangeCalled.Load())
	assert.True(t, onFailureCalled.Load())
}

func TestCircuitBreaker_HalfOpenMaxRequests(t *testing.T) {
	tests := []struct {
		name            string
		maxRequests     int
		wantMaxRequests int
	}{
		{
			name:            "default (zero becomes 1)",
			maxRequests:     0,
			wantMaxRequests: 1,
		},
		{
			name:            "negative becomes 1",
			maxRequests:     -5,
			wantMaxRequests: 1,
		},
		{
			name:            "custom value preserved",
			maxRequests:     5,
			wantMaxRequests: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mock := &mockTransport{
				sendDelay: 30 * time.Millisecond,
			}
			mock.setSendError(errors.New("error"))

			cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
				MaxFailures:         2,
				OpenTimeout:         50 * time.Millisecond,
				HalfOpenMaxRequests: tt.maxRequests,
			})

			cbTransport := cb.(*circuitBreakerTransport)

			// Trip circuit
			cb.Send(context.Background(), []byte("test"))
			cb.Send(context.Background(), []byte("test"))

			// Wait for Half-Open
			time.Sleep(100 * time.Millisecond)
			mock.setSendError(nil)

			// Act - try multiple concurrent requests
			var wg sync.WaitGroup
			const attempts = 10
			wg.Add(attempts)

			var successCount atomic.Int32

			for i := 0; i < attempts; i++ {
				go func() {
					defer wg.Done()
					err := cb.Send(context.Background(), []byte("test"))
					if err == nil {
						successCount.Add(1)
					}
				}()
			}

			wg.Wait()

			// Assert - only configured number should succeed
			assert.Equal(t, int32(tt.wantMaxRequests), successCount.Load())
			assert.Equal(t, StateClosed, cbTransport.State())
		})
	}
}

// =============================================================================
// 5. Observability Tests (3 tests)
// =============================================================================

func TestCircuitBreaker_OnStateChangeCallback(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")

	type stateTransition struct {
		from CircuitState
		to   CircuitState
	}

	var transitions []stateTransition
	var transitionMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 50 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			transitionMu.Lock()
			transitions = append(transitions, stateTransition{from: from, to: to})
			transitionMu.Unlock()
		},
	})

	// Act - trigger state transitions
	mock.setSendError(transportErr)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	time.Sleep(100 * time.Millisecond)
	mock.setSendError(nil)
	cb.Send(context.Background(), []byte("test"))

	time.Sleep(50 * time.Millisecond)

	// Assert - verify all transitions captured
	transitionMu.Lock()
	defer transitionMu.Unlock()

	// Callbacks are async, so we check for required transitions
	require.GreaterOrEqual(t, len(transitions), 3)

	// Verify transition types occurred
	hasClosedToOpen := false
	hasOpenToHalfOpen := false
	hasHalfOpenToClosed := false

	for _, tr := range transitions {
		if tr.from == StateClosed && tr.to == StateOpen {
			hasClosedToOpen = true
		}
		if tr.from == StateOpen && tr.to == StateHalfOpen {
			hasOpenToHalfOpen = true
		}
		if tr.from == StateHalfOpen && tr.to == StateClosed {
			hasHalfOpenToClosed = true
		}
	}

	assert.True(t, hasClosedToOpen, "Should have Closed→Open transition")
	assert.True(t, hasOpenToHalfOpen, "Should have Open→Half-Open transition")
	assert.True(t, hasHalfOpenToClosed, "Should have Half-Open→Closed transition")
}

func TestCircuitBreaker_OnFailureCallback(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	error1 := errors.New("error 1")
	error2 := errors.New("error 2")

	var failures []error
	var failureMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 5,
		OpenTimeout: 100 * time.Millisecond,
		OnFailure: func(err error) {
			failureMu.Lock()
			failures = append(failures, err)
			failureMu.Unlock()
		},
	})

	// Act - trigger failures
	mock.setSendError(error1)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	mock.setSendError(error2)
	cb.Send(context.Background(), []byte("test"))

	time.Sleep(50 * time.Millisecond)

	// Assert - all failures captured
	// Note: callbacks are async, so order may vary
	failureMu.Lock()
	defer failureMu.Unlock()

	require.Len(t, failures, 3)

	// Count error types
	error1Count := 0
	error2Count := 0
	for _, err := range failures {
		if err == error1 {
			error1Count++
		} else if err == error2 {
			error2Count++
		}
	}

	assert.Equal(t, 2, error1Count, "Should have 2 occurrences of error1")
	assert.Equal(t, 1, error2Count, "Should have 1 occurrence of error2")
}

func TestCircuitBreaker_Metrics(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 3,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act & Assert - verify metrics at each state

	// Initial state
	metrics := cbTransport.Metrics()
	assert.Equal(t, StateClosed, metrics.State)
	assert.Equal(t, 0, metrics.ConsecutiveFailures)
	assert.Equal(t, 0, metrics.HalfOpenRequests)
	assert.True(t, metrics.LastFailureTime.IsZero())

	// After 2 failures (still closed)
	mock.setSendError(transportErr)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	metrics = cbTransport.Metrics()
	assert.Equal(t, StateClosed, metrics.State)
	assert.Equal(t, 2, metrics.ConsecutiveFailures)
	assert.False(t, metrics.LastFailureTime.IsZero())

	// After 3rd failure (open)
	cb.Send(context.Background(), []byte("test"))
	metrics = cbTransport.Metrics()
	assert.Equal(t, StateOpen, metrics.State)
	assert.Equal(t, 3, metrics.ConsecutiveFailures)

	// Wait for Half-Open
	time.Sleep(150 * time.Millisecond)
	mock.setSendError(nil)

	// Start a request (Half-Open)
	go cb.Send(context.Background(), []byte("test"))
	time.Sleep(10 * time.Millisecond) // Let it enter Half-Open

	// Metrics might show Half-Open or Closed depending on timing
	metrics = cbTransport.Metrics()
	assert.Contains(t, []CircuitState{StateHalfOpen, StateClosed}, metrics.State)
}

// =============================================================================
// 6. Integration Tests (2 tests)
// =============================================================================

func TestCircuitBreaker_WithMockTransport(t *testing.T) {
	// Arrange
	mock := &mockTransport{
		receiveMsg: []byte(`{"jsonrpc":"2.0","result":true,"id":1}`),
	}

	var stateChanges []CircuitState
	var stateChangeMu sync.Mutex

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 3,
		OpenTimeout: 100 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			stateChangeMu.Lock()
			stateChanges = append(stateChanges, to)
			stateChangeMu.Unlock()
		},
	})

	ctx := context.Background()

	// Act - simulate realistic usage pattern

	// 1. Successful operations
	err := cb.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"test","id":1}`))
	require.NoError(t, err)

	data, err := cb.Receive(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// 2. Failures trip circuit
	mock.setSendError(errors.New("network error"))
	for i := 0; i < 3; i++ {
		cb.Send(ctx, []byte("test"))
	}

	// 3. Requests rejected while open
	err = cb.Send(ctx, []byte("test"))
	assert.Equal(t, ErrCircuitOpen, err)

	// 4. Recovery after timeout
	time.Sleep(150 * time.Millisecond)
	mock.setSendError(nil)
	err = cb.Send(ctx, []byte("test"))
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// Assert
	stateChangeMu.Lock()
	assert.Contains(t, stateChanges, StateOpen)
	assert.Contains(t, stateChanges, StateHalfOpen)
	assert.Contains(t, stateChanges, StateClosed)
	stateChangeMu.Unlock()
}

func TestCircuitBreaker_AllTransportMethods(t *testing.T) {
	// Arrange
	mock := &mockTransport{
		receiveMsg: []byte("response"),
	}

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 2,
		OpenTimeout: 100 * time.Millisecond,
	})

	ctx := context.Background()

	// Act & Assert - test all Transport interface methods

	// Start (not monitored by circuit breaker)
	err := cb.Start(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, mock.startCalls)

	// Send (monitored)
	err = cb.Send(ctx, []byte("message"))
	assert.NoError(t, err)
	sendCalls, _ := mock.getCalls()
	assert.Equal(t, 1, sendCalls)

	// Receive (monitored)
	data, err := cb.Receive(ctx)
	assert.NoError(t, err)
	assert.Equal(t, []byte("response"), data)
	_, receiveCalls := mock.getCalls()
	assert.Equal(t, 1, receiveCalls)

	// Close
	err = cb.Close()
	assert.NoError(t, err)
	assert.Equal(t, 1, mock.closeCalls)

	// Verify circuit breaker wrapped all methods correctly
	cbTransport, ok := cb.(*circuitBreakerTransport)
	require.True(t, ok)
	assert.Equal(t, StateClosed, cbTransport.State())
}

// =============================================================================
// Additional Edge Case Tests
// =============================================================================

func TestCircuitBreaker_StateString(t *testing.T) {
	tests := []struct {
		state CircuitState
		want  string
	}{
		{StateClosed, "Closed"},
		{StateOpen, "Open"},
		{StateHalfOpen, "Half-Open"},
		{CircuitState(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.String())
		})
	}
}

func TestCircuitBreaker_ZeroConsecutiveFailuresOnSuccess(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("error")

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures: 5,
		OpenTimeout: 100 * time.Millisecond,
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Act - alternate failures and successes
	mock.setSendError(transportErr)
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	metrics := cbTransport.Metrics()
	assert.Equal(t, 3, metrics.ConsecutiveFailures)

	// Success should reset
	mock.setSendError(nil)
	cb.Send(context.Background(), []byte("test"))

	// Assert - consecutive failures reset to 0
	metrics = cbTransport.Metrics()
	assert.Equal(t, 0, metrics.ConsecutiveFailures)
	assert.Equal(t, StateClosed, metrics.State)
}

func TestCircuitBreaker_NilCallbacksAllowed(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("error")
	mock.setSendError(transportErr)

	// Act - create with nil callbacks (should not panic)
	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures:   2,
		OpenTimeout:   100 * time.Millisecond,
		OnStateChange: nil,
		OnFailure:     nil,
	})

	// Trigger state changes and failures
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	cbTransport := cb.(*circuitBreakerTransport)

	// Assert - should work without callbacks
	assert.Equal(t, StateOpen, cbTransport.State())
}

func TestCircuitBreaker_HalfOpen_NonTrippingError(t *testing.T) {
	// Arrange
	mock := &mockTransport{}
	transportErr := errors.New("transport error")
	mock.setSendError(transportErr)

	cb := WithCircuitBreaker(mock, CircuitBreakerConfig{
		MaxFailures:         2,
		OpenTimeout:         100 * time.Millisecond,
		HalfOpenMaxRequests: 3, // Allow 3 test requests in Half-Open
	})

	cbTransport := cb.(*circuitBreakerTransport)

	// Trip circuit to Open state
	cb.Send(context.Background(), []byte("test"))
	cb.Send(context.Background(), []byte("test"))

	assert.Equal(t, StateOpen, cbTransport.State())

	// Wait for OpenTimeout to elapse
	time.Sleep(150 * time.Millisecond)

	// Circuit transitions to Half-Open only when next request arrives
	// Return non-tripping error (context canceled)
	mock.setSendError(context.Canceled)

	// Act - send request with non-tripping error, this triggers Half-Open transition
	err := cb.Send(context.Background(), []byte("test"))

	// Assert - should return the context.Canceled error
	assert.Equal(t, context.Canceled, err)

	// Circuit should be in Half-Open after first request post-timeout
	assert.Equal(t, StateHalfOpen, cbTransport.State())

	// Circuit should REMAIN Half-Open on non-tripping error, not transition to Closed
	// Send another non-tripping error
	mock.setSendError(context.Canceled)
	err = cb.Send(context.Background(), []byte("test"))
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, StateHalfOpen, cbTransport.State())

	// Now send successful request
	mock.setSendError(nil)
	err = cb.Send(context.Background(), []byte("test"))

	// Assert - successful request should close circuit
	assert.NoError(t, err)
	assert.Equal(t, StateClosed, cbTransport.State())
}
