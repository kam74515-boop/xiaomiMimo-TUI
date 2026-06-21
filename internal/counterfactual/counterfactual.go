// Package counterfactual compares two recorded agent sessions — a baseline and
// a variant in which one thing was changed (model channel, a pinned context
// item, a removed artifact) — and localizes where their trajectories diverge.
//
// This turns the replay gate from a pass/fail check into an experiment surface:
// you can ask "what changed if I changed X?" deterministically, offline, over
// recorded evidence — something a chat-first agent cannot do.
//
// It is intentionally offline (no re-execution): live counterfactual re-runs
// would execute real, side-effecting tools and belong behind worktree isolation.
package counterfactual

import (
	"fmt"
	"strings"

	"mimo-tui/internal/core"
	"mimo-tui/internal/eval"
	"mimo-tui/internal/provenance"
)

// Divergence is the first step at which the two sessions' tool sequences differ.
type Divergence struct {
	Step          int // 1-based; 0 means no divergence in the shared prefix
	BaselineTools []string
	VariantTools  []string
}

// Report summarizes the comparison.
type Report struct {
	BaselineSteps int
	VariantSteps  int
	Gate          eval.GateResult
	Divergence    Divergence
	ContextDiff   *provenance.Diff // context diff at the divergence step, if available
	BaselineAns   string
	VariantAns    string
}

// Compare analyzes a baseline vs a variant session.
func Compare(baseline, variant []core.AgentEvent) Report {
	bt := provenance.Build(baseline)
	vt := provenance.Build(variant)

	r := Report{
		BaselineSteps: len(bt.Steps),
		VariantSteps:  len(vt.Steps),
		Gate:          eval.EvaluateCandidate(baseline, variant),
		BaselineAns:   lastAnswer(bt),
		VariantAns:    lastAnswer(vt),
	}

	// First divergence by per-step tool name sequence.
	n := len(bt.Steps)
	if len(vt.Steps) < n {
		n = len(vt.Steps)
	}
	for i := 0; i < n; i++ {
		bNames := toolNames(bt.Steps[i])
		vNames := toolNames(vt.Steps[i])
		if strings.Join(bNames, ",") != strings.Join(vNames, ",") {
			r.Divergence = Divergence{Step: i + 1, BaselineTools: bNames, VariantTools: vNames}
			if bt.Steps[i].Context != nil && vt.Steps[i].Context != nil {
				d := provenance.ContextDiff(*bt.Steps[i].Context, *vt.Steps[i].Context)
				r.ContextDiff = &d
			}
			break
		}
	}
	// If no per-step divergence but step counts differ, mark the first extra step.
	if r.Divergence.Step == 0 && len(bt.Steps) != len(vt.Steps) {
		r.Divergence = Divergence{Step: n + 1}
	}
	return r
}

// Format renders a human-readable report.
func Format(r Report) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Counterfactual: baseline %d steps vs variant %d steps\n", r.BaselineSteps, r.VariantSteps))
	b.WriteString(fmt.Sprintf("Trajectory similarity: %.0f%% | tool-match: %.0f%% | gate: %s\n",
		r.Gate.TrajectorySimilarity*100, r.Gate.ToolMatchRate*100, passLabel(r.Gate.Passed)))
	if len(r.Gate.Failures) > 0 {
		b.WriteString("Gate failures:\n")
		for _, f := range r.Gate.Failures {
			b.WriteString("  - " + f + "\n")
		}
	}
	if r.Divergence.Step == 0 {
		b.WriteString("Divergence: none (identical tool trajectories)\n")
	} else {
		b.WriteString(fmt.Sprintf("First divergence at step %d:\n", r.Divergence.Step))
		b.WriteString(fmt.Sprintf("  baseline tools: %s\n", joinOrNone(r.Divergence.BaselineTools)))
		b.WriteString(fmt.Sprintf("  variant tools:  %s\n", joinOrNone(r.Divergence.VariantTools)))
		if r.ContextDiff != nil {
			b.WriteString("  context at divergence:\n")
			for _, line := range strings.Split(strings.TrimRight(provenance.FormatDiff(*r.ContextDiff), "\n"), "\n") {
				b.WriteString("    " + line + "\n")
			}
		}
	}
	if r.BaselineAns != r.VariantAns {
		b.WriteString("Final answers differ.\n")
	}
	return b.String()
}

func toolNames(s provenance.Step) []string {
	names := make([]string, 0, len(s.Tools))
	for _, t := range s.Tools {
		names = append(names, t.Name)
	}
	return names
}

func lastAnswer(t provenance.Timeline) string {
	ans := ""
	for _, s := range t.Steps {
		if strings.TrimSpace(s.Assistant) != "" {
			ans = strings.TrimSpace(s.Assistant)
		}
	}
	return ans
}

func passLabel(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "(none / final answer)"
	}
	return strings.Join(items, ", ")
}
