package eval

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func TestExtractTrajectorySingleStepFinalAnswer(t *testing.T) {
	events := []core.AgentEvent{
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-0-123", Status: core.TraceRunning, Goal: "Step 1: reason"},
		},
		{Type: core.EventMessageDelta, Message: "hello"},
		{Type: core.EventMessageDelta, Message: " world"},
		{
			Type: core.EventCostUpdate,
			Cost: &core.CostUpdate{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-0-123", Status: core.TraceDone, Observation: "final answer"},
		},
		{Type: core.EventDone},
	}

	traj := ExtractTrajectory(events)
	if len(traj.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(traj.Steps))
	}
	if !traj.Success {
		t.Fatal("expected success")
	}
	if len(traj.Steps[0].ToolCalls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(traj.Steps[0].ToolCalls))
	}
	if len(traj.Steps[0].TraceUpdates) < 2 {
		t.Fatalf("trace updates = %d, want >= 2", len(traj.Steps[0].TraceUpdates))
	}
	if traj.TokenCost.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", traj.TokenCost.TotalTokens)
	}
}

func TestExtractTrajectoryMultiStepWithTools(t *testing.T) {
	events := []core.AgentEvent{
		// Step 1: model requests a tool
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-0-123", Status: core.TraceRunning, Goal: "Step 1: reason"},
		},
		{Type: core.EventMessageDelta, Message: "let me check"},
		{
			Type: core.EventCostUpdate,
			Cost: &core.CostUpdate{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, EstimatedUSD: 0.001},
		},
		{
			Type:     core.EventToolStart,
			ToolName: "read_file",
			ToolCall: &core.ToolCall{ID: "call_1", Name: "read_file", Input: core.ToolInput{"path": "README.md"}},
		},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "tool-read_file-456", Status: core.TraceRunning},
		},
		{
			Type:     core.EventToolResult,
			ToolName: "read_file",
			Message:  "# README\nhello",
		},
		{
			Type:        core.EventObservation,
			ToolName:    "read_file",
			Observation: &core.Observation{Summary: "read README.md"},
		},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "tool-read_file-456", Status: core.TraceDone},
		},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-0-123", Status: core.TraceDone},
		},
		// Step 2: final answer
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-1-789", Status: core.TraceRunning, Goal: "Step 2: reason"},
		},
		{Type: core.EventMessageDelta, Message: "the file says hello"},
		{
			Type: core.EventCostUpdate,
			Cost: &core.CostUpdate{InputTokens: 60, OutputTokens: 10, TotalTokens: 70, EstimatedUSD: 0.003},
		},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-1-789", Status: core.TraceDone},
		},
		{Type: core.EventDone},
	}

	traj := ExtractTrajectory(events)
	if len(traj.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(traj.Steps))
	}
	if !traj.Success {
		t.Fatal("expected success")
	}
	if traj.TokenCost.InputTokens != 70 {
		t.Fatalf("input tokens = %d, want 70", traj.TokenCost.InputTokens)
	}
	if traj.TokenCost.OutputTokens != 15 {
		t.Fatalf("output tokens = %d, want 15", traj.TokenCost.OutputTokens)
	}
	if traj.TokenCost.TotalTokens != 85 {
		t.Fatalf("total tokens = %d, want 85", traj.TokenCost.TotalTokens)
	}
	if traj.TokenCost.EstimatedUSD != 0.004 {
		t.Fatalf("estimated USD = %f, want 0.004", traj.TokenCost.EstimatedUSD)
	}

	// Step 1 should have 1 tool call and 1 observation.
	if len(traj.Steps[0].ToolCalls) != 1 {
		t.Fatalf("step 0 tool calls = %d, want 1", len(traj.Steps[0].ToolCalls))
	}
	if traj.Steps[0].ToolCalls[0].Name != "read_file" {
		t.Fatalf("step 0 tool call = %s, want read_file", traj.Steps[0].ToolCalls[0].Name)
	}
	if len(traj.Steps[0].Observations) != 1 {
		t.Fatalf("step 0 observations = %d, want 1", len(traj.Steps[0].Observations))
	}
	if traj.Steps[0].Observations[0].Summary != "read README.md" {
		t.Fatalf("step 0 observation summary = %q, want 'read README.md'", traj.Steps[0].Observations[0].Summary)
	}

	// Step 2 should have 0 tool calls (final answer).
	if len(traj.Steps[1].ToolCalls) != 0 {
		t.Fatalf("step 1 tool calls = %d, want 0", len(traj.Steps[1].ToolCalls))
	}
}

func TestExtractTrajectoryWithError(t *testing.T) {
	events := []core.AgentEvent{
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-0-123", Status: core.TraceRunning},
		},
		{Type: core.EventMessageDelta, Message: "trying something"},
		{Type: core.EventError, Err: "something went wrong"},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-step-0-123", Status: core.TraceFailed},
		},
		{Type: core.EventDone},
	}

	traj := ExtractTrajectory(events)
	if traj.Success {
		t.Fatal("expected failure")
	}
	if traj.Error != "something went wrong" {
		t.Fatalf("error = %q, want 'something went wrong'", traj.Error)
	}
}

func TestExtractTrajectoryEmpty(t *testing.T) {
	traj := ExtractTrajectory(nil)
	if traj.Success {
		t.Fatal("expected failure for empty events")
	}
	if len(traj.Steps) != 0 {
		t.Fatalf("steps = %d, want 0", len(traj.Steps))
	}
}

