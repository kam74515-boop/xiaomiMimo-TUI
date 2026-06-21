package counterfactual

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

// session builds a minimal recorded session: each entry in tools is a step that
// calls those tools (empty => a final-answer step with the given text).
func session(steps [][]string, answer string) []core.AgentEvent {
	var ev []core.AgentEvent
	for i, tools := range steps {
		ev = append(ev, core.AgentEvent{Type: core.EventTraceUpdate, Trace: &core.TraceStep{
			ID: "agent-step-" + itoa(i), Status: core.TraceRunning,
		}})
		for _, name := range tools {
			ev = append(ev, core.AgentEvent{Type: core.EventToolStart, ToolName: name, ToolCall: &core.ToolCall{Name: name}})
			ev = append(ev, core.AgentEvent{Type: core.EventToolResult, ToolName: name})
		}
	}
	ev = append(ev, core.AgentEvent{Type: core.EventMessageDelta, Message: answer})
	ev = append(ev, core.AgentEvent{Type: core.EventDone})
	return ev
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestCompareIdentical(t *testing.T) {
	a := session([][]string{{"read_file"}, {"write_file"}}, "done")
	b := session([][]string{{"read_file"}, {"write_file"}}, "done")
	r := Compare(a, b)
	if r.Divergence.Step != 0 {
		t.Fatalf("identical sessions should not diverge, got step %d", r.Divergence.Step)
	}
	if !strings.Contains(Format(r), "Divergence: none") {
		t.Fatalf("Format should report no divergence: %q", Format(r))
	}
}

func TestCompareDivergence(t *testing.T) {
	a := session([][]string{{"read_file"}, {"write_file"}}, "done")
	b := session([][]string{{"read_file"}, {"apply_patch"}}, "done differently")
	r := Compare(a, b)
	if r.Divergence.Step != 2 {
		t.Fatalf("divergence should be at step 2, got %d", r.Divergence.Step)
	}
	if len(r.Divergence.BaselineTools) != 1 || r.Divergence.BaselineTools[0] != "write_file" {
		t.Fatalf("baseline tools at divergence wrong: %+v", r.Divergence.BaselineTools)
	}
	if r.Divergence.VariantTools[0] != "apply_patch" {
		t.Fatalf("variant tools at divergence wrong: %+v", r.Divergence.VariantTools)
	}
	out := Format(r)
	if !strings.Contains(out, "step 2") || !strings.Contains(out, "Final answers differ") {
		t.Fatalf("Format missing divergence detail: %q", out)
	}
}

func TestCompareDifferentLengths(t *testing.T) {
	a := session([][]string{{"read_file"}}, "short")
	b := session([][]string{{"read_file"}, {"write_file"}}, "longer")
	r := Compare(a, b)
	if r.BaselineSteps == r.VariantSteps {
		t.Fatalf("step counts should differ: %d vs %d", r.BaselineSteps, r.VariantSteps)
	}
	if r.Divergence.Step == 0 {
		t.Fatal("different-length sessions should report a divergence point")
	}
}
