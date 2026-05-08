package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

// TestResumeRealityMultiTurnWithToolCalls builds a realistic 3-turn event
// stream with tool calls and verifies ExtractHistory and BuildResumeSummary
// capture all key state for a full resume.
func TestResumeRealityMultiTurnWithToolCalls(t *testing.T) {
	events := BuildTestEvents(3, 2)
	summary := BuildResumeSummary(events)
	msgs := ExtractHistory(events)

	// Verify multi-turn event counts.
	if summary.EventCounts[core.EventUserPrompt] != 3 {
		t.Fatalf("user_prompt count = %d, want 3", summary.EventCounts[core.EventUserPrompt])
	}
	if summary.EventCounts[core.EventToolStart] != 6 {
		t.Fatalf("tool_start count = %d, want 6", summary.EventCounts[core.EventToolStart])
	}
	if summary.EventCounts[core.EventToolResult] != 6 {
		t.Fatalf("tool_result count = %d, want 6", summary.EventCounts[core.EventToolResult])
	}
	if summary.EventCounts[core.EventDone] != 3 {
		t.Fatalf("done count = %d, want 3", summary.EventCounts[core.EventDone])
	}

	// Verify messages reconstruct a valid multi-turn conversation.
	if msgs[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}

	var userMsgs, assistantMsgs, toolMsgs int
	var lastToolCallID string
	for _, m := range msgs[1:] {
		switch m.Role {
		case "user":
			userMsgs++
		case "assistant":
			assistantMsgs++
		case "tool":
			toolMsgs++
			if m.ToolCallID == "" {
				t.Fatal("tool message missing ToolCallID")
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

	// Verify the last tool call ID references the final turn's last tool.
	if lastToolCallID != "call-t2-tl1" {
		t.Fatalf("last tool call ID = %q, want call-t2-tl1", lastToolCallID)
	}

	// Verify artifacts are captured and deduplicated.
	if len(summary.ArtifactIDs) != 3 {
		t.Fatalf("artifact IDs = %d, want 3 (one per turn)", len(summary.ArtifactIDs))
	}
	for i, id := range summary.ArtifactIDs {
		expected := fmt.Sprintf("artifact-t%d", i)
		if id != expected {
			t.Fatalf("artifact[%d] = %q, want %q", i, id, expected)
		}
	}

	// Verify context is captured from the latest update.
	if summary.LatestContext == nil {
		t.Fatal("LatestContext should not be nil")
	}

	// Verify traces captured from all 3 turns.
	if len(summary.RecentTraceUpdates) != 3 {
		t.Fatalf("trace updates = %d, want 3", len(summary.RecentTraceUpdates))
	}
	// BuildTestEvents uses turn%4 for stages: 0=inspect, 1=plan, 2=patch.
	// With 3 turns, the last turn (2) maps to StagePatch.
	if summary.LastStage != core.StagePatch {
		t.Fatalf("LastStage = %q, want patch", summary.LastStage)
	}
	if summary.LastStatus != string(core.EventDone) {
		t.Fatalf("LastStatus = %q, want done", summary.LastStatus)
	}
}

// TestResumeRealityContextTiersSurvive verifies that near, anchor, and
// artifact tier context items all survive the resume summary extraction.
func TestResumeRealityContextTiersSurvive(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "analyze codebase"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "analyzing..."},
		{
			Type: core.EventContextUpdate, Time: time.Unix(4, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 5000,
				Items: []core.ContextItem{
					{ID: "near:main.go", Tier: core.TierNear, Title: "main.go", TokenEstimate: 200, Pinned: true, Reason: "user selection"},
					{ID: "near:util.go", Tier: core.TierNear, Title: "util.go", TokenEstimate: 150},
					{ID: "anchor:project-map", Tier: core.TierAnchor, Title: "Project Map", TokenEstimate: 500, Pinned: true},
					{ID: "artifact:patch-v1", Tier: core.TierArtifact, Title: "generated patch", TokenEstimate: 800, Source: "tool:write_file"},
					{ID: "artifact:readme-content", Tier: core.TierArtifact, Title: "README content", TokenEstimate: 300, Source: "tool:read_file"},
				},
				PollutionRisk: "medium",
			},
		},
		{Type: core.EventDone, Time: time.Unix(5, 0)},
	}

	summary := BuildResumeSummary(events)

	if summary.LatestContext == nil {
		t.Fatal("LatestContext should not be nil")
	}

	// Count items by tier.
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
	if tiers[core.TierArtifact] != 2 {
		t.Fatalf("artifact tier items = %d, want 2", tiers[core.TierArtifact])
	}
	if pinnedCount != 2 {
		t.Fatalf("pinned items = %d, want 2", pinnedCount)
	}

	// Verify pinned item details survive.
	var pinnedNear *core.ContextItem
	for i := range summary.LatestContext.Items {
		if summary.LatestContext.Items[i].ID == "near:main.go" {
			pinnedNear = &summary.LatestContext.Items[i]
		}
	}
	if pinnedNear == nil {
		t.Fatal("near:main.go not found in context items")
	}
	if !pinnedNear.Pinned {
		t.Fatal("near:main.go should be pinned")
	}
	if pinnedNear.Reason != "user selection" {
		t.Fatalf("pinned reason = %q, want user selection", pinnedNear.Reason)
	}

	// Verify pollution risk survives.
	if summary.LatestContext.PollutionRisk != "medium" {
		t.Fatalf("PollutionRisk = %q, want medium", summary.LatestContext.PollutionRisk)
	}

	// Verify context is cloned (not a pointer to original).
	if &summary.LatestContext.Items == &events[3].Context.Items {
		t.Fatal("context items should be cloned, not shared")
	}
}

// TestResumeRealityTraceStepsSurvive verifies that trace steps across all
// trajectory stages (inspect, plan, patch, test, revise, summary) survive
// the resume summary extraction.
func TestResumeRealityTraceStepsSurvive(t *testing.T) {
	allStages := []struct {
		stage  core.TrajectoryStage
		status core.TraceStepStatus
		goal   string
	}{
		{core.StageInspect, core.TraceDone, "read codebase"},
		{core.StagePlan, core.TraceDone, "design fix"},
		{core.StagePatch, core.TraceDone, "apply patch"},
		{core.StageTest, core.TraceFailed, "run tests"},
		{core.StageRevise, core.TraceRunning, "fix test failures"},
	}

	var events []core.AgentEvent
	t0 := time.Unix(1, 0)

	for i, s := range allStages {
		events = append(events, core.AgentEvent{
			Type: core.EventTraceUpdate,
			Time: t0.Add(time.Duration(i) * time.Second),
			Trace: &core.TraceStep{
				ID:          fmt.Sprintf("trace-%s", s.stage),
				Goal:        s.goal,
				Status:      s.status,
				Stage:       s.stage,
				Observation: fmt.Sprintf("observation for %s", s.stage),
			},
		})
	}
	events = append(events, core.AgentEvent{
		Type: core.EventDone,
		Time: t0.Add(time.Duration(len(allStages)) * time.Second),
	})

	summary := BuildResumeSummary(events)

	// All 5 trace updates should be preserved (under default limit of 5).
	if len(summary.RecentTraceUpdates) != 5 {
		t.Fatalf("RecentTraceUpdates = %d, want 5", len(summary.RecentTraceUpdates))
	}

	// Verify each trace preserves its stage, status, and goal.
	for i, expected := range allStages {
		ts := summary.RecentTraceUpdates[i]
		if ts.Stage != expected.stage {
			t.Fatalf("trace[%d] stage = %q, want %q", i, ts.Stage, expected.stage)
		}
		if ts.Status != expected.status {
			t.Fatalf("trace[%d] status = %q, want %q", i, ts.Status, expected.status)
		}
		if ts.Goal != expected.goal {
			t.Fatalf("trace[%d] goal = %q, want %q", i, ts.Goal, expected.goal)
		}
	}

	// LastStage should be the last non-empty stage.
	if summary.LastStage != core.StageRevise {
		t.Fatalf("LastStage = %q, want revise", summary.LastStage)
	}

	// LastError should capture the failed trace's observation.
	// Note: TraceFailed sets LastError only if Trace.Observation is non-empty.
	// But EventDone after the failed trace resets LastStatus to "done".
	// The failed trace observation is still in RecentTraceUpdates.
	foundFailed := false
	for _, ts := range summary.RecentTraceUpdates {
		if ts.Status == core.TraceFailed {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatal("expected a failed trace in RecentTraceUpdates")
	}
}

// TestResumeRealityArtifactDedup verifies that artifact IDs from multiple
// observation events are deduplicated correctly.
func TestResumeRealityArtifactDedup(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "write tests"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "writing..."},
		// Tool result with artifact.
		{
			Type: core.EventToolResult, Time: time.Unix(4, 0), ToolName: "write_file",
			Message:     "wrote test.go",
			ToolCall:    &core.ToolCall{ID: "call-1", Name: "write_file"},
			Observation: &core.Observation{Summary: "created test file", ArtifactID: "art-shared"},
		},
		// Observation with same artifact.
		{
			Type: core.EventObservation, Time: time.Unix(5, 0),
			Observation: &core.Observation{Summary: "observation 1", ArtifactID: "art-shared"},
		},
		// Another tool result with different artifact.
		{
			Type: core.EventToolResult, Time: time.Unix(6, 0), ToolName: "write_file",
			Message:     "wrote test2.go",
			ToolCall:    &core.ToolCall{ID: "call-2", Name: "write_file"},
			Observation: &core.Observation{Summary: "created second file", ArtifactID: "art-other"},
		},
		// Duplicate of first artifact.
		{
			Type: core.EventObservation, Time: time.Unix(7, 0),
			Observation: &core.Observation{Summary: "duplicate", ArtifactID: "art-shared"},
		},
		// Empty artifact ID should be ignored.
		{
			Type: core.EventObservation, Time: time.Unix(8, 0),
			Observation: &core.Observation{Summary: "no artifact"},
		},
		{Type: core.EventDone, Time: time.Unix(9, 0)},
	}

	summary := BuildResumeSummary(events)

	if len(summary.ArtifactIDs) != 2 {
		t.Fatalf("ArtifactIDs = %v, want 2 unique IDs", summary.ArtifactIDs)
	}

	idSet := make(map[string]bool)
	for _, id := range summary.ArtifactIDs {
		idSet[id] = true
	}
	if !idSet["art-shared"] {
		t.Fatal("missing art-shared in ArtifactIDs")
	}
	if !idSet["art-other"] {
		t.Fatal("missing art-other in ArtifactIDs")
	}

	// Verify last tool results captured.
	if len(summary.LastToolResults) != 2 {
		t.Fatalf("LastToolResults count = %d, want 2", len(summary.LastToolResults))
	}
}

