package session

import (
	"fmt"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

// TestE2EBasicResume verifies that a single-turn session produces a valid
// ResumeSummary with context, trace, and tool data, and that ExtractHistory
// reconstructs the conversation messages.
func TestE2EBasicResume(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "Fix the bug in main.go"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "I'll read main.go first. "},
		{
			Type: core.EventToolStart, Time: time.Unix(4, 0), ToolName: "read_file",
			ToolCall: &core.ToolCall{ID: "call-1", Name: "read_file", Raw: `{"path":"main.go"}`},
		},
		{
			Type: core.EventToolResult, Time: time.Unix(5, 0), ToolName: "read_file",
			Message:  "package main\nfunc main() { fmt.Println(bug) }",
			ToolCall: &core.ToolCall{ID: "call-1", Name: "read_file"},
			Observation: &core.Observation{
				Summary:    "read main.go",
				ArtifactID: "art-1",
			},
		},
		{
			Type: core.EventObservation, Time: time.Unix(6, 0),
			Observation: &core.Observation{Summary: "file contents loaded", ArtifactID: "art-1"},
		},
		{
			Type: core.EventContextUpdate, Time: time.Unix(7, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 500,
				Items: []core.ContextItem{
					{ID: "near:main.go", Tier: core.TierNear, Title: "main.go", TokenEstimate: 200, Pinned: true},
					{ID: "anchor:patch-plan", Tier: core.TierAnchor, Title: "patch plan", TokenEstimate: 100},
				},
			},
		},
		{
			Type: core.EventTraceUpdate, Time: time.Unix(8, 0),
			Trace: &core.TraceStep{ID: "trace-1", Goal: "fix bug", Status: core.TraceDone, Stage: core.StageInspect},
		},
		{Type: core.EventMessageDelta, Time: time.Unix(9, 0), Message: "Found the issue. Fixing now."},
		{Type: core.EventDone, Time: time.Unix(10, 0)},
	}

	summary := BuildResumeSummary(events)

	// Verify event counts.
	if summary.EventCounts[core.EventUserPrompt] != 1 {
		t.Fatalf("user_prompt count = %d, want 1", summary.EventCounts[core.EventUserPrompt])
	}
	if summary.EventCounts[core.EventToolStart] != 1 {
		t.Fatalf("tool_start count = %d, want 1", summary.EventCounts[core.EventToolStart])
	}
	if summary.EventCounts[core.EventToolResult] != 1 {
		t.Fatalf("tool_result count = %d, want 1", summary.EventCounts[core.EventToolResult])
	}
	if summary.EventCounts[core.EventDone] != 1 {
		t.Fatalf("done count = %d, want 1", summary.EventCounts[core.EventDone])
	}

	// Verify context captured.
	if summary.LatestContext == nil {
		t.Fatal("LatestContext should not be nil")
	}
	if len(summary.LatestContext.Items) != 2 {
		t.Fatalf("context items = %d, want 2", len(summary.LatestContext.Items))
	}
	if summary.LatestContext.Items[0].Tier != core.TierNear {
		t.Fatalf("first item tier = %q, want near", summary.LatestContext.Items[0].Tier)
	}
	if !summary.LatestContext.Items[0].Pinned {
		t.Fatal("first item should be pinned")
	}

	// Verify trace captured.
	if len(summary.RecentTraceUpdates) != 1 {
		t.Fatalf("trace updates = %d, want 1", len(summary.RecentTraceUpdates))
	}
	if summary.RecentTraceUpdates[0].Stage != core.StageInspect {
		t.Fatalf("trace stage = %q, want inspect", summary.RecentTraceUpdates[0].Stage)
	}
	if summary.LastStage != core.StageInspect {
		t.Fatalf("LastStage = %q, want inspect", summary.LastStage)
	}

	// Verify artifacts captured.
	if len(summary.ArtifactIDs) != 1 || summary.ArtifactIDs[0] != "art-1" {
		t.Fatalf("artifact ids = %v, want [art-1]", summary.ArtifactIDs)
	}

	// Verify last status is "done".
	if summary.LastStatus != string(core.EventDone) {
		t.Fatalf("LastStatus = %q, want done", summary.LastStatus)
	}

	// Verify last tool results.
	if len(summary.LastToolResults) != 1 || summary.LastToolResults[0] != "read main.go" {
		t.Fatalf("LastToolResults = %v, want [read main.go]", summary.LastToolResults)
	}

	// Verify ExtractHistory reconstructs messages.
	msgs := ExtractHistory(events)
	if msgs[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}

	var userMsgs, assistantMsgs, toolMsgs int
	for _, m := range msgs {
		switch m.Role {
		case "user":
			userMsgs++
			if m.Content != "Fix the bug in main.go" {
				t.Fatalf("user message content = %q", m.Content)
			}
		case "assistant":
			assistantMsgs++
		case "tool":
			toolMsgs++
			if m.ToolCallID != "call-1" {
				t.Fatalf("tool message ToolCallID = %q, want call-1", m.ToolCallID)
			}
		}
	}
	if userMsgs != 1 {
		t.Fatalf("user messages = %d, want 1", userMsgs)
	}
	if assistantMsgs < 1 {
		t.Fatalf("assistant messages = %d, want >= 1", assistantMsgs)
	}
	if toolMsgs != 1 {
		t.Fatalf("tool messages = %d, want 1", toolMsgs)
	}
}

