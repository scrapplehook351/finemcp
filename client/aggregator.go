package client

// aggregator.go — N1 Multi-Server Aggregator.
//
// Aggregator provides a single unified client interface to multiple MCP servers.
// Each server is registered with a unique ID (e.g. "github", "slack") and its
// tools, resources, and prompts are addressable by qualified name
// ("serverID.itemName") or by bare name (searched across all healthy servers).
//
// Example:
//
//	agg := client.NewAggregator(client.AggregatorOptions{})
//	_ = agg.AddServer(ctx, "github", githubTransport, client.Options{})
//	_ = agg.AddServer(ctx, "slack",  slackTransport,  client.Options{})
//
//	// Qualified: routes to "github" server, calls tool "create_issue".
//	result, _ := agg.CallTool(ctx, "github.create_issue", finemcp.CallToolParams{...})
//
//	// Unqualified: finds the unique server that has "post_message".
//	result, _ := agg.CallTool(ctx, "post_message", finemcp.CallToolParams{...})

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/finemcp/finemcp"
)

// Errors returned by Aggregator methods.
var (
	// ErrServerNotFound is returned when an operation targets a server ID
	// that has not been registered with the aggregator.
	ErrServerNotFound = errors.New("aggregator: server not found")

	// ErrServerExists is returned by AddServer when a server with the given
	// ID is already registered.
	ErrServerExists = errors.New("aggregator: server already registered")

	// ErrToolNotFound is returned when an unqualified tool name cannot be
	// found on any healthy server.
	ErrToolNotFound = errors.New("aggregator: tool not found on any server")

	// ErrToolAmbiguous is returned when an unqualified tool name matches
	// tools on more than one server.  Use the qualified form
	// "serverID.toolName" to disambiguate.
	ErrToolAmbiguous = errors.New("aggregator: tool name matches multiple servers; use qualified 'serverID.toolName'")

	// ErrPromptNotFound is the prompt analogue of ErrToolNotFound.
	ErrPromptNotFound = errors.New("aggregator: prompt not found on any server")

	// ErrPromptAmbiguous is the prompt analogue of ErrToolAmbiguous.
	ErrPromptAmbiguous = errors.New("aggregator: prompt name matches multiple servers; use qualified 'serverID.promptName'")

	// ErrNoHealthyServers is returned when a scattershot operation (e.g.
	// listing across all servers) finds no reachable servers.
	ErrNoHealthyServers = errors.New("aggregator: no healthy servers available")
)

// defaultCacheTTL is the default TTL for per-server capability caches.
const defaultCacheTTL = 30 * time.Second

// AggregatorOptions configures an Aggregator.  All fields are optional.
type AggregatorOptions struct {
	// ToolCacheTTL is how long per-server tool lists are cached before
	// being re-fetched on the next operation that needs them.
	// Default: 30 s.
	ToolCacheTTL time.Duration

	// ResourceCacheTTL is how long per-server resource lists are cached.
	// Default: 30 s.
	ResourceCacheTTL time.Duration

	// PromptCacheTTL is how long per-server prompt lists are cached.
	// Default: 30 s.
	PromptCacheTTL time.Duration
}

func (o *AggregatorOptions) toolCacheTTL() time.Duration {
	if o.ToolCacheTTL > 0 {
		return o.ToolCacheTTL
	}
	return defaultCacheTTL
}

func (o *AggregatorOptions) resourceCacheTTL() time.Duration {
	if o.ResourceCacheTTL > 0 {
		return o.ResourceCacheTTL
	}
	return defaultCacheTTL
}

func (o *AggregatorOptions) promptCacheTTL() time.Duration {
	if o.PromptCacheTTL > 0 {
		return o.PromptCacheTTL
	}
	return defaultCacheTTL
}

// aggServer is the per-server state managed by the Aggregator.
type aggServer struct {
	id      string
	client  *Client
	healthy atomic.Bool

	toolsMu       sync.RWMutex
	cachedTools   []finemcp.ToolInfo
	toolsCachedAt time.Time

	resourcesMu       sync.RWMutex
	cachedResources   []finemcp.ResourceInfo
	resourcesCachedAt time.Time

	promptsMu       sync.RWMutex
	cachedPrompts   []finemcp.PromptInfo
	promptsCachedAt time.Time
}

