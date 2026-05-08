package tools

import (
	"context"
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func TestExternalToolImplementsInterface(t *testing.T) {
	// Compile-time check is in mcp.go, but verify at runtime too.
	tool := NewExternalTool("github", "create_issue", "Create a GitHub issue", core.JSONSchema{
		"type":        "object",
		"description": "Create a GitHub issue",
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
			"body":  map[string]any{"type": "string"},
		},
	}, ".")

	var _ core.Tool = tool // compile-time assertion

	if tool.Name() != "mcp__github__create_issue" {
		t.Fatalf("Name() = %q, want mcp__github__create_issue", tool.Name())
	}
	if tool.ServerName() != "github" {
		t.Fatalf("ServerName() = %q, want github", tool.ServerName())
	}
	if tool.ToolName() != "create_issue" {
		t.Fatalf("ToolName() = %q, want create_issue", tool.ToolName())
	}
}

func TestExternalToolSchema(t *testing.T) {
	schema := core.JSONSchema{
		"type":        "object",
		"description": "Search files",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
	tool := NewExternalTool("fs", "search", "Search files", schema, ".")

	got := tool.Schema()
	if got["type"] != "object" {
		t.Fatalf("Schema type = %v, want object", got["type"])
	}
	if got["description"] != "Search files" {
		t.Fatalf("Schema description = %v, want Search files", got["description"])
	}
}

func TestExternalToolSchemaFallback(t *testing.T) {
	tool := NewExternalTool("test", "noop", "Does nothing", nil, ".")
	schema := tool.Schema()
	if schema["type"] != "object" {
		t.Fatalf("fallback schema type = %v, want object", schema["type"])
	}
}

func TestExternalToolSafety(t *testing.T) {
	tool := NewExternalTool("test", "tool", "A tool", nil, ".")
	if safety := tool.Safety(core.ToolInput{}); safety != core.SafetyWorkspaceMutation {
		t.Fatalf("Safety() = %s, want SafetyWorkspaceMutation", safety)
	}
}

func TestExternalToolPermission(t *testing.T) {
	tool := NewExternalTool("myserver", "mytool", "A tool", nil, ".")
	perm := tool.Permission(core.ToolInput{})
	if perm.Behavior != core.PermissionAsk {
		t.Fatalf("Permission() behavior = %s, want PermissionAsk", perm.Behavior)
	}
	if !strings.Contains(perm.Reason, "myserver") {
		t.Fatalf("Permission() reason = %q, should mention server name", perm.Reason)
	}
}

func TestExternalToolRunReturnsStub(t *testing.T) {
	tool := NewExternalTool("github", "list_repos", "List repos", nil, ".")
	result := tool.Run(context.Background(), core.ToolInput{})
	if result.ExitCode != 0 {
		t.Fatalf("Run() exit code = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Content, "not yet connected") {
		t.Fatalf("Run() content = %q, should mention not yet connected", result.Content)
	}
	if !strings.Contains(result.Content, "list_repos") {
		t.Fatalf("Run() content = %q, should mention tool name", result.Content)
	}
	if !strings.Contains(result.Content, "github") {
		t.Fatalf("Run() content = %q, should mention server name", result.Content)
	}
}

func TestExternalToolSummarize(t *testing.T) {
	tool := NewExternalTool("github", "list_repos", "List repos", nil, ".")
	result := tool.Run(context.Background(), core.ToolInput{})
	obs := tool.Summarize(result)
	if obs.Summary == "" {
		t.Fatal("Summarize() summary is empty")
	}
	if obs.ContextPlacement != core.TierArtifact {
		t.Fatalf("Summarize() placement = %s, want TierArtifact", obs.ContextPlacement)
	}
}

func TestExternalToolNameCollision(t *testing.T) {
	// Two tools from different servers should not collide.
	gh := NewExternalTool("github", "search", "Search GitHub", nil, ".")
	fs := NewExternalTool("filesystem", "search", "Search files", nil, ".")
	if gh.Name() == fs.Name() {
		t.Fatalf("tools from different servers should have different names: both = %q", gh.Name())
	}
}
