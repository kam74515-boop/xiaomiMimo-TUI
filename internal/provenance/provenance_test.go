package provenance

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func snap(used int, items ...core.ContextItem) core.ContextSnapshot {
	return core.ContextSnapshot{WindowTokens: 1000, UsedTokens: used, Items: items, PollutionRisk: "low"}
}

func ev(t core.EventType) core.AgentEvent { return core.AgentEvent{Type: t} }

func TestBuildTimeline(t *testing.T) {
	s1 := snap(100, core.ContextItem{ID: "a", Tier: core.TierAnchor, Title: "proj"})
	s2 := snap(180,
		core.ContextItem{ID: "a", Tier: core.TierAnchor, Title: "proj"},
		core.ContextItem{ID: "obs1", Tier: core.TierNear, Title: "read README"},
	)
	events := []core.AgentEvent{
		{Type: core.EventContextUpdate, Context: &s1},
		{Type: core.EventTraceUpdate, Trace: &core.TraceStep{ID: "agent-step-0-1", Status: core.TraceRunning, Goal: "step 1"}},
		{Type: core.EventToolStart, ToolName: "read_file", ToolCall: &core.ToolCall{Name: "read_file", Input: core.ToolInput{"path": "README.md"}}},
		{Type: core.EventToolResult, ToolName: "read_file"},
		{Type: core.EventObservation, ToolName: "read_file", Observation: &core.Observation{Summary: "read README.md", ArtifactID: "art-1"}},
		{Type: core.EventContextUpdate, Context: &s2},
		{Type: core.EventTraceUpdate, Trace: &core.TraceStep{ID: "agent-step-1-2", Status: core.TraceRunning, Goal: "step 2"}},
		{Type: core.EventMessageDelta, Message: "final answer"},
		ev(core.EventDone),
	}

	tl := Build(events)
	if len(tl.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(tl.Steps))
	}
	if len(tl.Snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(tl.Snapshots))
	}
	step1 := tl.Steps[0]
	if len(step1.Tools) != 1 {
		t.Fatalf("step1 tools = %d, want 1", len(step1.Tools))
	}
	if step1.Tools[0].ArtifactID != "art-1" || !strings.Contains(step1.Tools[0].Observation, "README") {
		t.Fatalf("tool evidence not linked: %+v", step1.Tools[0])
	}
	if step1.Context == nil || step1.Context.UsedTokens != 100 {
		t.Fatalf("step1 context not the snapshot active at the time: %+v", step1.Context)
	}
	if !strings.Contains(tl.Steps[1].Assistant, "final answer") {
		t.Fatalf("step2 assistant = %q", tl.Steps[1].Assistant)
	}
}

func TestContextDiff(t *testing.T) {
	before := snap(100,
		core.ContextItem{ID: "a", Tier: core.TierNear, Title: "A"},
		core.ContextItem{ID: "b", Tier: core.TierNear, Title: "B"},
	)
	after := snap(140,
		core.ContextItem{ID: "a", Tier: core.TierAnchor, Title: "A"}, // promoted
		core.ContextItem{ID: "c", Tier: core.TierNear, Title: "C"},   // added
	) // b removed

	d := ContextDiff(before, after)
	if len(d.Added) != 1 || d.Added[0].ID != "c" {
		t.Fatalf("added = %+v, want [c]", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0].ID != "b" {
		t.Fatalf("removed = %+v, want [b]", d.Removed)
	}
	if len(d.Changed) != 1 || d.Changed[0].ID != "a" || d.Changed[0].ToTier != core.TierAnchor {
		t.Fatalf("changed = %+v, want a near→anchor", d.Changed)
	}
	if d.TokensBefore != 100 || d.TokensAfter != 140 {
		t.Fatalf("token deltas wrong: %d → %d", d.TokensBefore, d.TokensAfter)
	}
	out := FormatDiff(d)
	if !strings.Contains(out, "+ C") || !strings.Contains(out, "- B") || !strings.Contains(out, "~ A") {
		t.Fatalf("FormatDiff missing entries: %q", out)
	}
}

func TestBuildNoStepsIsSafe(t *testing.T) {
	tl := Build(nil)
	if len(tl.Steps) != 0 || len(tl.Snapshots) != 0 {
		t.Fatalf("empty build should be empty: %+v", tl)
	}
	if !strings.Contains(Format(tl), "Steps: 0") {
		t.Fatal("Format should handle empty timeline")
	}
}
