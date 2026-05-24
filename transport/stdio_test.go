package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
)

const testProtocolVersion = "2025-11-25"

// helper: builds a single-line JSON-RPC message with trailing newline.
func jsonrpcLine(id any, method string, params any) string {
	m := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	data, _ := json.Marshal(m)
	return string(data) + "\n"
}

// nonEmptyLines splits output into non-empty lines.
func nonEmptyLines(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

// errWriter is a writer that always returns an error.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

// failingReader returns data once, then returns an error on the next read.
type failingReader struct {
	data      []byte
	failAfter bool
	done      bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, fmt.Errorf("reader broken")
	}
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		if len(r.data) == 0 && r.failAfter {
			r.done = true
		}
		return n, nil
	}
	return 0, fmt.Errorf("reader broken")
}

func TestStdioTransport_InitializeAndPing(t *testing.T) {
	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += jsonrpcLine(2, "ping", nil)

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, &writer)

	err := tr.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d: %v", len(lines), lines)
	}

	var initResp finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("unmarshal init response: %v", err)
	}
	if initResp.Error != nil {
		t.Errorf("init error: %s", initResp.Error.Message)
	}

	var pingResp finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &pingResp); err != nil {
		t.Fatalf("unmarshal ping response: %v", err)
	}
	if pingResp.Error != nil {
		t.Errorf("ping error: %s", pingResp.Error.Message)
	}
}

func TestStdioTransport_NotificationProducesNoOutput(t *testing.T) {
	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n"

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, &writer)

	if err := tr.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 1 {
		t.Errorf("expected 1 response (init only), got %d", len(lines))
	}
}

func TestStdioTransport_InvalidJSON(t *testing.T) {
	input := "{bad json}\n"

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, &writer)

	if err := tr.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(lines))
	}

	var resp finemcp.JSONRPCResponse
	json.Unmarshal([]byte(lines[0]), &resp)

	if resp.Error == nil || resp.Error.Code != finemcp.ErrCodeParseError {
		t.Errorf("expected parse error, got %+v", resp)
	}
}

func TestStdioTransport_SkipsBlankLines(t *testing.T) {
	input := "\n\n" + jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	}) + "\n\n"

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, &writer)

	if err := tr.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 1 {
		t.Errorf("expected 1 response, got %d", len(lines))
	}
}

func TestStdioTransport_CleanEOF(t *testing.T) {
	reader := strings.NewReader("")
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, &writer)

	err := tr.Run(context.Background())
	if err != nil {
		t.Errorf("expected nil on clean EOF, got: %v", err)
	}
}

func TestStdioTransport_ContextCancellation(t *testing.T) {
	pr, pw := io.Pipe()
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, pr, &writer)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- tr.Run(ctx)
	}()

	initMsg := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	pw.Write([]byte(initMsg))

	cancel()
	pw.Close()

	err := <-done
	if err != nil && err != context.Canceled {
		t.Errorf("expected nil or context.Canceled, got: %v", err)
	}
}

func TestStdioTransport_ToolsCallEndToEnd(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	echo, _ := finemcp.NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(echo)

	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += jsonrpcLine(2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hello"},
	})

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	tr := transport.NewStdioTransport(s, reader, &writer)
	if err := tr.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}

	var resp finemcp.JSONRPCResponse
	json.Unmarshal([]byte(lines[1]), &resp)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	raw, _ := json.Marshal(resp.Result)
	if !bytes.Contains(raw, []byte("content")) {
		t.Errorf("expected content in result: %s", raw)
	}
}

func TestStdioTransport_MethodBeforeInit(t *testing.T) {
	input := jsonrpcLine(1, "tools/list", nil)

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, &writer)

	if err := tr.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 1 {
		t.Fatalf("expected 1 response, got %d", len(lines))
	}

	var resp finemcp.JSONRPCResponse
	json.Unmarshal([]byte(lines[0]), &resp)
	if resp.Error == nil {
		t.Fatal("expected error for pre-init request")
	}
	if resp.Error.Code != finemcp.ErrCodeInvalidRequest {
		t.Errorf("code = %d, want %d", resp.Error.Code, finemcp.ErrCodeInvalidRequest)
	}
}

func TestStdioTransport_MultipleMessages(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	noop, _ := finemcp.NewTool("noop", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	s.RegisterTool(noop)

	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += jsonrpcLine(2, "tools/list", nil)
	input += jsonrpcLine(3, "tools/call", map[string]any{"name": "noop"})
	input += jsonrpcLine(4, "ping", nil)

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	tr := transport.NewStdioTransport(s, reader, &writer)
	if err := tr.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 4 {
		t.Fatalf("expected 4 responses, got %d", len(lines))
	}

	for i, line := range lines {
		var resp finemcp.JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
		if resp.Error != nil {
			t.Errorf("line %d: unexpected error: %s", i, resp.Error.Message)
		}
	}
}

func TestStdioTransport_WriteError(t *testing.T) {
	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})

	reader := strings.NewReader(input)
	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, reader, errWriter{})

	err := tr.Run(context.Background())
	if err == nil {
		t.Fatal("expected write error")
	}
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("error = %q, should mention write", err)
	}
}

func TestStdioTransport_ScannerError(t *testing.T) {
	r := &failingReader{data: []byte(jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})), failAfter: true}

	var writer bytes.Buffer
	s := finemcp.NewServer("test", "1.0")
	tr := transport.NewStdioTransport(s, r, &writer)

	err := tr.Run(context.Background())
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error = %q, should mention read", err)
	}
}

