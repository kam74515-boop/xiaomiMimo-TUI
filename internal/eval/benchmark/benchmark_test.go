package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

// --- Mock Client ---

// mockClient is a configurable mock ModelClient for testing.
// It returns responses from a list, advancing through them on each call.
type mockClient struct {
	responses []mockResponse
	callCount int32
}

type mockResponse struct {
	delta     string
	toolCalls []core.ToolCall
}

func newMockClient(responses ...mockResponse) *mockClient {
	return &mockClient{responses: responses}
}

func (m *mockClient) ChatStream(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
	idx := int(atomic.AddInt32(&m.callCount, 1) - 1)
	out := make(chan core.ModelEvent, 8)

	go func() {
		defer close(out)

		if idx < len(m.responses) {
			resp := m.responses[idx]
			if resp.delta != "" {
				out <- core.ModelEvent{Delta: resp.delta}
			}
			if len(resp.toolCalls) > 0 {
				out <- core.ModelEvent{ToolCalls: resp.toolCalls}
			}
		} else {
			// Default: return a final text answer.
			out <- core.ModelEvent{Delta: "I have completed the task."}
		}

		out <- core.ModelEvent{Usage: &core.CostUpdate{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		}}
		out <- core.ModelEvent{Done: true}
	}()

	return out, nil
}

// toolCallResponse creates a mockResponse with tool calls.
func toolCallResponse(calls ...core.ToolCall) mockResponse {
	return mockResponse{toolCalls: calls}
}

// textResponse creates a mockResponse with text delta.
func textResponse(text string) mockResponse {
	return mockResponse{delta: text}
}

// --- Tests ---

func TestTaskResultCapturesToolSequence(t *testing.T) {
	// Set up temp workspace with a README.
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "README.md"), "# Test Project\nThis is a test.\n")

	// Mock client: first call does read_file, second call gives final answer.
	client := newMockClient(
		toolCallResponse(core.ToolCall{
			ID:   "call_1",
			Name: "read_file",
			Raw:  `{"path":"README.md"}`,
			Input: core.ToolInput{
				"path": "README.md",
			},
		}),
		textResponse("The README says 'This is a test.' I suggest improving it."),
	)

	bus := core.NewBus()
	cfg := RunConfig{
		Client:    client,
		Workspace: tmpDir,
		Bus:       bus,
		Tasks: []Task{
			{
				Name:     "test-sequence",
				Prompt:   "Read README.md",
				MaxSteps: 4,
				Timeout:  30 * time.Second,
				Validate: func(result TaskResult) (bool, string) {
					if result.ToolCount != 1 {
						return false, "expected 1 tool call"
					}
					if len(result.ToolSequence) != 1 || result.ToolSequence[0] != "read_file" {
						return false, "expected [read_file] in sequence"
					}
					return true, ""
				},
			},
		},
	}

	results, err := RunAll(cfg)
	if err != nil {
		t.Fatalf("RunAll error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.TaskName != "test-sequence" {
		t.Fatalf("task name = %q, want test-sequence", r.TaskName)
	}
	if !r.Success {
		t.Fatalf("task failed: %s", r.FailureReason)
	}
	if r.ToolCount != 1 {
		t.Fatalf("tool count = %d, want 1", r.ToolCount)
	}
	if r.ToolSequence[0] != "read_file" {
		t.Fatalf("tool sequence = %v, want [read_file]", r.ToolSequence)
	}
	if r.Duration <= 0 {
		t.Fatal("duration should be positive")
	}
}

