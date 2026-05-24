package clienttest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/finemcp/finemcp/client"
)

// goldenEntry is a single line in the golden file.
type goldenEntry struct {
	Dir  string `json:"dir"`
	Data string `json:"data"` // base64-encoded raw JSON-RPC bytes
}

// jsonEqual reports whether two JSON byte slices are semantically equal
// by unmarshaling both into comparable representations.
func jsonEqual(a, b []byte) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	ra, err := json.Marshal(va)
	if err != nil {
		return false
	}
	rb, err := json.Marshal(vb)
	if err != nil {
		return false
	}
	return bytes.Equal(ra, rb)
}

// ─── RecordTransport ────────────────────────────────────────────────────────

// recordTransport wraps an existing transport and records every Send/Receive
// interaction to goldenPath. The file is written when Close is called.
type recordTransport struct {
	t          *testing.T
	inner      client.Transport
	goldenPath string

	mu      sync.Mutex
	log     []goldenEntry
	written bool
}

// RecordTransport wraps an existing transport and records every Send/Receive
// interaction to goldenPath. The file is written when Close() is called (or
// the test ends via t.Cleanup). If the file already exists it is overwritten.
func RecordTransport(t *testing.T, inner client.Transport, goldenPath string) client.Transport {
	t.Helper()
	rt := &recordTransport{
		t:          t,
		inner:      inner,
		goldenPath: goldenPath,
	}
	t.Cleanup(func() {
		// Write on cleanup even if Close was never called explicitly.
		rt.mu.Lock()
		written := rt.written
		rt.mu.Unlock()
		if !written {
			if err := rt.flush(); err != nil {
				t.Errorf("clienttest: RecordTransport cleanup flush: %v", err)
			}
		}
	})
	return rt
}

// Start implements [client.Transport] by delegating to the inner transport.
func (r *recordTransport) Start(ctx context.Context) error {
	return r.inner.Start(ctx)
}

