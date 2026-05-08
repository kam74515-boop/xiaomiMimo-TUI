package benchmark

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"mimo-tui/internal/core"
	"mimo-tui/internal/session"
)

func TestEnduranceMultiPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, tmpDir+"/README.md", "# Endurance Test\nA test project.\n")

	// 5 prompts, each prompt: 1 tool call + 1 text response = 2 calls each.
	responses := make([]mockResponse, 0, 10)
	for i := 0; i < 5; i++ {
		responses = append(responses,
			toolCallResponse(core.ToolCall{
				ID:    "call_read_" + string(rune('0'+i)),
				Name:  "read_file",
				Raw:   `{"path":"README.md"}`,
				Input: core.ToolInput{"path": "README.md"},
			}),
			textResponse("Completed prompt "+string(rune('0'+i))+"."),
		)
	}

	client := newMockClient(responses...)

	cfg := EnduranceConfig{
		Prompts: []string{
			"Read the README",
			"Find Go test files",
			"Count lines of code",
			"List exported functions",
			"Find TODO comments",
		},
		MaxStepsEach: 4,
		Timeout:      60 * time.Second,
	}

	result := RunEndurance(client, tmpDir, cfg)

	if result.PromptsCompleted != 5 {
		t.Fatalf("PromptsCompleted = %d, want 5", result.PromptsCompleted)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.Interrupted {
		t.Fatal("should not be interrupted")
	}
	if result.EventCount == 0 {
		t.Fatal("expected non-zero event count")
	}
	if result.TotalDuration <= 0 {
		t.Fatal("expected positive duration")
	}
	if len(result.SessionIDs) != 5 {
		t.Fatalf("SessionIDs count = %d, want 5", len(result.SessionIDs))
	}
}

func TestEnduranceInterruptResume(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, tmpDir+"/README.md", "# Interrupt Test\n")

	// 5 prompts, each needs 2 calls (tool + text).
	responses := make([]mockResponse, 0, 10)
	for i := 0; i < 5; i++ {
		responses = append(responses,
			toolCallResponse(core.ToolCall{
				ID:    "call_int_" + string(rune('0'+i)),
				Name:  "read_file",
				Raw:   `{"path":"README.md"}`,
				Input: core.ToolInput{"path": "README.md"},
			}),
			textResponse("Done with prompt "+string(rune('0'+i))+"."),
		)
	}

	client := newMockClient(responses...)

	cfg := EnduranceConfig{
		Prompts: []string{
			"Read the README",
			"Find Go test files",
			"Count lines of code",
			"List exported functions",
			"Find TODO comments",
		},
		MaxStepsEach: 4,
		Timeout:      60 * time.Second,
		InterruptAt:  3,
		ResumeAfter:  true,
	}

	result := RunEndurance(client, tmpDir, cfg)

	if !result.Interrupted {
		t.Fatal("expected interrupted flag to be true")
	}
	if !result.Resumed {
		t.Fatal("expected resumed flag to be true")
	}
	// All 5 prompts should complete because we resume after interrupt at 3.
	if result.PromptsCompleted != 5 {
		t.Fatalf("PromptsCompleted = %d, want 5", result.PromptsCompleted)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	// Should have a "resumed-after-3" session ID.
	foundResume := false
	for _, id := range result.SessionIDs {
		if strings.Contains(id, "resumed") {
			foundResume = true
			break
		}
	}
	if !foundResume {
		t.Fatalf("expected a resumed session ID, got: %v", result.SessionIDs)
	}
}