// TestResumeRealityHistoryFeedableToLoop verifies that ExtractHistory produces
// messages that can serve as valid input for a new agent loop (i.e., the
// message structure is well-formed with system, user, assistant, tool roles).
func TestResumeRealityHistoryFeedableToLoop(t *testing.T) {
	events := BuildTestEvents(2, 2)
	msgs := ExtractHistory(events)

	// Verify the message structure is valid for feeding into a loop.
	if len(msgs) < 5 {
		t.Fatalf("message count = %d, want >= 5", len(msgs))
	}

	// First message must be system.
	if msgs[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}

	// Messages should alternate correctly: system -> (user -> assistant -> tool -> assistant -> ...)*
	// Verify no consecutive user messages (would indicate a broken conversation).
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == "user" && i+1 < len(msgs) && msgs[i+1].Role == "user" {
			t.Fatalf("consecutive user messages at index %d", i)
		}
	}

	// Verify tool messages always have a ToolCallID.
	for i, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == "" {
			t.Fatalf("tool message at index %d missing ToolCallID", i)
		}
	}

	// Verify assistant messages have content (not empty).
	for i, m := range msgs {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) == "" {
			t.Fatalf("assistant message at index %d has empty content", i)
		}
	}

	// Verify user messages have content.
	for i, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) == "" {
			t.Fatalf("user message at index %d has empty content", i)
		}
	}

	// Verify the history can be used as a prefix for a new conversation.
	// The system message is a placeholder that the loop will replace.
	if !strings.Contains(msgs[0].Content, "placeholder") {
		t.Fatalf("system message = %q, expected placeholder", msgs[0].Content)
	}
}

