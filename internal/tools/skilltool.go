package tools

import (
	"context"
	"fmt"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
	"mimo-tui/internal/skill"
)

// skillTool lets the agent discover and read governed skill playbooks. Reading a
// skill returns its playbook so the model can follow it; the act is visible as a
// tool call + observation. Persistent activation is done by the user via the
// /skill command.
type skillTool struct {
	baseTool
}

// NewSkillTool constructs the skill discovery/read tool.
func NewSkillTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return skillTool{baseTool: newBase("skill", workspace, store, s)}
}

func (t skillTool) Schema() core.JSONSchema {
	return objectSchema("Discover and read governed skill playbooks (engineering procedures like plan, tdd, review, debug, verify). action=list to see available skills, action=show with a name to read a playbook and follow it.", map[string]any{
		"action": core.JSONSchema{
			"type":        "string",
			"description": "list to enumerate skills, show to read one",
			"enum":        []string{"list", "show"},
		},
		"name": stringSchema("Skill name when action=show."),
	})
}

func (t skillTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "skill only reads playbooks"}
}

func (t skillTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	store := skill.NewStore(t.workspace)
	action := strings.ToLower(strings.TrimSpace(stringInput(input, "action")))
	name := strings.TrimSpace(stringInput(input, "name"))
	if action == "" {
		if name != "" {
			action = "show"
		} else {
			action = "list"
		}
	}

	switch action {
	case "list":
		skills := store.List()
		if len(skills) == 0 {
			return core.ToolResult{Content: "no skills available"}
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%d skill(s):\n", len(skills)))
		for _, sk := range skills {
			b.WriteString(fmt.Sprintf("- %s (%s): %s\n", sk.Name, sk.Source, sk.Description))
		}
		return core.ToolResult{Content: strings.TrimRight(b.String(), "\n")}
	case "show", "read":
		if name == "" {
			return core.ToolResult{ExitCode: 2, Error: "name is required when action=show"}
		}
		sk, ok := store.Get(name)
		if !ok {
			return core.ToolResult{ExitCode: 1, Error: "unknown skill: " + name}
		}
		return core.ToolResult{Content: skill.Combine([]skill.Skill{sk})}
	default:
		return core.ToolResult{ExitCode: 2, Error: "unknown action " + action + " (want list or show)"}
	}
}

func (t skillTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t skillTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("skill", result, core.TierNear)
}
