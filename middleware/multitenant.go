package middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/finemcp/finemcp"
)

// ── Errors ──────────────────────────────────────────────────────────

var (
	// ErrTenantNotFound is returned by a TenantStore when the given tenant ID
	// does not match any known tenant.
	ErrTenantNotFound = errors.New("tenant not found")

	// ErrTenantRequired is returned when a tenant ID could not be extracted
	// from the request context (missing header, empty auth subject, etc.).
	ErrTenantRequired = errors.New("tenant identification required")
)

// maxTenantIDLength is the maximum allowed length of a tenant ID.
// Longer IDs are rejected to prevent denial-of-service via oversized strings
// in log entries, map keys, and downstream systems.
const maxTenantIDLength = 256

// ── TenantExtractor ─────────────────────────────────────────────────

// TenantExtractor extracts a tenant identifier from the request context.
// Returns "" if no tenant can be determined (e.g. missing auth info).
//
// Built-in extractors: [TenantFromAuthSubject], [TenantFromAuthMeta].
type TenantExtractor func(ctx context.Context) string

// TenantFromAuthSubject returns an extractor that uses AuthInfo.Subject as
// the tenant ID. This is suitable when each authenticated subject maps 1:1
// to a tenant.
//
// Returns "" if no AuthInfo is in the context.
func TenantFromAuthSubject() TenantExtractor {
	return func(ctx context.Context) string {
		info := finemcp.AuthInfoFromCtx(ctx)
		if info == nil {
			return ""
		}
		return info.Subject
	}
}

// TenantFromAuthMeta returns an extractor that reads a specific key from
// AuthInfo.Meta as the tenant ID. This is useful when the tenant is conveyed
// as a JWT claim or token metadata field rather than the subject itself.
//
// Returns "" if no AuthInfo is in the context, or the key is absent, or the
// value is not a string.
//
// Panics if key is empty (consistent with codebase conventions).
func TenantFromAuthMeta(key string) TenantExtractor {
	if key == "" {
		panic("middleware: TenantFromAuthMeta requires a non-empty key")
	}
	return func(ctx context.Context) string {
		info := finemcp.AuthInfoFromCtx(ctx)
		if info == nil || info.Meta == nil {
			return ""
		}
		v, ok := info.Meta[key].(string)
		if !ok {
			return ""
		}
		return v
	}
}

// ── TenantConfig ────────────────────────────────────────────────────

// TenantConfig defines what a specific tenant can access.
// Filter functions return true to allow the item, false to hide/deny it.
// A nil filter means "allow all" for that item type.
//
// Filter functions must be safe for concurrent use from multiple goroutines.
// They must not depend on external mutable state that may change after
// construction. The framework calls them on every request from the dispatch
// goroutine pool without additional synchronization.
//
// Performance: filter functions are invoked once per item per list request.
// Keep them O(1) — prefer map lookups or set membership tests over linear
// scans, network calls, or other high-latency operations.
type TenantConfig struct {
	// ToolFilter returns true if the tenant can see/call this tool.
	// nil = all tools visible.
	ToolFilter func(*finemcp.Tool) bool

	// ResourceFilter returns true if the tenant can see/read this resource.
	// nil = all resources visible.
	ResourceFilter func(*finemcp.Resource) bool

	// ResourceTemplateFilter returns true if the tenant can see/use this template.
	// nil = all resource templates visible.
	ResourceTemplateFilter func(*finemcp.ResourceTemplate) bool

	// PromptFilter returns true if the tenant can see/get this prompt.
	// nil = all prompts visible.
	PromptFilter func(*finemcp.Prompt) bool

	// Metadata holds arbitrary tenant-specific data (plan tier, quotas, etc.).
	// Read-only after construction. Not used by the framework itself but
	// available for custom middleware via TenantConfigFromCtx (if stored).
	Metadata map[string]any
}

// toItemFilter converts a TenantConfig to an ItemFilter suitable for the
// dispatch layer. nil-valued filter fields are preserved (allow-all semantics
// are handled by ItemFilter.Allow* methods).
func (tc *TenantConfig) toItemFilter() *finemcp.ItemFilter {
	if tc == nil {
		return nil
	}
	return &finemcp.ItemFilter{
		Tool:             tc.ToolFilter,
		Resource:         tc.ResourceFilter,
		ResourceTemplate: tc.ResourceTemplateFilter,
		Prompt:           tc.PromptFilter,
	}
}

// ── TenantStore ─────────────────────────────────────────────────────

// TenantStore retrieves tenant configuration by ID.
// Implementations must be safe for concurrent use from multiple goroutines.
//
// Lookup should return [ErrTenantNotFound] when the tenant ID is unknown.
// Other errors (database failures, timeouts) should be returned as-is;
// the framework scrubs them to a generic message before sending to the client.
//
// For production use, consider implementing rate limiting or caching in the
// Lookup path to mitigate brute-force tenant ID enumeration attacks.
type TenantStore interface {
	Lookup(ctx context.Context, tenantID string) (*TenantConfig, error)
}

