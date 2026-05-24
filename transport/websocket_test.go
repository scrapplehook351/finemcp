package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/finemcp/finemcp"
	"github.com/finemcp/finemcp/transport"
	"github.com/gorilla/websocket"
)

const websocketTestTimeout = 5 * time.Second

func newWebSocketTestServer(t *testing.T) (*httptest.Server, *finemcp.Server) {
	t.Helper()
	s := finemcp.NewServer("test", "1.0")
	tool, err := finemcp.NewTool("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("pong"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTool(tool); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(transport.WebSocketHandler(s))
	t.Cleanup(ts.Close)
	return ts, s
}

func dialWebSocket(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = websocketTestTimeout

	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestWebSocket_InitializeAndCall(t *testing.T) {
	t.Parallel()

	ts, _ := newWebSocketTestServer(t)
	conn := dialWebSocket(t, ts)

	initMsg := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(initMsg)); err != nil {
		t.Fatalf("write init: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read init response: %v", err)
	}

	var initResp finemcp.JSONRPCResponse
	if err := json.Unmarshal(data, &initResp); err != nil {
		t.Fatalf("unmarshal init response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("init error: %s", initResp.Error.Message)
	}

	callMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(callMsg)); err != nil {
		t.Fatalf("write tools/call: %v", err)
	}

	_, data, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read tools/call response: %v", err)
	}

	var callResp finemcp.JSONRPCResponse
	if err := json.Unmarshal(data, &callResp); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if callResp.Error != nil {
		t.Fatalf("tools/call error: %s", callResp.Error.Message)
	}
}

func TestWebSocket_ReceivesToolsListChangedNotification(t *testing.T) {
	t.Parallel()

	ts, server := newWebSocketTestServer(t)
	conn := dialWebSocket(t, ts)

	// Perform an initialize round-trip first so that ServeHTTP has called
	// AddSender before we broadcast the notification.  Without this, there is
	// a race between dialWebSocket returning (after the HTTP 101) and the
	// server goroutine registering the sender.
	initMsg := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(initMsg)); err != nil {
		t.Fatalf("write init: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read init response: %v", err)
	}

	// Trigger a tools/list_changed broadcast and expect a notification frame.
	server.NotifyToolsListChanged()

	type result struct {
		msgType int
		data    []byte
		err     error
	}

	ch := make(chan result, 1)
	go func() {
		msgType, data, err := conn.ReadMessage()
		ch <- result{msgType: msgType, data: data, err: err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read notification: %v", r.err)
		}
		if r.msgType != websocket.TextMessage {
			t.Fatalf("message type = %d, want TextMessage", r.msgType)
		}
		var notif finemcp.JSONRPCNotification
		if err := json.Unmarshal(r.data, &notif); err != nil {
			t.Fatalf("unmarshal notification: %v", err)
		}
		if notif.Method != "notifications/tools/list_changed" {
			t.Fatalf("notification method = %q, want notifications/tools/list_changed", notif.Method)
		}
	case <-time.After(websocketTestTimeout):
		t.Fatal("timeout waiting for tools/list_changed notification")
	}
}

func TestWebSocket_SessionCleanupOnDisconnect(t *testing.T) {
	t.Parallel()

	ts, _ := newWebSocketTestServer(t)
	conn := dialWebSocket(t, ts)

	// Send initialize.
	initMsg := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(initMsg)); err != nil {
		t.Fatalf("write init: %v", err)
	}

	_, _, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read init response: %v", err)
	}

	// Close the client side — the server should clean up without errors.
	if err := conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	); err != nil {
		t.Fatalf("write close: %v", err)
	}
}

func TestWebSocket_ConcurrentNotificationsDuringRequest(t *testing.T) {
	t.Parallel()

	ts, server := newWebSocketTestServer(t)
	conn := dialWebSocket(t, ts)

	// Send initialize first.
	initMsg := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"` +
		testProtocolVersion +
		`","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(initMsg)); err != nil {
		t.Fatalf("write init: %v", err)
	}
	_, _, err := conn.ReadMessage() // consume init response
	if err != nil {
		t.Fatalf("read init response: %v", err)
	}

	// Fire notifications concurrently while a request is in‑flight.
	callMsg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(callMsg)); err != nil {
		t.Fatalf("write tools/call: %v", err)
	}

	// Send multiple notifications from the server side concurrently.
	for i := 0; i < 5; i++ {
		go server.NotifyToolsListChanged()
	}

	// We should receive the tools/call response plus notifications.
	// Just verify we get at least the response without a hang.
	gotResponse := false
	deadline := time.After(websocketTestTimeout)
	for !gotResponse {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for tools/call response")
		default:
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read message: %v", err)
		}
		var resp finemcp.JSONRPCResponse
		if err := json.Unmarshal(data, &resp); err == nil && resp.ID != nil {
			gotResponse = true
		}
	}
}

func TestWebSocketOptions(t *testing.T) {
	t.Parallel()

	s := finemcp.NewServer("test", "1.0")

	// WithWebSocketPath: requests to the default path (/ws) should be rejected
	// when a different path is configured.
	customPath := "/mcp/ws"
	h := transport.WebSocketHandler(s, transport.WithWebSocketPath(customPath))
	if h == nil {
		t.Fatal("WebSocketHandler returned nil")
	}

	// WithWebSocketMaxMessageSize: just verify no panic and handler is returned.
	h2 := transport.WebSocketHandler(s, transport.WithWebSocketMaxMessageSize(1024*1024))
	if h2 == nil {
		t.Fatal("WebSocketHandler with max message size returned nil")
	}

	// WithWebSocketCheckOrigin: provide a custom origin checker.
	checked := false
	h3 := transport.WebSocketHandler(s, transport.WithWebSocketCheckOrigin(func(r *http.Request) bool {
		checked = true
		return true
	}))
	if h3 == nil {
		t.Fatal("WebSocketHandler with check origin returned nil")
	}

	// Exercise the handler: a non-WebSocket GET to the custom path should
	// result in a 400 Bad Request (upgrade failure), not 404.
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, err := http.Get(ts.URL + customPath) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", customPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("expected upgrade attempt on %s, got 404", customPath)
	}

	// Request to the old /ws path should give 404 now that path is changed.
	resp2, err := http.Get(ts.URL + "/ws") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /ws: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for /ws with custom path, got %d", resp2.StatusCode)
	}

	_ = checked // origin checker is only invoked on real WS upgrade
}
