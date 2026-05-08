package benchmark

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

func TestRunE2ESkipsWithoutAPIKey(t *testing.T) {
	// With empty API key and no env var, RunE2E should return nil, nil.
	t.Setenv("MIMO_API_KEY", "")
	results, err := RunE2E(E2EConfig{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results when API key is missing, got %d results", len(results))
	}
}

func TestRunE2ESkipsWithEmptyAPIKey(t *testing.T) {
	t.Setenv("MIMO_API_KEY", "")
	results, err := RunE2E(E2EConfig{APIKey: "   "})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results for whitespace-only API key, got %d results", len(results))
	}
}

func TestClassifyFailureTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"deadline exceeded", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFailure(tt.err, nil)
			if got != tt.want {
				t.Fatalf("classifyFailure(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyFailureAPIError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"auth failed", errors.New("mimo: MiMo API authentication failed. Check MIMO_API_KEY. (status 401)"), "api_error"},
		{"rate limit", errors.New("mimo: MiMo API rate limit exceeded (status 429)"), "api_error"},
		{"unavailable", errors.New("mimo: MiMo API temporarily unavailable (status 502)"), "api_error"},
		{"connection refused", errors.New("mimo: Cannot reach MiMo API at https://api.example.com: connection refused"), "api_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFailure(tt.err, nil)
			if got != tt.want {
				t.Fatalf("classifyFailure(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyFailureStreamError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"stream error", errors.New("mimo: stream error: quota exceeded"), "stream_error"},
		{"decode error", errors.New("mimo: decode stream chunk: invalid character"), "stream_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyFailure(tt.err, nil)
			if got != tt.want {
				t.Fatalf("classifyFailure(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyFailureMaxSteps(t *testing.T) {
	err := errors.New("agent loop reached max steps limit (16)")
	got := classifyFailure(err, nil)
	if got != "max_steps" {
		t.Fatalf("classifyFailure(%v) = %q, want %q", err, got, "max_steps")
	}
}

func TestClassifyFailureToolParseError(t *testing.T) {
	events := []core.AgentEvent{
		{
			Type: core.EventError,
			Err:  "failed to decode tool arguments: invalid JSON",
		},
	}
	err := errors.New("some error after tool failure")
	got := classifyFailure(err, events)
	if got != "tool_parse_error" {
		t.Fatalf("classifyFailure with tool parse error event = %q, want %q", got, "tool_parse_error")
	}
}

func TestClassifyFailureNoToolCalls(t *testing.T) {
	// Error with no tool_start events -> no_tool_calls.
	err := errors.New("model returned empty response")
	events := []core.AgentEvent{
		{Type: core.EventMessageDelta, Message: "hello"},
	}
	got := classifyFailure(err, events)
	if got != "no_tool_calls" {
		t.Fatalf("classifyFailure with no tool calls = %q, want %q", got, "no_tool_calls")
	}
}

func TestClassifyFailureNilError(t *testing.T) {
	got := classifyFailure(nil, nil)
	if got != "" {
		t.Fatalf("classifyFailure(nil, nil) = %q, want empty string", got)
	}
}

func TestClassifyFailureUnknown(t *testing.T) {
	// An error with tool calls present but no recognized pattern -> unknown.
	events := []core.AgentEvent{
		{Type: core.EventToolStart, ToolName: "read_file"},
	}
	err := errors.New("something completely unexpected happened")
	got := classifyFailure(err, events)
	if got != "unknown" {
		t.Fatalf("classifyFailure with unrecognized error = %q, want %q", got, "unknown")
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "***"},
		{"short", "***"},
		{"12345678", "***"},
		{"123456789", "12345678..."},
		{"abcdefghijklmnop", "abcdefgh..."},
		{"https://api.example.com/v1", "https://..."},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskAPIKey(tt.input)
			if got != tt.want {
				t.Fatalf("maskAPIKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultE2ETasksCount(t *testing.T) {
	tasks := DefaultE2ETasks()
	if len(tasks) != 5 {
		t.Fatalf("DefaultE2ETasks() returned %d tasks, want 5", len(tasks))
	}

	names := map[string]bool{}
	for _, task := range tasks {
		names[task.Name] = true
		if task.Prompt == "" {
			t.Fatalf("task %q has empty prompt", task.Name)
		}
		if task.MaxSteps <= 0 {
			t.Fatalf("task %q has non-positive MaxSteps: %d", task.Name, task.MaxSteps)
		}
		if task.Timeout <= 0 {
			t.Fatalf("task %q has non-positive Timeout", task.Name)
		}
	}

	expected := []string{"simple_question", "read_file", "search_code", "generate_patch", "run_test"}
	for _, name := range expected {
		if !names[name] {
			t.Fatalf("missing expected task %q", name)
		}
	}
}

func TestCountSteps(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventTraceUpdate, Trace: &core.TraceStep{Status: core.TraceDone}},
		{Type: core.EventTraceUpdate, Trace: &core.TraceStep{Status: core.TraceRunning}},
		{Type: core.EventTraceUpdate, Trace: &core.TraceStep{Status: core.TraceDone}},
		{Type: core.EventMessageDelta, Message: "hello"},
	}
	got := countSteps(events)
	if got != 2 {
		t.Fatalf("countSteps = %d, want 2", got)
	}
}

func TestCountToolCalls(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventToolStart, ToolName: "read_file"},
		{Type: core.EventToolResult, ToolName: "read_file"},
		{Type: core.EventToolStart, ToolName: "shell"},
		{Type: core.EventDone},
	}
	got := countToolCalls(events)
	if got != 2 {
		t.Fatalf("countToolCalls = %d, want 2", got)
	}
}

func TestRunE2EWithMockClient(t *testing.T) {
	// This test verifies the runner mechanics using a local httptest server
	// that returns a valid SSE response without tool calls.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello!\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1,\"total_tokens\":6}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	task := E2ETask{
		Name:              "mock_simple",
		Prompt:            "say hello",
		ExpectedToolCalls: 0,
		MaxSteps:          2,
		Timeout:           30 * time.Second,
	}

	results, err := RunE2E(E2EConfig{
		BaseURL: server.URL,
		APIKey:  "test-key-for-mock",
		Tasks:   []E2ETask{task},
		Timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunE2E returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].TaskName != "mock_simple" {
		t.Fatalf("task name = %q, want mock_simple", results[0].TaskName)
	}
	if !results[0].Success {
		t.Fatalf("expected success, got error class=%q msg=%q", results[0].ErrorClass, results[0].ErrorMessage)
	}
}

// TestE2EResultFields verifies the E2EResult struct has the expected fields.
func TestE2EResultFields(t *testing.T) {
	r := E2EResult{
		TaskName:     "test",
		Success:      true,
		ErrorClass:   "",
		Steps:        3,
		ToolCalls:    2,
		Duration:     5 * time.Second,
		ErrorMessage: "",
	}
	if r.TaskName != "test" {
		t.Fatal("TaskName mismatch")
	}
	if !r.Success {
		t.Fatal("Success should be true")
	}
	if r.Steps != 3 || r.ToolCalls != 2 {
		t.Fatal("Steps/ToolCalls mismatch")
	}
}

// TestE2EConfigDefaults verifies E2EConfig zero-value handling.
func TestE2EConfigDefaults(t *testing.T) {
	cfg := E2EConfig{}
	if cfg.BaseURL != "" {
		t.Fatal("empty BaseURL should be empty string")
	}
	if cfg.APIKey != "" {
		t.Fatal("empty APIKey should be empty string")
	}
	if cfg.Model != "" {
		t.Fatal("empty Model should be empty string")
	}
	if cfg.Timeout != 0 {
		t.Fatal("empty Timeout should be 0")
	}
}
