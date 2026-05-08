package eval

import (
	"fmt"
	"strings"

	"mimo-tui/internal/core"
)

// Trajectory represents a structured summary of an agent session.
type Trajectory struct {
	SessionID string
	Steps     []TrajectoryStep
	Success   bool
	TokenCost core.CostUpdate
	Error     string
}

// TrajectoryStep groups the model output, tool calls, observations,
// and trace updates that belong to a single loop iteration.
type TrajectoryStep struct {
	ToolCalls    []core.ToolCall
	Observations []core.Observation
	TraceUpdates []core.TraceStep
}

// ExtractTrajectory extracts a structured trajectory from agent events.
//
// It detects step boundaries via agent-step-* trace updates that enter the
// running status. All other events (tool_start, tool_result, observation,
// cost, error) are accumulated into the current step. Success is determined
// by the absence of error events.
func ExtractTrajectory(events []core.AgentEvent) Trajectory {
	var t Trajectory
	var currentStep *TrajectoryStep
	var totalCost core.CostUpdate
	var hasError bool
	var errMsg string

	for _, e := range events {
		// Accumulate cost across all events.
		if e.Cost != nil {
			totalCost.InputTokens += e.Cost.InputTokens
			totalCost.OutputTokens += e.Cost.OutputTokens
			totalCost.TotalTokens += e.Cost.TotalTokens
			totalCost.EstimatedUSD += e.Cost.EstimatedUSD
		}

		// Track errors.
		if e.Type == core.EventError {
			hasError = true
			errMsg = firstNonEmpty(e.Err, e.Message)
		}

		// Detect step boundaries via agent-step-* trace updates entering running state.
		if e.Type == core.EventTraceUpdate && e.Trace != nil &&
			strings.HasPrefix(e.Trace.ID, "agent-step-") &&
			e.Trace.Status == core.TraceRunning {
			t.Steps = append(t.Steps, TrajectoryStep{})
			currentStep = &t.Steps[len(t.Steps)-1]
			currentStep.TraceUpdates = append(currentStep.TraceUpdates, *e.Trace)
			continue
		}

		// Ensure a step exists before collecting events.
		if currentStep == nil {
			t.Steps = append(t.Steps, TrajectoryStep{})
			currentStep = &t.Steps[len(t.Steps)-1]
		}

		switch e.Type {
		case core.EventToolStart:
			if e.ToolCall != nil {
				currentStep.ToolCalls = append(currentStep.ToolCalls, *e.ToolCall)
			}
		case core.EventObservation:
			if e.Observation != nil {
				currentStep.Observations = append(currentStep.Observations, *e.Observation)
			}
		case core.EventTraceUpdate:
			if e.Trace != nil {
				currentStep.TraceUpdates = append(currentStep.TraceUpdates, *e.Trace)
			}
		case core.EventToolResult:
			if e.Observation != nil {
				currentStep.Observations = append(currentStep.Observations, *e.Observation)
			}
		}
	}

	t.Success = !hasError && len(t.Steps) > 0
	t.TokenCost = totalCost
	t.Error = errMsg
	return t
}

// CompareTrajectories compares two trajectories and returns a human-readable diff.
func CompareTrajectories(a, b Trajectory) string {
	var bld strings.Builder

	bld.WriteString(fmt.Sprintf("Session A: %s\n", a.SessionID))
	bld.WriteString(fmt.Sprintf("Session B: %s\n", b.SessionID))
	bld.WriteString(fmt.Sprintf("Steps: %d vs %d (%+d)\n",
		len(a.Steps), len(b.Steps), len(b.Steps)-len(a.Steps)))
	bld.WriteString(fmt.Sprintf("Success: %t vs %t\n", a.Success, b.Success))

	bld.WriteString("Token cost:\n")
	bld.WriteString(fmt.Sprintf("  A: in=%d out=%d total=%d est=$%.4f\n",
		a.TokenCost.InputTokens, a.TokenCost.OutputTokens,
		a.TokenCost.TotalTokens, a.TokenCost.EstimatedUSD))
	bld.WriteString(fmt.Sprintf("  B: in=%d out=%d total=%d est=$%.4f\n",
		b.TokenCost.InputTokens, b.TokenCost.OutputTokens,
		b.TokenCost.TotalTokens, b.TokenCost.EstimatedUSD))

	aToolCount := totalToolCalls(a)
	bToolCount := totalToolCalls(b)
	bld.WriteString(fmt.Sprintf("Tool calls: %d vs %d (%+d)\n",
		aToolCount, bToolCount, bToolCount-aToolCount))

	minSteps := len(a.Steps)
	if len(b.Steps) < minSteps {
		minSteps = len(b.Steps)
	}
	for i := 0; i < minSteps; i++ {
		aNames := toolCallNames(a.Steps[i])
		bNames := toolCallNames(b.Steps[i])
		if strings.Join(aNames, ",") != strings.Join(bNames, ",") {
			bld.WriteString(fmt.Sprintf("  Step %d: A=%v B=%v\n", i+1, aNames, bNames))
		}
	}

	if a.Error != b.Error {
		bld.WriteString(fmt.Sprintf("Error A: %q\n", a.Error))
		bld.WriteString(fmt.Sprintf("Error B: %q\n", b.Error))
	}

	return bld.String()
}

// FormatTrajectory returns a human-readable summary of a trajectory.
func FormatTrajectory(t Trajectory) string {
	var bld strings.Builder

	bld.WriteString(fmt.Sprintf("Session: %s\n", t.SessionID))
	bld.WriteString(fmt.Sprintf("Steps: %d\n", len(t.Steps)))
	bld.WriteString(fmt.Sprintf("Success: %t\n", t.Success))
	bld.WriteString(fmt.Sprintf("Tokens: in=%d out=%d total=%d est=$%.4f\n",
		t.TokenCost.InputTokens, t.TokenCost.OutputTokens,
		t.TokenCost.TotalTokens, t.TokenCost.EstimatedUSD))
	if t.Error != "" {
		bld.WriteString(fmt.Sprintf("Error: %s\n", t.Error))
	}

	for i, step := range t.Steps {
		bld.WriteString(fmt.Sprintf("\nStep %d:\n", i+1))
		if len(step.ToolCalls) == 0 {
			bld.WriteString("  (final answer, no tools)\n")
			continue
		}
		for _, tc := range step.ToolCalls {
			bld.WriteString(fmt.Sprintf("  Tool: %s %s\n", tc.Name, formatToolInput(tc.Input)))
		}
		for _, obs := range step.Observations {
			if obs.Summary != "" {
				bld.WriteString(fmt.Sprintf("  Observation: %s\n", obs.Summary))
			}
		}
	}

	return bld.String()
}

func formatToolInput(input core.ToolInput) string {
	if len(input) == 0 {
		return ""
	}
	parts := make([]string, 0, len(input))
	for k, v := range input {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func totalToolCalls(t Trajectory) int {
	n := 0
	for _, step := range t.Steps {
		n += len(step.ToolCalls)
	}
	return n
}

func toolCallNames(step TrajectoryStep) []string {
	names := make([]string, len(step.ToolCalls))
	for i, tc := range step.ToolCalls {
		names[i] = tc.Name
	}
	return names
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
