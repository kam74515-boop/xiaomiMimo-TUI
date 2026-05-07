package session

import (
	"strings"
	"time"

	"mimo-tui/internal/core"
)

const DefaultRecentTraceLimit = 5

type TraceStatus struct {
	ID     string
	Goal   string
	Status core.TraceStepStatus
	Time   time.Time
}

type ResumeSummary struct {
	EventCounts        map[core.EventType]int
	LastStatus         string
	LastError          string
	LatestContext      *core.ContextSnapshot
	RecentTraceUpdates []TraceStatus
	ArtifactIDs        []string
}

func BuildResumeSummary(events []core.AgentEvent) ResumeSummary {
	return BuildResumeSummaryWithLimit(events, DefaultRecentTraceLimit)
}

func BuildResumeSummaryWithLimit(events []core.AgentEvent, recentTraceLimit int) ResumeSummary {
	if recentTraceLimit < 0 {
		recentTraceLimit = 0
	}

	summary := ResumeSummary{
		EventCounts: make(map[core.EventType]int),
	}
	seenArtifacts := make(map[string]struct{})

	for _, event := range events {
		if event.Type != "" {
			summary.EventCounts[event.Type]++
		}
		if event.Context != nil {
			snapshot := cloneContextSnapshot(*event.Context)
			summary.LatestContext = &snapshot
		}
		if event.Observation != nil {
			addArtifactID(&summary, seenArtifacts, event.Observation.ArtifactID)
		}
		if event.Trace != nil && event.Trace.Status != "" {
			summary.LastStatus = string(event.Trace.Status)
			if event.Trace.Status == core.TraceFailed && event.Trace.Observation != "" {
				summary.LastError = event.Trace.Observation
			}
			if recentTraceLimit > 0 {
				summary.RecentTraceUpdates = append(summary.RecentTraceUpdates, TraceStatus{
					ID:     event.Trace.ID,
					Goal:   event.Trace.Goal,
					Status: event.Trace.Status,
					Time:   event.Time,
				})
				if len(summary.RecentTraceUpdates) > recentTraceLimit {
					summary.RecentTraceUpdates = summary.RecentTraceUpdates[1:]
				}
			}
		}

		switch {
		case event.Err != "":
			summary.LastStatus = "error"
			summary.LastError = event.Err
		case event.Type == core.EventError:
			summary.LastStatus = "error"
			if event.Message != "" {
				summary.LastError = event.Message
			}
		case event.Type == core.EventDone:
			summary.LastStatus = string(core.EventDone)
		}
	}

	return summary
}

func addArtifactID(summary *ResumeSummary, seen map[string]struct{}, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	summary.ArtifactIDs = append(summary.ArtifactIDs, id)
}

func cloneContextSnapshot(snapshot core.ContextSnapshot) core.ContextSnapshot {
	snapshot.Items = append([]core.ContextItem(nil), snapshot.Items...)
	return snapshot
}