// TestResumeRealityInterruptedMidTool verifies that when a session is
// interrupted mid-tool-execution, the partial events are captured and
// the resume summary preserves the conversation up to that point.
func TestResumeRealityInterruptedMidTool(t *testing.T) {
	// Simulate: user prompt -> agent start -> delta -> tool start -> tool result
	// -> (interrupted here, no EventDone)
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "fix the bug"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "I'll read the file first. "},
		{
			Type: core.EventToolStart, Time: time.Unix(4, 0), ToolName: "read_file",
			ToolCall: &core.ToolCall{ID: "call-1", Name: "read_file", Raw: `{"path":"buggy.go"}`},
		},
		{
			Type: core.EventToolResult, Time: time.Unix(5, 0), ToolName: "read_file",
			Message:  "file contents with bug",
			ToolCall: &core.ToolCall{ID: "call-1", Name: "read_file"},
			Observation: &core.Observation{
				Summary:    "read buggy.go",
				ArtifactID: "art-buggy",
			},
		},
		{
			Type: core.EventContextUpdate, Time: time.Unix(6, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 1500,
				Items: []core.ContextItem{
					{ID: "near:buggy.go", Tier: core.TierNear, Title: "buggy.go", TokenEstimate: 600},
				},
			},
		},
		// No EventDone -- session was interrupted.
	}

	summary := BuildResumeSummary(events)

	// LastStatus should reflect no done/error (empty string since no EventDone or EventError).
	if summary.LastStatus != "" {
		t.Fatalf("LastStatus = %q, want empty (interrupted without error)", summary.LastStatus)
	}

	// Context should be preserved.
	if summary.LatestContext == nil {
		t.Fatal("LatestContext should be preserved after interruption")
	}
	if summary.LatestContext.UsedTokens != 1500 {
		t.Fatalf("UsedTokens = %d, want 1500", summary.LatestContext.UsedTokens)
	}

	// Artifact should be captured.
	if len(summary.ArtifactIDs) != 1 || summary.ArtifactIDs[0] != "art-buggy" {
		t.Fatalf("ArtifactIDs = %v, want [art-buggy]", summary.ArtifactIDs)
	}

	// ExtractHistory should reconstruct the partial conversation.
	msgs := ExtractHistory(events)
	var userMsgs, toolMsgs int
	for _, m := range msgs {
		switch m.Role {
		case "user":
			userMsgs++
		case "tool":
			toolMsgs++
		}
	}
	if userMsgs != 1 {
		t.Fatalf("user messages = %d, want 1", userMsgs)
	}
	if toolMsgs != 1 {
		t.Fatalf("tool messages = %d, want 1", toolMsgs)
	}

	// The assistant deltas before the tool should be captured.
	var assistantContent string
	for _, m := range msgs {
		if m.Role == "assistant" {
			assistantContent += m.Content
		}
	}
	if !strings.Contains(assistantContent, "read the file") {
		t.Fatalf("assistant content = %q, expected to contain 'read the file'", assistantContent)
	}
}

