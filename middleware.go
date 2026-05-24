package finemcp

// Middleware wraps a ToolHandler, adding cross-cutting behaviour
// (logging, recovery, auth, etc.) without modifying the handler itself.
//
// Middleware is applied in registration order: the first middleware
// added via Server.Use is the outermost wrapper (executes first on
// the way in, last on the way out).
//
//	server.Use(logging, recovery)
//	// call order: logging → recovery → handler → recovery → logging
type Middleware func(ToolHandler) ToolHandler

// Use appends one or more middleware to the server's chain.
// Middleware are applied in the order they are added.
// Must be called before serving requests; not safe for concurrent use with CallTool.
func (s *Server) Use(mw ...Middleware) {
	s.middleware = append(s.middleware, mw...)
}

// buildChain wraps a handler with the server's middleware stack.
// Returns the original handler if no middleware is registered.
func (s *Server) buildChain(h ToolHandler) ToolHandler {
	// Apply in reverse so that middleware[0] is the outermost wrapper.
	for i := len(s.middleware) - 1; i >= 0; i-- {
		h = s.middleware[i](h)
	}
	return h
}
