# MiMo-TUI Architecture

## Overview

MiMo-TUI is a Go single-binary terminal AI coding agent. It amplifies MiMo model capabilities through a 4-panel TUI with context management, tool safety, and session persistence.

## Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/mimo` | CLI entry point, flag parsing, wiring |
| `internal/agent` | Agent loop, system prompt, step management |
| `internal/provider/mimo` | MiMo API client, SSE streaming |
| `internal/tools` | Tool registry, executor, 11 built-in tools |
| `internal/context` | Context manager, oracle, compression |
| `internal/tui` | Bubble Tea TUI, 4-panel layout |
| `internal/session` | Session resume, history extraction |
| `internal/core` | Shared types, event bus |
| `internal/model` | Model registry, channel gating |
| `internal/eval` | Evaluation, benchmarks, golden sessions |
| `internal/replay` | Event log replay |
| `internal/artifact` | Artifact storage, rollback |
| `internal/config` | Policy configuration |

## TUI Layout

```
┌─────────────┬─────────────┐
│ Context Map │ Chat Stream │
│ (Near/      │ (Assistant  │
│  Anchor/    │  Output)    │
│  Artifact)  │             │
├─────────────┼─────────────┤
│ Agent Trace │ Tool Cockpit│
│ (Plan/      │ (Results/   │
│  Action/    │  Timing/    │
│  Observe)   │  Rollback)  │
└─────────────┴─────────────┘
```

## Event Flow

1. User input → TUI captures prompt
2. Agent loop → Streams to MiMo API
3. Tool calls → Executor runs with safety checks
4. Observations → Promoted into context map
5. Oracle → Periodically reviews context
6. Event bus → All events for TUI, persistence, replay

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
