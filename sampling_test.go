package finemcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// ── Wire type tests ─────────────────────────────────────────────────

func TestSamplingMessage_JSON(t *testing.T) {
	t.Parallel()
	msg := SamplingMessage{
		Role:    "user",
		Content: TextContent{Text: "Hello, world"},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the wire format contains role and content with type discriminator.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["role"]; !ok {
		t.Error("missing 'role' in JSON output")
	}
	if _, ok := raw["content"]; !ok {
		t.Error("missing 'content' in JSON output")
	}

	// Check content has the "type" discriminator.
	var content map[string]any
	if err := json.Unmarshal(raw["content"], &content); err != nil {
		t.Fatal(err)
	}
	if content["type"] != "text" {
		t.Errorf("content.type = %q, want %q", content["type"], "text")
	}
	if content["text"] != "Hello, world" {
		t.Errorf("content.text = %q, want %q", content["text"], "Hello, world")
	}
}

func TestModelPreferences_JSON(t *testing.T) {
	t.Parallel()
	cost := 0.3
	speed := 0.5
	intel := 0.9
	prefs := ModelPreferences{
		Hints:                []ModelHint{{Name: "claude-4-sonnet"}},
		CostPriority:         &cost,
		SpeedPriority:        &speed,
		IntelligencePriority: &intel,
	}

	data, err := json.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}

	var decoded ModelPreferences
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Hints) != 1 || decoded.Hints[0].Name != "claude-4-sonnet" {
		t.Errorf("hints round-trip failed: %+v", decoded.Hints)
	}
	if decoded.CostPriority == nil || *decoded.CostPriority != 0.3 {
		t.Errorf("costPriority mismatch: %v", decoded.CostPriority)
	}
	if decoded.IntelligencePriority == nil || *decoded.IntelligencePriority != 0.9 {
		t.Errorf("intelligencePriority mismatch: %v", decoded.IntelligencePriority)
	}
}

func TestModelPreferences_EmptyHints(t *testing.T) {
	t.Parallel()
	prefs := ModelPreferences{}

	data, err := json.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}

	// "hints" and priority fields should be omitted when nil/empty.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"hints", "costPriority", "speedPriority", "intelligencePriority"} {
		if _, ok := raw[key]; ok {
			t.Errorf("%s should be omitted for empty ModelPreferences", key)
		}
	}
}

func TestModelPreferences_ZeroPriorityPreserved(t *testing.T) {
	t.Parallel()
	zero := 0.0
	prefs := ModelPreferences{
		CostPriority: &zero,
	}

	data, err := json.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}

	// A zero pointer means "explicitly set to 0" and must appear in JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["costPriority"]; !ok {
		t.Error("costPriority should be present when explicitly set to 0.0")
	}
}

func TestCreateMessageParams_JSON(t *testing.T) {
	t.Parallel()
	temp := 0.7
	params := CreateMessageParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: TextContent{Text: "Summarize this"}},
		},
		ModelPreferences: &ModelPreferences{
			Hints: []ModelHint{{Name: "gpt-4"}},
		},
		SystemPrompt:   "You are a helpful assistant.",
		IncludeContext: "thisServer",
		Temperature:    &temp,
		MaxTokens:      1024,
		StopSequences:  []string{"\n\n"},
		Metadata:       map[string]any{"custom": "value"},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	// Verify structure via raw map instead of round-trip (Content is an
	// interface and doesn't support unmarshaling).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	// Check maxTokens.
	var maxTokens int
	if err := json.Unmarshal(raw["maxTokens"], &maxTokens); err != nil {
		t.Fatal(err)
	}
	if maxTokens != 1024 {
		t.Errorf("maxTokens = %d, want 1024", maxTokens)
	}

	// Check systemPrompt.
	var sysPrompt string
	if err := json.Unmarshal(raw["systemPrompt"], &sysPrompt); err != nil {
		t.Fatal(err)
	}
	if sysPrompt != "You are a helpful assistant." {
		t.Errorf("systemPrompt = %q", sysPrompt)
	}

	// Check temperature.
	var temperature float64
	if err := json.Unmarshal(raw["temperature"], &temperature); err != nil {
		t.Fatal(err)
	}
	if temperature != 0.7 {
		t.Errorf("temperature = %f, want 0.7", temperature)
	}

	// Check includeContext.
	var includeCtx string
	if err := json.Unmarshal(raw["includeContext"], &includeCtx); err != nil {
		t.Fatal(err)
	}
	if includeCtx != "thisServer" {
		t.Errorf("includeContext = %q", includeCtx)
	}

	// Check stopSequences.
	var stopSeqs []string
	if err := json.Unmarshal(raw["stopSequences"], &stopSeqs); err != nil {
		t.Fatal(err)
	}
	if len(stopSeqs) != 1 || stopSeqs[0] != "\n\n" {
		t.Errorf("stopSequences = %v", stopSeqs)
	}

	// Check messages is present.
	if _, ok := raw["messages"]; !ok {
		t.Error("missing 'messages'")
	}

	// Check modelPreferences.
	if _, ok := raw["modelPreferences"]; !ok {
		t.Error("missing 'modelPreferences'")
	}
}

