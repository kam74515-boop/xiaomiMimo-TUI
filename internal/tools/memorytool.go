package tools

import (
	"context"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
	"mimo-tui/internal/memory"
)

// memoryTool lets the agent read and append to persistent cross-session memory.
// Writes go to the project memory file (.mimo/MEMORY.md) and are picked up on
// the next turn when the runtime reloads memory into the system prompt.
type memoryTool struct {
	baseTool
}

// NewMemoryTool constructs the persistent-memory tool.
func NewMemoryTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return memoryTool{baseTool: newBase("memory", workspace, store, s)}
}

func (t memoryTool) Schema() core.JSONSchema {
	return objectSchema("Read or append to persistent cross-session memory (project .mimo/MEMORY.md). Record durable facts about the project, decisions, conventions, or user preferences so they survive across sessions. Use action=show to read current memory, action=add with a note to record a new fact.", map[string]any{
		"action": core.JSONSchema{
			"type":        "string",
			"description": "show to read memory, add to record a new fact",
			"enum":        []string{"show", "add"},
		},
		"note": stringSchema("The fact to record when action=add. Keep it concise and durable (one fact per call)."),
	})
}

func (t memoryTool) Safety(input core.ToolInput) core.SafetyGrade {
	if strings.EqualFold(strings.TrimSpace(stringInput(input, "action")), "add") {
		return core.SafetyWorkspaceMutation
	}
	return core.SafetyReadOnly
}

func (t memoryTool) Permission(input core.ToolInput) core.PermissionRequest {
	// Memory is the agent's own low-risk notebook scoped to .mimo/MEMORY.md, so
	// it is allowed without interactive approval.
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "memory only reads/appends the project memory file"}
}

func (t memoryTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	store := memory.NewStore(t.workspace)
	action := strings.ToLower(strings.TrimSpace(stringInput(input, "action")))
	if action == "" {
		// Default to show when a note is absent, add when a note is present.
		if strings.TrimSpace(stringInput(input, "note")) != "" {
			action = "add"
		} else {
			action = "show"
		}
	}

	switch action {
	case "add", "append", "+":
		note := strings.TrimSpace(stringInput(input, "note"))
		if note == "" {
			return core.ToolResult{ExitCode: 2, Error: "note is required when action=add"}
		}
		if err := store.Append(memory.ScopeProject, note); err != nil {
			return core.ToolResult{ExitCode: 1, Error: err.Error()}
		}
		return core.ToolResult{Content: "recorded memory: " + note}
	case "show", "read", "list":
		content := store.LoadBounded(memory.MaxModelMemoryBytes)
		if content == "" {
			return core.ToolResult{Content: "persistent memory is empty"}
		}
		return core.ToolResult{Content: content}
	default:
		return core.ToolResult{ExitCode: 2, Error: "unknown action " + action + " (want show or add)"}
	}
}

func (t memoryTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t memoryTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	// Memory content is compact and high-signal; keep it Near so it stays
	// visible rather than being pushed to an artifact.
	obs := summarizeResult("memory", result, core.TierNear)
	return obs
}