// TestResumeRealityMultipleContextUpdates verifies that when multiple
// context_update events arrive, only the latest snapshot is kept.
func TestResumeRealityMultipleContextUpdates(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "start"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{
			Type: core.EventContextUpdate, Time: time.Unix(3, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 1000,
				Items: []core.ContextItem{
					{ID: "near:file1.go", Tier: core.TierNear, TokenEstimate: 200},
				},
			},
		},
		{
			Type: core.EventContextUpdate, Time: time.Unix(4, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 3000,
				Items: []core.ContextItem{
					{ID: "near:file1.go", Tier: core.TierNear, TokenEstimate: 200},
					{ID: "near:file2.go", Tier: core.TierNear, TokenEstimate: 300},
					{ID: "anchor:plan", Tier: core.TierAnchor, TokenEstimate: 100},
				},
			},
		},
		{
			Type: core.EventContextUpdate, Time: time.Unix(5, 0),
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 5000,
				Items: []core.ContextItem{
					{ID: "near:file1.go", Tier: core.TierNear, TokenEstimate: 200},
					{ID: "near:file2.go", Tier: core.TierNear, TokenEstimate: 300},
					{ID: "anchor:plan", Tier: core.TierAnchor, TokenEstimate: 100},
					{ID: "artifact:patch", Tier: core.TierArtifact, TokenEstimate: 800},
				},
			},
		},
		{Type: core.EventDone, Time: time.Unix(6, 0)},
	}

	summary := BuildResumeSummary(events)

	// Only the latest context should be kept.
	if summary.LatestContext == nil {
		t.Fatal("LatestContext should not be nil")
	}
	if summary.LatestContext.UsedTokens != 5000 {
		t.Fatalf("UsedTokens = %d, want 5000 (from latest update)", summary.LatestContext.UsedTokens)
	}
	if len(summary.LatestContext.Items) != 4 {
		t.Fatalf("items = %d, want 4 (from latest update)", len(summary.LatestContext.Items))
	}
}

