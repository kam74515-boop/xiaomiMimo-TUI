package benchmark

import (
	"fmt"
	"time"

	"mimo-tui/internal/core"
	"mimo-tui/internal/session"
)

// ResumeBenchmarkResult captures performance metrics for resume operations.
type ResumeBenchmarkResult struct {
	EventCount       int
	ExtractHistoryNs int64
	BuildSummaryNs   int64
	MessageCount     int
	ArtifactCount    int
	TraceCount       int
	ContextItemCount int
}

// GenerateResumeEvents creates N synthetic AgentEvents for benchmarking.
// Events are distributed across a realistic mix of event types.
func GenerateResumeEvents(n int) []core.AgentEvent {
	events := make([]core.AgentEvent, 0, n)
	t := time.Unix(1000, 0)

	// Distribute events across turns. Each turn has roughly:
	// 1 user_prompt, 1 agent_started, 3 message_delta, 2 tool_start,
	// 2 tool_result, 2 observation, 1 context_update, 1 trace_update, 1 done.

	for turn := 0; len(events) < n; turn++ {
		addEvent := func(e core.AgentEvent) {
			if len(events) < n {
				events = append(events, e)
			}
		}

		addEvent(core.AgentEvent{Type: core.EventUserPrompt, Time: t, Message: fmt.Sprintf("Fix bug #%d", turn)})
		t = t.Add(time.Millisecond)
		addEvent(core.AgentEvent{Type: core.EventAgentStarted, Time: t})
		t = t.Add(time.Millisecond)

		for d := 0; d < 3 && len(events) < n; d++ {
			addEvent(core.AgentEvent{Type: core.EventMessageDelta, Time: t, Message: fmt.Sprintf("Step %d of analysis. ", d)})
			t = t.Add(time.Millisecond)
		}

		for toolIdx := 0; toolIdx < 2 && len(events) < n; toolIdx++ {
			callID := fmt.Sprintf("call-bm-t%d-tl%d", turn, toolIdx)
			addEvent(core.AgentEvent{
				Type: core.EventToolStart, Time: t, ToolName: "read_file",
				ToolCall: &core.ToolCall{ID: callID, Name: "read_file", Raw: `{"path":"file.go"}`},
			})
			t = t.Add(time.Millisecond)
			addEvent(core.AgentEvent{
				Type: core.EventToolResult, Time: t, ToolName: "read_file",
				Message:  "file contents here",
				ToolCall: &core.ToolCall{ID: callID, Name: "read_file"},
				Observation: &core.Observation{
					Summary:    "read file",
					ArtifactID: fmt.Sprintf("art-bm-%d", turn),
				},
			})
			t = t.Add(time.Millisecond)
			addEvent(core.AgentEvent{
				Type: core.EventObservation, Time: t,
				Observation: &core.Observation{Summary: "observation", ArtifactID: fmt.Sprintf("art-bm-%d", turn)},
			})
			t = t.Add(time.Millisecond)
		}

		addEvent(core.AgentEvent{
			Type: core.EventContextUpdate, Time: t,
			Context: &core.ContextSnapshot{
				WindowTokens: 128000, UsedTokens: 1000 * (turn + 1),
				Items: []core.ContextItem{
					{ID: fmt.Sprintf("near:file-%d.go", turn), Tier: core.TierNear, TokenEstimate: 500},
				},
			},
		})
		t = t.Add(time.Millisecond)

		addEvent(core.AgentEvent{
			Type: core.EventTraceUpdate, Time: t,
			Trace: &core.TraceStep{
				ID: fmt.Sprintf("trace-bm-%d", turn), Goal: "fix bug",
				Status: core.TraceDone, Stage: core.StagePatch,
			},
		})
		t = t.Add(time.Millisecond)

		addEvent(core.AgentEvent{Type: core.EventDone, Time: t})
		t = t.Add(time.Millisecond)
	}

	return events[:n]
}

// RunResumeBenchmark runs ExtractHistory and BuildResumeSummary on synthetic
// events and returns timing results.
func RunResumeBenchmark(eventCount int) ResumeBenchmarkResult {
	events := GenerateResumeEvents(eventCount)

	start := time.Now()
	msgs := session.ExtractHistory(events)
	extractNs := time.Since(start).Nanoseconds()

	start = time.Now()
	summary := session.BuildResumeSummary(events)
	buildNs := time.Since(start).Nanoseconds()

	return ResumeBenchmarkResult{
		EventCount:       eventCount,
		ExtractHistoryNs: extractNs,
		BuildSummaryNs:   buildNs,
		MessageCount:     len(msgs),
		ArtifactCount:    len(summary.ArtifactIDs),
		TraceCount:       len(summary.RecentTraceUpdates),
		ContextItemCount: contextItemCount(summary.LatestContext),
	}
}

func contextItemCount(ctx *core.ContextSnapshot) int {
	if ctx == nil {
		return 0
	}
	return len(ctx.Items)
}

// FormatResumeReport produces a human-readable benchmark report.
func FormatResumeReport(results []ResumeBenchmarkResult) string {
	report := "# Resume Benchmark Results\n\n"
	report += "| Events | ExtractHistory (ms) | BuildSummary (ms) | Messages | Artifacts | Traces |\n"
	report += "|--------|--------------------|--------------------|----------|-----------|--------|\n"

	for _, r := range results {
		report += fmt.Sprintf("| %d | %.3f | %.3f | %d | %d | %d |\n",
			r.EventCount,
			float64(r.ExtractHistoryNs)/1e6,
			float64(r.BuildSummaryNs)/1e6,
			r.MessageCount,
			r.ArtifactCount,
			r.TraceCount,
		)
	}

	return report
}
