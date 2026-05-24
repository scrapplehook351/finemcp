package stdio_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/finemcp/finemcp/client/transport/stdio"
)

// TestMain allows this test binary to act as a line-echo helper subprocess
// when GO_TEST_STDIO_HELPER=1 is set. Tests that need a live stdio server
// spawn os.Args[0] with that variable to get an echo process without any
// external dependency.
func TestMain(m *testing.M) {
	if os.Getenv("GO_TEST_STDIO_HELPER") == "1" {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Fprintln(os.Stdout, scanner.Text())
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// helperConfig returns a Config that spawns this test binary as a line-echo
// subprocess.
func helperConfig() stdio.Config {
	return stdio.Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=^$"},
		Env:     append(os.Environ(), "GO_TEST_STDIO_HELPER=1"),
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	tr := stdio.New(stdio.Config{Command: "test-server", Args: []string{"--arg"}, Dir: "/tmp"})
	if tr == nil {
		t.Fatal("New returned nil")
	}
}

func TestTransport_ConnectSendReceive(t *testing.T) {
	t.Parallel()
	tr := stdio.New(helperConfig())
	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	msg := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(resp) != string(msg) {
		t.Fatalf("got %q, want %q", string(resp), string(msg))
	}
}

func TestTransport_SendStripsControlChars(t *testing.T) {
	t.Parallel()
	tr := stdio.New(helperConfig())
	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tr.Close()

	// Message with embedded newline, carriage return, and tab — Send must strip them.
	msg := []byte("{\"jsonrpc\":\"2.0\",\n\"id\":1,\r\n\t\"method\":\"ping\"}")
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	for _, b := range resp {
		if b == '\n' || b == '\r' || b == '\t' {
			t.Fatalf("response contains control character: %q", string(resp))
		}
	}
}

func TestTransport_SendOnClosed(t *testing.T) {
	t.Parallel()
	tr := stdio.New(helperConfig())
	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tr.Send(ctx, []byte(`{}`)); err == nil {
		t.Fatal("expected error sending on closed transport")
	}
}

func TestTransport_ReceiveAfterClose(t *testing.T) {
	t.Parallel()
	tr := stdio.New(helperConfig())
	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Close shuts down the subprocess; its stdout pipe reaches EOF.
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := tr.Receive(ctx)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after subprocess exit, got %v", err)
	}
}

func TestTransport_CloseIdempotent(t *testing.T) {
	t.Parallel()
	tr := stdio.New(helperConfig())
	ctx := context.Background()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTransport_StartInvalidCommand(t *testing.T) {
	t.Parallel()
	tr := stdio.New(stdio.Config{Command: "/nonexistent/command/that/does/not/exist"})
	if err := tr.Start(context.Background()); err == nil {
		t.Fatal("expected error starting nonexistent command")
	}
}