func TestExtractTrajectoryWithoutStepMarker(t *testing.T) {
	// Events from RunOnce (trace ID "agent-123" not "agent-step-*").
	events := []core.AgentEvent{
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-1234567890", Status: core.TraceRunning, Goal: "Respond to user"},
		},
		{Type: core.EventMessageDelta, Message: "answer"},
		{
			Type:  core.EventTraceUpdate,
			Trace: &core.TraceStep{ID: "agent-1234567890", Status: core.TraceDone},
		},
		{Type: core.EventDone},
	}

	traj := ExtractTrajectory(events)
	if len(traj.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 (fallback step)", len(traj.Steps))
	}
	if !traj.Success {
		t.Fatal("expected success")
	}
	// Trace updates should be collected in the fallback step.
	if len(traj.Steps[0].TraceUpdates) != 2 {
		t.Fatalf("trace updates = %d, want 2", len(traj.Steps[0].TraceUpdates))
	}
}

func TestCompareTrajectories(t *testing.T) {
	a := Trajectory{
		SessionID: "session-a",
		Steps: []TrajectoryStep{
			{ToolCalls: []core.ToolCall{{Name: "read_file"}, {Name: "git_status"}}},
			{ToolCalls: nil},
		},
		Success:   true,
		TokenCost: core.CostUpdate{InputTokens: 100, OutputTokens: 50, TotalTokens: 150, EstimatedUSD: 0.005},
	}

	b := Trajectory{
		SessionID: "session-b",
		Steps: []TrajectoryStep{
			{ToolCalls: []core.ToolCall{{Name: "read_file"}}},
		},
		Success:   true,
		TokenCost: core.CostUpdate{InputTokens: 50, OutputTokens: 30, TotalTokens: 80, EstimatedUSD: 0.002},
	}

	diff := CompareTrajectories(a, b)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "session-a") || !strings.Contains(diff, "session-b") {
		t.Fatalf("diff missing session IDs: %s", diff)
	}
	if !strings.Contains(diff, "2 vs 1") {
		t.Fatalf("diff missing step count: %s", diff)
	}
	if !strings.Contains(diff, "2 vs 1") {
		t.Fatalf("diff missing tool call count: %s", diff)
	}
	if !strings.Contains(diff, "Step 1:") {
		t.Fatalf("diff missing step-level comparison: %s", diff)
	}
}

func TestCompareTrajectoriesIdentical(t *testing.T) {
	a := Trajectory{
		SessionID: "a",
		Steps:     []TrajectoryStep{{ToolCalls: []core.ToolCall{{Name: "list_dir"}}}},
		Success:   true,
		TokenCost: core.CostUpdate{TotalTokens: 100},
	}
	b := a
	b.SessionID = "b"

	diff := CompareTrajectories(a, b)
	if !strings.Contains(diff, "1 vs 1") {
		t.Fatalf("diff should show equal steps: %s", diff)
	}
	if strings.Contains(diff, "Step 1:") {
		t.Fatalf("diff should not show step diffs for identical tool sequences: %s", diff)
	}
}

func TestCompareTrajectoriesDifferentErrors(t *testing.T) {
	a := Trajectory{
		SessionID: "a",
		Steps:     []TrajectoryStep{{}},
		Success:   false,
		Error:     "timeout",
	}
	b := Trajectory{
		SessionID: "b",
		Steps:     []TrajectoryStep{{}},
		Success:   false,
		Error:     "max steps",
	}

	diff := CompareTrajectories(a, b)
	if !strings.Contains(diff, "timeout") || !strings.Contains(diff, "max steps") {
		t.Fatalf("diff missing error comparison: %s", diff)
	}
}

func TestFormatTrajectory(t *testing.T) {
	traj := Trajectory{
		SessionID: "20260508T150000",
		Steps: []TrajectoryStep{
			{
				ToolCalls: []core.ToolCall{
					{Name: "read_file", Input: core.ToolInput{"path": "README.md"}},
				},
				Observations: []core.Observation{
					{Summary: "read README.md"},
				},
			},
			{ToolCalls: nil},
		},
		Success:   true,
		TokenCost: core.CostUpdate{InputTokens: 100, OutputTokens: 50, TotalTokens: 150, EstimatedUSD: 0.0012},
	}

	out := FormatTrajectory(traj)
	if !strings.Contains(out, "20260508T150000") {
		t.Fatalf("output missing session ID: %s", out)
	}
	if !strings.Contains(out, "Steps: 2") {
		t.Fatalf("output missing step count: %s", out)
	}
	if !strings.Contains(out, "Success: true") {
		t.Fatalf("output missing success: %s", out)
	}
	if !strings.Contains(out, "in=100") || !strings.Contains(out, "out=50") {
		t.Fatalf("output missing token counts: %s", out)
	}
	if !strings.Contains(out, "read_file") || !strings.Contains(out, "README.md") {
		t.Fatalf("output missing tool info: %s", out)
	}
	if !strings.Contains(out, "final answer, no tools") {
		t.Fatalf("output missing final answer marker: %s", out)
	}
	if !strings.Contains(out, "read README.md") {
		t.Fatalf("output missing observation: %s", out)
	}
}