func TestCreateMessageParams_OptionalFieldsOmitted(t *testing.T) {
	t.Parallel()
	params := CreateMessageParams{
		Messages:  []SamplingMessage{{Role: "user", Content: TextContent{Text: "hi"}}},
		MaxTokens: 100,
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	// Optional fields should not appear when zero/nil.
	for _, key := range []string{"modelPreferences", "systemPrompt", "temperature", "stopSequences", "metadata", "_meta"} {
		if _, ok := raw[key]; ok {
			t.Errorf("field %q should be omitted, but was present", key)
		}
	}
}

func TestCreateMessageResult_JSON(t *testing.T) {
	t.Parallel()
	result := CreateMessageResult{
		Role:       "assistant",
		Content:    json.RawMessage(`{"type":"text","text":"Hello!"}`),
		Model:      "claude-4-sonnet-20250514",
		StopReason: "endTurn",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	var decoded CreateMessageResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Role != "assistant" {
		t.Errorf("role = %q, want %q", decoded.Role, "assistant")
	}
	if decoded.Model != "claude-4-sonnet-20250514" {
		t.Errorf("model = %q", decoded.Model)
	}
	if decoded.StopReason != "endTurn" {
		t.Errorf("stopReason = %q", decoded.StopReason)
	}
}

func TestCreateMessageResult_StopReasonOmitted(t *testing.T) {
	t.Parallel()
	result := CreateMessageResult{
		Role:    "assistant",
		Content: json.RawMessage(`{"type":"text","text":"ok"}`),
		Model:   "test-model",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["stopReason"]; ok {
		t.Error("stopReason should be omitted when empty")
	}
}

// ── CreateMessage server method tests ───────────────────────────────

// initServerWithSampling creates an initialized server whose client has
// declared sampling capability.
func initServerWithSampling(t *testing.T) *Server {
	t.Helper()
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"sampling": map[string]any{},
		},
		"clientInfo": map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatalf("initialize: unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize: unexpected response error: %s", resp.Error.Message)
	}
	return s
}

// fakeSender returns a RequestSender that delivers the given result.
func fakeSender(result any, rpcErr *JSONRPCError) RequestSender {
	return func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      "fake-1",
			Result:  result,
			Error:   rpcErr,
		}, nil
	}
}

// failingSender returns a RequestSender that returns a transport error.
func failingSender(err error) RequestSender {
	return func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		return nil, err
	}
}

func validCreateParams() CreateMessageParams {
	return CreateMessageParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: TextContent{Text: "Hello"}},
		},
		MaxTokens: 100,
	}
}

func TestCreateMessage_Success(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	sender := fakeSender(map[string]any{
		"role":       "assistant",
		"content":    map[string]any{"type": "text", "text": "Hi there!"},
		"model":      "test-model",
		"stopReason": "endTurn",
	}, nil)

	ctx := WithRequestSender(context.Background(), sender)
	result, err := s.CreateMessage(ctx, validCreateParams())
	if err != nil {
		t.Fatal(err)
	}
	if result.Role != "assistant" {
		t.Errorf("role = %q, want %q", result.Role, "assistant")
	}
	if result.Model != "test-model" {
		t.Errorf("model = %q", result.Model)
	}
	if result.StopReason != "endTurn" {
		t.Errorf("stopReason = %q", result.StopReason)
	}
}