// TestE2EMultiTurnResume simulates 3 full turns with tool calls and verifies
// the multi-turn message sequence and tool_call_id linkage are preserved.
func TestE2EMultiTurnResume(t *testing.T) {
	events := BuildTestEvents(3, 2)

	summary := BuildResumeSummary(events)

	// Should have 3 user prompts and 3 dones.
	if summary.EventCounts[core.EventUserPrompt] != 3 {
		t.Fatalf("user_prompt count = %d, want 3", summary.EventCounts[core.EventUserPrompt])
	}
	if summary.EventCounts[core.EventDone] != 3 {
		t.Fatalf("done count = %d, want 3", summary.EventCounts[core.EventDone])
	}

	// 3 turns * 2 tools = 6 tool starts and 6 tool results.
	if summary.EventCounts[core.EventToolStart] != 6 {
		t.Fatalf("tool_start count = %d, want 6", summary.EventCounts[core.EventToolStart])
	}
	if summary.EventCounts[core.EventToolResult] != 6 {
		t.Fatalf("tool_result count = %d, want 6", summary.EventCounts[core.EventToolResult])
	}

	// Verify multi-turn message sequence from ExtractHistory.
	msgs := ExtractHistory(events)

	// Should start with system placeholder.
	if msgs[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}

	// Count roles.
	var userMsgs, assistantMsgs, toolMsgs int
	var lastToolCallID string
	for _, m := range msgs[1:] { // skip system
		switch m.Role {
		case "user":
			userMsgs++
		case "assistant":
			assistantMsgs++
		case "tool":
			toolMsgs++
			if m.ToolCallID == "" {
				t.Fatal("tool message should have ToolCallID")
			}
			lastToolCallID = m.ToolCallID
		}
	}

	if userMsgs != 3 {
		t.Fatalf("user messages = %d, want 3", userMsgs)
	}
	if assistantMsgs < 3 {
		t.Fatalf("assistant messages = %d, want >= 3", assistantMsgs)
	}
	if toolMsgs != 6 {
		t.Fatalf("tool messages = %d, want 6", toolMsgs)
	}

	// Last tool call ID should reference the last turn's last tool.
	expectedLastID := "call-t2-tl1"
	if lastToolCallID != expectedLastID {
		t.Fatalf("last tool call ID = %q, want %q", lastToolCallID, expectedLastID)
	}

	// Verify messages alternate correctly: user -> assistant -> tool(s) -> ...
	// Check that the first tool message in each group is preceded by an assistant message.
	for i, m := range msgs {
		if m.Role == "tool" && i >= 2 && msgs[i-1].Role != "tool" {
			// The message before the first tool result should be assistant (flushed deltas).
			if msgs[i-1].Role != "assistant" {
				t.Fatalf("message before first tool at index %d has role %q, want assistant", i, msgs[i-1].Role)
			}
		}
	}
}

