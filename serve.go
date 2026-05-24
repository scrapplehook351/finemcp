package finemcp

import (
	"context"
	"errors"
)

// Start runs the server's lifespan hook (if configured) and calls runFn.
// The lifespan hook is called before runFn to initialize shared resources;
// its cleanup function runs after runFn returns.
//
// If no lifespan hook is configured, Start simply calls runFn(ctx).
//
// This is the recommended way to run a transport with lifespan support:
//
//	server := finemcp.NewServer("app", "1.0",
//	    finemcp.WithLifespan(func(ctx context.Context, s *finemcp.Server) (context.Context, func(), error) {
//	        db, err := sql.Open("postgres", dsn)
//	        if err != nil {
//	            return nil, nil, err
//	        }
//	        return context.WithValue(ctx, dbKey, db), func() { db.Close() }, nil
//	    }),
//	)
//	err := server.Start(ctx, func(ctx context.Context) error {
//	    return transport.StartStreamable(ctx, server, ":8080")
//	})
func (s *Server) Start(ctx context.Context, runFn func(ctx context.Context) error) error {
	if ctx == nil {
		return errors.New("finemcp: Server.Start requires a non-nil context")
	}
	if s.lifespan != nil {
		enrichedCtx, cleanup, err := s.lifespan(ctx, s)
		if err != nil {
			return err
		}
		if cleanup != nil {
			defer cleanup()
		}
		if enrichedCtx != nil {
			ctx = enrichedCtx
		}
	}
	return runFn(ctx)
}

// Shutdown gracefully stops the server by waiting for in-flight requests
// to complete. If the context expires before all requests finish,
// it returns the context error.
//
// Usage:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	if err := server.Shutdown(ctx); err != nil {
//	    log.Println("shutdown timed out:", err)
//	}
func (s *Server) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