func TestCreateMessage_NotInitialized(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	_, err := s.CreateMessage(ctx, validCreateParams())
	if !errors.Is(err, errNotInitializedYet) {
		t.Errorf("expected errNotInitializedYet, got: %v", err)
	}
}

func TestCreateMessage_NoSamplingCapability(t *testing.T) {
	t.Parallel()
	// Initialize WITHOUT sampling capability.
	s := initServer(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	_, err := s.CreateMessage(ctx, validCreateParams())
	if !errors.Is(err, errSamplingNotSupported) {
		t.Errorf("expected errSamplingNotSupported, got: %v", err)
	}
}

func TestCreateMessage_NoRequestSender(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	// Call CreateMessage without a RequestSender in context.
	_, err := s.CreateMessage(context.Background(), validCreateParams())
	if !errors.Is(err, errNoRequestSender) {
		t.Errorf("expected errNoRequestSender, got: %v", err)
	}
}

func TestCreateMessage_EmptyMessages(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	params := CreateMessageParams{
		Messages:  []SamplingMessage{},
		MaxTokens: 100,
	}
	_, err := s.CreateMessage(ctx, params)
	if err == nil || err.Error() != "messages must not be empty" {
		t.Errorf("expected empty messages error, got: %v", err)
	}
}

func TestCreateMessage_NilMessages(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	params := CreateMessageParams{
		Messages:  nil,
		MaxTokens: 100,
	}
	_, err := s.CreateMessage(ctx, params)
	if err == nil || err.Error() != "messages must not be empty" {
		t.Errorf("expected empty messages error, got: %v", err)
	}
}

func TestCreateMessage_ZeroMaxTokens(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	params := CreateMessageParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: TextContent{Text: "hi"}},
		},
		MaxTokens: 0,
	}
	_, err := s.CreateMessage(ctx, params)
	if err == nil || err.Error() != "maxTokens must be positive" {
		t.Errorf("expected maxTokens error, got: %v", err)
	}
}

func TestCreateMessage_NegativeMaxTokens(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	params := CreateMessageParams{
		Messages: []SamplingMessage{
			{Role: "user", Content: TextContent{Text: "hi"}},
		},
		MaxTokens: -5,
	}
	_, err := s.CreateMessage(ctx, params)
	if err == nil || err.Error() != "maxTokens must be positive" {
		t.Errorf("expected maxTokens error, got: %v", err)
	}
}

func TestCreateMessage_TooManyMessages(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))

	// Build a params object with 1001 messages (exceeds maxSamplingMessages of 1000).
	messages := make([]SamplingMessage, 1001)
	for i := range messages {
		messages[i] = SamplingMessage{Role: "user", Content: TextContent{Text: "msg"}}
	}

	params := CreateMessageParams{
		Messages:  messages,
		MaxTokens: 100,
	}

	_, err := s.CreateMessage(ctx, params)
	if err == nil {
		t.Fatal("expected error for too many messages")
	}
	if got := err.Error(); got != "too many messages (max 1000)" {
		t.Errorf("error = %q, want %q", got, "too many messages (max 1000)")
	}
}

func TestCreateMessage_ClientReturnsError(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	sender := fakeSender(nil, &JSONRPCError{Code: -32600, Message: "refused"})
	ctx := WithRequestSender(context.Background(), sender)
	_, err := s.CreateMessage(ctx, validCreateParams())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "client returned error: refused (code -32600)" {
		t.Errorf("error = %q", got)
	}
}

func TestCreateMessage_TransportError(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	sender := failingSender(errors.New("connection reset"))
	ctx := WithRequestSender(context.Background(), sender)
	_, err := s.CreateMessage(ctx, validCreateParams())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "sampling request failed: connection reset" {
		t.Errorf("error = %q", got)
	}
}