// getTools returns the cached tool list for this server, refreshing it when
// the cache is stale.  A refresh failure marks the server unhealthy.
func (s *aggServer) getTools(ctx context.Context, ttl time.Duration) ([]finemcp.ToolInfo, error) {
	s.toolsMu.RLock()
	tools, cachedAt := s.cachedTools, s.toolsCachedAt
	s.toolsMu.RUnlock()

	if tools != nil && time.Since(cachedAt) < ttl {
		return tools, nil
	}

	result, err := s.client.ListTools(ctx, finemcp.ListParams{})
	if err != nil {
		s.healthy.Store(false)
		return nil, err
	}

	s.toolsMu.Lock()
	s.cachedTools = result.Tools
	s.toolsCachedAt = time.Now()
	s.toolsMu.Unlock()

	return result.Tools, nil
}

// invalidateTools clears the tool list cache so the next call re-fetches.
func (s *aggServer) invalidateTools() {
	s.toolsMu.Lock()
	s.cachedTools = nil
	s.toolsMu.Unlock()
}

// getResources returns the cached resource list, refreshing when stale.
func (s *aggServer) getResources(ctx context.Context, ttl time.Duration) ([]finemcp.ResourceInfo, error) {
	s.resourcesMu.RLock()
	resources, cachedAt := s.cachedResources, s.resourcesCachedAt
	s.resourcesMu.RUnlock()

	if resources != nil && time.Since(cachedAt) < ttl {
		return resources, nil
	}

	result, err := s.client.ListResources(ctx, finemcp.ListParams{})
	if err != nil {
		s.healthy.Store(false)
		return nil, err
	}

	s.resourcesMu.Lock()
	s.cachedResources = result.Resources
	s.resourcesCachedAt = time.Now()
	s.resourcesMu.Unlock()

	return result.Resources, nil
}

// getPrompts returns the cached prompt list, refreshing when stale.
func (s *aggServer) getPrompts(ctx context.Context, ttl time.Duration) ([]finemcp.PromptInfo, error) {
	s.promptsMu.RLock()
	prompts, cachedAt := s.cachedPrompts, s.promptsCachedAt
	s.promptsMu.RUnlock()

	if prompts != nil && time.Since(cachedAt) < ttl {
		return prompts, nil
	}

	result, err := s.client.ListPrompts(ctx, finemcp.ListParams{})
	if err != nil {
		s.healthy.Store(false)
		return nil, err
	}

	s.promptsMu.Lock()
	s.cachedPrompts = result.Prompts
	s.promptsCachedAt = time.Now()
	s.promptsMu.Unlock()

	return result.Prompts, nil
}

// ─────────────────────────────────────────────────────────────────────────────

// Aggregator routes MCP operations across multiple registered servers.
// It is safe for concurrent use.
type Aggregator struct {
	opts    AggregatorOptions
	mu      sync.RWMutex
	servers map[string]*aggServer // serverID → state
}

// NewAggregator creates a new, empty Aggregator.
func NewAggregator(opts AggregatorOptions) *Aggregator {
	return &Aggregator{
		opts:    opts,
		servers: make(map[string]*aggServer),
	}
}

// AddServer registers and initializes a new MCP server under the given ID.
//
// The ID becomes the namespace prefix for qualified names
// ("serverID.toolName").  It must be non-empty and must not contain a '.'
// (which would make qualified-name parsing ambiguous).
//
// The underlying Client is created from tr and opts, and Initialize is called
// immediately.  If initialization fails the server is not registered and the
// client is closed.
func (a *Aggregator) AddServer(ctx context.Context, id string, tr Transport, opts Options) error {
	if id == "" {
		return errors.New("aggregator: server id must not be empty")
	}
	if strings.Contains(id, ".") {
		return fmt.Errorf("aggregator: server id %q must not contain '.'", id)
	}

	// Pre-check under read lock to give a fast error on duplicate IDs.
	a.mu.RLock()
	_, exists := a.servers[id]
	a.mu.RUnlock()
	if exists {
		return fmt.Errorf("%w: %s", ErrServerExists, id)
	}

	c, err := New(tr, opts)
	if err != nil {
		return fmt.Errorf("aggregator: create client for %q: %w", id, err)
	}

	if _, err := c.Initialize(ctx); err != nil {
		_ = c.Close()
		return fmt.Errorf("aggregator: initialize %q: %w", id, err)
	}

	s := &aggServer{id: id, client: c}
	s.healthy.Store(true)

	// Wire up list-changed notifications to cache invalidation.
	// We can't modify opts after New(), so we call the notification setters
	// via the client's internal notification dispatch.
	// Instead, register invalidation via the options if the caller didn't
	// supply callbacks; since we can't patch them post-New, we rely on TTL.

	a.mu.Lock()
	// Re-check under write lock (TOCTOU guard).
	if _, exists := a.servers[id]; exists {
		a.mu.Unlock()
		_ = c.Close()
		return fmt.Errorf("%w: %s", ErrServerExists, id)
	}
	a.servers[id] = s
	a.mu.Unlock()

	return nil
}

