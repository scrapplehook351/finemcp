package client

import (
	"context"
	"fmt"
	"io"

	"github.com/finemcp/finemcp"
)

// IteratorOptions configures safety limits for pagination iterators.
type IteratorOptions struct {
	// MaxPages limits the number of pages fetched. 0 means unlimited.
	// Defaults to 1000 to prevent unbounded memory growth.
	MaxPages int

	// MaxItems limits the total number of items collected in All(). 0 means unlimited.
	// Defaults to 100,000 to prevent memory exhaustion.
	MaxItems int
}

// DefaultIteratorOptions returns safe default limits for iterators.
func DefaultIteratorOptions() IteratorOptions {
	return IteratorOptions{
		MaxPages: 1000,
		MaxItems: 100_000,
	}
}

// ToolsIterator provides cursor-based iteration over tools.
type ToolsIterator struct {
	client    *Client
	params    finemcp.ListParams
	buffer    []finemcp.ToolInfo
	index     int
	cursor    string
	done      bool
	err       error
	maxPages  int
	maxItems  int
	pageCount int
}

// IterateTools creates a new iterator for listing all tools with automatic pagination.
// Use opts to configure safety limits; if nil, safe defaults are applied.
func (c *Client) IterateTools(params finemcp.ListParams, opts *IteratorOptions) *ToolsIterator {
	if opts == nil {
		defaultOpts := DefaultIteratorOptions()
		opts = &defaultOpts
	}
	return &ToolsIterator{
		client:   c,
		params:   params,
		index:    0,
		maxPages: opts.MaxPages,
		maxItems: opts.MaxItems,
	}
}

// Next returns the next tool. Returns io.EOF when no more tools are available.
func (i *ToolsIterator) Next(ctx context.Context) (*finemcp.ToolInfo, error) {
	if i.err != nil {
		return nil, i.err
	}

	// Fetch next page if buffer is exhausted
	if i.index >= len(i.buffer) {
		if i.done {
			return nil, io.EOF
		}

		// Check page limit
		if i.maxPages > 0 && i.pageCount >= i.maxPages {
			i.err = fmt.Errorf("pagination: max pages limit reached (%d)", i.maxPages)
			return nil, i.err
		}

		// Set cursor for next page
		if i.cursor != "" {
			i.params.Cursor = i.cursor
		}

		result, err := i.client.ListTools(ctx, i.params)
		if err != nil {
			i.err = err
			return nil, err
		}

		i.buffer = result.Tools
		i.index = 0
		i.cursor = result.NextCursor
		i.pageCount++ // Track pages fetched

		// No more pages
		if i.cursor == "" || len(i.buffer) == 0 {
			i.done = true
		}

		if len(i.buffer) == 0 {
			return nil, io.EOF
		}
	}

	tool := &i.buffer[i.index]
	i.index++
	return tool, nil
}

// HasMore returns true if there are more tools to fetch.
func (i *ToolsIterator) HasMore() bool {
	return !i.done || i.index < len(i.buffer)
}

// All fetches all remaining tools into a slice, up to the configured limits.
// Returns an error if MaxPages or MaxItems is exceeded.
func (i *ToolsIterator) All(ctx context.Context) ([]finemcp.ToolInfo, error) {
	var tools []finemcp.ToolInfo
	for {
		// Check item limit before fetching
		if i.maxItems > 0 && len(tools) >= i.maxItems {
			return tools, fmt.Errorf("pagination: max items limit reached (%d)", i.maxItems)
		}

		tool, err := i.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return tools, err
		}
		tools = append(tools, *tool)
	}
	return tools, nil
}

// ResourcesIterator provides cursor-based iteration over resources.
type ResourcesIterator struct {
	client    *Client
	params    finemcp.ListParams
	buffer    []finemcp.ResourceInfo
	index     int
	cursor    string
	done      bool
	err       error
	maxPages  int
	maxItems  int
	pageCount int
}

// IterateResources creates a new iterator for listing all resources with automatic pagination.
// Use opts to configure safety limits; if nil, safe defaults are applied.
func (c *Client) IterateResources(params finemcp.ListParams, opts *IteratorOptions) *ResourcesIterator {
	if opts == nil {
		defaultOpts := DefaultIteratorOptions()
		opts = &defaultOpts
	}
	return &ResourcesIterator{
		client:   c,
		params:   params,
		index:    0,
		maxPages: opts.MaxPages,
		maxItems: opts.MaxItems,
	}
}

