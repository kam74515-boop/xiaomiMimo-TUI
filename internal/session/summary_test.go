package session

import (
	"testing"
	"time"

	"mimo-tui/internal/core"
)

func TestBuildResumeSummaryCompactsRecentEvents(t *testing.T) {
	firstContext := core.ContextSnapshot{
		WindowTokens: 100,
		UsedTokens:   10,
		Items: []core.ContextItem{{
			ID:            "near:first",
			Tier:          core.TierNear,
			TokenEstimate: 10,
		}},
	}
	latestContext := core.ContextSnapshot{
		WindowTokens: 100,
		UsedTokens:   20,
		Items: []core.ContextItem{{
			ID:            "anchor:latest",
			Tier:          core.TierAnchor,
			TokenEstimate: 20,
		}},
	}

	events := []core.AgentEvent{
		{
			Type:    core.EventTraceUpdate,
			Time:    time.Unix(1, 0),
			Trace:   &core.TraceStep{ID: "trace-1", Goal: "inspect", Status: core.TraceRunning},
			Message: "ignored for summary",
		},
		{
			Type:    core.EventContextUpdate,
			Time:    time.Unix(2, 0),
			Context: &firstContext,
		},
		{
			Type:        core.EventObservation,
			Time:        time.Unix(3, 0),
			Observation: &core.Observation{Summary: "read file", ArtifactID: "artifact-a"},
		},
		{
			Type:        core.EventObservation,
			Time:        time.Unix(4, 0),
			Observation: &core.Observation{Summary: "read file again", ArtifactID: "artifact-a"},
		},
		{
			Type:  core.EventTraceUpdate,
			Time:  time.Unix(5, 0),
			Trace: &core.TraceStep{ID: "trace-2", Goal: "patch", Status: core.TraceDone},
		},
		{
			Type:    core.EventContextUpdate,
			Time:    time.Unix(6, 0),
			Context: &latestContext,
		},
		{
			Type: core.EventError,
			Time: time.Unix(7, 0),
			Err:  "boom",
		},
	}

	got := BuildResumeSummary(events)
	if got.EventCounts[core.EventTraceUpdate] != 2 ||
		got.EventCounts[core.EventContextUpdate] != 2 ||
		got.EventCounts[core.EventObservation] != 2 ||
		got.EventCounts[core.EventError] != 1 {
		t.Fatalf("event counts = %+v", got.EventCounts)
	}
	if got.LastStatus != "error" || got.LastError != "boom" {
		t.Fatalf("last status/error = %q/%q, want error/boom", got.LastStatus, got.LastError)
	}
	if got.LatestContext == nil || got.LatestContext.UsedTokens != latestContext.UsedTokens {
		t.Fatalf("latest context = %+v, want used tokens %d", got.LatestContext, latestContext.UsedTokens)
	}
	if got.LatestContext == &latestContext {
		t.Fatal("latest context should be cloned")
	}
	if len(got.LatestContext.Items) != 1 || got.LatestContext.Items[0].ID != "anchor:latest" {
		t.Fatalf("latest context items = %+v", got.LatestContext.Items)
	}
	if len(got.RecentTraceUpdates) != 2 {
		t.Fatalf("recent traces = %d, want 2", len(got.RecentTraceUpdates))
	}
	if got.RecentTraceUpdates[0].ID != "trace-1" || got.RecentTraceUpdates[0].Status != core.TraceRunning {
		t.Fatalf("first trace = %+v", got.RecentTraceUpdates[0])
	}
	if got.RecentTraceUpdates[1].ID != "trace-2" || got.RecentTraceUpdates[1].Status != core.TraceDone {
		t.Fatalf("second trace = %+v", got.RecentTraceUpdates[1])
	}
	if len(got.ArtifactIDs) != 1 || got.ArtifactIDs[0] != "artifact-a" {
		t.Fatalf("artifact ids = %+v, want [artifact-a]", got.ArtifactIDs)
	}
}

func TestBuildResumeSummaryIncludesMessagesAndStage(t *testing.T) {
	events := []core.AgentEvent{
		{
			Type:    core.EventMessageDelta,
			Time:    time.Unix(1, 0),
			Message: "Let me read the file.",
		},
		{
			Type:        core.EventToolResult,
			Time:        time.Unix(2, 0),
			ToolName:    "read_file",
			Observation: &core.Observation{Summary: "read README.md"},
		},
		{
			Type:    core.EventMessageDelta,
			Time:    time.Unix(3, 0),
			Message: "Now I'll write the fix.",
		},
		{
			Type:  core.EventTraceUpdate,
			Time:  time.Unix(4, 0),
			Trace: &core.TraceStep{ID: "step-1", Goal: "patch README", Status: core.TraceDone, Stage: core.StagePatch},
		},
	}

	got := BuildResumeSummary(events)

	if len(got.RecentMessages) == 0 {
		t.Fatal("expected recent messages to be populated")
	}
	foundContent := false
	for _, msg := range got.RecentMessages {
		if msg.Role == "assistant" && len(msg.Content) > 0 {
			foundContent = true
			break
		}
	}
	if !foundContent {
		t.Fatalf("expected assistant message content, got: %+v", got.RecentMessages)
	}
	if got.LastStage != core.StagePatch {
		t.Fatalf("last stage = %q, want patch", got.LastStage)
	}
	if len(got.LastToolResults) != 1 || got.LastToolResults[0] != "read README.md" {
		t.Fatalf("last tool results = %v, want [read README.md]", got.LastToolResults)
	}

	// Verify stage is captured in RecentTraceUpdates.
	if len(got.RecentTraceUpdates) != 1 {
		t.Fatalf("recent traces = %d, want 1", len(got.RecentTraceUpdates))
	}
	if got.RecentTraceUpdates[0].Stage != core.StagePatch {
		t.Fatalf("trace stage = %q, want patch", got.RecentTraceUpdates[0].Stage)
	}
}