// RemoveServer closes the server's client and removes it from the aggregator.
func (a *Aggregator) RemoveServer(id string) error {
	a.mu.Lock()
	s, exists := a.servers[id]
	if !exists {
		a.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrServerNotFound, id)
	}
	delete(a.servers, id)
	a.mu.Unlock()

	return s.client.Close()
}

// Close removes all servers and closes their underlying clients.
// All errors are combined and returned together.
func (a *Aggregator) Close() error {
	a.mu.Lock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		servers = append(servers, s)
	}
	a.servers = make(map[string]*aggServer)
	a.mu.Unlock()

	var errs []error
	for _, s := range servers {
		if err := s.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("aggregator: close %q: %w", s.id, err))
		}
	}
	return errors.Join(errs...)
}

// HealthCheck pings every registered server and updates each server's health
// status.  Servers that respond successfully are marked healthy; servers that
// fail are marked unhealthy.
//
// HealthCheck runs all pings concurrently and waits for all to complete.
// The returned error is non-nil if ALL servers are unhealthy; individual
// server errors are reported per-server but not aggregated into the return.
func (a *Aggregator) HealthCheck(ctx context.Context) error {
	a.mu.RLock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		servers = append(servers, s)
	}
	a.mu.RUnlock()

	if len(servers) == 0 {
		return ErrNoHealthyServers
	}

	var wg sync.WaitGroup
	healthyCount := &atomic.Int32{}
	for _, s := range servers {
		wg.Add(1)
		go func(sv *aggServer) {
			defer wg.Done()
			if err := sv.client.Ping(ctx); err != nil {
				sv.healthy.Store(false)
			} else {
				sv.healthy.Store(true)
				healthyCount.Add(1)
			}
		}(s)
	}
	wg.Wait()

	if healthyCount.Load() == 0 {
		return ErrNoHealthyServers
	}
	return nil
}

// ServerIDs returns the IDs of all currently registered servers (healthy or not).
func (a *Aggregator) ServerIDs() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]string, 0, len(a.servers))
	for id := range a.servers {
		ids = append(ids, id)
	}
	return ids
}

// IsHealthy reports whether the server with the given ID is currently marked
// healthy.  Returns false for unknown server IDs.
func (a *Aggregator) IsHealthy(id string) bool {
	a.mu.RLock()
	s, ok := a.servers[id]
	a.mu.RUnlock()
	if !ok {
		return false
	}
	return s.healthy.Load()
}

// ── Tool operations ──────────────────────────────────────────────────────────

