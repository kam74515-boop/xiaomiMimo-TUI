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
	Stage  core.TrajectoryStage
	Time   time.Time
}

type ResumeSummary struct {
	EventCounts        map[core.EventType]int
	LastStatus         string
	LastError          string
	LatestContext      *core.ContextSnapshot
	RecentTraceUpdates []TraceStatus
	ArtifactIDs        []string
	RecentMessages     []core.Message
	LastStage          core.TrajectoryStage
	LastToolResults    []string
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
			if event.Trace.Stage != "" {
				summary.LastStage = event.Trace.Stage
			}
			if event.Trace.Status == core.TraceFailed && event.Trace.Observation != "" {
				summary.LastError = event.Trace.Observation
			}
			if recentTraceLimit > 0 {
				summary.RecentTraceUpdates = append(summary.RecentTraceUpdates, TraceStatus{
					ID:     event.Trace.ID,
					Goal:   event.Trace.Goal,
					Status: event.Trace.Status,
					Stage:  event.Trace.Stage,
					Time:   event.Time,
				})
				if len(summary.RecentTraceUpdates) > recentTraceLimit {
					summary.RecentTraceUpdates = summary.RecentTraceUpdates[1:]
				}
			}
		}

		// Collect tool result summaries (last 5).
		if event.Type == core.EventToolResult && event.Observation != nil {
			summary.LastToolResults = append(summary.LastToolResults, event.Observation.Summary)
			if len(summary.LastToolResults) > 5 {
				summary.LastToolResults = summary.LastToolResults[1:]
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

	// Extract recent messages for history propagation.
	summary.RecentMessages = ExtractHistory(events)

	return summary
}

// ExtractHistory reconstructs conversation messages from a sequence of
// AgentEvents. It rebuilds user, assistant, and tool messages so that the
// agent loop can continue the conversation with full historical context.
//
// The returned slice always starts with a placeholder system message,
// which the agent loop will overwrite with the current system prompt
// and context summary.
//
// The initial agent run emits EventMessageDelta without a preceding
// EventAgentStarted, so collection begins on the first delta. Multi-turn
// turns are marked by EventUserPrompt / EventAgentStarted pairs.
func ExtractHistory(events []core.AgentEvent) []core.Message {
	messages := []core.Message{
		{Role: "system", Content: "placeholder; will be replaced by agent loop"},
	}

	var deltas strings.Builder
	collecting := true // initial turn has no EventAgentStarted marker

	for _, e := range events {
		switch e.Type {
		case core.EventUserPrompt:
			// Flush any in-progress assistant message from previous turn.
			if deltas.Len() > 0 {
				messages = append(messages, core.Message{
					Role:    "assistant",
					Content: deltas.String(),
				})
				deltas.Reset()
			}
			messages = append(messages, core.Message{
				Role:    "user",
				Content: e.Message,
			})
		case core.EventAgentStarted:
			// New turn; flush previous assistant if any and reset.
			if deltas.Len() > 0 {
				messages = append(messages, core.Message{
					Role:    "assistant",
					Content: deltas.String(),
				})
				deltas.Reset()
			}
			collecting = true
		case core.EventMessageDelta:
			if collecting {
				deltas.WriteString(e.Message)
			}
		case core.EventToolResult:
			// Flush assistant deltas before the tool result.
			if deltas.Len() > 0 {
				messages = append(messages, core.Message{
					Role:    "assistant",
					Content: deltas.String(),
				})
				deltas.Reset()
			}
			toolCallID := ""
			if e.ToolCall != nil {
				toolCallID = e.ToolCall.ID
			}
			// Only include a brief summary, not raw artifact content.
			content := e.Message
			if len(content) > 1000 {
				content = content[:1000] + "..."
			}
			messages = append(messages, core.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: toolCallID,
			})
			// Continue collecting for follow-up assistant responses.
			collecting = true
		case core.EventDone:
			if deltas.Len() > 0 {
				messages = append(messages, core.Message{
					Role:    "assistant",
					Content: deltas.String(),
				})
				deltas.Reset()
			}
			collecting = false
		}
	}

	return messages
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
