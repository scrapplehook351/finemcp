package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/finemcp/finemcp"
)

// IDGenerator produces unique job IDs for async execution.
// The default uses a monotonic counter prefixed with "job-".
type IDGenerator func() string

// AsyncOption configures the async middleware.
type AsyncOption func(*asyncConfig)

type asyncConfig struct {
	store *finemcp.JobStore
	idGen IDGenerator
	wg    sync.WaitGroup
}

// WithJobStore sets the job store for tracking async jobs.
// Panics if store is nil.
func WithJobStore(store *finemcp.JobStore) AsyncOption {
	if store == nil {
		panic("middleware.Async: WithJobStore: store must not be nil")
	}
	return func(c *asyncConfig) { c.store = store }
}

// WithIDGenerator sets a custom ID generator for async jobs.
// Panics if fn is nil.
func WithIDGenerator(fn IDGenerator) AsyncOption {
	if fn == nil {
		panic("middleware.Async: WithIDGenerator: id generator must not be nil")
	}
	return func(c *asyncConfig) { c.idGen = fn }
}

// defaultIDGen returns a simple monotonic counter-based ID generator.
func defaultIDGen() IDGenerator {
	var counter int64
	return func() string {
		n := atomic.AddInt64(&counter, 1)
		return fmt.Sprintf("job-%d", n)
	}
}

// Async returns a middleware that executes tool handlers asynchronously.
// The tool call returns immediately with a job ID, and the actual execution
// happens in a background goroutine. To query job status, provide a job
// store via [WithJobStore] and read from that [*finemcp.JobStore].
//
// Important: handlers run in detached goroutines with a context
// disconnected from the original request (via [context.WithoutCancel])
// so that HTTP response completion does not cancel background work.
// Consider combining with Sandbox middleware for timeout enforcement.
//
// Panics inside the background goroutine are recovered automatically
// and recorded as job failures, so a misbehaving handler will not
// crash the process.
//
// Use [AsyncWaiter.Wait] on the returned waiter to drain in-flight
// background goroutines during graceful shutdown.
//
// Usage:
//
//	store := finemcp.NewJobStore()
//	mw, waiter := middleware.Async(
//	    middleware.WithJobStore(store),
//	)
//	server.Use(mw)
//	// on shutdown: waiter.Wait()
//
// The tool call returns a JSON object with "jobId" on success.
// Query store.Get(jobId) to check progress.
func Async(opts ...AsyncOption) (finemcp.Middleware, *AsyncWaiter) {
	cfg := &asyncConfig{
		store: finemcp.NewJobStore(),
		idGen: defaultIDGen(),
	}
	for _, o := range opts {
		o(cfg)
	}

	waiter := &AsyncWaiter{wg: &cfg.wg}

	mw := func(next finemcp.ToolHandler) finemcp.ToolHandler {
		return func(ctx context.Context, input []byte) ([]byte, error) {
			toolName := finemcp.ToolName(ctx)
			jobID := cfg.idGen()

			if _, err := cfg.store.Submit(jobID, toolName); err != nil {
				return nil, fmt.Errorf("async: submit job: %w", err)
			}

			// Detach from the request context so the background goroutine
			// is not cancelled when the HTTP response is sent.
			bgCtx := context.WithoutCancel(ctx)

			// Run the handler in a background goroutine.
			cfg.wg.Add(1)
			go func() {
				defer cfg.wg.Done()
				defer func() {
					if r := recover(); r != nil {
						_ = cfg.store.Fail(jobID, fmt.Sprintf("panic: %v", r))
					}
				}()

				if err := cfg.store.MarkRunning(jobID); err != nil {
					// Job was likely cancelled between Submit and MarkRunning.
					return
				}

				out, handlerErr := next(bgCtx, input)
				if handlerErr != nil {
					_ = cfg.store.Fail(jobID, handlerErr.Error())
					return
				}

				result := finemcp.NewTextResult(string(out))
				_ = cfg.store.Complete(jobID, result)
			}()

			// Return the job ID immediately.
			resp := map[string]string{
				"jobId":  jobID,
				"status": string(finemcp.JobStatusPending),
			}
			data, _ := json.Marshal(resp)
			return data, nil
		}
	}

	return mw, waiter
}

// AsyncWaiter provides a mechanism to wait for all in-flight background
// goroutines started by the Async middleware to complete. Use this
// during graceful shutdown to ensure no work is lost.
type AsyncWaiter struct {
	wg *sync.WaitGroup
}

// Wait blocks until all in-flight async handlers have completed.
func (w *AsyncWaiter) Wait() {
	w.wg.Wait()
}