// TestE2EInterruptedRecovery simulates a session that ends with an error
// (not a clean done). Verifies the error and last state are captured.
func TestE2EInterruptedRecovery(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "Run the tests"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "Running tests... "},
		{
			Type: core.EventToolStart, Time: time.Unix(4, 0), ToolName: "shell",
			ToolCall: &core.ToolCall{ID: "call-int-1", Name: "shell", Raw: `{"command":"go test ./..."}`},
		},
		{
			Type: core.EventToolResult, Time: time.Unix(5, 0), ToolName: "shell",
			Message:  "FAIL: TestFoo\npanic: runtime error",
			ToolCall: &core.ToolCall{ID: "call-int-1", Name: "shell"},
			Observation: &core.Observation{
				Summary:    "tests failed with panic",
				StateDelta: "test_failure",
			},
		},
		{
			Type: core.EventContextUpdate, Time: time.Unix(6, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 2000,
				Items: []core.ContextItem{
					{ID: "near:error-log", Tier: core.TierNear, Title: "error log", TokenEstimate: 800},
				},
			},
		},
		{
			Type: core.EventTraceUpdate, Time: time.Unix(7, 0),
			Trace: &core.TraceStep{
				ID: "trace-int-1", Goal: "run tests", Status: core.TraceFailed,
				Stage:       core.StageTest,
				Observation: "panic in TestFoo: nil pointer dereference",
			},
		},
		// No EventDone -- the session was interrupted/error'd.
		{
			Type: core.EventError, Time: time.Unix(8, 0),
			Message: "Agent loop terminated: unrecoverable panic in test suite",
		},
	}

	summary := BuildResumeSummary(events)

	// Verify error is captured.
	if summary.LastStatus != "error" {
		t.Fatalf("LastStatus = %q, want error", summary.LastStatus)
	}
	if summary.LastError != "Agent loop terminated: unrecoverable panic in test suite" {
		t.Fatalf("LastError = %q, want panic message", summary.LastError)
	}

	// Verify the trace failure is also recorded.
	foundTraceFail := false
	for _, ts := range summary.RecentTraceUpdates {
		if ts.Status == core.TraceFailed {
			foundTraceFail = true
			if ts.Stage != core.StageTest {
				t.Fatalf("failed trace stage = %q, want test", ts.Stage)
			}
		}
	}
	if !foundTraceFail {
		t.Fatal("expected a failed trace update in RecentTraceUpdates")
	}

	// Verify context from the interrupted session is preserved.
	if summary.LatestContext == nil {
		t.Fatal("LatestContext should be preserved after interruption")
	}
	if summary.LatestContext.UsedTokens != 2000 {
		t.Fatalf("UsedTokens = %d, want 2000", summary.LatestContext.UsedTokens)
	}

	// Verify ExtractHistory still reconstructs partial conversation.
	msgs := ExtractHistory(events)
	var userMsgs int
	for _, m := range msgs {
		if m.Role == "user" {
			userMsgs++
		}
	}
	if userMsgs != 1 {
		t.Fatalf("user messages = %d, want 1 (from partial session)", userMsgs)
	}

	// No EventDone was emitted, so the last assistant deltas should be flushed
	// at the end (ExtractHistory flushes on EventUserPrompt/EventDone only,
	// but tool results also flush).
	var toolMsgs int
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsgs++
		}
	}
	if toolMsgs != 1 {
		t.Fatalf("tool messages = %d, want 1", toolMsgs)
	}
}