func TestEnduranceEventLogReplay(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, tmpDir+"/test.txt", "hello world\n")

	// Run 2 prompts, then verify the event log can build a resume summary.
	client := newMockClient(
		// Prompt 0: tool call + text
		toolCallResponse(core.ToolCall{
			ID:    "call_replay_0",
			Name:  "read_file",
			Raw:   `{"path":"test.txt"}`,
			Input: core.ToolInput{"path": "test.txt"},
		}),
		textResponse("Read the file successfully."),
		// Prompt 1: tool call + text
		toolCallResponse(core.ToolCall{
			ID:    "call_replay_1",
			Name:  "read_file",
			Raw:   `{"path":"test.txt"}`,
			Input: core.ToolInput{"path": "test.txt"},
		}),
		textResponse("Analyzed the file contents."),
	)

	cfg := EnduranceConfig{
		Prompts: []string{
			"Read test.txt",
			"Analyze test.txt",
		},
		MaxStepsEach: 4,
		Timeout:      60 * time.Second,
	}

	result := RunEndurance(client, tmpDir, cfg)

	if result.PromptsCompleted != 2 {
		t.Fatalf("PromptsCompleted = %d, want 2", result.PromptsCompleted)
	}
	if result.EventCount == 0 {
		t.Fatal("expected non-zero event count for replay")
	}

	// Now verify that ExtractHistory works on the collected events by
	// running a fresh endurance with a resume. We do this by running
	// a second endurance with a prior history built from the first run's
	// event pattern. Since we cannot access the internal allEvents directly,
	// we verify the end-to-end behavior: the second run should succeed
	// with a resumed session.
	client2 := newMockClient(
		// Single prompt, tool call + text.
		toolCallResponse(core.ToolCall{
			ID:    "call_replay_2",
			Name:  "read_file",
			Raw:   `{"path":"test.txt"}`,
			Input: core.ToolInput{"path": "test.txt"},
		}),
		textResponse("Resumed and completed."),
	)

	// Use interrupt/resume to exercise the event-log-replay path.
	cfg2 := EnduranceConfig{
		Prompts: []string{
			"Read test.txt",
			"Continue analysis",
		},
		MaxStepsEach: 4,
		Timeout:      60 * time.Second,
		InterruptAt:  1,
		ResumeAfter:  true,
	}

	result2 := RunEndurance(client2, tmpDir, cfg2)

	if !result2.Interrupted {
		t.Fatal("expected interrupt")
	}
	if !result2.Resumed {
		t.Fatal("expected resume after interrupt")
	}
	if result2.PromptsCompleted != 2 {
		t.Fatalf("after resume: PromptsCompleted = %d, want 2", result2.PromptsCompleted)
	}
}

func TestEnduranceGoroutineStability(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, tmpDir+"/README.md", "# Goroutine Test\n")

	// Warm up the runtime to get a stable baseline.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	// 5 prompts, each: tool call + text.
	responses := make([]mockResponse, 0, 10)
	for i := 0; i < 5; i++ {
		responses = append(responses,
			toolCallResponse(core.ToolCall{
				ID:    "call_gor_" + string(rune('0'+i)),
				Name:  "read_file",
				Raw:   `{"path":"README.md"}`,
				Input: core.ToolInput{"path": "README.md"},
			}),
			textResponse("Done."),
		)
	}

	client := newMockClient(responses...)

	cfg := EnduranceConfig{
		Prompts: []string{
			"Read the README",
			"Find Go test files",
			"Count lines of code",
			"List exported functions",
			"Find TODO comments",
		},
		MaxStepsEach: 4,
		Timeout:      60 * time.Second,
	}

	result := RunEndurance(client, tmpDir, cfg)

	if result.PromptsCompleted != 5 {
		t.Fatalf("PromptsCompleted = %d, want 5", result.PromptsCompleted)
	}

	// Allow a small settling period for goroutines to exit.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	final := runtime.NumGoroutine()
	growth := final - baseline

	// Allow up to 3 goroutines of slack for background runtime noise,
	// but the endurance run should not leak goroutines.
	if growth > 3 {
		t.Errorf("goroutine leak: baseline=%d, final=%d, growth=%d", baseline, final, growth)
	}
}

func TestEnduranceSessionTasks(t *testing.T) {
	tasks := EnduranceSessionTasks()
	if len(tasks) != 10 {
		t.Fatalf("EnduranceSessionTasks count = %d, want 10", len(tasks))
	}
	// Verify each task is non-empty.
	for i, task := range tasks {
		if strings.TrimSpace(task) == "" {
			t.Errorf("task %d is empty", i)
		}
	}
}

func TestEnduranceSessionSummaryFromEvents(t *testing.T) {
	// Verify that session.BuildResumeSummary produces a valid summary
	// from a realistic set of endurance events.
	events := []core.AgentEvent{
		{Type: core.EventMessageDelta, Message: "Reading file..."},
		{Type: core.EventToolStart, ToolName: "read_file"},
		{Type: core.EventToolResult, ToolName: "read_file", Message: "file contents here"},
		{Type: core.EventObservation, Observation: &core.Observation{Summary: "read complete"}},
		{Type: core.EventDone},
	}

	summary := session.BuildResumeSummary(events)

	if summary.LastStatus != string(core.EventDone) {
		t.Fatalf("LastStatus = %q, want %q", summary.LastStatus, core.EventDone)
	}
	if summary.EventCounts[core.EventToolResult] != 1 {
		t.Fatalf("tool result count = %d, want 1", summary.EventCounts[core.EventToolResult])
	}
	if summary.EventCounts[core.EventDone] != 1 {
		t.Fatalf("done count = %d, want 1", summary.EventCounts[core.EventDone])
	}

	// ExtractHistory should produce at least a system message + assistant message.
	history := session.ExtractHistory(events)
	if len(history) < 2 {
		t.Fatalf("history length = %d, want at least 2", len(history))
	}
	if history[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", history[0].Role)
	}
}
