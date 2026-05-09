# MiMo-TUI Architecture

## Overview

MiMo-TUI is a Go single-binary terminal AI coding agent. It amplifies MiMo model capabilities through a transcript-first TUI with context dashboards, tool safety, and session persistence.

## Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/mimo` | CLI entry point, flag parsing, wiring |
| `internal/agent` | Agent loop, system prompt, step management |
| `internal/provider/mimo` | MiMo API client, SSE streaming |
| `internal/tools` | Tool registry, executor, 12 built-in tools |
| `internal/context` | Context manager, oracle, compression |
| `internal/tui` | Bubble Tea TUI, transcript-first layout plus dashboards |
| `internal/session` | Session resume, history extraction |
| `internal/core` | Shared types, event bus |
| `internal/model` | Model registry, channel gating |
| `internal/eval` | Evaluation, benchmarks, golden sessions |
| `internal/replay` | Event log replay |
| `internal/artifact` | Artifact storage, rollback |
| `internal/config` | Policy configuration |

## TUI Layout

```
┌─────────────────────────────────────────────┐
│ Transcript                                  │
│ user -> MiMo -> tool -> observation timeline│
│ full-height, scrollable, transcript-first   │
└─────────────────────────────────────────────┘

Tab / Shift+Tab opens a dashboard view:

┌──────────────────────────────┬──────────────┐
│ Transcript                   │ Context Map  │
│ continuous conversation      │ Agent Trace  │
│ with inline tool blocks      │ Tool Cockpit │
└──────────────────────────────┴──────────────┘
```

## Event Flow

1. User input → TUI captures prompt
2. Agent loop → Streams to MiMo API
3. Tool calls → Executor runs with safety checks
4. Observations → Promoted into context map
5. Oracle → Periodically reviews context
6. Event bus → All events for TUI, persistence, replay

## ActivityEvent

`ActivityEvent` is the product-level visibility contract for background work.
It is a typed event stream for everything that should be reviewable but should
not necessarily interrupt the main conversation:

- tool lifecycle: queued, approved, running, streamed output, summarized, failed
- skill lifecycle: discovered, loaded, applied, skipped, blocked
- MCP lifecycle: server configured, connection pending, tool discovered, call requested, call completed
- sub-agent lifecycle: delegated, started, step update, result summary, blocked, merged
- safety lifecycle: permission requested, denied, redacted, artifact created

Suggested fields:

| Field | Purpose |
|-------|---------|
| `id` | Stable event id for replay and UI selection |
| `ts` | Event timestamp |
| `kind` | `tool`, `skill`, `mcp`, `subagent`, `safety`, `context` |
| `actor` | Main agent, sub-agent id, MCP server, or tool name |
| `parent_id` | Optional parent activity for grouped timelines |
| `status` | `queued`, `running`, `waiting`, `done`, `failed`, `redacted` |
| `summary` | Short human-readable dashboard line |
| `artifact_id` | Optional raw evidence pointer |
| `context_effect` | Optional Near / Anchor / Artifact placement result |
| `privacy` | `public`, `workspace`, `sensitive`, or `redacted` |

`ActivityEvent` is not hidden reasoning and not a replacement for
`AgentEvent`. `AgentEvent` remains the runtime bus contract; `ActivityEvent`
is the user-facing observability layer that can be derived from runtime events
or emitted directly by future tool / MCP / sub-agent runtimes.

## Activity Timeline

The Activity Timeline is the right-side dashboard view for background activity.
Its job is to make delegation visible without polluting the main transcript.

Transcript remains the quiet conversation:

```text
user -> MiMo answer -> concise tool/observation markers -> final answer
```

Activity Timeline carries the full operational trail:

```text
skill loaded
tool approved
MCP server discovered
sub-agent delegated
sub-agent tool call
artifact stored
observation admitted
result merged
```

This is the main difference from black-box delegation: background work is not
hidden, but it is also not sprayed into the primary chat. Users can tab into the
dashboard, inspect a specific activity, open its artifact, and replay the event
sequence later from the session log.

## Sub-agent Observatory

The Sub-agent Observatory is the dashboard slice dedicated to delegated work.
It should show:

- active sub-agents, goal, owner, status, elapsed time, and current step
- worktree or isolation boundary when available
- tools, skills, and MCP servers used by each sub-agent
- artifact ids and summarized observations produced by each sub-agent
- result merge state: pending review, accepted, rejected, or blocked
- safety state: approvals requested, redactions applied, policy failures

MiMo-TUI differs from Claude Code here by treating delegation as an observable
runtime surface. Sub-agents may run in the background, but their activity is
logged, inspectable, replayable, and summarized into the main transcript only
when it matters. The main chat stays focused on user intent and final synthesis;
the observatory carries operational detail.

## Context Management

Three-tier system:
- **Near**: Evictable working memory (85% budget cap)
- **Anchor**: Pinned reference material (never evicted)
- **Artifact**: Raw output storage (bypasses admission)

## Tool Safety

Four grades: `read_only`, `workspace_mutation`, `shell_mutation`, `destructive`

Policy precedence: allowlist → denylist → require_confirm → safety grade default

## Session Persistence

Events persisted to `.mimo/sessions/` as NDJSON. Resume reconstructs history, context, trace, and artifacts.
