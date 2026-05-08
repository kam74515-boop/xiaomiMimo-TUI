package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mimo-tui/internal/core"
	"mimo-tui/internal/replay"
)

// makeToolEvents builds events with the given tool names in order,
// plus an EventDone at the end. Each tool call gets a distinct ID.
func makeToolEvents(toolNames ...string) []core.AgentEvent {
	var events []core.AgentEvent
	for i, name := range toolNames {
		events = append(events, core.AgentEvent{
			Type:     core.EventToolStart,
			ToolName: name,
			ToolCall: &core.ToolCall{ID: fmt.Sprintf("call_%d", i), Name: name},
		})
	}
	events = append(events, core.AgentEvent{Type: core.EventDone})
	return events
}

// makeErrorEvents builds events with tool calls followed by an error
// then EventDone.
func makeErrorEvents(toolNames ...string) []core.AgentEvent {
	events := makeToolEvents(toolNames...)
	// Insert an error before the last event (EventDone).
	last := len(events) - 1
	errEvent := core.AgentEvent{Type: core.EventError, Err: "something went wrong"}
	events = append(events[:last], append([]core.AgentEvent{errEvent}, events[last:]...)...)
	return events
}

func TestGatePassesIdenticalTrajectory(t *testing.T) {
	golden := makeToolEvents("read_file", "shell", "write_file", "apply_patch", "read_file")
	candidate := makeToolEvents("read_file", "shell", "write_file", "apply_patch", "read_file")

	result := EvaluateCandidate(golden, candidate)
	if !result.Passed {
		t.Fatalf("expected gate to pass, got failures: %v", result.Failures)
	}
	if result.TrajectorySimilarity != 1.0 {
		t.Fatalf("trajectory similarity = %.2f, want 1.0", result.TrajectorySimilarity)
	}
	if result.ToolMatchRate != 1.0 {
		t.Fatalf("tool match rate = %.2f, want 1.0", result.ToolMatchRate)
	}
	if result.Score < 0.95 {
		t.Fatalf("score = %.2f, want >= 0.95", result.Score)
	}
}

func TestGateRejectsDivergentToolCalls(t *testing.T) {
	golden := makeToolEvents("read_file", "shell", "write_file", "apply_patch")
	// Candidate swaps write_file for list_dir and drops apply_patch.
	candidate := makeToolEvents("read_file", "shell", "list_dir")

	result := EvaluateCandidate(golden, candidate)
	if result.Passed {
		t.Fatal("expected gate to reject divergent tool calls")
	}

	// Key tools in golden: [shell, write_file, apply_patch]
	// Key tools in candidate: [shell] -- mismatch should appear in failures.
	foundKeyMismatch := false
	for _, f := range result.Failures {
		if containsAll(f, "key tool", "mismatch") {
			foundKeyMismatch = true
			break
		}
	}
	if !foundKeyMismatch {
		t.Fatalf("expected key tool mismatch failure, got: %v", result.Failures)
	}

	if result.TrajectorySimilarity > 0.7 {
		t.Fatalf("trajectory similarity = %.2f, want <= 0.7 for divergent tools", result.TrajectorySimilarity)
	}
}

func TestGateRejectsSessionWithErrors(t *testing.T) {
	golden := makeToolEvents("read_file", "shell", "write_file")
	candidate := makeErrorEvents("read_file", "shell", "write_file")

	result := EvaluateCandidate(golden, candidate)
	if result.Passed {
		t.Fatal("expected gate to reject session with errors")
	}

	foundError := false
	for _, f := range result.Failures {
		if containsAll(f, "error") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Fatalf("expected error-related failure, got: %v", result.Failures)
	}
}

func TestGateRejectsSessionWithoutDone(t *testing.T) {
	golden := makeToolEvents("read_file", "shell")
	// Candidate has tools but no EventDone.
	candidate := []core.AgentEvent{
		{Type: core.EventToolStart, ToolName: "read_file", ToolCall: &core.ToolCall{ID: "call_0", Name: "read_file"}},
		{Type: core.EventToolStart, ToolName: "shell", ToolCall: &core.ToolCall{ID: "call_1", Name: "shell"}},
	}

	result := EvaluateCandidate(golden, candidate)
	if result.Passed {
		t.Fatal("expected gate to reject session without event_done")
	}

	foundNoDone := false
	for _, f := range result.Failures {
		if containsAll(f, "event_done") {
			foundNoDone = true
			break
		}
	}
	if !foundNoDone {
		t.Fatalf("expected missing event_done failure, got: %v", result.Failures)
	}
}

func TestGateEmptyGoldenAndCandidate(t *testing.T) {
	golden := []core.AgentEvent{{Type: core.EventDone}}
	candidate := []core.AgentEvent{{Type: core.EventDone}}

	result := EvaluateCandidate(golden, candidate)
	if !result.Passed {
		t.Fatalf("expected gate to pass for empty sessions, got: %v", result.Failures)
	}
	if result.TrajectorySimilarity != 1.0 {
		t.Fatalf("trajectory similarity = %.2f, want 1.0", result.TrajectorySimilarity)
	}
}

