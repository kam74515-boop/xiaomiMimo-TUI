// Package provenance reconstructs an evidence-linked, time-travellable view of
// an agent session from its event log.
//
// This is MiMo-TUI's signature differentiator: because every run is a replayable
// JSONL event stream with context snapshots and artifact-backed observations,
// we can bind each step's assistant output to the exact evidence that was in
// scope when it was produced, and diff the Context Map between any two steps.
package provenance

import (
	"fmt"
	"sort"
	"strings"

	"mimo-tui/internal/core"
)

// ToolRun is a single tool invocation within a step, with its artifact-backed
// evidence.
type ToolRun struct {
	Name        string
	Input       core.ToolInput
	ArtifactID  string
	Observation string
	Error       string
}

// Step is one agent loop iteration with the evidence that fed it.
type Step struct {
	Index     int
	Goal      string
	Stage     core.TrajectoryStage
	Status    core.TraceStepStatus
	Context   *core.ContextSnapshot // context active when the step ran
	Tools     []ToolRun
	Assistant string
	Cost      core.CostUpdate
}

// Timeline is the reconstructed session.
type Timeline struct {
	SessionID string
	Steps     []Step
	Snapshots []core.ContextSnapshot // every context snapshot, in order
}

// Build reconstructs a Timeline from session events. Step boundaries are the
// agent-step-* trace updates entering the running state (consistent with
// eval.ExtractTrajectory).
func Build(events []core.AgentEvent) Timeline {
	var t Timeline
	var cur *Step
	var latest *core.ContextSnapshot

	ensureStep := func() {
		if cur == nil {
			t.Steps = append(t.Steps, Step{Index: len(t.Steps) + 1})
			cur = &t.Steps[len(t.Steps)-1]
			if latest != nil {
				snap := *latest
				cur.Context = &snap
			}
		}
	}

	for _, e := range events {
		if e.Cost != nil && cur != nil {
			cur.Cost.InputTokens += e.Cost.InputTokens
			cur.Cost.OutputTokens += e.Cost.OutputTokens
			cur.Cost.TotalTokens += e.Cost.TotalTokens
			cur.Cost.EstimatedUSD += e.Cost.EstimatedUSD
		}

		if e.Type == core.EventContextUpdate && e.Context != nil {
			snap := *e.Context
			t.Snapshots = append(t.Snapshots, snap)
			latest = &t.Snapshots[len(t.Snapshots)-1]
			// A step binds to the snapshot active when its model call started
			// (set at the step boundary). Later snapshots in the same step are
			// produced by that step's own tool runs and feed the NEXT step, so we
			// do not refresh the current step here.
			continue
		}

		if e.Type == core.EventTraceUpdate && e.Trace != nil &&
			strings.HasPrefix(e.Trace.ID, "agent-step-") && e.Trace.Status == core.TraceRunning {
			t.Steps = append(t.Steps, Step{
				Index:  len(t.Steps) + 1,
				Goal:   e.Trace.Goal,
				Stage:  e.Trace.Stage,
				Status: e.Trace.Status,
			})
			cur = &t.Steps[len(t.Steps)-1]
			if latest != nil {
				snap := *latest
				cur.Context = &snap
			}
			continue
		}

		switch e.Type {
		case core.EventMessageDelta:
			ensureStep()
			cur.Assistant += e.Message
		case core.EventToolStart:
			ensureStep()
			run := ToolRun{Name: e.ToolName}
			if e.ToolCall != nil {
				run.Name = firstNonEmpty(e.ToolCall.Name, e.ToolName)
				run.Input = e.ToolCall.Input
			}
			cur.Tools = append(cur.Tools, run)
		case core.EventToolResult:
			ensureStep()
			if n := len(cur.Tools); n > 0 && e.Err != "" {
				cur.Tools[n-1].Error = e.Err
			}
		case core.EventObservation:
			ensureStep()
			if e.Observation == nil {
				break
			}
			// Attach to the in-flight tool run when it carries evidence;
			// otherwise it is a step-level note we fold into the last tool run if
			// one exists, else ignore for the structured view.
			if n := len(cur.Tools); n > 0 {
				if cur.Tools[n-1].Observation == "" {
					cur.Tools[n-1].Observation = e.Observation.Summary
				}
				if e.Observation.ArtifactID != "" && cur.Tools[n-1].ArtifactID == "" {
					cur.Tools[n-1].ArtifactID = e.Observation.ArtifactID
				}
			}
		case core.EventTraceUpdate:
			ensureStep()
			if e.Trace != nil {
				if e.Trace.Stage != "" {
					cur.Stage = e.Trace.Stage
				}
				if e.Trace.Status != "" {
					cur.Status = e.Trace.Status
				}
			}
		}
	}
	return t
}