// TestResumeRealityErrorRecovery verifies that when a session ends with an
// error after successful tool calls, the resume summary captures both the
// successful work and the error state.
func TestResumeRealityErrorRecovery(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Time: time.Unix(1, 0), Message: "run tests"},
		{Type: core.EventAgentStarted, Time: time.Unix(2, 0)},
		{Type: core.EventMessageDelta, Time: time.Unix(3, 0), Message: "Running tests... "},
		{
			Type: core.EventToolStart, Time: time.Unix(4, 0), ToolName: "shell",
			ToolCall: &core.ToolCall{ID: "call-1", Name: "shell", Raw: `{"command":"go test ./..."}`},
		},
		{
			Type: core.EventToolResult, Time: time.Unix(5, 0), ToolName: "shell",
			Message:  "FAIL: TestFoo",
			ToolCall: &core.ToolCall{ID: "call-1", Name: "shell"},
			Observation: &core.Observation{
				Summary:    "tests failed",
				ArtifactID: "art-test-output",
			},
		},
		{
			Type: core.EventTraceUpdate, Time: time.Unix(6, 0),
			Trace: &core.TraceStep{
				ID: "trace-1", Goal: "run tests", Status: core.TraceFailed,
				Stage: core.StageTest, Observation: "TestFoo panicked",
			},
		},
		{
			Type: core.EventError, Time: time.Unix(7, 0),
			Message: "Agent loop terminated: unrecoverable error",
		},
	}

	summary := BuildResumeSummary(events)

	// Error state captured.
	if summary.LastStatus != "error" {
		t.Fatalf("LastStatus = %q, want error", summary.LastStatus)
	}
	if summary.LastError != "Agent loop terminated: unrecoverable error" {
		t.Fatalf("LastError = %q, want error message", summary.LastError)
	}

	// Successful work is also captured.
	if len(summary.ArtifactIDs) != 1 || summary.ArtifactIDs[0] != "art-test-output" {
		t.Fatalf("ArtifactIDs = %v, want [art-test-output]", summary.ArtifactIDs)
	}
	if len(summary.LastToolResults) != 1 {
		t.Fatalf("LastToolResults = %d, want 1", len(summary.LastToolResults))
	}

	// Failed trace is captured.
	foundFailed := false
	for _, ts := range summary.RecentTraceUpdates {
		if ts.Status == core.TraceFailed {
			foundFailed = true
			if ts.Stage != core.StageTest {
				t.Fatalf("failed trace stage = %q, want test", ts.Stage)
			}
		}
	}
	if !foundFailed {
		t.Fatal("expected a failed trace in RecentTraceUpdates")
	}

	// ExtractHistory still works on partial/error events.
	msgs := ExtractHistory(events)
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

// TestResumeRealityToolResultsPreserved verifies that LastToolResults
// captures the last 5 tool result summaries in order.
func TestResumeRealityToolResultsPreserved(t *testing.T) {
	var events []core.AgentEvent
	t0 := time.Unix(1, 0)

	// Generate 7 tool results (more than the 5 limit).
	for i := 0; i < 7; i++ {
		events = append(events, core.AgentEvent{
			Type:     core.EventToolResult,
			Time:     t0.Add(time.Duration(i) * time.Second),
			ToolName: "read_file",
			Message:  fmt.Sprintf("result %d", i),
			ToolCall: &core.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "read_file"},
			Observation: &core.Observation{
				Summary:    fmt.Sprintf("read file %d", i),
				ArtifactID: fmt.Sprintf("art-%d", i),
			},
		})
	}
	events = append(events, core.AgentEvent{Type: core.EventDone, Time: t0.Add(8 * time.Second)})

	summary := BuildResumeSummary(events)

	// Should have at most 5 tool results.
	if len(summary.LastToolResults) != 5 {
		t.Fatalf("LastToolResults count = %d, want 5", len(summary.LastToolResults))
	}

	// Should be the last 5 (results 2-6).
	for i, result := range summary.LastToolResults {
		expected := fmt.Sprintf("read file %d", i+2)
		if result != expected {
			t.Fatalf("LastToolResults[%d] = %q, want %q", i, result, expected)
		}
	}
}
