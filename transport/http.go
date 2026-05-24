package transport

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/finemcp/finemcp"
)

// defaultHTTPMaxBodySize is the default limit for incoming request bodies (4 MB).
const defaultHTTPMaxBodySize int64 = 4 << 20

// Handler returns an http.Handler that implements the MCP Streamable HTTP transport.
// It accepts JSON-RPC 2.0 messages via POST and returns JSON-RPC 2.0 responses.
//
// Use this for:
//   - Embedding into an existing HTTP server: router.Handle("/mcp", transport.Handler(server))
//   - Standalone mode: http.ListenAndServe(":8080", transport.Handler(server))
//   - Testing with curl
//
// Only POST is supported (per MCP spec). Other methods receive 405 Method Not Allowed.
// Content-Type must be application/json.
func Handler(s *finemcp.Server, maxBodySize ...int64) http.Handler {
	limit := defaultHTTPMaxBodySize
	if len(maxBodySize) > 0 && maxBodySize[0] > 0 {
		limit = maxBodySize[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, limit)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		defer func() { _ = r.Body.Close() }()

		if len(body) == 0 {
			http.Error(w, "empty request body", http.StatusBadRequest)
			return
		}

		resp, err := s.HandleMessage(r.Context(), body)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Notifications produce no response — return 204 No Content.
		if resp == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		data, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	})
}

// StartHTTP starts an HTTP server on the given address with the MCP handler.
// This is a convenience for standalone mode. It blocks until the server stops.
//
// Usage:
//
//	s := finemcp.NewServer("myapp", "1.0")
//	s.RegisterTool(myTool)
//	log.Fatal(transport.StartHTTP(s, ":8080"))
func StartHTTP(s *finemcp.Server, addr string) error {
	return http.ListenAndServe(addr, Handler(s)) // #nosec G114 -- convenience function; users needing timeouts should use http.Server directly
}
