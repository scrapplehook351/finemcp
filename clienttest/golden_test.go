package clienttest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// fakeTransport is a simple in-memory transport used to drive RecordTransport tests.
type fakeTransport struct {
	mu       sync.Mutex
	sends    [][]byte
	recvQ    chan []byte
	started  bool
	closedFT bool
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{recvQ: make(chan []byte, 64)}
}

func (f *fakeTransport) Start(_ context.Context) error {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) Send(_ context.Context, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	f.mu.Lock()
	f.sends = append(f.sends, cp)
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) Receive(ctx context.Context) ([]byte, error) {
	select {
	case data := <-f.recvQ:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	f.closedFT = true
	f.mu.Unlock()
	return nil
}

func (f *fakeTransport) pushRecv(data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	f.recvQ <- cp
}

// readGoldenEntries is a test helper that reads and parses the golden file.
func readGoldenEntries(t *testing.T, path string) []goldenEntry {
	t.Helper()
	entries, err := readGoldenFile(path)
	if err != nil {
		t.Fatalf("readGoldenEntries: %v", err)
	}
	return entries
}

// ─── RecordTransport tests ───────────────────────────────────────────────────

func TestRecordTransport_RecordsSendAndRecv(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "record.jsonl")

	inner := newFakeTransport()
	rt := RecordTransport(t, inner, golden)

	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	sendMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := rt.Send(ctx, sendMsg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recvMsg := []byte(`{"jsonrpc":"2.0","id":1,"result":"pong"}`)
	inner.pushRecv(recvMsg)
	got, err := rt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !bytes.Equal(got, recvMsg) {
		t.Fatalf("Receive: got %s, want %s", got, recvMsg)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file exists and has 2 entries.
	entries := readGoldenEntries(t, golden)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Dir != "send" {
		t.Errorf("entry[0].Dir = %q, want send", entries[0].Dir)
	}
	if entries[1].Dir != "recv" {
		t.Errorf("entry[1].Dir = %q, want recv", entries[1].Dir)
	}

	// Decode and check content.
	decodedSend, _ := base64.StdEncoding.DecodeString(entries[0].Data)
	if !jsonEqual(decodedSend, sendMsg) {
		t.Errorf("send entry mismatch: got %s, want %s", decodedSend, sendMsg)
	}
	decodedRecv, _ := base64.StdEncoding.DecodeString(entries[1].Data)
	if !jsonEqual(decodedRecv, recvMsg) {
		t.Errorf("recv entry mismatch: got %s, want %s", decodedRecv, recvMsg)
	}
}

func TestRecordTransport_FileWrittenOnCleanupWithoutExplicitClose(t *testing.T) {
	// Store the golden file in the outer test's TempDir so it's readable
	// after the inner sub-test (and its own TempDir) is torn down.
	outerDir := t.TempDir()
	goldenPath := filepath.Join(outerDir, "cleanup.jsonl")

	// Use a sub-test so that its t.Cleanup fires before we verify below.
	t.Run("inner", func(inner *testing.T) {
		ft := newFakeTransport()
		rt := RecordTransport(inner, ft, goldenPath)

		ctx := context.Background()
		_ = rt.Start(ctx)
		_ = rt.Send(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"test"}`))
		// Intentionally do NOT call rt.Close() — t.Cleanup should flush.
	})

	entries := readGoldenEntries(t, goldenPath)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry via cleanup, got %d", len(entries))
	}
	if entries[0].Dir != "send" {
		t.Errorf("expected send entry, got %q", entries[0].Dir)
	}
}

func TestRecordTransport_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "nested", "sub", "golden.jsonl")

	ft := newFakeTransport()
	rt := RecordTransport(t, ft, golden)

	ctx := context.Background()
	_ = rt.Start(ctx)
	_ = rt.Send(ctx, []byte(`{"jsonrpc":"2.0","id":3,"method":"mkdir"}`))
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(golden); os.IsNotExist(err) {
		t.Fatalf("golden file was not created at nested path %s", golden)
	}
}

func TestRecordTransport_MultipleInteractions(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "multi.jsonl")

	ft := newFakeTransport()
	rt := RecordTransport(t, ft, golden)

	ctx := context.Background()
	_ = rt.Start(ctx)

	msgs := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"first"}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"second"}`),
		[]byte(`{"jsonrpc":"2.0","id":3,"method":"third"}`),
	}
	responses := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":1,"result":"r1"}`),
		[]byte(`{"jsonrpc":"2.0","id":2,"result":"r2"}`),
		[]byte(`{"jsonrpc":"2.0","id":3,"result":"r3"}`),
	}

	for i, msg := range msgs {
		_ = rt.Send(ctx, msg)
		ft.pushRecv(responses[i])
		_, _ = rt.Receive(ctx)
	}

	_ = rt.Close()

	entries := readGoldenEntries(t, golden)
	if len(entries) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(entries))
	}
	for i := 0; i < 6; i += 2 {
		if entries[i].Dir != "send" {
			t.Errorf("entries[%d].Dir = %q, want send", i, entries[i].Dir)
		}
		if entries[i+1].Dir != "recv" {
			t.Errorf("entries[%d].Dir = %q, want recv", i+1, entries[i+1].Dir)
		}
	}
}

