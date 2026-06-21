// Package subagent runs a delegated sub-agent in an isolated git worktree and
// returns its work as a reviewable diff. Delegation stays visible (progress is
// published to the parent bus as sub-agent activity) and controllable (the
// sub-agent is sandboxed — it can read and write files in its worktree but
// shell/destructive tools are denied, and nothing is merged automatically).
package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"mimo-tui/internal/agent"
	"mimo-tui/internal/artifact"
	"mimo-tui/internal/config"
	contextmap "mimo-tui/internal/context"
	"mimo-tui/internal/core"
	"mimo-tui/internal/tools"
	"mimo-tui/internal/tools/summarizers"
	"mimo-tui/internal/worktree"
)

// Deps are the runtime dependencies a Spawner needs.
type Deps struct {
	RepoRoot      string
	Client        core.ModelClient
	ParentBus     *core.Bus
	LoopConfig    agent.LoopConfig
	ContextWindow int
}

// Spawner runs delegated sub-agents. It implements tools.SubAgentSpawner.
type Spawner struct {
	deps    Deps
	counter int64
}

// NewSpawner builds a Spawner.
func NewSpawner(deps Deps) *Spawner { return &Spawner{deps: deps} }

// sandboxPolicy lets the sub-agent read/write files in its worktree but denies
// shell and destructive tools (a worktree isolates git, not the whole system).
// Using only allow/deny means no approval prompt is needed — the run is headless.
func sandboxPolicy() config.PolicyConfig {
	return config.PolicyConfig{Defaults: config.Defaults{
		ReadOnly:          "allow",
		WorkspaceMutation: "allow",
		ShellMutation:     "deny",
		Destructive:       "deny",
	}}
}

// Spawn runs a sub-agent for goal and returns its summary and proposed diff.
func (s *Spawner) Spawn(ctx context.Context, goal string) (tools.SubAgentResult, error) {
	taskID := s.nextID(goal)
	s.publish(taskID, goal, core.ActivityRunning, "delegated to sub-agent")

	wt, err := worktree.Create(s.deps.RepoRoot, goal)
	if err != nil {
		s.publish(taskID, goal, core.ActivityFailed, "worktree: "+err.Error())
		return tools.SubAgentResult{}, err
	}
	defer wt.Remove()

	subBus := core.NewBus()
	subEvents := subBus.Subscribe(256)
	done := make(chan struct{})
	go s.forward(taskID, goal, subEvents, done)

	store := artifact.NewStore(wt.Path)
	reg := tools.NewDefaultRegistryWithStore(wt.Path, store, summarizers.NewRegistry)
	reg.Unregister("subagent") // no nested delegation

	exec := tools.NewExecutor(reg, subBus,
		tools.WithArtifactStore(store, wt.Path),
		tools.WithPolicyConfig(sandboxPolicy()),
	)

	window := s.deps.ContextWindow
	if window <= 0 {
		window = contextmap.DefaultWindowTokens
	}
	ctxMgr := contextmap.NewSeeded(window, "Sub-agent isolated worktree at "+wt.Path, goal)

	cfg := s.deps.LoopConfig
	// A sub-agent does not inherit the parent's goal gate, memory, or skills.
	cfg.GoalCondition = ""
	cfg.Judge = nil
	cfg.Memory = ""
	cfg.Skills = ""

	history, loopErr := agent.Loop(ctx, goal, s.deps.Client, exec, ctxMgr, reg.ToolSpecs(), subBus, cfg, nil)
	close(done)

	steps, summary := summarize(history)
	diff, diffErr := wt.Diff()
	res := tools.SubAgentResult{Summary: truncate(summary, 600), Diff: diff, Steps: steps}

	if loopErr != nil {
		s.publish(taskID, goal, core.ActivityFailed, loopErr.Error())
		return res, loopErr
	}
	if diffErr != nil {
		// The loop succeeded but we could not capture its changes; the worktree
		// is about to be removed, so surface this as an error rather than a
		// silent "no changes" success.
		s.publish(taskID, goal, core.ActivityFailed, "diff capture failed: "+diffErr.Error())
		return res, fmt.Errorf("subagent: capture diff: %w", diffErr)
	}
	s.publish(taskID, goal, core.ActivityDone, fmt.Sprintf("%d step(s), %d diff line(s)", steps, strings.Count(diff, "\n")))
	return res, nil
}

func (s *Spawner) nextID(goal string) string {
	n := atomic.AddInt64(&s.counter, 1)
	return fmt.Sprintf("%s-%d", sanitize(goal), n)
}

// forward translates sub-agent bus events into parent-visible sub-agent activity.
func (s *Spawner) forward(taskID, goal string, events <-chan core.AgentEvent, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case ev := <-events:
			switch ev.Type {
			case core.EventTraceUpdate:
				if ev.Trace != nil && strings.HasPrefix(ev.Trace.ID, "agent-step-") && ev.Trace.Status == core.TraceRunning {
					s.publish(taskID, goal, core.ActivityRunning, ev.Trace.Goal)
				}
			case core.EventToolStart:
				if ev.ToolCall != nil {
					s.publish(taskID, goal, core.ActivityRunning, "tool: "+ev.ToolCall.Name)
				}
			}
		}
	}
}

func (s *Spawner) publish(taskID, goal string, status core.ActivityStatus, summary string) {
	if s.deps.ParentBus == nil {
		return
	}
	ev := core.NewEvent(core.EventActivityUpdate)
	ev.Activity = &core.ActivityEvent{
		ID:      "subagent:" + taskID,
		Kind:    core.ActivitySubAgent,
		Name:    truncate(goal, 48),
		Title:   truncate(goal, 48),
		Status:  status,
		Summary: summary,
		Role:    "subagent",
	}
	s.deps.ParentBus.Publish(ev)
}

func summarize(history []core.Message) (steps int, summary string) {
	for _, m := range history {
		if m.Role == "assistant" {
			steps++
			if txt := strings.TrimSpace(m.TextContent()); txt != "" {
				summary = txt
			}
		}
	}
	if summary == "" {
		summary = "sub-agent produced no final message"
	}
	return steps, summary
}

func truncate(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max-1]) + "…"
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
		if b.Len() >= 16 {
			break
		}
	}
	if b.Len() == 0 {
		return "task"
	}
	return b.String()
}