// Next returns the next resource. Returns io.EOF when no more resources are available.
func (i *ResourcesIterator) Next(ctx context.Context) (*finemcp.ResourceInfo, error) {
	if i.err != nil {
		return nil, i.err
	}

	if i.index >= len(i.buffer) {
		if i.done {
			return nil, io.EOF
		}

		if i.maxPages > 0 && i.pageCount >= i.maxPages {
			i.err = fmt.Errorf("pagination: max pages limit reached (%d)", i.maxPages)
			return nil, i.err
		}

		if i.cursor != "" {
			i.params.Cursor = i.cursor
		}

		result, err := i.client.ListResources(ctx, i.params)
		if err != nil {
			i.err = err
			return nil, err
		}

		i.buffer = result.Resources
		i.index = 0
		i.cursor = result.NextCursor
		i.pageCount++

		if i.cursor == "" || len(i.buffer) == 0 {
			i.done = true
		}

		if len(i.buffer) == 0 {
			return nil, io.EOF
		}
	}

	resource := &i.buffer[i.index]
	i.index++
	return resource, nil
}

// HasMore returns true if there are more resources to fetch.
func (i *ResourcesIterator) HasMore() bool {
	return !i.done || i.index < len(i.buffer)
}

// All fetches all remaining resources into a slice, up to the configured limits.
func (i *ResourcesIterator) All(ctx context.Context) ([]finemcp.ResourceInfo, error) {
	var resources []finemcp.ResourceInfo
	for {
		if i.maxItems > 0 && len(resources) >= i.maxItems {
			return resources, fmt.Errorf("pagination: max items limit reached (%d)", i.maxItems)
		}
		resource, err := i.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return resources, err
		}
		resources = append(resources, *resource)
	}
	return resources, nil
}

// PromptsIterator provides cursor-based iteration over prompts.
type PromptsIterator struct {
	client    *Client
	params    finemcp.ListParams
	buffer    []finemcp.PromptInfo
	index     int
	cursor    string
	done      bool
	err       error
	maxPages  int
	maxItems  int
	pageCount int
}

// IteratePrompts creates a new iterator for listing all prompts with automatic pagination.
// Use opts to configure safety limits; if nil, safe defaults are applied.
func (c *Client) IteratePrompts(params finemcp.ListParams, opts *IteratorOptions) *PromptsIterator {
	if opts == nil {
		defaultOpts := DefaultIteratorOptions()
		opts = &defaultOpts
	}
	return &PromptsIterator{
		client:   c,
		params:   params,
		index:    0,
		maxPages: opts.MaxPages,
		maxItems: opts.MaxItems,
	}
}

// Next returns the next prompt. Returns io.EOF when no more prompts are available.
func (i *PromptsIterator) Next(ctx context.Context) (*finemcp.PromptInfo, error) {
	if i.err != nil {
		return nil, i.err
	}

	if i.index >= len(i.buffer) {
		if i.done {
			return nil, io.EOF
		}

		if i.maxPages > 0 && i.pageCount >= i.maxPages {
			i.err = fmt.Errorf("pagination: max pages limit reached (%d)", i.maxPages)
			return nil, i.err
		}

		if i.cursor != "" {
			i.params.Cursor = i.cursor
		}

		result, err := i.client.ListPrompts(ctx, i.params)
		if err != nil {
			i.err = err
			return nil, err
		}

		i.buffer = result.Prompts
		i.index = 0
		i.cursor = result.NextCursor
		i.pageCount++

		if i.cursor == "" || len(i.buffer) == 0 {
			i.done = true
		}

		if len(i.buffer) == 0 {
			return nil, io.EOF
		}
	}

	prompt := &i.buffer[i.index]
	i.index++
	return prompt, nil
}

// HasMore returns true if there are more prompts to fetch.
func (i *PromptsIterator) HasMore() bool {
	return !i.done || i.index < len(i.buffer)
}

// All fetches all remaining prompts into a slice, up to the configured limits.
func (i *PromptsIterator) All(ctx context.Context) ([]finemcp.PromptInfo, error) {
	var prompts []finemcp.PromptInfo
	for {
		if i.maxItems > 0 && len(prompts) >= i.maxItems {
			return prompts, fmt.Errorf("pagination: max items limit reached (%d)", i.maxItems)
		}
		prompt, err := i.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return prompts, err
		}
		prompts = append(prompts, *prompt)
	}
	return prompts, nil
}

// RootsIterator provides cursor-based iteration over roots.
type RootsIterator struct {
	client    *Client
	params    finemcp.ListParams
	buffer    []finemcp.RootInfo
	index     int
	cursor    string
	done      bool
	err       error
	maxPages  int
	maxItems  int
	pageCount int
}