func TestCreateMessage_ContextCancelled(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	// A sender that blocks forever until context is cancelled.
	sender := func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithRequestSender(ctx, sender)

	done := make(chan error, 1)
	go func() {
		_, err := s.CreateMessage(ctx, validCreateParams())
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
}

// ── PendingRequests tests ───────────────────────────────────────────

func TestPendingRequests_SendAndDeliver(t *testing.T) {
	t.Parallel()

	writtenCh := make(chan []byte, 1)
	pr := NewPendingRequests(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		writtenCh <- cp
		return nil
	})

	done := make(chan struct{})
	var result *JSONRPCResponse
	var sendErr error

	go func() {
		defer close(done)
		result, sendErr = pr.Send(context.Background(), "sampling/createMessage", map[string]any{"maxTokens": 100})
	}()

	// Wait for the request to be written.
	var written []byte
	select {
	case written = <-writtenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request to be written")
	}

	// Parse the written request to get the ID.
	var req struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
	}
	if err := json.Unmarshal(written, &req); err != nil {
		t.Fatalf("unmarshal written request: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", req.JSONRPC)
	}
	if req.Method != "sampling/createMessage" {
		t.Errorf("method = %q", req.Method)
	}
	if req.ID == "" {
		t.Fatal("request ID is empty")
	}

	// Deliver a matching response.
	respData, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"role":    "assistant",
			"content": map[string]any{"type": "text", "text": "Hi!"},
			"model":   "test-model",
		},
	})
	if !pr.Deliver(respData) {
		t.Fatal("Deliver returned false")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Send to return")
	}

	if sendErr != nil {
		t.Fatalf("Send error: %v", sendErr)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error != nil {
		t.Errorf("unexpected error in response: %+v", result.Error)
	}
}

func TestPendingRequests_SendContextCancelled(t *testing.T) {
	t.Parallel()

	writtenCh := make(chan struct{}, 1)
	pr := NewPendingRequests(func(data []byte) error {
		writtenCh <- struct{}{}
		return nil // accept the write
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := pr.Send(ctx, "test/method", nil)
		done <- err
	}()

	// Wait for the request to be written, then cancel.
	select {
	case <-writtenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func TestPendingRequests_SendWriteError(t *testing.T) {
	t.Parallel()

	pr := NewPendingRequests(func(data []byte) error {
		return errors.New("broken pipe")
	})

	_, err := pr.Send(context.Background(), "test/method", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "write request: broken pipe" {
		t.Errorf("error = %q", got)
	}
}

func TestPendingRequests_CloseAllCancelsPending(t *testing.T) {
	t.Parallel()

	writtenCh := make(chan struct{}, 1)
	pr := NewPendingRequests(func(data []byte) error {
		writtenCh <- struct{}{}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := pr.Send(context.Background(), "test/method", nil)
		done <- err
	}()

	// Wait for the request to be sent, then close.
	select {
	case <-writtenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write")
	}
	pr.CloseAll()

	select {
	case err := <-done:
		if err == nil || err.Error() != "transport closed" {
			t.Errorf("expected 'transport closed' error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out; CloseAll did not unblock Send")
	}
}

func TestPendingRequests_SendAfterClose(t *testing.T) {
	t.Parallel()

	pr := NewPendingRequests(func(data []byte) error {
		return nil
	})
	pr.CloseAll()

	_, err := pr.Send(context.Background(), "test/method", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "transport closed" {
		t.Errorf("error = %q", got)
	}
}

func TestPendingRequests_DeliverNoMatch(t *testing.T) {
	t.Parallel()

	pr := NewPendingRequests(func(data []byte) error {
		return nil
	})

	respData, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "nonexistent",
		"result":  map[string]any{},
	})
	if pr.Deliver(respData) {
		t.Error("Deliver should return false for unknown ID")
	}
}

func TestPendingRequests_DeliverInvalidJSON(t *testing.T) {
	t.Parallel()

	pr := NewPendingRequests(func(data []byte) error {
		return nil
	})

	if pr.Deliver([]byte("{bad json")) {
		t.Error("Deliver should return false for invalid JSON")
	}
}

func TestPendingRequests_ConcurrentSends(t *testing.T) {
	t.Parallel()

	const n = 10
	writtenCh := make(chan string, n) // receives request IDs as they are written

	pr := NewPendingRequests(func(data []byte) error {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			return err
		}
		writtenCh <- req.ID
		return nil
	})

	results := make(chan error, n)

	for i := 0; i < n; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := pr.Send(ctx, "test/method", nil)
			results <- err
		}()
	}

	// Collect all written request IDs.
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		select {
		case id := <-writtenCh:
			ids = append(ids, id)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for request %d to be written", i)
		}
	}

	if len(ids) != n {
		t.Fatalf("expected %d requests, got %d", n, len(ids))
	}

	// Deliver responses for all pending requests.
	for _, id := range ids {
		respData, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  map[string]any{"role": "assistant"},
		})
		pr.Deliver(respData)
	}

	// Collect all results.
	for i := 0; i < n; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("Send %d returned error: %v", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for Send %d", i)
		}
	}
}