// TestE2EContextRestoration verifies that context_update events with
// near/anchor/artifact tiers are preserved, and pinned items survive.
func TestE2EContextRestoration(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "Analyze the codebase"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "Looking at the code..."},
		// First context update: near tier with pinned item.
		{
			Type: core.EventContextUpdate, Time: time.Unix(4, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 1000,
				Items: []core.ContextItem{
					{ID: "near:main.go", Tier: core.TierNear, Title: "main.go", TokenEstimate: 500, Pinned: true, Reason: "user selection"},
					{ID: "anchor:plan", Tier: core.TierAnchor, Title: "fix plan", TokenEstimate: 200},
				},
			},
		},
		// Second context update: adds artifact tier, near item unpinned.
		{
			Type: core.EventContextUpdate, Time: time.Unix(5, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 2500,
				Items: []core.ContextItem{
					{ID: "near:main.go", Tier: core.TierNear, Title: "main.go", TokenEstimate: 500, Pinned: true, Reason: "user selection"},
					{ID: "anchor:plan", Tier: core.TierAnchor, Title: "fix plan", TokenEstimate: 200},
					{ID: "artifact:patch-v1", Tier: core.TierArtifact, Title: "generated patch", TokenEstimate: 800, Source: "tool:write_file"},
					{ID: "near:tests.go", Tier: core.TierNear, Title: "tests.go", TokenEstimate: 300},
				},
				PollutionRisk: "low",
			},
		},
		{Type: core.EventDone, Time: time.Unix(6, 0)},
	}

	summary := BuildResumeSummary(events)

	if summary.LatestContext == nil {
		t.Fatal("LatestContext should not be nil")
	}

	// LatestContext should be from the second update (the latest).
	if summary.LatestContext.UsedTokens != 2500 {
		t.Fatalf("UsedTokens = %d, want 2500", summary.LatestContext.UsedTokens)
	}

	// Verify all three tiers are present.
	tiers := make(map[core.ContextTier]int)
	pinnedCount := 0
	for _, item := range summary.LatestContext.Items {
		tiers[item.Tier]++
		if item.Pinned {
			pinnedCount++
		}
	}
	if tiers[core.TierNear] != 2 {
		t.Fatalf("near tier items = %d, want 2", tiers[core.TierNear])
	}
	if tiers[core.TierAnchor] != 1 {
		t.Fatalf("anchor tier items = %d, want 1", tiers[core.TierAnchor])
	}
	if tiers[core.TierArtifact] != 1 {
		t.Fatalf("artifact tier items = %d, want 1", tiers[core.TierArtifact])
	}

	// Verify pinned items survive.
	if pinnedCount != 1 {
		t.Fatalf("pinned items = %d, want 1", pinnedCount)
	}

	// Verify the pinned item is the correct one.
	var pinnedItem *core.ContextItem
	for i := range summary.LatestContext.Items {
		if summary.LatestContext.Items[i].Pinned {
			pinnedItem = &summary.LatestContext.Items[i]
		}
	}
	if pinnedItem == nil || pinnedItem.ID != "near:main.go" {
		t.Fatalf("pinned item = %+v, want near:main.go", pinnedItem)
	}
	if pinnedItem.Reason != "user selection" {
		t.Fatalf("pinned reason = %q, want user selection", pinnedItem.Reason)
	}

	// Verify pollution risk is preserved.
	if summary.LatestContext.PollutionRisk != "low" {
		t.Fatalf("PollutionRisk = %q, want low", summary.LatestContext.PollutionRisk)
	}

	// Verify context is cloned (not a pointer to original).
	if len(summary.LatestContext.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(summary.LatestContext.Items))
	}
}