func TestValidationFunctionRejectsEmptyToolSequence(t *testing.T) {
	// Mock client that returns only text (no tool calls).
	client := newMockClient(
		textResponse("I did not use any tools."),
	)

	tmpDir := t.TempDir()
	task := ReadmeEditTask()
	task.Timeout = 15 * time.Second

	cfg := RunConfig{
		Client:    client,
		Workspace: tmpDir,
		Bus:       core.NewBus(),
		Tasks:     []Task{task},
	}

	results, err := RunAll(cfg)
	if err != nil {
		t.Fatalf("RunAll error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Success {
		t.Fatal("task should have failed because no tools were called")
	}
	if !strings.Contains(r.FailureReason, "no tools were called") {
		t.Fatalf("failure reason = %q, want contains 'no tools were called'", r.FailureReason)
	}
}

func TestReportGenerationProducesValidMarkdown(t *testing.T) {
	results := []TaskResult{
		{
			TaskName:      "task-pass",
			Success:       true,
			Duration:      2 * time.Second,
			ToolCount:     2,
			ToolSequence:  []string{"read_file", "rg"},
			TokenEstimate: 300,
		},
		{
			TaskName:      "task-fail",
			Success:       false,
			FailureReason: "no tools called",
			Duration:      1 * time.Second,
			ToolCount:     0,
			TokenEstimate: 100,
			Errors:        []string{"timeout"},
		},
	}

	report := GenerateReport(results)

	// Verify markdown structure.
	if !strings.Contains(report, "# MiMo Benchmark Report") {
		t.Fatal("report missing header")
	}
	if !strings.Contains(report, "## Summary") {
		t.Fatal("report missing summary section")
	}
	if !strings.Contains(report, "## Results") {
		t.Fatal("report missing results section")
	}
	if !strings.Contains(report, "## Task Details") {
		t.Fatal("report missing task details section")
	}
	if !strings.Contains(report, "task-pass") {
		t.Fatal("report missing task-pass")
	}
	if !strings.Contains(report, "task-fail") {
		t.Fatal("report missing task-fail")
	}
	if !strings.Contains(report, "PASS") {
		t.Fatal("report missing PASS status")
	}
	if !strings.Contains(report, "FAIL") {
		t.Fatal("report missing FAIL status")
	}
	if !strings.Contains(report, "50%") {
		t.Fatalf("report missing 50%% pass rate, got:\n%s", report)
	}
	if !strings.Contains(report, "read_file -> rg") {
		t.Fatalf("report missing tool sequence, got:\n%s", report)
	}
}

func TestMultipleTasksRunIndependently(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "README.md"), "# Hello\nWorld\n")

	// Provide enough responses for both tasks: task 1 uses 2 calls, task 2 uses 2 calls.
	client := newMockClient(
		// Task 1: call 1 - tool call
		toolCallResponse(core.ToolCall{
			ID:    "call_1",
			Name:  "read_file",
			Raw:   `{"path":"README.md"}`,
			Input: core.ToolInput{"path": "README.md"},
		}),
		// Task 1: call 2 - final text
		textResponse("Done with task 1."),
		// Task 2: call 3 - tool call
		toolCallResponse(core.ToolCall{
			ID:    "call_2",
			Name:  "read_file",
			Raw:   `{"path":"README.md"}`,
			Input: core.ToolInput{"path": "README.md"},
		}),
		// Task 2: call 4 - final text
		textResponse("Done with task 2."),
	)

	task := Task{
		Name:     "multi-test",
		Prompt:   "Read README.md",
		MaxSteps: 3,
		Timeout:  15 * time.Second,
		Validate: func(result TaskResult) (bool, string) {
			return result.ToolCount > 0, "expected tool calls"
		},
	}

	cfg := RunConfig{
		Client:    client,
		Workspace: tmpDir,
		Bus:       core.NewBus(),
		Tasks:     []Task{task, task}, // run the same task twice
	}

	results, err := RunAll(cfg)
	if err != nil {
		t.Fatalf("RunAll error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if !r.Success {
			t.Fatalf("task %d failed: %s", i, r.FailureReason)
		}
	}
}

func TestSafetyCheckTaskWithShell(t *testing.T) {
	tmpDir := t.TempDir()

	// Mock client: first call invokes shell with a destructive command,
	// second call gives final answer.
	client := newMockClient(
		toolCallResponse(core.ToolCall{
			ID:   "call_1",
			Name: "shell",
			Raw:  `{"command":"rm -rf /tmp/benchmark-test-dir"}`,
			Input: core.ToolInput{
				"command": "rm -rf /tmp/benchmark-test-dir",
			},
		}),
		textResponse("The command was executed through the approval flow."),
	)

	task := SafetyCheckTask()
	task.Timeout = 15 * time.Second

	cfg := RunConfig{
		Client:    client,
		Workspace: tmpDir,
		Bus:       core.NewBus(),
		Tasks:     []Task{task},
	}

	results, err := RunAll(cfg)
	if err != nil {
		t.Fatalf("RunAll error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if !r.Success {
		t.Fatalf("safety check task should pass, got: %s", r.FailureReason)
	}
	// The shell tool should have been called.
	if r.ToolCount < 1 {
		t.Fatal("expected at least 1 tool call for safety check")
	}
}

func TestContextExploreTask(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "README.md"), "# Test\nHello\n")
	writeFile(t, filepath.Join(tmpDir, "go.mod"), "module test\n")
	writeFile(t, filepath.Join(tmpDir, "main.go"), "package main\nfunc main() {}\n")

	client := newMockClient(
		toolCallResponse(core.ToolCall{
			ID:    "call_1",
			Name:  "read_file",
			Raw:   `{"path":"README.md"}`,
			Input: core.ToolInput{"path": "README.md"},
		}),
		toolCallResponse(core.ToolCall{
			ID:    "call_2",
			Name:  "read_file",
			Raw:   `{"path":"go.mod"}`,
			Input: core.ToolInput{"path": "go.mod"},
		}),
		textResponse("This project is a terminal coding agent."),
	)

	task := ContextExploreTask()
	task.Timeout = 15 * time.Second

	cfg := RunConfig{
		Client:    client,
		Workspace: tmpDir,
		Bus:       core.NewBus(),
		Tasks:     []Task{task},
	}

	results, err := RunAll(cfg)
	if err != nil {
		t.Fatalf("RunAll error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if !r.Success {
		t.Fatalf("context explore task should pass, got: %s", r.FailureReason)
	}
	if r.ToolCount < 2 {
		t.Fatalf("expected at least 2 tool calls, got %d", r.ToolCount)
	}
}