// ── StaticTenantStore ───────────────────────────────────────────────

// StaticTenantStore is a TenantStore backed by an in-memory map.
// The map is defensively copied at construction time; later mutations
// to the original map have no effect. Thread-safe for concurrent reads
// (read-only after construction).
type StaticTenantStore struct {
	configs map[string]*TenantConfig
}

// NewStaticTenantStore creates a StaticTenantStore from the given config map.
// The map and its values are defensively copied at construction time.
//
// Panics if configs is nil (use an empty map for a store with no tenants).
func NewStaticTenantStore(configs map[string]*TenantConfig) *StaticTenantStore {
	if configs == nil {
		panic("middleware: NewStaticTenantStore requires a non-nil configs map")
	}
	cp := make(map[string]*TenantConfig, len(configs))
	for k, v := range configs {
		cp[k] = deepCopyConfig(v)
	}
	return &StaticTenantStore{configs: cp}
}

// deepCopyConfig returns a deep copy of a TenantConfig. Filter functions
// (which are immutable closures) are shared; only the Metadata map is cloned
// to prevent post-construction mutation from bypassing tenant isolation.
func deepCopyConfig(tc *TenantConfig) *TenantConfig {
	if tc == nil {
		return nil
	}
	out := *tc // shallow struct copy — shares func pointers (intentional)
	if tc.Metadata != nil {
		out.Metadata = make(map[string]any, len(tc.Metadata))
		for k, v := range tc.Metadata {
			out.Metadata[k] = v
		}
	}
	return &out
}

// Lookup returns the TenantConfig for the given tenant ID.
// Returns [ErrTenantNotFound] if the tenant ID is not in the store.
func (s *StaticTenantStore) Lookup(_ context.Context, tenantID string) (*TenantConfig, error) {
	cfg, ok := s.configs[tenantID]
	if !ok {
		return nil, ErrTenantNotFound
	}
	return cfg, nil
}

// ── NewTenantResolver builder ───────────────────────────────────────

// MultiTenantOption configures the tenant resolver built by [NewTenantResolver].
type MultiTenantOption func(*multiTenantConfig)

type multiTenantConfig struct {
	fallbackTenant string // fallback tenant ID; "" = reject when no tenant found
}

// WithFallbackTenant sets a fallback tenant ID used when the extractor
// returns "". By default (no WithFallbackTenant), requests without a
// resolvable tenant ID are rejected with ErrCodeTenantRequired.
//
// Security note: when a fallback is configured, requests with missing
// credentials silently proceed under the fallback tenant's permissions.
// Ensure the fallback tenant's config is appropriately restrictive.
func WithFallbackTenant(id string) MultiTenantOption {
	return func(cfg *multiTenantConfig) {
		cfg.fallbackTenant = id
	}
}

// NewTenantResolver creates a [finemcp.TenantResolver] from an extractor and store.
//
// The returned resolver:
//  1. Calls extractor(ctx) to get the tenant ID.
//  2. Falls back to the configured fallback tenant if the ID is empty.
//  3. Rejects with an error if no tenant ID is available.
//  4. Calls store.Lookup(ctx, tenantID) to get the TenantConfig.
//  5. Converts TenantConfig filters to a [finemcp.ItemFilter].
//  6. Injects the tenant ID into context via [finemcp.WithTenantID].
//
// The resolver is safe for concurrent use (it delegates to the store and
// extractor, which must themselves be concurrent-safe).
//
// Usage:
//
//	server.SetTenantResolver(middleware.NewTenantResolver(
//	    middleware.TenantFromAuthSubject(),
//	    middleware.NewStaticTenantStore(configs),
//	))
func NewTenantResolver(extractor TenantExtractor, store TenantStore, opts ...MultiTenantOption) finemcp.TenantResolver {
	if extractor == nil {
		panic("middleware: NewTenantResolver requires a non-nil extractor")
	}
	if store == nil {
		panic("middleware: NewTenantResolver requires a non-nil store")
	}

	cfg := multiTenantConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx context.Context) (*finemcp.ItemFilter, error) {
		tenantID := extractor(ctx)

		// Fallback to configured default when extractor returns empty.
		if tenantID == "" {
			tenantID = cfg.fallbackTenant
		}

		if tenantID == "" {
			return nil, ErrTenantRequired
		}

		if len(tenantID) > maxTenantIDLength {
			return nil, ErrTenantRequired
		}

		tc, err := store.Lookup(ctx, tenantID)
		if err != nil {
			// Wrap to ensure the dispatch layer can identify the failure.
			// The actual error message is scrubbed by handleRequest.
			return nil, fmt.Errorf("tenant lookup: %w", err)
		}

		filter := tc.toItemFilter()
		if filter == nil {
			filter = &finemcp.ItemFilter{}
		}
		filter.TenantID = tenantID
		return filter, nil
	}
}
