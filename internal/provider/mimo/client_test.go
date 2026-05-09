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

	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v, want one completed accumulated call", calls)
	}
	last := calls[0]
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

func TestNormalizeRequestSwitchesToMultimodalModelForMedia(t *testing.T) {
	t.Setenv("MIMO_MULTIMODAL_MODEL", "")
	client := New(config.ProviderConfig{Model: "mimo-v2.5-pro", Mock: true})
	req := client.normalizeRequest(core.ChatRequest{
		Messages: []core.Message{
			{
				Role: "user",
				ContentParts: []core.ContentPart{
					{Type: "image_url", ImageURL: &core.ImageURLPart{URL: "data:image/png;base64,abc"}},
					{Type: "text", Text: "describe"},
				},
			},
		},
	})

	if req.Model != DefaultMultimodalModel {
		t.Fatalf("model = %q, want %q for media input", req.Model, DefaultMultimodalModel)
	}
}

func TestNormalizeRequestKeepsMultimodalModel(t *testing.T) {
	client := New(config.ProviderConfig{Model: "mimo-v2-omni", Mock: true})
	req := client.normalizeRequest(core.ChatRequest{
		Messages: []core.Message{
			{
				Role: "user",
				ContentParts: []core.ContentPart{
					{Type: "input_audio", InputAudio: &core.InputAudioPart{Data: "https://example.com/a.wav"}},
				},
			},
		},
	})

	if req.Model != "mimo-v2-omni" {
		t.Fatalf("model = %q, want mimo-v2-omni", req.Model)
	}
}

func TestChatStreamEncodesMultimodalContentArray(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-v2.5-pro",
	})
	events, err := client.ChatStream(context.Background(), core.ChatRequest{
		Messages: []core.Message{
			{
				Role: "user",
				ContentParts: []core.ContentPart{
					{Type: "image_url", ImageURL: &core.ImageURLPart{URL: "data:image/png;base64,abc"}},
					{Type: "text", Text: "describe"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	_ = collectModelEvents(t, events)

	if got["model"] != DefaultMultimodalModel {
		t.Fatalf("model = %#v, want multimodal fallback", got["model"])
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message = %#v", messages[0])
	}
	content, ok := first["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v, want two content parts", first["content"])
	}
}

func TestChatStreamRetriesWithoutWebSearchWhenPluginDisabled(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, got)
		if len(requests) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"web search tool found in the request body, but webSearchEnabled is false"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"fallback\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-v2.5-pro",
	})
	events, err := client.ChatStream(context.Background(), core.ChatRequest{
		Messages: []core.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "latest news"}},
		Tools:    []core.ToolSpec{{Type: "web_search", MaxKeyword: 3, Limit: 1}},
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	var content string
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			t.Fatalf("unexpected event error: %v", event.Err)
		}
		content += event.Delta
	}
	if content != "fallback" {
		t.Fatalf("content = %q, want fallback", content)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want retry without web_search", len(requests))
	}
	firstTools := requests[0]["tools"].([]any)
	if len(firstTools) != 1 {
		t.Fatalf("first tools = %#v", firstTools)
	}
	if secondTools, ok := requests[1]["tools"].([]any); ok && len(secondTools) != 0 {
		t.Fatalf("second tools = %#v, want web_search stripped", secondTools)
	}
	messages := requests[1]["messages"].([]any)
	systemMessage := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(systemMessage, "Web Search Plugin is not enabled") {
		t.Fatalf("fallback system message missing runtime note: %q", systemMessage)
	}

	requests = nil
	events, err = client.ChatStream(context.Background(), core.ChatRequest{
		Messages: []core.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "another turn"}},
		Tools:    []core.ToolSpec{{Type: "web_search", MaxKeyword: 3, Limit: 1}},
	})
	if err != nil {
		t.Fatalf("second ChatStream returned error: %v", err)
	}
	_ = collectModelEvents(t, events)
	if len(requests) != 1 {
		t.Fatalf("second turn requests = %d, want cached disabled state to avoid retry", len(requests))
	}
	if secondTools, ok := requests[0]["tools"].([]any); ok && len(secondTools) != 0 {
		t.Fatalf("cached-disabled request tools = %#v, want web_search stripped", secondTools)
	}
}

