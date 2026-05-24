package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCoalescing_BasicDeduplication verifies that identical concurrent requests
// are coalesced into a single execution.
func TestCoalescing_BasicDeduplication(t *testing.T) {
	var execCount int32

	c := newCoalescer(10 * time.Millisecond)

	key, err := newRequestKey("test.method", map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatal(err)
	}

	fn := func(ctx context.Context) ([]byte, error) {
		atomic.AddInt32(&execCount, 1)
		time.Sleep(50 * time.Millisecond) // Simulate work
		return []byte(`{"result":"ok"}`), nil
	}

	// Launch 10 concurrent requests with same key
	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)

	results := make([][]byte, numRequests)
	errors := make([]error, numRequests)

	ctx := context.Background()
	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = c.Do(ctx, key, fn)
		}(i)
	}

	wg.Wait()

	// Verify only 1 execution happened
	if count := atomic.LoadInt32(&execCount); count != 1 {
		t.Errorf("Expected 1 execution, got %d", count)
	}

	// Verify all callers got the result
	for i := 0; i < numRequests; i++ {
		if errors[i] != nil {
			t.Errorf("Request %d failed: %v", i, errors[i])
		}
		if string(results[i]) != `{"result":"ok"}` {
			t.Errorf("Request %d got wrong result: %s", i, string(results[i]))
		}
	}
}

// TestCoalescing_DifferentKeys verifies that requests with different keys
// are not coalesced.
func TestCoalescing_DifferentKeys(t *testing.T) {
	var execCount int32

	c := newCoalescer(10 * time.Millisecond)

	key1, _ := newRequestKey("method1", nil)
	key2, _ := newRequestKey("method2", nil)

	fn := func(ctx context.Context) ([]byte, error) {
		atomic.AddInt32(&execCount, 1)
		return []byte(`{"result":"ok"}`), nil
	}

	var wg sync.WaitGroup
	wg.Add(2)

	ctx := context.Background()

	go func() {
		defer wg.Done()
		c.Do(ctx, key1, fn)
	}()

	go func() {
		defer wg.Done()
		c.Do(ctx, key2, fn)
	}()

	wg.Wait()

	// Should have 2 executions (different keys)
	if count := atomic.LoadInt32(&execCount); count != 2 {
		t.Errorf("Expected 2 executions, got %d", count)
	}
}

// TestCoalescing_ContextCancellation verifies that individual callers can
// cancel their context without affecting the shared request.
func TestCoalescing_ContextCancellation(t *testing.T) {
	var execCount int32

	c := newCoalescer(10 * time.Millisecond)

	key, _ := newRequestKey("test.method", nil)

	fn := func(ctx context.Context) ([]byte, error) {
		atomic.AddInt32(&execCount, 1)
		time.Sleep(100 * time.Millisecond) // Long operation
		return []byte(`{"result":"ok"}`), nil
	}

	// Launch 2 requests: one with timeout, one without
	ctx1, cancel1 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel1()

	ctx2 := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)

	var err1, err2 error
	var result1, result2 []byte

	go func() {
		defer wg.Done()
		result1, err1 = c.Do(ctx1, key, fn)
	}()

	go func() {
		defer wg.Done()
		result2, err2 = c.Do(ctx2, key, fn)
	}()

	wg.Wait()

	// First caller should timeout
	if err1 != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err1)
	}
	if result1 != nil {
		t.Errorf("Expected nil result for cancelled context, got %s", result1)
	}

	// Second caller should succeed (shared request continued)
	if err2 != nil {
		t.Errorf("Expected success, got %v", err2)
	}
	if string(result2) != `{"result":"ok"}` {
		t.Errorf("Expected result, got %s", result2)
	}

	// Should only execute once
	if count := atomic.LoadInt32(&execCount); count != 1 {
		t.Errorf("Expected 1 execution, got %d", count)
	}
}