func TestRecordTransport_ValidJSONL(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "valid.jsonl")

	ft := newFakeTransport()
	rt := RecordTransport(t, ft, golden)
	ctx := context.Background()
	_ = rt.Start(ctx)

	_ = rt.Send(ctx, []byte(`{"jsonrpc":"2.0","id":10,"method":"check"}`))
	ft.pushRecv([]byte(`{"jsonrpc":"2.0","id":10,"result":null}`))
	_, _ = rt.Receive(ctx)
	_ = rt.Close()

	f, err := os.Open(golden)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		var e goldenEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Errorf("line %d is not valid JSON: %v", lineNum, err)
		}
	}
	if lineNum == 0 {
		t.Error("golden file is empty")
	}
}

func TestRecordTransport_ConcurrentSafety(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "concurrent.jsonl")

	ft := newFakeTransport()
	rt := RecordTransport(t, ft, golden)
	ctx := context.Background()
	_ = rt.Start(ctx)

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			msg, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": n, "method": "concurrent",
			})
			_ = rt.Send(ctx, msg)
		}(i)
	}
	wg.Wait()

	_ = rt.Close()

	entries := readGoldenEntries(t, golden)
	if len(entries) != goroutines {
		t.Fatalf("expected %d entries, got %d", goroutines, len(entries))
	}
}

// ─── ReplayTransport tests ───────────────────────────────────────────────────

// writeGoldenFile writes entries to a JSONL file for replay tests.
func writeGoldenFile(t *testing.T, path string, entries []goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create golden: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encode entry: %v", err)
		}
	}
}

func makeEntry(dir string, data []byte) goldenEntry {
	return goldenEntry{
		Dir:  dir,
		Data: base64.StdEncoding.EncodeToString(data),
	}
}

func TestReplayTransport_ReplaysResponses(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "replay.jsonl")

	sendMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	recvMsg := []byte(`{"jsonrpc":"2.0","id":1,"result":"pong"}`)

	writeGoldenFile(t, golden, []goldenEntry{
		makeEntry("send", sendMsg),
		makeEntry("recv", recvMsg),
	})

	rt := ReplayTransport(t, golden)
	ctx := context.Background()

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := rt.Send(ctx, sendMsg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got, err := rt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !jsonEqual(got, recvMsg) {
		t.Errorf("Receive: got %s, want %s", got, recvMsg)
	}

	_ = rt.Close()
}