func TestServeWithIO_FullLifecycle(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	tool, _ := finemcp.NewTool("echo", func(_ context.Context, input []byte) ([]byte, error) {
		return input, nil
	})
	s.RegisterTool(tool)

	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += jsonrpcLine(2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hello"},
	})

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	err := transport.ServeWithIO(context.Background(), s, reader, &writer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := nonEmptyLines(writer.String())
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}
}

func TestServeWithIO_ContextCancellation(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	pr, pw := io.Pipe()
	var output bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- transport.ServeWithIO(ctx, s, pr, &output)
	}()

	pw.Write([]byte(jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})))

	cancel()
	pw.Close()

	err := <-done
	if err != nil && err != context.Canceled {
		t.Errorf("expected nil or context.Canceled, got: %v", err)
	}
}

func TestServeWithIO_ResponsesAreValidJSON(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += jsonrpcLine(2, "ping", nil)

	reader := strings.NewReader(input)
	var output bytes.Buffer

	transport.ServeWithIO(context.Background(), s, reader, &output)

	for _, line := range nonEmptyLines(output.String()) {
		var resp finemcp.JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Errorf("invalid JSON response: %s", line)
		}
		if resp.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
		}
	}
}

func TestStdioTransport_ProgressNotification(t *testing.T) {
	s := finemcp.NewServer("test", "1.0")

	// Tool emits two progress reports at 25% and 75% then returns.
	worker, _ := finemcp.NewTool("worker", func(ctx context.Context, _ []byte) ([]byte, error) {
		finemcp.ReportProgress(ctx, 25, 100)
		finemcp.ReportProgress(ctx, 75, 100)
		return []byte(`"done"`), nil
	})
	s.RegisterTool(worker)

	input := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	input += jsonrpcLine(99, "tools/call", map[string]any{"name": "worker"})

	reader := strings.NewReader(input)
	var writer bytes.Buffer

	tr := transport.NewStdioTransport(s, reader, &writer)
	if err := tr.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := nonEmptyLines(writer.String())
	// Expected output order: init response, progress(25), progress(75), tool response.
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (init + 2 progress + result), got %d: %v", len(lines), lines)
	}

	// Progress lines should be notifications (no "id" field, method = "notifications/progress").
	for _, idx := range []int{1, 2} {
		var n map[string]any
		if err := json.Unmarshal([]byte(lines[idx]), &n); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", idx, err)
		}
		if n["method"] != "notifications/progress" {
			t.Errorf("line %d: method = %q, want %q", idx, n["method"], "notifications/progress")
		}
		if _, hasID := n["id"]; hasID {
			t.Errorf("line %d: progress notification must not have an id field", idx)
		}
		params, _ := n["params"].(map[string]any)
		if params == nil {
			t.Fatalf("line %d: missing params", idx)
		}
		// progressToken should match the request id (99 -> float64 after JSON round-trip).
		if params["progressToken"] != float64(99) {
			t.Errorf("line %d: progressToken = %v, want 99", idx, params["progressToken"])
		}
	}

	// The final line should be the tool result response.
	var resp finemcp.JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[3]), &resp); err != nil {
		t.Fatalf("line 3: invalid JSON: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("unexpected error in tool response: %s", resp.Error.Message)
	}
}

// safeWriter is a bytes.Buffer with a mutex so that concurrent writes from the
// transport goroutine and reads from the test goroutine don't race.
// ready is closed the first time a newline is written, allowing tests to block
// deterministically on the first response line instead of polling.
type safeWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	once  sync.Once
	ready chan struct{}
}

func newSafeWriter() *safeWriter {
	return &safeWriter{ready: make(chan struct{})}
}

func (w *safeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	if bytes.Contains(p, []byte("\n")) {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *safeWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestStdioTransport_ListChangedNotification(t *testing.T) {
	s := finemcp.NewServer("test", "1.0", finemcp.WithResourceSubscriptions())

	pr, pw := io.Pipe()
	defer pw.Close()

	writer := newSafeWriter()
	tr := transport.NewStdioTransport(s, pr, writer)

	// Since Run() blocks reading until EOF, run it in a goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = tr.Run(ctx)
	}()

	// Initialize request so the server considers us connected.
	initMsg := jsonrpcLine(1, "initialize", map[string]any{
		"protocolVersion": testProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
	})
	fmt.Fprint(pw, initMsg)

	// Block until the initialize response has been flushed to the output
	// stream (first newline). AddSender is called before the scan loop starts,
	// so the sender is already registered; waiting here ensures the init
	// response is written first so that the tools/list_changed notification
	// appears on the second line rather than interleaving with it.
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	defer waitCancel()
	select {
	case <-writer.ready:
	case <-waitCtx.Done():
		t.Fatalf("timed out waiting for initialize response")
	}

	// Trigger the notification from outside.
	// Use a no-op RegisterTool so auto-notify fires the list_changed notification.
	noopTool, _ := finemcp.NewTool("notify-trigger", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	_ = s.RegisterTool(noopTool)

	// Shut down the transport loop and wait.
	cancel()
	pw.Close() // Close pipe so scanner unwedges immediately
	wg.Wait()

	lines := nonEmptyLines(writer.String())
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines (init resp + notification), got %d:\n%s", len(lines), writer.String())
	}

	// The second line should be the tools/list_changed notification.
	var n finemcp.JSONRPCNotification
	if err := json.Unmarshal([]byte(lines[1]), &n); err != nil {
		t.Fatalf("invalid json for line 2: %v", err)
	}
	if n.Method != "notifications/tools/list_changed" {
		t.Errorf("expected notifications/tools/list_changed, got %q", n.Method)
	}
}