// ItemChange records a context item whose placement changed between snapshots.
type ItemChange struct {
	ID       string
	Title    string
	FromTier core.ContextTier
	ToTier   core.ContextTier
	Pinned   bool
}

// Diff is the difference between two Context Map snapshots.
type Diff struct {
	Added        []core.ContextItem
	Removed      []core.ContextItem
	Changed      []ItemChange
	TokensBefore int
	TokensAfter  int
}

// ContextDiff compares two context snapshots by item ID.
func ContextDiff(before, after core.ContextSnapshot) Diff {
	d := Diff{TokensBefore: before.UsedTokens, TokensAfter: after.UsedTokens}
	beforeByID := map[string]core.ContextItem{}
	for _, it := range before.Items {
		beforeByID[it.ID] = it
	}
	afterByID := map[string]core.ContextItem{}
	for _, it := range after.Items {
		afterByID[it.ID] = it
	}
	for _, it := range after.Items {
		prev, ok := beforeByID[it.ID]
		if !ok {
			d.Added = append(d.Added, it)
			continue
		}
		if prev.Tier != it.Tier || prev.Pinned != it.Pinned {
			d.Changed = append(d.Changed, ItemChange{
				ID: it.ID, Title: it.Title, FromTier: prev.Tier, ToTier: it.Tier, Pinned: it.Pinned,
			})
		}
	}
	for _, it := range before.Items {
		if _, ok := afterByID[it.ID]; !ok {
			d.Removed = append(d.Removed, it)
		}
	}
	sort.Slice(d.Added, func(i, j int) bool { return d.Added[i].ID < d.Added[j].ID })
	sort.Slice(d.Removed, func(i, j int) bool { return d.Removed[i].ID < d.Removed[j].ID })
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].ID < d.Changed[j].ID })
	return d
}

// Format renders a human-readable timeline.
func Format(t Timeline) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Provenance timeline: %s\n", firstNonEmpty(t.SessionID, "(session)")))
	b.WriteString(fmt.Sprintf("Steps: %d | context snapshots: %d\n", len(t.Steps), len(t.Snapshots)))
	for _, s := range t.Steps {
		b.WriteString(fmt.Sprintf("\nStep %d", s.Index))
		if s.Stage != "" {
			b.WriteString(" [" + string(s.Stage) + "]")
		}
		b.WriteString("\n")
		if s.Context != nil {
			b.WriteString(fmt.Sprintf("  context: %d/%d tokens (%s), %d item(s)\n",
				s.Context.UsedTokens, s.Context.WindowTokens, s.Context.PollutionRisk, len(s.Context.Items)))
		}
		for _, run := range s.Tools {
			line := "  tool " + run.Name
			if run.ArtifactID != "" {
				line += " → artifact " + run.ArtifactID
			}
			if run.Error != "" {
				line += " [error: " + run.Error + "]"
			}
			b.WriteString(line + "\n")
			if run.Observation != "" {
				b.WriteString("    evidence: " + run.Observation + "\n")
			}
		}
		if strings.TrimSpace(s.Assistant) != "" {
			b.WriteString("  answer: " + truncate(strings.TrimSpace(s.Assistant), 160) + "\n")
		}
	}
	return b.String()
}

// FormatDiff renders a human-readable context diff.
func FormatDiff(d Diff) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Context tokens: %d → %d (%+d)\n", d.TokensBefore, d.TokensAfter, d.TokensAfter-d.TokensBefore))
	if len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 {
		b.WriteString("(no item changes)\n")
		return b.String()
	}
	for _, it := range d.Added {
		b.WriteString(fmt.Sprintf("  + %s [%s]\n", itemLabel(it), it.Tier))
	}
	for _, it := range d.Removed {
		b.WriteString(fmt.Sprintf("  - %s [%s]\n", itemLabel(it), it.Tier))
	}
	for _, c := range d.Changed {
		label := c.Title
		if label == "" {
			label = c.ID
		}
		b.WriteString(fmt.Sprintf("  ~ %s: %s → %s\n", label, c.FromTier, c.ToTier))
	}
	return b.String()
}

func itemLabel(it core.ContextItem) string {
	if strings.TrimSpace(it.Title) != "" {
		return it.Title
	}
	return it.ID
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