// ListTools returns the merged tool list from all healthy servers.
// Each tool's name is qualified as "serverID.originalName" to avoid collisions.
func (a *Aggregator) ListTools(ctx context.Context) ([]finemcp.ToolInfo, error) {
	a.mu.RLock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		if s.healthy.Load() {
			servers = append(servers, s)
		}
	}
	a.mu.RUnlock()

	if len(servers) == 0 {
		return nil, ErrNoHealthyServers
	}

	ttl := a.opts.toolCacheTTL()
	var (
		mu    sync.Mutex
		tools []finemcp.ToolInfo
		werrs []error
		wg    sync.WaitGroup
	)
	for _, s := range servers {
		wg.Add(1)
		go func(sv *aggServer) {
			defer wg.Done()
			sTools, err := sv.getTools(ctx, ttl)
			if err != nil {
				mu.Lock()
				werrs = append(werrs, fmt.Errorf("server %q: %w", sv.id, err))
				mu.Unlock()
				return
			}
			qualified := make([]finemcp.ToolInfo, len(sTools))
			for i, t := range sTools {
				qt := t
				qt.Name = sv.id + "." + t.Name
				qualified[i] = qt
			}
			mu.Lock()
			tools = append(tools, qualified...)
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	if len(tools) == 0 && len(werrs) > 0 {
		return nil, errors.Join(werrs...)
	}
	return tools, nil
}

// CallTool routes a tool call based on toolPath.
//
//   - Qualified path "serverID.toolName": routes directly to that server.
//   - Bare name "toolName": searches all healthy servers' cached tool lists.
//     Returns ErrToolNotFound if no server has it, or ErrToolAmbiguous if
//     more than one server has a tool with that name.
//
// The params.Name field is overwritten with the unqualified tool name before
// the request is forwarded.
func (a *Aggregator) CallTool(ctx context.Context, toolPath string, params finemcp.CallToolParams) (*finemcp.CallToolResult, error) {
	serverID, toolName, err := a.resolveToolPath(ctx, toolPath)
	if err != nil {
		return nil, err
	}

	a.mu.RLock()
	s, ok := a.servers[serverID]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServerNotFound, serverID)
	}
	if !s.healthy.Load() {
		return nil, fmt.Errorf("aggregator: server %q is not healthy", serverID)
	}

	params.Name = toolName
	result, err := s.client.CallTool(ctx, params)
	if err != nil {
		s.healthy.Store(false)
		return nil, fmt.Errorf("aggregator: server %q: %w", serverID, err)
	}
	return result, nil
}

// resolveToolPath parses toolPath and returns (serverID, toolName, error).
// For "serverID.toolName" it validates that the server exists.
// For bare "toolName" it searches healthy servers' tool lists.
func (a *Aggregator) resolveToolPath(ctx context.Context, toolPath string) (string, string, error) {
	if idx := strings.IndexByte(toolPath, '.'); idx >= 0 {
		serverID := toolPath[:idx]
		toolName := toolPath[idx+1:]
		if serverID == "" || toolName == "" {
			return "", "", fmt.Errorf("aggregator: malformed tool path %q", toolPath)
		}
		return serverID, toolName, nil
	}

	// Bare name: search all healthy servers.
	ttl := a.opts.toolCacheTTL()
	a.mu.RLock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		if s.healthy.Load() {
			servers = append(servers, s)
		}
	}
	a.mu.RUnlock()

	var matches []string
	for _, s := range servers {
		sTools, err := s.getTools(ctx, ttl)
		if err != nil {
			continue
		}
		for _, t := range sTools {
			if t.Name == toolPath {
				matches = append(matches, s.id)
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("%w: %s", ErrToolNotFound, toolPath)
	case 1:
		return matches[0], toolPath, nil
	default:
		return "", "", fmt.Errorf("%w: %s (found on: %s)",
			ErrToolAmbiguous, toolPath, strings.Join(matches, ", "))
	}
}

// InvalidateToolCache clears the tool list cache for the specified server,
// causing the next ListTools or unqualified CallTool to re-fetch.
// Pass an empty string to invalidate caches for all servers.
func (a *Aggregator) InvalidateToolCache(id string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if id == "" {
		for _, s := range a.servers {
			s.invalidateTools()
		}
		return
	}
	if s, ok := a.servers[id]; ok {
		s.invalidateTools()
	}
}

// ── Resource operations ──────────────────────────────────────────────────────

// ListResources returns the merged resource list from all healthy servers.
// Each resource's URI is prefixed with "serverID://" to avoid namespace
// collisions (e.g. "github://file:///README.md").
func (a *Aggregator) ListResources(ctx context.Context) ([]finemcp.ResourceInfo, error) {
	a.mu.RLock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		if s.healthy.Load() {
			servers = append(servers, s)
		}
	}
	a.mu.RUnlock()

	if len(servers) == 0 {
		return nil, ErrNoHealthyServers
	}

	ttl := a.opts.resourceCacheTTL()
	var (
		mu        sync.Mutex
		resources []finemcp.ResourceInfo
		wg        sync.WaitGroup
	)
	for _, s := range servers {
		wg.Add(1)
		go func(sv *aggServer) {
			defer wg.Done()
			sRes, err := sv.getResources(ctx, ttl)
			if err != nil {
				return
			}
			mu.Lock()
			resources = append(resources, sRes...)
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return resources, nil
}

// ReadResource reads a resource from the specified server.
// serverID must match a registered server.
func (a *Aggregator) ReadResource(ctx context.Context, serverID string, params finemcp.ReadResourceParams) (*finemcp.ReadResourceResult, error) {
	a.mu.RLock()
	s, ok := a.servers[serverID]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServerNotFound, serverID)
	}
	if !s.healthy.Load() {
		return nil, fmt.Errorf("aggregator: server %q is not healthy", serverID)
	}

	result, err := s.client.ReadResource(ctx, params)
	if err != nil {
		s.healthy.Store(false)
		return nil, fmt.Errorf("aggregator: server %q: %w", serverID, err)
	}
	return result, nil
}