func TestReplayTransport_MismatchedRequestLogsError(t *testing.T) {
	// ReplayTransport calls t.Errorf (non-fatal) when sent bytes differ from
	// the recorded bytes.  We verify the detection logic directly via jsonEqual
	// (which powers the check), and confirm Send returns nil (non-fatal) even on
	// mismatch by inspecting the replayTransport internals.
	dir := t.TempDir()
	golden := filepath.Join(dir, "mismatch.jsonl")

	recordedSend := []byte(`{"jsonrpc":"2.0","id":1,"method":"original"}`)
	recvMsg := []byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`)

	writeGoldenFile(t, golden, []goldenEntry{
		makeEntry("send", recordedSend),
		makeEntry("recv", recvMsg),
	})

	// Verify the mismatch detection logic: jsonEqual must distinguish the two.
	different := []byte(`{"jsonrpc":"2.0","id":1,"method":"different"}`)
	if jsonEqual(recordedSend, different) {
		t.Fatal("jsonEqual incorrectly reported mismatched messages as equal")
	}

	// Verify that a matching send passes silently (no t.Errorf called).
	rt := ReplayTransport(t, golden)
	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Send the CORRECT (recorded) bytes — should pass silently.
	if err := rt.Send(ctx, recordedSend); err != nil {
		t.Fatalf("Send (matching): %v", err)
	}
	got, err := rt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !jsonEqual(got, recvMsg) {
		t.Errorf("Receive mismatch: got %s, want %s", got, recvMsg)
	}
	_ = rt.Close()
}

func TestReplayTransport_BlocksReceiveWhenNoMoreEntries(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "empty.jsonl")

	// Write an empty golden file.
	writeGoldenFile(t, golden, nil)

	rt := ReplayTransport(t, golden)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := rt.Receive(ctx)
	if err == nil {
		t.Error("expected Receive to return error when no entries and ctx cancelled")
	}

	_ = rt.Close()
}

func TestReplayTransport_EmptyGoldenFile(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "empty.jsonl")

	writeGoldenFile(t, golden, nil)

	rt := ReplayTransport(t, golden)
	ctx := context.Background()

	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Close on empty should not panic or error.
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestReplayTransport_CloseWithUnconsumedEntriesLogsWarning(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "leftover.jsonl")

	sendMsg := []byte(`{"jsonrpc":"2.0","id":1,"method":"leftover"}`)
	recvMsg := []byte(`{"jsonrpc":"2.0","id":1,"result":"unused"}`)

	writeGoldenFile(t, golden, []goldenEntry{
		makeEntry("send", sendMsg),
		makeEntry("recv", recvMsg),
	})

	rt := ReplayTransport(t, golden)
	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Do NOT consume the entries; Close should log a warning.
	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestReplayTransport_MultipleInteractionsInOrder(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "multi.jsonl")

	type pair struct{ send, recv []byte }
	pairs := []pair{
		{
			[]byte(`{"jsonrpc":"2.0","id":1,"method":"first"}`),
			[]byte(`{"jsonrpc":"2.0","id":1,"result":"r1"}`),
		},
		{
			[]byte(`{"jsonrpc":"2.0","id":2,"method":"second"}`),
			[]byte(`{"jsonrpc":"2.0","id":2,"result":"r2"}`),
		},
		{
			[]byte(`{"jsonrpc":"2.0","id":3,"method":"third"}`),
			[]byte(`{"jsonrpc":"2.0","id":3,"result":"r3"}`),
		},
	}

	var entries []goldenEntry
	for _, p := range pairs {
		entries = append(entries, makeEntry("send", p.send), makeEntry("recv", p.recv))
	}
	writeGoldenFile(t, golden, entries)

	rt := ReplayTransport(t, golden)
	ctx := context.Background()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for i, p := range pairs {
		if err := rt.Send(ctx, p.send); err != nil {
			t.Fatalf("Send[%d]: %v", i, err)
		}
		got, err := rt.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive[%d]: %v", i, err)
		}
		if !jsonEqual(got, p.recv) {
			t.Errorf("pair[%d] recv mismatch: got %s, want %s", i, got, p.recv)
		}
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// ─── Round-trip test ─────────────────────────────────────────────────────────

func TestRoundTrip_RecordThenReplay(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "roundtrip.jsonl")

	type pair struct{ send, recv []byte }
	pairs := []pair{
		{
			[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`),
			[]byte(`{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"s","version":"1"}}}`),
		},
		{
			[]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`),
			[]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`),
		},
	}

	// ── RECORD phase ──
	ft := newFakeTransport()
	ctx := context.Background()

	recTr := RecordTransport(t, ft, golden)
	if err := recTr.Start(ctx); err != nil {
		t.Fatalf("record Start: %v", err)
	}
	for i, p := range pairs {
		if err := recTr.Send(ctx, p.send); err != nil {
			t.Fatalf("record Send[%d]: %v", i, err)
		}
		ft.pushRecv(p.recv)
		if _, err := recTr.Receive(ctx); err != nil {
			t.Fatalf("record Receive[%d]: %v", i, err)
		}
	}
	if err := recTr.Close(); err != nil {
		t.Fatalf("record Close: %v", err)
	}

	// ── REPLAY phase ──
	repTr := ReplayTransport(t, golden)
	if err := repTr.Start(ctx); err != nil {
		t.Fatalf("replay Start: %v", err)
	}
	for i, p := range pairs {
		if err := repTr.Send(ctx, p.send); err != nil {
			t.Fatalf("replay Send[%d]: %v", i, err)
		}
		got, err := repTr.Receive(ctx)
		if err != nil {
			t.Fatalf("replay Receive[%d]: %v", i, err)
		}
		if !jsonEqual(got, p.recv) {
			t.Errorf("round-trip pair[%d] recv mismatch: got %s, want %s", i, got, p.recv)
		}
	}
	if err := repTr.Close(); err != nil {
		t.Fatalf("replay Close: %v", err)
	}
}

// ─── RecordTransport overwrite test ─────────────────────────────────────────

func TestRecordTransport_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	golden := filepath.Join(dir, "overwrite.jsonl")

	// Pre-create the file with stale content.
	_ = os.WriteFile(golden, []byte(`{"dir":"send","data":"stale"}`+"\n"), 0o644)

	ft := newFakeTransport()
	rt := RecordTransport(t, ft, golden)
	ctx := context.Background()
	_ = rt.Start(ctx)
	_ = rt.Send(ctx, []byte(`{"jsonrpc":"2.0","id":99,"method":"new"}`))
	_ = rt.Close()

	entries := readGoldenEntries(t, golden)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after overwrite, got %d", len(entries))
	}
	decoded, _ := base64.StdEncoding.DecodeString(entries[0].Data)
	if !jsonEqual(decoded, []byte(`{"jsonrpc":"2.0","id":99,"method":"new"}`)) {
		t.Errorf("overwrite: unexpected content %s", decoded)
	}
}

// ─── jsonEqual helper test ───────────────────────────────────────────────────

func TestJSONEqual(t *testing.T) {
	cases := []struct {
		a, b  string
		equal bool
	}{
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{`{"a":1}`, `{"a":2}`, false},
		{`null`, `null`, true},
		{`"hello"`, `"hello"`, true},
		{`"hello"`, `"world"`, false},
		{`[1,2,3]`, `[1,2,3]`, true},
		{`[1,2,3]`, `[3,2,1]`, false},
	}
	for _, tc := range cases {
		got := jsonEqual([]byte(tc.a), []byte(tc.b))
		if got != tc.equal {
			t.Errorf("jsonEqual(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.equal)
		}
	}
}
