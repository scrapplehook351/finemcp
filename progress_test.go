package finemcp

import (
	"context"
	"testing"
)

func TestReportProgress_NoReporter(t *testing.T) {
	// ReportProgress should be a no-op when no reporter is injected.
	// Call with a bare context; if this panics or blocks, the test will fail.
	ReportProgress(context.Background(), 1, 10)
}

func TestReportProgress_InvokesReporter(t *testing.T) {
	var gotProgress, gotTotal float64
	called := false

	reporter := ProgressReporter(func(progress, total float64) {
		called = true
		gotProgress = progress
		gotTotal = total
	})

	ctx := withProgressReporter(context.Background(), reporter)
	ReportProgress(ctx, 5, 20)

	if !called {
		t.Fatal("expected reporter to be called")
	}
	if gotProgress != 5 {
		t.Errorf("progress = %v, want 5", gotProgress)
	}
	if gotTotal != 20 {
		t.Errorf("total = %v, want 20", gotTotal)
	}
}

func TestNewProgressNotification(t *testing.T) {
	n := newProgressNotification("tok-42", 3, 10)

	if n.JSONRPC != jsonrpcVersion {
		t.Errorf("JSONRPC = %q, want %q", n.JSONRPC, jsonrpcVersion)
	}
	if n.Method != methodProgress {
		t.Errorf("Method = %q, want %q", n.Method, methodProgress)
	}

	params, ok := n.Params.(ProgressParams)
	if !ok {
		t.Fatalf("Params type = %T, want ProgressParams", n.Params)
	}
	if params.ProgressToken != "tok-42" {
		t.Errorf("ProgressToken = %v, want %q", params.ProgressToken, "tok-42")
	}
	if params.Progress != 3 {
		t.Errorf("Progress = %v, want 3", params.Progress)
	}
	if params.Total != 10 {
		t.Errorf("Total = %v, want 10", params.Total)
	}
}