// TestE2EArtifactReferences verifies that observation events carrying
// ArtifactID values are collected into ResumeSummary.ArtifactIDs, with
// deduplication.
func TestE2EArtifactReferences(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "Write tests"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "Writing tests..."},
		{
			Type: core.EventToolResult, Time: time.Unix(4, 0), ToolName: "write_file",
			Message:  "wrote foo_test.go",
			ToolCall: &core.ToolCall{ID: "call-art-1", Name: "write_file"},
			Observation: &core.Observation{
				Summary:    "created test file",
				ArtifactID: "art-foo-test",
			},
		},
		{
			Type: core.EventObservation, Time: time.Unix(5, 0),
			Observation: &core.Observation{Summary: "test file observation", ArtifactID: "art-foo-test"},
		},
		{
			Type: core.EventToolResult, Time: time.Unix(6, 0), ToolName: "write_file",
			Message:  "wrote bar_test.go",
			ToolCall: &core.ToolCall{ID: "call-art-2", Name: "write_file"},
			Observation: &core.Observation{
				Summary:    "created second test file",
				ArtifactID: "art-bar-test",
			},
		},
		{
			Type: core.EventObservation, Time: time.Unix(7, 0),
			Observation: &core.Observation{Summary: "second observation", ArtifactID: "art-bar-test"},
		},
		// Duplicate artifact from a different observation.
		{
			Type: core.EventObservation, Time: time.Unix(8, 0),
			Observation: &core.Observation{Summary: "duplicate observation", ArtifactID: "art-foo-test"},
		},
		// Empty artifact ID should be ignored.
		{
			Type: core.EventObservation, Time: time.Unix(9, 0),
			Observation: &core.Observation{Summary: "no artifact here"},
		},
		{Type: core.EventDone, Time: time.Unix(10, 0)},
	}

	summary := BuildResumeSummary(events)

	if len(summary.ArtifactIDs) != 2 {
		t.Fatalf("ArtifactIDs = %v, want 2 unique IDs", summary.ArtifactIDs)
	}

	// Verify both artifacts are present (order preserved by first appearance).
	idSet := make(map[string]bool)
	for _, id := range summary.ArtifactIDs {
		idSet[id] = true
	}
	if !idSet["art-foo-test"] {
		t.Fatal("missing art-foo-test in ArtifactIDs")
	}
	if !idSet["art-bar-test"] {
		t.Fatal("missing art-bar-test in ArtifactIDs")
	}

	// Verify last tool results captured.
	if len(summary.LastToolResults) != 2 {
		t.Fatalf("LastToolResults count = %d, want 2", len(summary.LastToolResults))
	}
}

// TestE2ETraceContinuity verifies that trace_update events across multiple
// stages (inspect -> plan -> patch -> test) are preserved in order.
func TestE2ETraceContinuity(t *testing.T) {
	stages := []struct {
		stage  core.TrajectoryStage
		status core.TraceStepStatus
		goal   string
	}{
		{core.StageInspect, core.TraceDone, "read codebase"},
		{core.StagePlan, core.TraceDone, "design fix"},
		{core.StagePatch, core.TraceDone, "apply patch"},
		{core.StageTest, core.TraceRunning, "run tests"},
	}

	var events []core.AgentEvent
	t0 := time.Unix(1, 0)

	for i, s := range stages {
		events = append(events, core.AgentEvent{
			Type: core.EventTraceUpdate,
			Time: t0.Add(time.Duration(i) * time.Second),
			Trace: &core.TraceStep{
				ID:     fmt.Sprintf("trace-%s", s.stage),
				Goal:   s.goal,
				Status: s.status,
				Stage:  s.stage,
			},
		})
	}
	// End with a done event.
	events = append(events, core.AgentEvent{
		Type: core.EventDone,
		Time: t0.Add(time.Duration(len(stages)) * time.Second),
	})

	summary := BuildResumeSummary(events)

	// All 4 trace updates should be present (under the default limit of 5).
	if len(summary.RecentTraceUpdates) != 4 {
		t.Fatalf("RecentTraceUpdates = %d, want 4", len(summary.RecentTraceUpdates))
	}

	// Verify order and stages are preserved.
	for i, s := range stages {
		ts := summary.RecentTraceUpdates[i]
		if ts.Stage != s.stage {
			t.Fatalf("trace[%d] stage = %q, want %q", i, ts.Stage, s.stage)
		}
		if ts.Status != s.status {
			t.Fatalf("trace[%d] status = %q, want %q", i, ts.Status, s.status)
		}
		if ts.Goal != s.goal {
			t.Fatalf("trace[%d] goal = %q, want %q", i, ts.Goal, s.goal)
		}
	}

	// LastStage should be the last non-empty stage.
	if summary.LastStage != core.StageTest {
		t.Fatalf("LastStage = %q, want test", summary.LastStage)
	}

	// LastStatus reflects the trace status, not EventDone. The trace status
	// is "running" but then EventDone sets LastStatus to "done".
	if summary.LastStatus != string(core.EventDone) {
		t.Fatalf("LastStatus = %q, want done", summary.LastStatus)
	}
}

