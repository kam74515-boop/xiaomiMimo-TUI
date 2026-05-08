package benchmark

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// DogfoodE2ETasks returns a set of tasks that simulate a real coding session
// where the agent reads code, plans a change, applies a patch, runs
// diagnostics, runs tests, and summarizes the result.
//
// These tasks are designed to be run against a mock HTTP server that
// returns valid SSE responses, proving the E2E framework mechanics work
// end-to-end without requiring a live MiMo API key.
func DogfoodE2ETasks() []E2ETask {
	return []E2ETask{
		{
			Name:              "inspect_code",
			Prompt:            "Read the file cmd/mimo/main.go and find where CLI flags are defined.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
		{
			Name:              "plan_change",
			Prompt:            "Plan: add a -version flag to the CLI that prints 'MiMo-TUI 1.0-rc' and exits.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
		{
			Name:              "search_existing_flags",
			Prompt:            "Search the codebase for where 'flag.BoolVar' is used in cmd/mimo/main.go.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
		{
			Name:              "run_diagnostics",
			Prompt:            "Run 'go vet ./...' to check for any issues in the codebase.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
		{
			Name:              "run_tests",
			Prompt:            "Run 'go test ./cmd/mimo/...' to verify nothing is broken.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
		{
			Name:              "summarize",
			Prompt:            "Summarize what was done: a -version flag was added to the CLI.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
	}
}

// TestDogfoodCodingSession verifies that the E2E framework can run a
// multi-task coding session through the agent loop using a mock server.
//
// This proves the end-to-end tool infrastructure works:
//   - The agent loop accepts tasks and produces events
//   - The tool executor is wired correctly
//   - The context manager tracks state across tasks
//   - The event bus delivers events to subscribers
//
// The mock server returns minimal SSE responses (no tool calls), so each
// task completes in a single step. This is intentional: the test proves
// the framework wiring, not the model's coding ability.
func TestDogfoodCodingSession(t *testing.T) {
	// Mock server returns a simple text response with no tool calls.
	// Each task should complete in one step.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Task acknowledged.\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	tasks := DogfoodE2ETasks()
	if len(tasks) != 6 {
		t.Fatalf("expected 6 dogfood tasks, got %d", len(tasks))
	}

	results, err := RunE2E(E2EConfig{
		BaseURL: server.URL,
		APIKey:  "dogfood-test-key",
		Tasks:   tasks,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("RunE2E returned error: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}

	// Every task should succeed (the mock server always returns valid responses).
	for _, r := range results {
		if !r.Success {
			t.Errorf("task %q failed: class=%q msg=%q", r.TaskName, r.ErrorClass, r.ErrorMessage)
		}
		if r.Duration <= 0 {
			t.Errorf("task %q has non-positive duration: %v", r.TaskName, r.Duration)
		}
		t.Logf("task %q: steps=%d tools=%d duration=%v",
			r.TaskName, r.Steps, r.ToolCalls, r.Duration.Round(time.Millisecond))
	}
}

// TestDogfoodTaskNames verifies the dogfood task set has the expected
// task names matching the coding session flow.
func TestDogfoodTaskNames(t *testing.T) {
	tasks := DogfoodE2ETasks()
	expected := []string{
		"inspect_code",
		"plan_change",
		"search_existing_flags",
		"run_diagnostics",
		"run_tests",
		"summarize",
	}

	if len(tasks) != len(expected) {
		t.Fatalf("expected %d tasks, got %d", len(expected), len(tasks))
	}

	for i, want := range expected {
		if tasks[i].Name != want {
			t.Errorf("task[%d].Name = %q, want %q", i, tasks[i].Name, want)
		}
		if tasks[i].Prompt == "" {
			t.Errorf("task[%d] %q has empty prompt", i, tasks[i].Name)
		}
		if tasks[i].MaxSteps <= 0 {
			t.Errorf("task[%d] %q has non-positive MaxSteps: %d", i, tasks[i].Name, tasks[i].MaxSteps)
		}
		if tasks[i].Timeout <= 0 {
			t.Errorf("task[%d] %q has non-positive Timeout", i, tasks[i].Name)
		}
	}
}
