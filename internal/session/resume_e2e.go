package session

import (
	"fmt"
	"time"

	"mimo-tui/internal/core"
)

// BuildTestEvents generates a realistic sequence of AgentEvents simulating
// a multi-turn coding session. It is used by E2E tests and benchmarks.
//
// Each turn produces: user_prompt -> agent_started -> message_delta ->
// (tool_start -> tool_result -> observation -> context_update) repeated
// toolsPerTurn times -> trace_update -> done.
func BuildTestEvents(turns int, toolsPerTurn int) []core.AgentEvent {
	if turns < 1 {
		turns = 1
	}
	if toolsPerTurn < 1 {
		toolsPerTurn = 1
	}

	var events []core.AgentEvent
	t := time.Unix(1000, 0)

	for turn := 0; turn < turns; turn++ {
		// User prompt
		events = append(events, core.AgentEvent{
			Type:    core.EventUserPrompt,
			Time:    t,
			Message: fmt.Sprintf("Turn %d: please fix the bug in handler_%d.go", turn, turn),
		})
		t = t.Add(time.Second)

		// Agent started
		events = append(events, core.AgentEvent{
			Type: core.EventAgentStarted,
			Time: t,
		})
		t = t.Add(time.Second)

		// Assistant message delta
		events = append(events, core.AgentEvent{
			Type:    core.EventMessageDelta,
			Time:    t,
			Message: fmt.Sprintf("I'll look into handler_%d.go now. ", turn),
		})
		t = t.Add(time.Second)

		for toolIdx := 0; toolIdx < toolsPerTurn; toolIdx++ {
			callID := fmt.Sprintf("call-t%d-tl%d", turn, toolIdx)
			toolName := "read_file"
			if toolIdx%2 == 1 {
				toolName = "write_file"
			}

			// Tool start
			events = append(events, core.AgentEvent{
				Type:     core.EventToolStart,
				Time:     t,
				ToolName: toolName,
				ToolCall: &core.ToolCall{
					ID:   callID,
					Name: toolName,
					Raw:  fmt.Sprintf(`{"path":"handler_%d.go"}`, turn),
				},
			})
			t = t.Add(time.Second)

			artifactID := ""
			if toolIdx == toolsPerTurn-1 {
				artifactID = fmt.Sprintf("artifact-t%d", turn)
			}

			// Tool result
			events = append(events, core.AgentEvent{
				Type:     core.EventToolResult,
				Time:     t,
				ToolName: toolName,
				Message:  fmt.Sprintf("Result from %s on handler_%d.go", toolName, turn),
				ToolCall: &core.ToolCall{
					ID:   callID,
					Name: toolName,
				},
				Observation: &core.Observation{
					Summary:    fmt.Sprintf("%s completed for handler_%d.go", toolName, turn),
					ArtifactID: artifactID,
				},
			})
			t = t.Add(time.Second)

			// Observation event
			events = append(events, core.AgentEvent{
				Type: core.EventObservation,
				Time: t,
				Observation: &core.Observation{
					Summary:    fmt.Sprintf("Observed %s result for handler_%d.go", toolName, turn),
					ArtifactID: artifactID,
				},
			})
			t = t.Add(time.Second)

			// Context update
			tier := core.TierNear
			if toolIdx == toolsPerTurn-1 {
				tier = core.TierAnchor
			}
			events = append(events, core.AgentEvent{
				Type: core.EventContextUpdate,
				Time: t,
				Context: &core.ContextSnapshot{
					WindowTokens: 128000,
					UsedTokens:   1000 * (turn*toolsPerTurn + toolIdx + 1),
					Items: []core.ContextItem{
						{
							ID:              fmt.Sprintf("%s:handler_%d.go", tier, turn),
							Tier:            tier,
							Title:           fmt.Sprintf("handler_%d.go", turn),
							Source:          "tool",
							TokenEstimate:   500,
							Pinned:          toolIdx == 0 && turn == 0,
							SelectionReason: "recent_use",
						},
					},
				},
			})
			t = t.Add(time.Second)
		}

		// Trace update for the turn
		stages := []core.TrajectoryStage{core.StageInspect, core.StagePlan, core.StagePatch, core.StageTest}
		stage := stages[turn%len(stages)]
		events = append(events, core.AgentEvent{
			Type: core.EventTraceUpdate,
			Time: t,
			Trace: &core.TraceStep{
				ID:     fmt.Sprintf("trace-t%d", turn),
				Goal:   fmt.Sprintf("Fix handler_%d.go", turn),
				Status: core.TraceDone,
				Stage:  stage,
			},
		})
		t = t.Add(time.Second)

		// Done
		events = append(events, core.AgentEvent{
			Type: core.EventDone,
			Time: t,
		})
		t = t.Add(time.Second)
	}

	return events
}
