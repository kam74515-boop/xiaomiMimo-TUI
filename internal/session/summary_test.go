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