func TestMarkAndListGolden(t *testing.T) {
	// Create a temporary workspace with a session.
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, replay.SessionsDir)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a session file.
	sessionID := "golden-test-session"
	sessionPath, err := replay.SessionPath(tmpDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	content := `{"type":"tool_start","time":"2026-05-08T12:00:00Z","tool_name":"shell","tool_call":{"id":"c1","name":"shell"}}
{"type":"done","time":"2026-05-08T12:01:00Z"}
`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mark as golden.
	if err := MarkGolden(tmpDir, sessionID, "test description"); err != nil {
		t.Fatalf("MarkGolden: %v", err)
	}

	// Verify golden file exists.
	goldenPath := filepath.Join(tmpDir, GoldenDir, sessionID+".jsonl")
	if _, err := os.Stat(goldenPath); err != nil {
		t.Fatalf("golden file not found at %s: %v", goldenPath, err)
	}

	// List golden sessions.
	sessions, err := ListGolden(tmpDir)
	if err != nil {
		t.Fatalf("ListGolden: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ListGolden returned %d sessions, want 1", len(sessions))
	}
	if sessions[0].SessionID != sessionID {
		t.Fatalf("golden session ID = %q, want %q", sessions[0].SessionID, sessionID)
	}
	if sessions[0].Description != "test description" {
		t.Fatalf("golden description = %q, want %q", sessions[0].Description, "test description")
	}

	// Load golden events.
	events, err := LoadGolden(tmpDir, sessionID)
	if err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("LoadGolden returned %d events, want 2", len(events))
	}
	if events[0].Type != core.EventToolStart {
		t.Fatalf("first event type = %s, want tool_start", events[0].Type)
	}
	if events[1].Type != core.EventDone {
		t.Fatalf("second event type = %s, want done", events[1].Type)
	}
}

func TestListGoldenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	sessions, err := ListGolden(tmpDir)
	if err != nil {
		t.Fatalf("ListGolden on empty dir: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("ListGolden returned %d sessions, want 0", len(sessions))
	}
}

func TestEvaluateCandidateToolMatchRatePartial(t *testing.T) {
	// Golden: read_file, shell, write_file, apply_patch
	// Candidate: read_file, shell, write_file, read_file (3 of 4 match at same positions)
	golden := makeToolEvents("read_file", "shell", "write_file", "apply_patch")
	candidate := makeToolEvents("read_file", "shell", "write_file", "read_file")

	result := EvaluateCandidate(golden, candidate)

	// 3 of max(4,4) = 4 match -> 0.75
	if result.ToolMatchRate != 0.75 {
		t.Fatalf("tool match rate = %.2f, want 0.75", result.ToolMatchRate)
	}

	// Key tools in golden: [shell, write_file, apply_patch]
	// Key tools in candidate: [shell, write_file]
	// These don't match -> should have failure.
	if result.Passed {
		t.Fatal("expected gate to reject due to key tool mismatch (apply_patch missing)")
	}
}

func TestExtractToolCallNames(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventToolStart, ToolName: "read_file", ToolCall: &core.ToolCall{Name: "read_file"}},
		{Type: core.EventMessageDelta, Message: "thinking"},
		{Type: core.EventToolStart, ToolName: "shell", ToolCall: &core.ToolCall{Name: "shell"}},
		{Type: core.EventDone},
	}

	names := extractToolCallNames(events)
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2: %v", len(names), names)
	}
	if names[0] != "read_file" || names[1] != "shell" {
		t.Fatalf("got %v, want [read_file, shell]", names)
	}
}

func TestToolSequenceSimilarity(t *testing.T) {
	tests := []struct {
		a, b []string
		want float64
	}{
		{nil, nil, 1.0},
		{[]string{}, []string{}, 1.0},
		{[]string{"a"}, []string{"a"}, 1.0},
		{[]string{"a"}, []string{"b"}, 0.0},
		{[]string{"a", "b"}, []string{"a"}, 0.5},      // LCS=1, max=2
		{[]string{"a", "b"}, []string{"b", "a"}, 0.5}, // LCS=1, max=2
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 1.0},
		{[]string{"a", "b", "c"}, []string{"a", "c"}, 2.0 / 3.0}, // LCS=2 (a,c), max=3
	}
	for _, tt := range tests {
		got := toolSequenceSimilarity(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("toolSequenceSimilarity(%v, %v) = %.2f, want %.2f", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestFilterKeyTools(t *testing.T) {
	names := []string{"read_file", "shell", "list_dir", "write_file", "git_status", "apply_patch"}
	filtered := filterKeyTools(names)
	want := []string{"shell", "write_file", "apply_patch"}
	if len(filtered) != len(want) {
		t.Fatalf("filterKeyTools = %v, want %v", filtered, want)
	}
	for i := range want {
		if filtered[i] != want[i] {
			t.Fatalf("filterKeyTools[%d] = %q, want %q", i, filtered[i], want[i])
		}
	}
}

func TestSequencesEqual(t *testing.T) {
	if !sequencesEqual(nil, nil) {
		t.Error("nil slices should be equal")
	}
	if !sequencesEqual([]string{"a"}, []string{"a"}) {
		t.Error("identical slices should be equal")
	}
	if sequencesEqual([]string{"a"}, []string{"b"}) {
		t.Error("different slices should not be equal")
	}
	if sequencesEqual([]string{"a", "b"}, []string{"a"}) {
		t.Error("different length slices should not be equal")
	}
}

// containsAll checks if s contains all the given substrings (case-insensitive).
func containsAll(s string, subs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range subs {
		if !strings.Contains(lower, strings.ToLower(sub)) {
			return false
		}
	}
	return true
}