func TestRunAllRejectsNilClient(t *testing.T) {
	cfg := RunConfig{
		Client: nil,
		Tasks:  []Task{{Name: "test"}},
	}
	_, err := RunAll(cfg)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestRunAllRejectsEmptyTasks(t *testing.T) {
	client := newMockClient(textResponse("ok"))
	cfg := RunConfig{
		Client: client,
		Tasks:  []Task{},
	}
	_, err := RunAll(cfg)
	if err == nil {
		t.Fatal("expected error for empty tasks")
	}
}

func TestReportHandlesEmptyResults(t *testing.T) {
	report := GenerateReport([]TaskResult{})
	if !strings.Contains(report, "| Total Tasks | 0 |") {
		t.Fatalf("report should show 0 tasks, got:\n%s", report)
	}
	if !strings.Contains(report, "| Pass Rate | 0% |") {
		t.Fatalf("report should show 0%% pass rate, got:\n%s", report)
	}
}

func TestTokenEstimateFromCostEvents(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "test.txt"), "content\n")

	client := newMockClient(
		toolCallResponse(core.ToolCall{
			ID:    "call_1",
			Name:  "read_file",
			Raw:   `{"path":"test.txt"}`,
			Input: core.ToolInput{"path": "test.txt"},
		}),
		textResponse("Done."),
	)

	task := Task{
		Name:     "token-test",
		Prompt:   "Read test.txt",
		MaxSteps: 3,
		Timeout:  15 * time.Second,
		Validate: func(result TaskResult) (bool, string) {
			if result.TokenEstimate == 0 {
				return false, "expected non-zero token estimate"
			}
			return true, ""
		},
	}

	cfg := RunConfig{
		Client:    client,
		Workspace: tmpDir,
		Bus:       core.NewBus(),
		Tasks:     []Task{task},
	}

	results, err := RunAll(cfg)
	if err != nil {
		t.Fatalf("RunAll error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Success {
		t.Fatalf("token test failed: %s", results[0].FailureReason)
	}
}

// --- Helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

// countingMockClient wraps mock responses and tracks call count externally.
type countingMockClient struct {
	responses []mockResponse
	count     *int32
}

func (m *countingMockClient) ChatStream(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
	idx := int(atomic.AddInt32(m.count, 1) - 1)
	out := make(chan core.ModelEvent, 8)

	go func() {
		defer close(out)

		if idx < len(m.responses) {
			resp := m.responses[idx]
			if resp.delta != "" {
				out <- core.ModelEvent{Delta: resp.delta}
			}
			if len(resp.toolCalls) > 0 {
				out <- core.ModelEvent{ToolCalls: resp.toolCalls}
			}
		} else {
			out <- core.ModelEvent{Delta: "Task complete."}
		}

		out <- core.ModelEvent{Usage: &core.CostUpdate{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		}}
		out <- core.ModelEvent{Done: true}
	}()

	return out, nil
}

// --- Mock client that returns tool calls from JSON ---

// parseToolCalls is a helper to create tool calls from JSON arguments.
func parseToolCalls(name string, argsJSON string) core.ToolCall {
	var input core.ToolInput
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil {
		input = nil
	}
	return core.ToolCall{
		ID:    "call_" + name,
		Name:  name,
		Raw:   argsJSON,
		Input: input,
	}
}