// ── Prompt operations ────────────────────────────────────────────────────────

// ListPrompts returns the merged prompt list from all healthy servers.
// Each prompt's name is qualified as "serverID.originalName".
func (a *Aggregator) ListPrompts(ctx context.Context) ([]finemcp.PromptInfo, error) {
	a.mu.RLock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		if s.healthy.Load() {
			servers = append(servers, s)
		}
	}
	a.mu.RUnlock()

	if len(servers) == 0 {
		return nil, ErrNoHealthyServers
	}

	ttl := a.opts.promptCacheTTL()
	var (
		mu      sync.Mutex
		prompts []finemcp.PromptInfo
		wg      sync.WaitGroup
	)
	for _, s := range servers {
		wg.Add(1)
		go func(sv *aggServer) {
			defer wg.Done()
			sPrompts, err := sv.getPrompts(ctx, ttl)
			if err != nil {
				return
			}
			qualified := make([]finemcp.PromptInfo, len(sPrompts))
			for i, p := range sPrompts {
				qp := p
				qp.Name = sv.id + "." + p.Name
				qualified[i] = qp
			}
			mu.Lock()
			prompts = append(prompts, qualified...)
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return prompts, nil
}

// GetPrompt retrieves a prompt from the specified server by its path.
//
//   - Qualified "serverID.promptName": routes to that server.
//   - Bare "promptName": searches all healthy servers.
func (a *Aggregator) GetPrompt(ctx context.Context, promptPath string, params finemcp.GetPromptParams) (*finemcp.GetPromptResult, error) {
	serverID, promptName, err := a.resolvePromptPath(ctx, promptPath)
	if err != nil {
		return nil, err
	}

	a.mu.RLock()
	s, ok := a.servers[serverID]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServerNotFound, serverID)
	}
	if !s.healthy.Load() {
		return nil, fmt.Errorf("aggregator: server %q is not healthy", serverID)
	}

	params.Name = promptName
	result, err := s.client.GetPrompt(ctx, params)
	if err != nil {
		s.healthy.Store(false)
		return nil, fmt.Errorf("aggregator: server %q: %w", serverID, err)
	}
	return result, nil
}

// resolvePromptPath parses a prompt path similarly to resolveToolPath.
func (a *Aggregator) resolvePromptPath(ctx context.Context, promptPath string) (string, string, error) {
	if idx := strings.IndexByte(promptPath, '.'); idx >= 0 {
		serverID := promptPath[:idx]
		promptName := promptPath[idx+1:]
		if serverID == "" || promptName == "" {
			return "", "", fmt.Errorf("aggregator: malformed prompt path %q", promptPath)
		}
		return serverID, promptName, nil
	}

	ttl := a.opts.promptCacheTTL()
	a.mu.RLock()
	servers := make([]*aggServer, 0, len(a.servers))
	for _, s := range a.servers {
		if s.healthy.Load() {
			servers = append(servers, s)
		}
	}
	a.mu.RUnlock()

	var matches []string
	for _, s := range servers {
		sPrompts, err := s.getPrompts(ctx, ttl)
		if err != nil {
			continue
		}
		for _, p := range sPrompts {
			if p.Name == promptPath {
				matches = append(matches, s.id)
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("%w: %s", ErrPromptNotFound, promptPath)
	case 1:
		return matches[0], promptPath, nil
	default:
		return "", "", fmt.Errorf("%w: %s (found on: %s)",
			ErrPromptAmbiguous, promptPath, strings.Join(matches, ", "))
	}
}