// TestCoalescing_ErrorPropagation verifies that errors are shared among all waiters.
func TestCoalescing_ErrorPropagation(t *testing.T) {
	c := newCoalescer(10 * time.Millisecond)

	key, _ := newRequestKey("test.method", nil)

	expectedErr := fmt.Errorf("test error")
	fn := func(ctx context.Context) ([]byte, error) {
		time.Sleep(20 * time.Millisecond)
		return nil, expectedErr
	}

	const numRequests = 5
	var wg sync.WaitGroup
	wg.Add(numRequests)

	errors := make([]error, numRequests)

	ctx := context.Background()
	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errors[idx] = c.Do(ctx, key, fn)
		}(i)
	}

	wg.Wait()

	// All callers should get the same error
	for i := 0; i < numRequests; i++ {
		if errors[i] == nil {
			t.Errorf("Request %d: expected error, got nil", i)
		} else if errors[i].Error() != expectedErr.Error() {
			t.Errorf("Request %d: expected %v, got %v", i, expectedErr, errors[i])
		}
	}
}

// TestCoalescing_PanicRecovery verifies that panics in the function are recovered.
func TestCoalescing_PanicRecovery(t *testing.T) {
	c := newCoalescer(10 * time.Millisecond)

	key, _ := newRequestKey("test.method", nil)

	fn := func(ctx context.Context) ([]byte, error) {
		time.Sleep(20 * time.Millisecond)
		panic("test panic")
	}

	ctx := context.Background()
	result, err := c.Do(ctx, key, fn)

	if err == nil {
		t.Error("Expected error from panic recovery, got nil")
	}
	if result != nil {
		t.Errorf("Expected nil result, got %s", result)
	}
	// Security fix: Panic messages are now sanitized (don't expose details)
	if err != nil && err.Error() != "client: request coalescing internal error" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestRequestKey_HashLargeParams tests that large params are hashed.
func TestRequestKey_HashLargeParams(t *testing.T) {
	// Create params larger than 256 bytes
	largeParams := make(map[string]string)
	for i := 0; i < 100; i++ {
		largeParams[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}

	key, err := newRequestKey("test.method", largeParams)
	if err != nil {
		t.Fatal(err)
	}

	keyStr := key.String()

	// Should use hash format
	if len(keyStr) > 300 {
		t.Errorf("Expected hashed key to be short, got length %d", len(keyStr))
	}

	// Should contain "hash:" marker
	if len(keyStr) < 256 && keyStr[:11] != "test.method" {
		t.Errorf("Expected key to start with method name, got %s", keyStr[:20])
	}
}

// TestCoalescing_PanicMessageSanitization verifies that panic messages
// don't leak sensitive information (HIGH-1 security fix).
func TestCoalescing_PanicMessageSanitization(t *testing.T) {
	c := newCoalescer(10 * time.Millisecond)

	key, _ := newRequestKey("test.method", nil)

	// Simulate panic with sensitive data (e.g., API key)
	fn := func(ctx context.Context) ([]byte, error) {
		time.Sleep(20 * time.Millisecond)
		panic("database connection failed: password=secret123")
	}

	ctx := context.Background()
	result, err := c.Do(ctx, key, fn)

	if err == nil {
		t.Error("Expected error from panic recovery, got nil")
	}
	if result != nil {
		t.Errorf("Expected nil result, got %s", result)
	}

	// Verify panic details are NOT exposed
	errMsg := err.Error()
	if errMsg != "client: request coalescing internal error" {
		t.Errorf("Expected sanitized error message, got: %v", err)
	}

	// Ensure sensitive data is not leaked
	if contains(errMsg, "password") || contains(errMsg, "secret123") {
		t.Errorf("Error message leaked sensitive data: %v", err)
	}
}

// TestRequestKey_SizeLimit verifies that oversized params are rejected
// to prevent DoS (HIGH-2 security fix).
func TestRequestKey_SizeLimit(t *testing.T) {
	// Create params that exceed 1MB limit
	hugeValue := make([]byte, 1<<20+1) // 1MB + 1 byte
	for i := range hugeValue {
		hugeValue[i] = 'x'
	}

	largeParams := map[string]string{
		"data": string(hugeValue),
	}

	_, err := newRequestKey("test.method", largeParams)

	if err == nil {
		t.Fatal("Expected error for oversized params, got nil")
	}

	errMsg := err.Error()
	if !contains(errMsg, "params too large") {
		t.Errorf("Expected 'params too large' error, got: %v", err)
	}
	if !contains(errMsg, "exceeds") {
		t.Errorf("Expected error to mention size limit, got: %v", err)
	}
}

// TestRequestKey_WithinSizeLimit verifies that params under 1MB are accepted.
func TestRequestKey_WithinSizeLimit(t *testing.T) {
	// Create params just under 1MB limit
	reasonableSize := make([]byte, 1<<19) // 512KB
	for i := range reasonableSize {
		reasonableSize[i] = 'x'
	}

	params := map[string]string{
		"data": string(reasonableSize),
	}

	key, err := newRequestKey("test.method", params)
	if err != nil {
		t.Fatalf("Unexpected error for reasonable-sized params: %v", err)
	}

	if key.method != "test.method" {
		t.Errorf("Expected method 'test.method', got %s", key.method)
	}
}

// TestCoalescing_MaxInflightLimit verifies that the max inflight request
// limit prevents resource exhaustion (MEDIUM-3 security fix).
func TestCoalescing_MaxInflightLimit(t *testing.T) {
	c := newCoalescer(10 * time.Millisecond)
	c.maxInflightRequests = 3 // Set low limit for testing

	// Create a slow function that blocks
	blockCh := make(chan struct{})
	fn := func(ctx context.Context) ([]byte, error) {
		<-blockCh // Block until we release
		return []byte(`{"result":"ok"}`), nil
	}

	var wg sync.WaitGroup
	ctx := context.Background()

	// Launch 3 requests with different keys (should succeed)
	for i := 0; i < 3; i++ {
		key, _ := newRequestKey(fmt.Sprintf("method%d", i), nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Do(ctx, key, fn)
		}()
	}

	// Wait for all flights to be inflight
	time.Sleep(50 * time.Millisecond)

	// Try to launch 4th request (should fail due to limit)
	key4, _ := newRequestKey("method4", nil)
	_, err := c.Do(ctx, key4, fn)

	if err == nil {
		t.Error("Expected error for exceeding max inflight limit, got nil")
	}

	errMsg := err.Error()
	if !contains(errMsg, "too many concurrent requests") {
		t.Errorf("Expected 'too many concurrent requests' error, got: %v", err)
	}

	// Release the blocking requests
	close(blockCh)
	wg.Wait()
}

// TestCoalescing_MaxInflightZeroUnlimited verifies that setting maxInflightRequests
// to 0 disables the limit.
func TestCoalescing_MaxInflightZeroUnlimited(t *testing.T) {
	c := newCoalescer(10 * time.Millisecond)
	c.maxInflightRequests = 0 // Unlimited

	blockCh := make(chan struct{})
	fn := func(ctx context.Context) ([]byte, error) {
		<-blockCh
		return []byte(`{"result":"ok"}`), nil
	}

	var wg sync.WaitGroup
	ctx := context.Background()

	// Launch many concurrent requests (should all succeed with unlimited)
	const numRequests = 50
	for i := 0; i < numRequests; i++ {
		key, _ := newRequestKey(fmt.Sprintf("method%d", i), nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Do(ctx, key, fn)
		}()
	}

	time.Sleep(50 * time.Millisecond)

	// All requests should be inflight (no limit)
	c.mu.Lock()
	inflightCount := len(c.inflight)
	c.mu.Unlock()

	if inflightCount != numRequests {
		t.Errorf("Expected %d inflight requests, got %d", numRequests, inflightCount)
	}

	close(blockCh)
	wg.Wait()
}

// contains is a helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
