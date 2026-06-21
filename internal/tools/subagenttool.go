package tools

import (
	"context"
	"fmt"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

// SubAgentResult is what a delegated sub-agent returns.
type SubAgentResult struct {
	Summary string
	Diff    string
	Steps   int
}

// SubAgentSpawner runs a delegated sub-agent for a goal in an isolated worktree.
// The composition root provides the implementation; the tool stays decoupled
// from the agent/runtime packages.
type SubAgentSpawner interface {
	Spawn(ctx context.Context, goal string) (SubAgentResult, error)
}

// subAgentTool lets the main agent delegate a self-contained sub-task to a
// sub-agent that runs in an isolated git worktree. The sub-agent's changes come
// back as a reviewable diff (artifact-backed); they are NOT merged automatically.
type subAgentTool struct {
	baseTool
	spawner SubAgentSpawner
}

// NewSubAgentTool constructs the delegation tool. Without a spawner it reports a
// not-connected placeholder (like an unconfigured MCP tool).
func NewSubAgentTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return &subAgentTool{baseTool: newBase("subagent", workspace, store, s)}
}

// SetSpawner wires the runtime spawner so the tool can actually delegate.
func (t *subAgentTool) SetSpawner(s SubAgentSpawner) { t.spawner = s }

// Stubbed reports whether delegation is wired.
func (t *subAgentTool) Stubbed() bool { return t.spawner == nil }

func (t *subAgentTool) Schema() core.JSONSchema {
	return objectSchema("Delegate a self-contained sub-task to a sub-agent that works in an isolated git worktree. Use for parallel or well-scoped work whose file changes you want reviewed as a diff before merging. Provide a clear, complete goal — the sub-agent does not see this conversation.", map[string]any{
		"goal": stringSchema("The complete, self-contained task for the sub-agent."),
	})
}

func (t *subAgentTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyWorkspaceMutation
}

func (t *subAgentTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "delegating to a sub-agent spawns an isolated worktree run"}
}

func (t *subAgentTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	goal := strings.TrimSpace(stringInput(input, "goal"))
	if goal == "" {
		return core.ToolResult{ExitCode: 2, Error: "goal is required"}
	}
	if t.spawner == nil {
		return core.ToolResult{Content: "subagent delegation is configured but not connected in this runtime"}
	}

	res, err := t.spawner.Spawn(ctx, goal)
	if err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error(), Content: res.Summary}
	}

	content := fmt.Sprintf("sub-agent finished in %d step(s): %s", res.Steps, res.Summary)
	if strings.TrimSpace(res.Diff) == "" {
		content += "\n(no file changes)"
		return core.ToolResult{Content: content}
	}
	artifactID, aerr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "diff",
		Inputs:   map[string]any{"goal": goal},
		Payloads: []artifact.Payload{{Name: "changes.diff", Data: []byte(res.Diff)}},
	})
	if aerr != nil {
		// Do not report success: the reviewable diff could not be persisted and
		// the worktree is already gone, so the work is lost.
		content += "\nwarning: sub-agent produced changes but the diff could not be stored: " + aerr.Error()
		return core.ToolResult{Content: content, ExitCode: 1, Error: aerr.Error()}
	}
	content += fmt.Sprintf("\nproposed changes stored as artifact %s (%d lines); review before merging", artifactID, strings.Count(res.Diff, "\n"))
	return core.ToolResult{Content: content, ArtifactID: artifactID}
}

func (t *subAgentTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t *subAgentTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	// The diff is artifact-backed; keep a compact observation in Near.
	return summarizeResult("subagent", result, core.TierNear)
}