func TestChatStreamDoesNotRetryForcedWebSearchWhenPluginDisabled(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"web search tool found in the request body, but webSearchEnabled is false"}}`))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-v2.5-pro",
	})
	events, err := client.ChatStream(context.Background(), core.ChatRequest{
		Messages: []core.Message{{Role: "user", Content: "latest news"}},
		Tools:    []core.ToolSpec{{Type: "web_search", ForceSearch: true}},
	})
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}
	var gotErr error
	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "webSearchEnabled is false") {
		t.Fatalf("error = %v, want disabled web search error", gotErr)
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want no retry for forced search", requestCount)
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

func TestToolCallMarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		tc   core.ToolCall
		want map[string]any
	}{
		{
			name: "full tool call",
			tc:   core.ToolCall{ID: "call_1", Name: "read_file", Raw: `{"path":"README.md"}`},
			want: map[string]any{
				"id":   "call_1",
				"type": "function",
				"function": map[string]any{
					"name":      "read_file",
					"arguments": `{"path":"README.md"}`,
				},
			},
		},
		{
			name: "tool call without id",
			tc:   core.ToolCall{Name: "list_dir", Raw: `{}`},
			want: map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":      "list_dir",
					"arguments": `{}`,
				},
			},
		},
		{
			name: "empty raw falls back to empty json",
			tc:   core.ToolCall{Name: "git_status", Raw: ""},
			want: map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":      "git_status",
					"arguments": `{}`,
				},
			},
		},
		{
			name: "raw from input marshaling",
			tc:   core.ToolCall{Name: "shell", Input: core.ToolInput{"command": "go test ./..."}},
			want: map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":      "shell",
					"arguments": `{"command":"go test ./..."}`,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.tc)
			if err != nil {
				t.Fatalf("MarshalJSON error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}

			if got["type"] != test.want["type"] {
				t.Fatalf("type = %v, want %v", got["type"], test.want["type"])
			}
			wantFn := test.want["function"].(map[string]any)
			gotFn, ok := got["function"].(map[string]any)
			if !ok {
				t.Fatalf("missing 'function' key in %s", data)
			}
			if gotFn["name"] != wantFn["name"] {
				t.Fatalf("function.name = %v, want %v", gotFn["name"], wantFn["name"])
			}
			if gotFn["arguments"] != wantFn["arguments"] {
				t.Fatalf("function.arguments = %v, want %v", gotFn["arguments"], wantFn["arguments"])
			}
			if _, hasID := test.want["id"]; hasID {
				if got["id"] != test.want["id"] {
					t.Fatalf("id = %v, want %v", got["id"], test.want["id"])
				}
			}
		})
	}
}

func TestToolCallUnmarshalJSON(t *testing.T) {
	input := []byte(`{"id":"call_2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"main.go\"}"}}`)
	var tc core.ToolCall
	if err := json.Unmarshal(input, &tc); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	if tc.ID != "call_2" {
		t.Fatalf("ID = %q, want call_2", tc.ID)
	}
	if tc.Name != "read_file" {
		t.Fatalf("Name = %q, want read_file", tc.Name)
	}
	if tc.Raw != `{"path":"main.go"}` {
		t.Fatalf("Raw = %q, want {\"path\":\"main.go\"}", tc.Raw)
	}
	if tc.Input == nil || tc.Input["path"] != "main.go" {
		t.Fatalf("Input = %#v, want parsed path", tc.Input)
	}
}

func TestMessageSerialization(t *testing.T) {
	// Assistant message with tool_calls must have content field (even empty).
	msg := core.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []core.ToolCall{
			{ID: "call_3", Name: "read_file", Raw: `{"path":"x.go"}`},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal assistant message: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Content must be present (not omitted).
	content, hasContent := raw["content"]
	if !hasContent {
		t.Fatalf("content field is missing for assistant message with tool_calls; got keys: %v", raw)
	}
	if content != "" {
		t.Fatalf("content = %q, want empty string", content)
	}

	// Tool message must include tool_call_id.
	toolMsg := core.Message{
		Role:       "tool",
		Content:    "result text",
		ToolCallID: "call_3",
	}
	data2, err := json.Marshal(toolMsg)
	if err != nil {
		t.Fatalf("marshal tool message: %v", err)
	}
	var raw2 map[string]any
	if err := json.Unmarshal(data2, &raw2); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if raw2["tool_call_id"] != "call_3" {
		t.Fatalf("tool_call_id = %v, want call_3", raw2["tool_call_id"])
	}

	// User message normal serialization.
	userMsg := core.Message{Role: "user", Content: "hello world"}
	data3, err := json.Marshal(userMsg)
	if err != nil {
		t.Fatalf("marshal user message: %v", err)
	}
	var raw3 map[string]any
	if err := json.Unmarshal(data3, &raw3); err != nil {
		t.Fatalf("unmarshal user result: %v", err)
	}
	if raw3["role"] != "user" {
		t.Fatalf("role = %v, want user", raw3["role"])
	}
	if raw3["content"] != "hello world" {
		t.Fatalf("content = %v, want hello world", raw3["content"])
	}
}

func TestChatRequestSerialization(t *testing.T) {
	req := core.ChatRequest{
		Model: "mimo-v2.5-pro",
		Messages: []core.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "hello"},
		},
		Stream: true,
		Tools: []core.ToolSpec{
			{
				Type: "function",
				Function: core.ToolFunctionSpec{
					Name:        "list_dir",
					Description: "List directory contents",
					Parameters:  core.JSONSchema{"type": "object", "properties": map[string]any{}},
				},
			},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal ChatRequest: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal ChatRequest: %v", err)
	}

	tools, ok := raw["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want 1 tool in array", raw["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("tool.type = %v, want 'function'", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "list_dir" {
		t.Fatalf("tool.function.name = %v, want list_dir", fn["name"])
	}
	if fn["description"] != "List directory contents" {
		t.Fatalf("tool.function.description = %v, want 'List directory contents'", fn["description"])
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatal("tool.function.parameters missing or not an object")
	}
	if params["type"] != "object" {
		t.Fatalf("parameters.type = %v, want object", params["type"])
	}
}

func TestMalformedToolArguments(t *testing.T) {
	// Direct unit test: parseToolInput returns nil for malformed JSON (no panic).
	if input := parseToolInput("{not valid"); input != nil {
		t.Fatalf("parseToolInput with malformed JSON should return nil, got %v", input)
	}
	if input := parseToolInput(""); input != nil {
		t.Fatalf("parseToolInput with empty string should return nil, got %v", input)
	}
	if input := parseToolInput(`{"key":"val"}`); input == nil || input["key"] != "val" {
		t.Fatalf("parseToolInput with valid JSON should parse correctly, got %v", input)
	}

	// Also test accumulated malformed arguments through tool call state.
	s := toolCallState{id: "x", name: "y", arguments: `{broken}`}
	tc := s.toolCall()
	if tc.Input != nil {
		t.Fatalf("toolCall with malformed accumulated arguments: Input should be nil, got %v", tc.Input)
	}
	if tc.Raw != `{broken}` {
		t.Fatalf("toolCall Raw = %q, want {broken}", tc.Raw)
	}

	// Now test through SSE stream with malformed arguments.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEChunk(t, w, map[string]any{
			"choices": []any{
				map[string]any{
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"index": 0,
								"id":    "call_mal",
								"type":  "function",
								"function": map[string]any{
									"name":      "shell",
									"arguments": `{not valid json`,
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
		APIKey:  "key",
		Model:   "mimo-test",
	})

	events, err := client.ChatStream(context.Background(), core.ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	for _, event := range collectModelEvents(t, events) {
		if event.Err != nil {
			t.Fatalf("unexpected error: %v", event.Err)
		}
		for _, tc := range event.ToolCalls {
			// Malformed args: Raw preserved, Input must be nil (no panic).
			if tc.Raw != `{not valid json` {
				t.Fatalf("Raw = %q, want preserved raw string", tc.Raw)
			}
			if tc.Input != nil {
				t.Fatalf("Input = %#v, want nil for malformed JSON", tc.Input)
			}
		}
	}
}

func TestClientHandles400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"model not found: bad-model"}}`))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "bad-model",
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
		t.Fatal("expected an error for 400 response")
	}
	if !strings.Contains(gotErr.Error(), "400") {
		t.Fatalf("error = %q, want status code 400", gotErr.Error())
	}
	if !strings.Contains(gotErr.Error(), "model not found") {
		t.Fatalf("error = %q, want body message", gotErr.Error())
	}
}

func TestClientDoesNotRetryOn400(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer server.Close()

	client := New(config.ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "key",
		Model:   "mimo-test",
	})

	events, _ := client.ChatStream(context.Background(), core.ChatRequest{})
	for _, event := range collectModelEvents(t, events) {
		_ = event.Err
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1 (no retry on 400)", requestCount)
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
