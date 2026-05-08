# MiMo Value Amplifier TUI

MiMo Value Amplifier TUI is a MiMo-first terminal coding agent.

It has two goals:

1. Make hidden MiMo capabilities visible, controllable, and replayable.
2. Make MiMo a strong coding agent inside the terminal.

This project is not a generic chat TUI wrapped around a model. It is a coding
cockpit for MiMo: context governance, evidence flow, tool execution, artifacts,
agent trace, replay, and model-update gates all live in one terminal workflow.

## Product Principle

The guiding question is:

> Which MiMo capability is not yet felt by the user, and how can the TUI turn it
> into visible, controllable, replayable productivity?

That means:

- 1M context becomes a governed `Context Map`, not a giant prompt bucket.
- SWA/GA is represented as evidence placement, not fake attention.
- HySparse-inspired thinking becomes a `Context Oracle` that promotes and demotes evidence.
- MTP streaming becomes perceived momentum: chat deltas, tool progress, trace, and cost update together.
- Agentic RL becomes visible `goal -> plan -> action -> observation -> revision` traces.
- Model updates go through `default / candidate / labs` channels and replay gates.
- Raw tool output never enters the model context directly; it is stored as an artifact first.

## Current Status

Approximate completion:

- Usable MVP: about 62%
- Stable daily AI coding product: about 38%
- Full MiMo value-amplifier vision: about 25%

Already implemented:

- Go single-binary CLI: `cmd/mimo`.
- Bubble Tea / Lip Gloss TUI with Context Map, Chat Stream, Agent Trace, Tool Cockpit.
- Multi-turn agent loop wired from TUI prompt input via `EventUserPrompt`.
- OpenAI-compatible MiMo SSE streaming client with mock fallback.
- Structured HTTP errors, retry/backoff, and streaming parser tests.
- Model registry with `default`, `candidate`, and `labs` channels.
- Replay gate with golden sessions and trajectory comparison.
- Context engine with `Near / Anchor / Artifact`, `Admit`, `AutoBudget`, `SelectionReason`, and `ReplacedBy`.
- HySparse-inspired Context Oracle with scoring and promote/demote review.
- Tool registry, permissions, approval flow, and artifact-backed raw output.
- RTK-style budget-aware summarizers for built-in tools.
- JSONL session replay, resume skeleton, and trajectory eval.

## Architecture

```mermaid
flowchart TD
    User["Developer / Terminal User"]

    CLI["cmd/mimo\nCLI flags, config, runtime wiring"]
    TUI["internal/tui\nBubble Tea UI\nContext Map / Chat / Trace / Tool Cockpit"]
    Bus["internal/core.Bus\nAgentEvent event bus"]

    Agent["internal/agent\nmulti-turn agent loop\nplan -> tool -> observation -> revision"]
    Provider["internal/provider/mimo\nMiMo OpenAI-compatible SSE client\nmock + real streaming"]
    ModelRegistry["internal/model\nmodel registry\ndefault / candidate / labs"]

    Tools["internal/tools\nregistry + executor + permissions"]
    Summarizers["internal/tools/summarizers\nRTK-style budget-aware summaries"]
    Artifact["internal/artifact\nraw stdout/stderr/diff/file artifact store"]

    Context["internal/context\nNear / Anchor / Artifact\nAdmit / AutoBudget / Oracle"]
    Oracle["internal/context/oracle\nHySparse-inspired evidence scoring"]

    Replay["internal/replay\nsession JSONL event log"]
    Session["internal/session\nresume skeleton"]
    Eval["internal/eval\ntrajectory extraction\nreplay gate / golden sessions"]

    Config["internal/config\nENV + config files"]

    User --> TUI
    User --> CLI

    CLI --> Config
    CLI --> ModelRegistry
    CLI --> Bus
    CLI --> TUI
    CLI --> Agent
    CLI --> Tools
    CLI --> Context
    CLI --> Replay

    TUI --> Bus
    Bus --> TUI

    Agent --> Provider
    Provider --> Agent
    Agent --> Tools
    Agent --> Context
    Agent --> Bus

    Tools --> Summarizers
    Tools --> Artifact
    Tools --> Bus

    Summarizers --> Context
    Artifact --> Context

    Context --> Oracle
    Oracle --> Context
    Context --> Bus

    Bus --> Replay
    Replay --> Session
    Replay --> Eval
    Eval --> ModelRegistry
```

## Core Data Flow

