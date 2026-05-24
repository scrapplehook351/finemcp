package ws_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/finemcp/finemcp"
	wstransport "github.com/finemcp/finemcp/client/transport/ws"
	"github.com/finemcp/finemcp/transport"
)

const testProtocolVersion = "2025-11-25"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := finemcp.NewServer("test", "1.0")
	ts := httptest.NewServer(transport.WebSocketHandler(s))
	t.Cleanup(ts.Close)
	return ts
}

func serverURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
}

// dialAndInit dials the server and completes an initialize round-trip so that
// the server's AddSender call has completed before the test proceeds.
func dialAndInit(t *testing.T, tr *wstransport.Transport) {
	t.Helper()
	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	initMsg := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	if err := tr.Send(ctx, []byte(initMsg)); err != nil {
		t.Fatalf("Send init: %v", err)
	}
	resp, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive init response: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty init response")
	}
}

func TestNew_Defaults(t *testing.T) {
	t.Parallel()
	tr := wstransport.New(wstransport.Config{URL: "ws://localhost:0"})
	if tr == nil {
		t.Fatal("New returned nil")
	}
}

func TestTransport_ConnectSendReceive(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	tr := wstransport.New(wstransport.Config{URL: serverURL(ts)})
	defer tr.Close()
	dialAndInit(t, tr)
}

func TestTransport_HeadersPassedOnDial(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	tr := wstransport.New(wstransport.Config{
		URL:     serverURL(ts),
		Headers: map[string]string{"X-Custom": "test"},
	})
	defer tr.Close()
	dialAndInit(t, tr)
}

func TestTransport_SendOnClosed(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	tr := wstransport.New(wstransport.Config{URL: serverURL(ts)})
	dialAndInit(t, tr)

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tr.Send(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error sending on closed transport")
	}
}

func TestTransport_CloseIdempotent(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	tr := wstransport.New(wstransport.Config{URL: serverURL(ts)})
	dialAndInit(t, tr)

	if err := tr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTransport_StartDialError(t *testing.T) {
	t.Parallel()
	// Port 1 is reserved and the dial should fail immediately.
	tr := wstransport.New(wstransport.Config{URL: "ws://localhost:1"})
	if err := tr.Start(context.Background()); err == nil {
		t.Fatal("expected dial error for unreachable address")
	}
}
