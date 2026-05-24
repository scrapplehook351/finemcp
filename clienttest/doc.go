// Package clienttest provides a mock MCP server transport for testing code that
// depends on github.com/finemcp/finemcp/client.
//
// Basic usage:
//
//	m := clienttest.NewInitializedMockServer()
//	m.QueueToolsList([]finemcp.ToolInfo{{Name: "echo", InputSchema: map[string]any{"type": "object"}}})
//
//	c, _ := client.New(m.AsTransport(), client.Options{
//		ClientInfo: finemcp.ProcessInfo{Name: "test-client", Version: "1.0"},
//	})
//	defer c.Close()
//
//	_, _ = c.Initialize(context.Background())
//	res, _ := c.ListTools(context.Background(), finemcp.ListParams{})
//	_ = res
//
// MockServer records requests, serves queued responses/errors, and can push
// server notifications (for callbacks like OnProgress) via SendNotification.
package clienttest
