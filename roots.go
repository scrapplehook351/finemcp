package finemcp

import (
	"errors"
	"sort"
)

var (
	errRootURIEmpty      = errors.New("root URI must not be empty")
	errRootAlreadyExists = errors.New("root already registered")
	errRootNil           = errors.New("root must not be nil")
)

// Root represents a workspace root directory that the server is aware of.
// Roots provide context about the server's workspace boundaries.
type Root struct {
	// URI is the unique identifier for this root (e.g. "file:///workspace/project").
	// Must not be empty.
	URI string

	// Name is an optional human-readable name for the root (e.g. "Project Name").
	// May be empty.
	Name string
}

// RootOption is a functional option for configuring a Root.
type RootOption func(*Root)

// WithRootName sets the root's human-readable name.
func WithRootName(name string) RootOption {
	return func(r *Root) { r.Name = name }
}

// NewRoot creates a new Root with the given URI and options.
func NewRoot(uri string, opts ...RootOption) (*Root, error) {
	if uri == "" {
		return nil, errRootURIEmpty
	}
	r := &Root{URI: uri}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// RegisterRoot adds a root to the server's registry.
// Returns an error if the root is nil, URI is empty, or the URI is already registered.
func (s *Server) RegisterRoot(r *Root) error {
	if r == nil {
		return errRootNil
	}
	if r.URI == "" {
		return errRootURIEmpty
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.roots[r.URI]; exists {
		return errRootAlreadyExists
	}

	s.roots[r.URI] = r
	return nil
}

// ListRoots returns all registered roots, sorted by URI.
func (s *Server) ListRoots() []*Root {
	s.mu.RLock()
	defer s.mu.RUnlock()

	roots := make([]*Root, 0, len(s.roots))
	for _, r := range s.roots {
		roots = append(roots, r)
	}

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].URI < roots[j].URI
	})

	return roots
}
