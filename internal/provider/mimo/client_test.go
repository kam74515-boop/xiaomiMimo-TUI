package mimo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
)

func TestChatStreamParsesOpenAISSE(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotReq core.ChatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL + "/v1",
		APIKey:  "secret",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{
		Messages: []core.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var content string
	var usage *core.CostUpdate
	done := false
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			t.Fatalf("unexpected model error: %v", event.Err)
		}
		content += event.Delta
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Done {
			done = true
		}
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("request path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization = %q, want bearer token", gotAuth)
	}
	if gotReq.Model != "mimo-test" {
		t.Fatalf("model = %q, want mimo-test", gotReq.Model)
	}
	if !gotReq.Stream {
		t.Fatal("stream flag was not forced on")
	}
	if content != "hello" {
		t.Fatalf("content = %q, want hello", content)
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 2 || usage.TotalTokens != 5 {
		t.Fatalf("usage = %#v, want 3/2/5 tokens", usage)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func TestChatStreamParsesToolCallDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEChunk(t, w, map[string]any{
			"choices": []any{
				map[string]any{
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"index": 0,
								"id":    "call_1",
								"type":  "function",
								"function": map[string]any{
									"name":      "read_",
									"arguments": `{"pa`,
								},
							},
						},
					},
				},
			},
		})
		writeSSEChunk(t, w, map[string]any{
			"choices": []any{
				map[string]any{
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"index": 0,
								"function": map[string]any{
									"name":      "file",
									"arguments": `th":"README.md"}`,
								},
							},
						},
					},
				},
			},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "secret",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var calls []core.ToolCall
	done := false
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			t.Fatalf("unexpected model error: %v", event.Err)
		}
		calls = append(calls, event.ToolCalls...)
		if event.Done {
			done = true
		}
	}

	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v, want two accumulated snapshots", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "read_" || calls[0].Raw != `{"pa` || calls[0].Input != nil {
		t.Fatalf("first tool call = %#v, want partial accumulated call", calls[0])
	}
	last := calls[len(calls)-1]
	if last.ID != "call_1" || last.Name != "read_file" || last.Raw != `{"path":"README.md"}` {
		t.Fatalf("last tool call = %#v, want accumulated name and raw args", last)
	}
	if last.Input == nil || last.Input["path"] != "README.md" {
		t.Fatalf("last input = %#v, want parsed path", last.Input)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func TestChatStreamEmitsStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"quota exceeded\"}}\n\n")
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "secret",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "quota exceeded") {
		t.Fatalf("error = %v, want quota exceeded", gotErr)
	}
}

func TestChatStreamUsesMockFallbackWithoutAPIKey(t *testing.T) {
	client := New(config.ProviderConfig{
		BaseURL: "://invalid-url",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{
		Messages: []core.Message{{Role: "user", Content: "say hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var content string
	var usage *core.CostUpdate
	done := false
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			t.Fatalf("unexpected model error: %v", event.Err)
		}
		content += event.Delta
		if event.Usage != nil {
			usage = event.Usage
		}
		if event.Done {
			done = true
		}
	}
	if !strings.Contains(content, "say hi") {
		t.Fatalf("mock content = %q, want prompt echoed", content)
	}
	if usage == nil || usage.TotalTokens == 0 {
		t.Fatalf("usage = %#v, want non-zero mock usage", usage)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func writeSSEChunk(t *testing.T, w http.ResponseWriter, chunk any) {
	t.Helper()
	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal SSE chunk: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func TestClientHandles401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "bad-key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error for 401 response")
	}
	if !strings.Contains(gotErr.Error(), "MiMo API authentication failed") {
		t.Fatalf("error = %q, want authentication failed message", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "Check MIMO_API_KEY") {
		t.Fatalf("error = %q, want hint to check MIMO_API_KEY", gotErr.Error())
	}
}

func TestClientHandles429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limit exceeded"))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error for 429 response")
	}
	if !strings.Contains(gotErr.Error(), "MiMo API rate limit exceeded") {
		t.Fatalf("error = %q, want rate limit message", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "Retry-After: 30") {
		t.Fatalf("error = %q, want Retry-After info", gotErr.Error())
	}
}

func TestClientHandles5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream error"))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error for 502 response")
	}
	if !strings.Contains(gotErr.Error(), "MiMo API temporarily unavailable") {
		t.Fatalf("error = %q, want temporarily unavailable message", gotErr.Error())
	}
}

func TestClientRetriesOn502(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("upstream error"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var content string
	done := false
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			t.Fatalf("unexpected model error: %v", event.Err)
		}
		content += event.Delta
		if event.Done {
			done = true
		}
	}

	if requestCount < 2 {
		t.Fatalf("request count = %d, want at least 2 (retry occurred)", requestCount)
	}
	if content != "ok" {
		t.Fatalf("content = %q, want ok", content)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func TestClientDoesNotRetryOn401(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "bad-key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error for 401 response")
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1 (no retry on 401)", requestCount)
	}
}

func TestClientConnectionRefused(t *testing.T) {
	// Use a closed server to simulate connection refused.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close() // immediately close to cause connection refused

	client := New(config.ProviderConfig{
		BaseURL: serverURL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil {
		t.Fatal("expected an error for connection refused")
	}
	if !strings.Contains(gotErr.Error(), "Cannot reach MiMo API at") {
		t.Fatalf("error = %q, want 'Cannot reach MiMo API at' message", gotErr.Error())
	}
}

func TestHealthCheckReturnsNilWhenReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck returned error for reachable server: %v", err)
	}
}

func TestHealthCheckReturnsNilEvenWithErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck should return nil even on error status, got: %v", err)
	}
}

func TestHealthCheckReturnsErrorWhenUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close()

	client := New(config.ProviderConfig{
		BaseURL: serverURL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	err := client.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("HealthCheck should return error for unreachable server")
	}
	if !strings.Contains(err.Error(), "Cannot reach MiMo API at") {
		t.Fatalf("error = %q, want 'Cannot reach MiMo API at' message", err.Error())
	}
}

func TestHealthCheckMockReturnsNil(t *testing.T) {
	client := NewMock("mimo-test")
	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck on mock client returned error: %v", err)
	}
}

func collectModelEvents(t *testing.T, events <-chan core.ModelEvent) []core.ModelEvent {
	t.Helper()
	var got []core.ModelEvent
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, event)
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for model events")
		}
	}
}
