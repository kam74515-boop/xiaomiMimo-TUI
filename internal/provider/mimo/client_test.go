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
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for model events")
		}
	}
}