```text
user prompt
  -> EventUserPrompt
  -> agent.Loop
  -> MiMo streaming
  -> tool calls
  -> tool executor
  -> raw output -> artifact
  -> budget-aware summarizer -> observation
  -> Context.Admit
  -> Context Map / Agent Trace / Tool Cockpit
  -> replay JSONL
```

The important rule:

```text
raw output -> artifact -> bounded observation -> context admission
```

Raw stdout, stderr, diffs, file content, screenshots, and future audio/device
state must stay artifact-backed unless intentionally summarized.

## How This Differs From DeepSeek-TUI

DeepSeek-TUI is a mature DeepSeek V4 terminal coding agent. MiMo-TUI is a
MiMo-architecture-driven coding cockpit.

| Area | DeepSeek-TUI | MiMo-TUI |
| --- | --- | --- |
| Product center | DeepSeek coding agent | MiMo capability amplifier + coding agent |
| Model logic | DeepSeek V4, thinking mode, auto model/thinking routing | MiMo model registry, candidate/labs channels, replay gate |
| Context | 1M tracking, compaction, prefix-cache telemetry | Near / Anchor / Artifact, admission, oracle, selection reasons |
| Reasoning display | DeepSeek reasoning blocks | Agent Trace without fake hidden thinking |
| Tool output | Tool results in coding workflow | Artifact-first output, RTK-style summarized observations |
| Safety | Plan / Agent / YOLO, approval, rollback | Approval, context admission, replay; rollback still planned |
| Direction | Mature terminal agent | MiMo-specific evidence cockpit and future multimodal/device adapters |

We borrow architecture lessons, not code. The project stays clean-room.

## Local Run

```sh
go run ./cmd/mimo
```

Headless smoke mode:

```sh
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session smoke-local
```

With MiMo Token Plan credentials:

```sh
export MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
export MIMO_API_KEY="..."
export MIMO_MODEL="mimo-v2.5-pro"
go run ./cmd/mimo
```

Never commit API keys. Keep credentials in environment variables or local,
ignored config files.

## Useful CLI Flags

- `-workspace <dir>`: run against a workspace other than the configured default.
- `-session <id>`: choose the `.mimo/sessions/<id>.jsonl` event log name.
- `-resume-latest`: inject a compact latest-session summary into startup context.
- `-smoke`: run the event pipeline without launching the full-screen TUI.
- `-smoke-timeout <duration>`: override headless validation timeout.
- `-eval`: extract and print trajectory info for a session.
- `-eval-session <id>`: evaluate a specific session.
- `-list-models`: print registered models.
- `-golden-session <id>`: mark a session as golden.
- `-candidate-session <id>`: candidate session used for replay gate comparison.
- `-model-accept <model>`: accept a candidate model if replay gate passes.

Labs models are gated. Set this only when intentionally testing labs:

```sh
export MIMO_LABS=1
```

## TUI Controls

- `i` or `/`: enter prompt input mode.
- `Enter`: submit prompt.
- `Esc`: cancel prompt/help/approval.
- `Tab`: switch panel focus.
- `Ctrl+L`: clear chat display.
- `Ctrl+R`: request context oracle review.
- `?`: toggle help.
- `q` or `Ctrl+C`: quit.

## Validation

Run before committing:

```sh
gofmt -w <changed go files>
go test ./...
go vet ./...
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session smoke-local
```

Real MiMo smoke, only with environment-provided credentials:

```sh
MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1" \
MIMO_MODEL="mimo-v2.5-pro" \
go run ./cmd/mimo -smoke -smoke-timeout 60s -session smoke-real
```

## Next Priorities

The next development steps should focus on usability and coding-task success:

1. Persist model registry changes to config so `-model-accept` survives restart.
2. Add prompt queue and interrupt/cancel for long-running agent turns.
3. Ensure `Ctrl+R` fully triggers runtime oracle review in active sessions.
4. Build a standard coding trajectory: inspect -> plan -> patch -> test -> revise -> summary.
5. Add rollback snapshots before mutating turns.
6. Add LSP diagnostics after edits.
7. Run real MiMo coding smoke tests and harden provider tool-call parsing.
8. Add MCP and sub-agent support after the local tool loop is stable.

## Documentation

- [Full development guide](docs/FULL_DEVELOPMENT_GUIDE.md)
- [Architecture notes](docs/ARCHITECTURE.md)
- [MiMo value amplifier spec](docs/MIMO_VALUE_AMPLIFIER.md)
- [Agent contracts](docs/AGENT_CONTRACTS.md)
- [Critical thinking agent contract](docs/CRITICAL_THINKING_AGENT.md)
- [Eval plan](docs/EVALS.md)
- [Clean-room policy](docs/CLEAN_ROOM.md)
