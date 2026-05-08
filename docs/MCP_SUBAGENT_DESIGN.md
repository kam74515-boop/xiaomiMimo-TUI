# MCP Server Support & Sub-Agent Task Abstraction

## Overview

This document describes the MVP architecture for two related features:

1. **MCP (Model Context Protocol) server integration** — allowing MiMo-TUI to discover and invoke tools provided by external MCP servers.
2. **Sub-agent task abstraction** — a data model for decomposing complex goals into delegatable sub-tasks with tracked steps.

## Architecture

### MCP Config Layer

**File:** `internal/config/mcp.go`

Configuration is loaded from `.mimo/mcp.toml` (project-local) or `~/.mimo-tui/mcp.toml` (user-global), following the same precedence pattern as `policy.toml`.

```toml
[[servers]]
name = "github"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
enabled = true

[[servers]]
name = "filesystem"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
enabled = false
```

The `MCPConfig` struct provides:
- `LoadMCPConfig()` — loads from the first candidate path that exists
- `LoadMCPConfigFromPath(path)` — loads from an explicit path (used in tests)
- `EnabledServers()` — filters to only enabled servers
- `Validate()` — checks for missing names/commands and duplicate server names

### External Tool

**File:** `internal/tools/mcp.go`

The `ExternalTool` struct implements `core.Tool` and wraps a tool exposed by an MCP server. Key design decisions:

- **Naming convention:** `mcp__<serverName>__<toolName>` — avoids collisions with built-in tools and makes the server origin explicit. Example: `mcp__github__create_issue`.
- **Safety:** Always `SafetyWorkspaceMutation` (conservative). MCP tools can perform arbitrary operations; we default to the most cautious non-destructive grade.
- **Permission:** Always `PermissionAsk`. External tools require explicit user approval because their behavior is opaque.
- **Run (MVP):** Returns a placeholder `ToolResult` indicating the tool is not yet connected. This lets the tool be registered, discovered, and schema-inspected without requiring a live MCP server.

### Sub-Agent Task

**File:** `internal/agent/subagent.go`

A pure data structure for representing decomposed work:

```
SubAgentTask
  ├── ID, Goal, Status, ParentID
  ├── Steps []SubAgentStep
  │     ├── Number, Action, Observation, Status
  │     └── Error, StartedAt, CompletedAt
  ├── Result, Error
  └── CreatedAt, StartedAt, CompletedAt
```

Tasks form a tree via `ParentID`. Each task tracks an ordered sequence of steps. The status model follows a simple state machine:

```
pending → running → done
                  → failed
```

Helper methods (`AddStep`, `CompleteStep`, `FailStep`, `Start`, `Complete`, `Fail`) enforce valid state transitions.

## What's Implemented (MVP)

| Component | Status | Notes |
|-----------|--------|-------|
| `MCPConfig` struct | Done | TOML-backed, validates, loads from standard paths |
| `MCPServer` struct | Done | Name, Command, Args, Env, Enabled |
| `ExternalTool` struct | Done | Implements `core.Tool`, stub Run() |
| Tool naming convention | Done | `mcp__<server>__<tool>` |
| Safety/Permission defaults | Done | Conservative: mutation + ask |
| `SubAgentTask` data model | Done | Full lifecycle helpers |
| `SubAgentStep` data model | Done | Status tracking per step |
| Config tests | Done | Load, default, validate, invalid TOML |
| Tool tests | Done | Interface, safety, permission, run stub, summarize, name collision |

## What's NOT Implemented (Future)

| Component | Priority | Notes |
|-----------|----------|-------|
| MCP server process management | High | Spawn/kill server processes, health checks |
| MCP JSON-RPC transport | High | stdio-based communication per MCP spec |
| `tools/list` discovery | High | Query servers for available tools at startup |
| `tools/call` execution | High | Forward tool calls to MCP servers |
| Connection pool | Medium | Reuse connections, handle reconnection |
| Server trust levels | Medium | Per-server safety/permission overrides |
| Sub-agent execution engine | Medium | Dispatch tasks, manage parallelism |
| Sub-agent context isolation | Medium | Each sub-agent gets its own context window |
| Sub-agent result aggregation | Medium | Merge sub-task results into parent context |
| Tool schema validation | Low | Validate inputs against MCP-provided schemas |
| Server capability negotiation | Low | Handle server-specific protocol versions |

## Security Considerations

### MCP Servers

1. **Arbitrary code execution:** MCP servers are external processes. The `command` field in config can run anything. Mitigation: always require `PermissionAsk` for MCP tools.
2. **Supply chain risk:** `npx -y <package>` downloads and runs code on each invocation. Mitigation: pin package versions in args, audit server packages.
3. **Privilege escalation:** An MCP server inherits the agent's file system access. Mitigation: sandbox servers where possible, prefer read-only tools.
4. **Data exfiltration:** An MCP server could send data to external endpoints. Mitigation: network policy enforcement (future), server audit logging.

### Sub-Agents

1. **Resource exhaustion:** Unbounded sub-agent spawning could exhaust memory/tokens. Mitigation: enforce a maximum task depth and concurrent task limit.
2. **Context pollution:** Sub-agents with shared context could corrupt each other's state. Mitigation: isolate sub-agent context windows.
3. **Recursive delegation:** A sub-agent could spawn sub-agents indefinitely. Mitigation: enforce a maximum nesting depth (e.g., 3 levels).

## Testing Strategy

- **Config tests** (`internal/config/mcp_test.go`): Load from file, default config, invalid TOML, validation rules.
- **Tool tests** (`internal/tools/mcp_test.go`): Interface compliance, naming, safety, permission, stub run behavior, summarize output, cross-server name uniqueness.

All tests run with `go test ./...` and pass `go vet ./...` and `gofmt -l .`.
