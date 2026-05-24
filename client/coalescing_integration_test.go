package client_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/client"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestClient_RequestCoalescing_ListToolsSharesWireRequest(t *testing.T) {
	mt := newMockTransport()
	autoResponder(t, mt)

	c, err := client.New(mt, client.Options{
		ClientInfo:        finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		RequestCoalescing: true,
		CoalescingWindow:  5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			if _, err := c.ListTools(context.Background(), finemcp.ListParams{}); err != nil {
				t.Errorf("ListTools() error = %v", err)
			}
		}()
	}

	wg.Wait()

	if got := countSentRequestsByMethod(mt, finemcp.MethodToolsList); got != 1 {
		t.Fatalf("tools/list wire request count = %d, want 1", got)
	}
}

func TestClient_RequestCoalescing_DoesNotCoalesceCallTool(t *testing.T) {
	mt := newMockTransport()
	autoResponder(t, mt)

	c, err := client.New(mt, client.Options{
		ClientInfo:        finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		RequestCoalescing: true,
		CoalescingWindow:  5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const callers = 4
	var wg sync.WaitGroup
	wg.Add(callers)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			_, err := c.CallTool(context.Background(), finemcp.CallToolParams{
				Name:      "echo",
				Arguments: json.RawMessage(`{"value":"hello"}`),
			})
			if err != nil {
				t.Errorf("CallTool() error = %v", err)
			}
		}()
	}

	wg.Wait()

	if got := countSentRequestsByMethod(mt, finemcp.MethodToolsCall); got != callers {
		t.Fatalf("tools/call wire request count = %d, want %d", got, callers)
	}
}

func TestClient_RequestCoalescing_PreservesTraceContext(t *testing.T) {
	mt := newMockTransport()
	autoResponder(t, mt)

	c, err := client.New(mt, client.Options{
		ClientInfo:        finemcp.ProcessInfo{Name: "test-client", Version: "1.0.0"},
		RequestCoalescing: true,
		CoalescingWindow:  5 * time.Millisecond,
		TracerProvider:    sdktrace.NewTracerProvider(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if _, err := c.ListTools(context.Background(), finemcp.ListParams{}); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	req := findSentRequestByMethod(mt, finemcp.MethodToolsList)
	if req == nil {
		t.Fatal("expected tools/list request on wire")
	}

	params, ok := req["params"].(map[string]any)
	if !ok {
		t.Fatal("expected params object on tools/list request")
	}

	meta, ok := params["_meta"].(map[string]any)
	if !ok {
		t.Fatal("expected _meta object on coalesced request")
	}

	traceparent, ok := meta["traceparent"].(string)
	if !ok || traceparent == "" {
		t.Fatalf("expected non-empty traceparent on coalesced request, got %#v", meta["traceparent"])
	}
}

func countSentRequestsByMethod(mt *mockTransport, method string) int {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	count := 0
	for _, data := range mt.sent {
		var req map[string]any
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		if reqMethod, _ := req["method"].(string); reqMethod == method {
			count++
		}
	}

	return count
}

func findSentRequestByMethod(mt *mockTransport, method string) map[string]any {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	for _, data := range mt.sent {
		var req map[string]any
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		if reqMethod, _ := req["method"].(string); reqMethod == method {
			return req
		}
	}

	return nil
}