func TestPendingRequests_UniqueIDs(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	ids := make(map[string]bool)
	var duplicates []string

	pr := NewPendingRequests(func(data []byte) error {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if ids[req.ID] {
			duplicates = append(duplicates, req.ID)
		}
		ids[req.ID] = true
		return nil
	})

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			pr.Send(ctx, "test/method", nil)
		}()
	}

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(duplicates) > 0 {
		t.Errorf("duplicate IDs found: %v", duplicates)
	}
	if len(ids) != n {
		t.Errorf("expected %d unique IDs, got %d", n, len(ids))
	}
}

// ── IsResponse tests ────────────────────────────────────────────────

func TestIsResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want bool
	}{
		{
			name: "success response",
			data: `{"jsonrpc":"2.0","id":"srv-1","result":{"role":"assistant"}}`,
			want: true,
		},
		{
			name: "error response",
			data: `{"jsonrpc":"2.0","id":"srv-1","error":{"code":-32600,"message":"bad"}}`,
			want: true,
		},
		{
			name: "numeric ID response",
			data: `{"jsonrpc":"2.0","id":42,"result":{}}`,
			want: true,
		},
		{
			name: "request (has method)",
			data: `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
			want: false,
		},
		{
			name: "notification (no id)",
			data: `{"jsonrpc":"2.0","method":"notifications/progress","params":{}}`,
			want: false,
		},
		{
			name: "request with result-like method value",
			data: `{"jsonrpc":"2.0","id":1,"method":"tools/call","result":{}}`,
			want: false,
		},
		{
			name: "empty object",
			data: `{}`,
			want: false,
		},
		{
			name: "id but no result/error",
			data: `{"jsonrpc":"2.0","id":"x"}`,
			want: false,
		},
		{
			name: "invalid JSON",
			data: `{bad`,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsResponse([]byte(tc.data))
			if got != tc.want {
				t.Errorf("IsResponse(%s) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// ── Context helpers tests ───────────────────────────────────────────

func TestRequestSenderContext(t *testing.T) {
	t.Parallel()

	// Without sender in context.
	if got := RequestSenderFromCtx(context.Background()); got != nil {
		t.Error("expected nil sender from background context")
	}

	// With sender in context.
	sender := func(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
		return nil, nil
	}
	ctx := WithRequestSender(context.Background(), sender)
	got := RequestSenderFromCtx(ctx)
	if got == nil {
		t.Error("expected non-nil sender")
	}
}

// ── Method constant test ────────────────────────────────────────────

func TestMethodSamplingCreateMessage(t *testing.T) {
	t.Parallel()
	if methodSamplingCreateMessage != "sampling/createMessage" {
		t.Errorf("method = %q, want %q", methodSamplingCreateMessage, "sampling/createMessage")
	}
}

// ── Client capability plumbing test ─────────────────────────────────

func TestInitialize_StoresSamplingCapability(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"sampling": map[string]any{},
		},
		"clientInfo": map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %s", resp.Error.Message)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.clientCaps.Sampling == nil {
		t.Error("expected clientCaps.Sampling to be set")
	}
}

func TestInitialize_NoSamplingCapability(t *testing.T) {
	t.Parallel()
	s := NewServer("test", "1.0")

	data := jsonrpcReq(1, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "testclient", "version": "0.1"},
	})

	resp, err := s.HandleMessage(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("error: %s", resp.Error.Message)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.clientCaps.Sampling != nil {
		t.Error("expected clientCaps.Sampling to be nil")
	}
}

// ── End-to-end: CreateMessage through PendingRequests ───────────────

func TestCreateMessage_EndToEndWithPendingRequests(t *testing.T) {
	t.Parallel()

	// Simulate a transport: use PendingRequests with an in-memory pipe.
	writtenCh := make(chan []byte, 1)
	pr := NewPendingRequests(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		writtenCh <- cp
		return nil
	})
	defer pr.CloseAll()

	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), pr.Send)
	params := validCreateParams()

	done := make(chan struct{})
	var result *CreateMessageResult
	var createErr error

	go func() {
		defer close(done)
		result, createErr = s.CreateMessage(ctx, params)
	}()

	// Wait for the request to be written.
	var writtenData []byte
	select {
	case writtenData = <-writtenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request to be written")
	}

	// Extract the request ID.
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(writtenData, &req); err != nil {
		t.Fatal(err)
	}

	// Deliver a response.
	respData, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"role":       "assistant",
			"content":    map[string]any{"type": "text", "text": "End-to-end works!"},
			"model":      "e2e-model",
			"stopReason": "endTurn",
		},
	})
	if !pr.Deliver(respData) {
		t.Fatal("Deliver returned false")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}

	if createErr != nil {
		t.Fatalf("CreateMessage error: %v", createErr)
	}
	if result.Role != "assistant" {
		t.Errorf("role = %q", result.Role)
	}
	if result.Model != "e2e-model" {
		t.Errorf("model = %q", result.Model)
	}
	if result.StopReason != "endTurn" {
		t.Errorf("stopReason = %q", result.StopReason)
	}
}

// ── IncludeContext validation ─────────────────────────────────────────

func TestCreateMessage_ValidIncludeContext(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	for _, val := range []string{"", "none", "thisServer", "allServers"} {
		sender := fakeSender(map[string]any{
			"role":    "assistant",
			"content": map[string]any{"type": "text", "text": "ok"},
			"model":   "m",
		}, nil)
		ctx := WithRequestSender(context.Background(), sender)
		params := validCreateParams()
		params.IncludeContext = val
		if _, err := s.CreateMessage(ctx, params); err != nil {
			t.Errorf("includeContext=%q should be valid, got: %v", val, err)
		}
	}
}

func TestCreateMessage_InvalidIncludeContext(t *testing.T) {
	t.Parallel()
	s := initServerWithSampling(t)

	ctx := WithRequestSender(context.Background(), fakeSender(nil, nil))
	params := validCreateParams()
	params.IncludeContext = "everything"
	_, err := s.CreateMessage(ctx, params)
	if err == nil {
		t.Fatal("expected error for invalid includeContext")
	}
	if got := err.Error(); got != `invalid includeContext "everything": must be none, thisServer, or allServers` {
		t.Errorf("error = %q", got)
	}
}

// ── Deliver with client error response ──────────────────────────────

func TestPendingRequests_DeliverErrorResponse(t *testing.T) {
	t.Parallel()

	writtenCh := make(chan string, 1) // receives the request ID
	pr := NewPendingRequests(func(data []byte) error {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			return err
		}
		writtenCh <- req.ID
		return nil
	})

	done := make(chan struct{})
	var result *JSONRPCResponse
	var sendErr error

	go func() {
		defer close(done)
		result, sendErr = pr.Send(context.Background(), "test/method", nil)
	}()

	// Wait for the request to be written and get the ID.
	var id string
	select {
	case id = <-writtenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write")
	}

	respData, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32600,
			"message": "request denied",
		},
	})

	if !pr.Deliver(respData) {
		t.Fatal("Deliver returned false")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	if sendErr != nil {
		t.Fatalf("Send error: %v", sendErr)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error == nil {
		t.Fatal("expected error in response")
	}
	if result.Error.Code != -32600 {
		t.Errorf("error code = %d, want -32600", result.Error.Code)
	}
}