// IterateRoots creates a new iterator for listing all roots with automatic pagination.
// Use opts to configure safety limits; if nil, safe defaults are applied.
func (c *Client) IterateRoots(params finemcp.ListParams, opts *IteratorOptions) *RootsIterator {
	if opts == nil {
		defaultOpts := DefaultIteratorOptions()
		opts = &defaultOpts
	}
	return &RootsIterator{
		client:   c,
		params:   params,
		index:    0,
		maxPages: opts.MaxPages,
		maxItems: opts.MaxItems,
	}
}

// Next returns the next root. Returns io.EOF when no more roots are available.
func (i *RootsIterator) Next(ctx context.Context) (*finemcp.RootInfo, error) {
	if i.err != nil {
		return nil, i.err
	}

	if i.index >= len(i.buffer) {
		if i.done {
			return nil, io.EOF
		}

		if i.maxPages > 0 && i.pageCount >= i.maxPages {
			i.err = fmt.Errorf("pagination: max pages limit reached (%d)", i.maxPages)
			return nil, i.err
		}

		if i.cursor != "" {
			i.params.Cursor = i.cursor
		}

		result, err := i.client.ListRoots(ctx, i.params)
		if err != nil {
			i.err = err
			return nil, err
		}

		i.buffer = result.Roots
		i.index = 0
		i.cursor = result.NextCursor
		i.pageCount++

		if i.cursor == "" || len(i.buffer) == 0 {
			i.done = true
		}

		if len(i.buffer) == 0 {
			return nil, io.EOF
		}
	}

	root := &i.buffer[i.index]
	i.index++
	return root, nil
}

// HasMore returns true if there are more roots to fetch.
func (i *RootsIterator) HasMore() bool {
	return !i.done || i.index < len(i.buffer)
}

// All fetches all remaining roots into a slice, up to the configured limits.
func (i *RootsIterator) All(ctx context.Context) ([]finemcp.RootInfo, error) {
	var roots []finemcp.RootInfo
	for {
		if i.maxItems > 0 && len(roots) >= i.maxItems {
			return roots, fmt.Errorf("pagination: max items limit reached (%d)", i.maxItems)
		}
		root, err := i.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return roots, err
		}
		roots = append(roots, *root)
	}
	return roots, nil
}

// TasksIterator provides cursor-based iteration over tasks.
type TasksIterator struct {
	client    *Client
	params    finemcp.ListParams
	buffer    []finemcp.Task
	index     int
	cursor    string
	done      bool
	err       error
	maxPages  int
	maxItems  int
	pageCount int
}

// IterateTasks creates a new iterator for listing all tasks with automatic pagination.
// Use opts to configure safety limits; if nil, safe defaults are applied.
func (c *Client) IterateTasks(params finemcp.ListParams, opts *IteratorOptions) *TasksIterator {
	if opts == nil {
		defaultOpts := DefaultIteratorOptions()
		opts = &defaultOpts
	}
	return &TasksIterator{
		client:   c,
		params:   params,
		index:    0,
		maxPages: opts.MaxPages,
		maxItems: opts.MaxItems,
	}
}

// Next returns the next task. Returns io.EOF when no more tasks are available.
func (i *TasksIterator) Next(ctx context.Context) (*finemcp.Task, error) {
	if i.err != nil {
		return nil, i.err
	}

	if i.index >= len(i.buffer) {
		if i.done {
			return nil, io.EOF
		}

		if i.maxPages > 0 && i.pageCount >= i.maxPages {
			i.err = fmt.Errorf("pagination: max pages limit reached (%d)", i.maxPages)
			return nil, i.err
		}

		if i.cursor != "" {
			i.params.Cursor = i.cursor
		}

		result, err := i.client.ListTasks(ctx, i.params)
		if err != nil {
			i.err = err
			return nil, err
		}

		i.buffer = result.Tasks
		i.index = 0
		i.cursor = result.NextCursor
		i.pageCount++

		if i.cursor == "" || len(i.buffer) == 0 {
			i.done = true
		}

		if len(i.buffer) == 0 {
			return nil, io.EOF
		}
	}

	task := &i.buffer[i.index]
	i.index++
	return task, nil
}

// HasMore returns true if there are more tasks to fetch.
func (i *TasksIterator) HasMore() bool {
	return !i.done || i.index < len(i.buffer)
}

// All fetches all remaining tasks into a slice, up to the configured limits.
func (i *TasksIterator) All(ctx context.Context) ([]finemcp.Task, error) {
	var tasks []finemcp.Task
	for {
		if i.maxItems > 0 && len(tasks) >= i.maxItems {
			return tasks, fmt.Errorf("pagination: max items limit reached (%d)", i.maxItems)
		}
		task, err := i.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return tasks, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, nil
}