func TestBuildResumeSummaryLimitsRecentTraceStatuses(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventTraceUpdate, Time: time.Unix(1, 0), Trace: &core.TraceStep{ID: "one", Status: core.TraceRunning}},
		{Type: core.EventTraceUpdate, Time: time.Unix(2, 0), Trace: &core.TraceStep{ID: "two", Status: core.TraceFailed, Observation: "compile failed"}},
		{Type: core.EventTraceUpdate, Time: time.Unix(3, 0), Trace: &core.TraceStep{ID: "three", Status: core.TraceDone}},
	}

	got := BuildResumeSummaryWithLimit(events, 2)
	if len(got.RecentTraceUpdates) != 2 {
		t.Fatalf("recent trace count = %d, want 2", len(got.RecentTraceUpdates))
	}
	if got.RecentTraceUpdates[0].ID != "two" || got.RecentTraceUpdates[1].ID != "three" {
		t.Fatalf("recent traces = %+v, want two/three", got.RecentTraceUpdates)
	}
	if got.LastStatus != string(core.TraceDone) {
		t.Fatalf("last status = %q, want %q", got.LastStatus, core.TraceDone)
	}
	if got.LastError != "compile failed" {
		t.Fatalf("last error = %q, want compile failed", got.LastError)
	}

	got = BuildResumeSummaryWithLimit(events, 0)
	if len(got.RecentTraceUpdates) != 0 {
		t.Fatalf("recent traces with zero limit = %+v, want none", got.RecentTraceUpdates)
	}
}

func TestExtractHistoryRebuildsConversation(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Message: "hello world"},
		{Type: core.EventAgentStarted},
		{Type: core.EventMessageDelta, Message: "Hello! "},
		{Type: core.EventMessageDelta, Message: "How can I help?"},
		{Type: core.EventDone},
		{Type: core.EventUserPrompt, Message: "read the file"},
		{Type: core.EventAgentStarted},
		{Type: core.EventMessageDelta, Message: "Let me read it."},
		{Type: core.EventToolResult, Message: "file contents...", ToolCall: &core.ToolCall{ID: "call-1", Name: "read_file"}},
		{Type: core.EventMessageDelta, Message: "Done reading."},
		{Type: core.EventDone},
	}

	msgs := ExtractHistory(events)

	// messages[0] should be the system placeholder.
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", msgs[0].Role)
	}

	// Count user messages.
	userCount := 0
	assistantCount := 0
	toolCount := 0
	for _, m := range msgs {
		switch m.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		case "tool":
			toolCount++
		}
	}
	if userCount != 2 {
		t.Fatalf("user messages = %d, want 2", userCount)
	}
	if assistantCount < 1 {
		t.Fatalf("assistant messages = %d, want >= 1", assistantCount)
	}
	if toolCount != 1 {
		t.Fatalf("tool messages = %d, want 1", toolCount)
	}
}

func TestExtractHistoryTruncatesLongToolContent(t *testing.T) {
	longContent := ""
	for i := 0; i < 2000; i++ {
		longContent += "x"
	}
	events := []core.AgentEvent{
		{Type: core.EventUserPrompt, Message: "do it"},
		{Type: core.EventAgentStarted},
		{Type: core.EventMessageDelta, Message: "ok"},
		{Type: core.EventToolResult, Message: longContent, ToolCall: &core.ToolCall{ID: "call-1", Name: "shell"}},
		{Type: core.EventDone},
	}

	msgs := ExtractHistory(events)
	var toolMsg *core.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			toolMsg = &msgs[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("expected tool message")
	}
	if len(toolMsg.Content) > 1010 {
		t.Fatalf("tool content length = %d, want <= 1010 (len=%d)", len(toolMsg.Content), len(toolMsg.Content))
	}
	if toolMsg.ToolCallID != "call-1" {
		t.Fatalf("tool call id = %q, want call-1", toolMsg.ToolCallID)
	}
}

func TestExtractHistoryEmptyEvents(t *testing.T) {
	msgs := ExtractHistory(nil)
	if len(msgs) != 1 {
		t.Fatalf("empty events should yield 1 system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("sole message role = %q, want system", msgs[0].Role)
	}
}