// Send implements [client.Transport]. It forwards data to the inner transport
// and appends the sent bytes to the golden log.
func (r *recordTransport) Send(ctx context.Context, data []byte) error {
	if err := r.inner.Send(ctx, data); err != nil {
		return err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	r.mu.Lock()
	r.log = append(r.log, goldenEntry{
		Dir:  "send",
		Data: base64.StdEncoding.EncodeToString(cp),
	})
	r.mu.Unlock()
	return nil
}

// Receive implements [client.Transport]. It reads the next message from the
// inner transport and appends the received bytes to the golden log.
func (r *recordTransport) Receive(ctx context.Context) ([]byte, error) {
	data, err := r.inner.Receive(ctx)
	if err != nil {
		return nil, err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	r.mu.Lock()
	r.log = append(r.log, goldenEntry{
		Dir:  "recv",
		Data: base64.StdEncoding.EncodeToString(cp),
	})
	r.mu.Unlock()
	return data, nil
}

// Close implements [client.Transport]. It flushes the golden log to disk
// and closes the inner transport.
func (r *recordTransport) Close() error {
	if err := r.flush(); err != nil {
		r.t.Errorf("clienttest: RecordTransport flush: %v", err)
	}
	return r.inner.Close()
}

func (r *recordTransport) flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.written {
		return nil
	}
	r.written = true

	if err := os.MkdirAll(filepath.Dir(r.goldenPath), 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.Create(r.goldenPath)
	if err != nil {
		return fmt.Errorf("create golden file: %w", err)
	}

	enc := json.NewEncoder(f)
	for _, entry := range r.log {
		if err := enc.Encode(entry); err != nil {
			_ = f.Close()
			return fmt.Errorf("encode golden entry: %w", err)
		}
	}
	return f.Close()
}

// ─── ReplayTransport ────────────────────────────────────────────────────────

// replayTransport replays interactions previously recorded by RecordTransport.
type replayTransport struct {
	t          *testing.T
	goldenPath string

	mu      sync.Mutex
	entries []goldenEntry
	cursor  int
	closed  bool

	// recvCh delivers recv entries to Receive callers.
	recvCh chan []byte
}

// ReplayTransport creates a transport that replays interactions previously
// recorded by RecordTransport. It reads goldenPath at Start() time.
func ReplayTransport(t *testing.T, goldenPath string) client.Transport {
	t.Helper()
	return &replayTransport{
		t:          t,
		goldenPath: goldenPath,
		recvCh:     make(chan []byte, 256),
	}
}

// Start implements [client.Transport]. It reads and parses the golden file,
// populating the replay sequence before any Send/Receive calls.
func (r *replayTransport) Start(_ context.Context) error {
	r.t.Helper()
	entries, err := readGoldenFile(r.goldenPath)
	if err != nil {
		r.t.Fatalf("clienttest: ReplayTransport: read golden file %q: %v", r.goldenPath, err)
		return err // unreachable after Fatal, but satisfies compiler
	}
	r.mu.Lock()
	r.entries = entries
	r.cursor = 0
	r.mu.Unlock()
	return nil
}

// Send implements [client.Transport]. It compares the outgoing request against
// the next recorded send entry and fails the test on mismatch, then delivers
// the immediately following recv entry to the Receive channel.
func (r *replayTransport) Send(_ context.Context, data []byte) error {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()

	// Skip past non-send entries (shouldn't normally happen, but be safe).
	for r.cursor < len(r.entries) && r.entries[r.cursor].Dir != "send" {
		r.cursor++
	}

	if r.cursor >= len(r.entries) {
		r.t.Errorf("clienttest: ReplayTransport.Send: no more recorded send entries (cursor=%d)", r.cursor)
		return nil
	}

	recorded, err := base64.StdEncoding.DecodeString(r.entries[r.cursor].Data)
	if err != nil {
		r.t.Errorf("clienttest: ReplayTransport.Send: decode recorded data: %v", err)
		r.cursor++
		return nil
	}
	r.cursor++

	if !jsonEqual(data, recorded) {
		r.t.Errorf("clienttest: ReplayTransport.Send: request mismatch\n  got:  %s\n  want: %s", data, recorded)
	}

	// Deliver the immediately following recv entry (if any) to recvCh.
	if r.cursor < len(r.entries) && r.entries[r.cursor].Dir == "recv" {
		recvData, decErr := base64.StdEncoding.DecodeString(r.entries[r.cursor].Data)
		if decErr == nil {
			cp := make([]byte, len(recvData))
			copy(cp, recvData)
			r.cursor++
			r.recvCh <- cp
		} else {
			r.t.Errorf("clienttest: ReplayTransport.Send: decode recv entry: %v", decErr)
			r.cursor++
		}
	}
	return nil
}

// Receive implements [client.Transport]. It blocks until a recorded recv
// entry is available or the context is cancelled.
func (r *replayTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case data := <-r.recvCh:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close implements [client.Transport]. It marks the transport as closed and
// closes the recv channel so any blocked Receive calls return.
func (r *replayTransport) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true

	remaining := len(r.entries) - r.cursor
	if remaining > 0 {
		r.t.Logf("clienttest: ReplayTransport.Close: %d unconsumed golden entries remain (cursor=%d, total=%d)",
			remaining, r.cursor, len(r.entries))
	}
	return nil
}

// readGoldenFile reads a newline-delimited JSON golden file.
func readGoldenFile(path string) ([]goldenEntry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is supplied by the test author, not user input
	if err != nil {
		return nil, err
	}

	var entries []goldenEntry
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e goldenEntry
		if err := json.Unmarshal(line, &e); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		entries = append(entries, e)
	}
	scanErr := scanner.Err()
	if closeErr := f.Close(); closeErr != nil && scanErr == nil {
		return nil, closeErr
	}
	if scanErr != nil {
		return nil, scanErr
	}
	return entries, nil
}
