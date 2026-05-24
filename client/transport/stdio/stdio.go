// Package stdio provides a client-side stdio transport for MCP.
//
// It spawns a subprocess and communicates with the server via
// newline-delimited JSON-RPC messages on stdin/stdout.
package stdio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// Config configures the stdio client transport.
type Config struct {
	// Command is the path to the MCP server executable.
	Command string

	// Args are the arguments to pass to the server command.
	Args []string

	// Env are extra environment variables for the subprocess.
	// If nil, the current process environment is inherited.
	Env []string

	// Dir is the working directory for the subprocess.
	Dir string
}

// Transport implements client.Transport for stdio-based MCP servers.
type Transport struct {
	cfg Config

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner

	mu     sync.Mutex
	closed bool
}

// New creates a new stdio client transport.
func New(cfg Config) *Transport {
	return &Transport{cfg: cfg}
}

// Start spawns the subprocess and sets up stdin/stdout pipes.
func (t *Transport) Start(ctx context.Context) error {
	// #nosec G204 -- Command is from user configuration, not untrusted input
	t.cmd = exec.CommandContext(ctx, t.cfg.Command, t.cfg.Args...)
	if t.cfg.Dir != "" {
		t.cmd.Dir = t.cfg.Dir
	}
	if t.cfg.Env != nil {
		t.cmd.Env = t.cfg.Env
	}

	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio transport: stdin pipe: %w", err)
	}

	stdoutPipe, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdio transport: stdout pipe: %w", err)
	}

	t.stdout = bufio.NewScanner(stdoutPipe)
	t.stdout.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("stdio transport: start: %w", err)
	}
	return nil
}

// Send writes a newline-delimited JSON-RPC message to the subprocess stdin.
func (t *Transport) Send(_ context.Context, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return errors.New("stdio transport: closed")
	}

	// Compact to remove embedded newlines.
	var buf bytes.Buffer
	for _, b := range data {
		if b != '\n' && b != '\r' && b != '\t' {
			buf.WriteByte(b)
		}
	}
	buf.WriteByte('\n')

	if _, err := t.stdin.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("stdio transport: write: %w", err)
	}
	return nil
}

// Receive reads the next newline-delimited JSON-RPC message from stdout.
func (t *Transport) Receive(_ context.Context) ([]byte, error) {
	if !t.stdout.Scan() {
		if err := t.stdout.Err(); err != nil {
			// cmd.Wait() closes the stdout pipe after the process exits, which
			// causes the scanner to surface an os.ErrClosed read error. Map
			// that to io.EOF so callers get a consistent end-of-stream signal.
			if errors.Is(err, os.ErrClosed) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("stdio transport: read: %w", err)
		}
		return nil, io.EOF
	}

	line := t.stdout.Bytes()
	cp := make([]byte, len(line))
	copy(cp, line)
	return cp, nil
}

// Close shuts down the subprocess by closing stdin and waiting for exit.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	if t.stdin != nil {
		_ = t.stdin.Close()
	}

	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Wait()
	}
	return nil
}
