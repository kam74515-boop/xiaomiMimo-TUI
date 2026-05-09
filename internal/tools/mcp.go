package tools

import (
	"context"
	"fmt"

	"mimo-tui/internal/core"
)

// ExternalTool wraps an MCP server tool as a core.Tool. This is the bridge
// between the MCP protocol and MiMo-TUI's tool system. In the MVP, the tool
// is a stub that returns a placeholder result. The real implementation will
// connect to the MCP server process via stdio JSON-RPC.
type ExternalTool struct {
	baseTool
	serverName  string
	toolName    string
	toolSchema  core.JSONSchema
	description string
}

// NewExternalTool creates an ExternalTool for a tool exposed by an MCP server.
// The tool is registered under the name "mcp__<serverName>__<toolName>" to
// avoid collisions with built-in tools and to make the server origin clear.
func NewExternalTool(serverName, toolName, description string, schema core.JSONSchema, workspace string) *ExternalTool {
	fullName := fmt.Sprintf("mcp__%s__%s", serverName, toolName)
	return &ExternalTool{
		baseTool:    newBase(fullName, workspace, nil, nil),
		serverName:  serverName,
		toolName:    toolName,
		toolSchema:  schema,
		description: description,
	}
}

// Name returns the fully qualified tool name: mcp__<server>__<tool>.
func (t *ExternalTool) Name() string {
	return t.name
}

// ServerName returns the MCP server that provides this tool.
func (t *ExternalTool) ServerName() string {
	return t.serverName
}

// ToolName returns the original tool name as exposed by the MCP server.
func (t *ExternalTool) ToolName() string {
	return t.toolName
}

// Stubbed reports whether this ExternalTool is still using the local MVP stub
// instead of a live MCP connection.
func (t *ExternalTool) Stubbed() bool {
	return true
}

// Schema returns the JSON schema for this tool. If the MCP server provided a
// schema it is used; otherwise a minimal object schema is returned.
func (t *ExternalTool) Schema() core.JSONSchema {
	if t.toolSchema != nil {
		return t.toolSchema
	}
	return objectSchema(t.description, map[string]any{})
}

// Safety returns SafetyWorkspaceMutation because MCP tools can perform
// arbitrary operations. This is the conservative choice; individual servers
// could override this once trust levels are implemented.
func (t *ExternalTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyWorkspaceMutation
}

// Permission always returns PermissionAsk. External tools from MCP servers
// require explicit user approval because their behavior is opaque to the agent.
func (t *ExternalTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{
		Behavior: core.PermissionAsk,
		Reason:   fmt.Sprintf("MCP tool from server %q requires approval", t.serverName),
	}
}

// Run executes the MCP tool. In this MVP, it returns a placeholder result
// indicating the tool is not yet connected. The real implementation will:
//  1. Look up the MCP server connection from a connection pool
//  2. Send a tools/call request via JSON-RPC over stdio
//  3. Parse the response and convert it to a ToolResult
func (t *ExternalTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	return core.ToolResult{
		Content:  fmt.Sprintf("MCP tool %q from server %q is configured but not yet connected", t.toolName, t.serverName),
		ExitCode: 0,
	}
}

// Summarize converts the tool result into an observation for the context window.
func (t *ExternalTool) Summarize(result core.ToolResult) core.Observation {
	return core.Observation{
		Summary:          result.Content,
		StateDelta:       "MCP tool stub executed",
		RiskDelta:        "no risk (stub)",
		ContextPlacement: core.TierArtifact,
		ArtifactID:       result.ArtifactID,
	}
}

// Compile-time check that ExternalTool implements core.Tool.
var _ core.Tool = (*ExternalTool)(nil)