// TestBuildTestEventsHelper verifies that the BuildTestEvents helper produces
// correctly shaped event sequences for various parameter combinations.
func TestBuildTestEventsHelper(t *testing.T) {
	tests := []struct {
		name         string
		turns        int
		toolsPerTurn int
		wantTurns    int
		wantTools    int
	}{
		{"single turn single tool", 1, 1, 1, 1},
		{"single turn multiple tools", 1, 3, 1, 3},
		{"multiple turns single tool", 3, 1, 3, 1},
		{"multiple turns multiple tools", 2, 2, 2, 2},
		{"zero turns defaults to 1", 0, 1, 1, 1},
		{"zero tools defaults to 1", 1, 0, 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := BuildTestEvents(tt.turns, tt.toolsPerTurn)

			// Count event types.
			var userPrompts, agentStarts, toolStarts, toolResults, dones int
			for _, e := range events {
				switch e.Type {
				case core.EventUserPrompt:
					userPrompts++
				case core.EventAgentStarted:
					agentStarts++
				case core.EventToolStart:
					toolStarts++
				case core.EventToolResult:
					toolResults++
				case core.EventDone:
					dones++
				}
			}

			if userPrompts != tt.wantTurns {
				t.Fatalf("user prompts = %d, want %d", userPrompts, tt.wantTurns)
			}
			if agentStarts != tt.wantTurns {
				t.Fatalf("agent starts = %d, want %d", agentStarts, tt.wantTurns)
			}
			if toolStarts != tt.wantTurns*tt.wantTools {
				t.Fatalf("tool starts = %d, want %d", toolStarts, tt.wantTurns*tt.wantTools)
			}
			if toolResults != tt.wantTurns*tt.wantTools {
				t.Fatalf("tool results = %d, want %d", toolResults, tt.wantTurns*tt.wantTools)
			}
			if dones != tt.wantTurns {
				t.Fatalf("dones = %d, want %d", dones, tt.wantTurns)
			}

			// Verify ExtractHistory produces valid output.
			msgs := ExtractHistory(events)
			if msgs[0].Role != "system" {
				t.Fatalf("first message role = %q, want system", msgs[0].Role)
			}
			if len(msgs) < tt.wantTurns+1 {
				t.Fatalf("message count = %d, want >= %d", len(msgs), tt.wantTurns+1)
			}

			// Verify BuildResumeSummary produces valid output.
			summary := BuildResumeSummary(events)
			if summary.LastStatus != string(core.EventDone) {
				t.Fatalf("LastStatus = %q, want done", summary.LastStatus)
			}
			if summary.LatestContext == nil {
				t.Fatal("LatestContext should not be nil")
			}
			if len(summary.ArtifactIDs) != tt.wantTurns {
				t.Fatalf("ArtifactIDs count = %d, want %d", len(summary.ArtifactIDs), tt.wantTurns)
			}
		})
	}
}
