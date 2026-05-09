# MiMo-TUI 1.0 Release Notes

MiMo-TUI is a Go single-binary terminal coding agent built specifically for the
MiMo model family. It turns MiMo capabilities -- 1M context, HySparse thinking,
MTP streaming, agentic RL -- into visible, controllable, replayable terminal
productivity.

**Release date:** 2026-05-08

---

## What's New in 1.0

### 4-Panel TUI

A full-screen terminal interface built on Bubble Tea and Lip Gloss with four
panels:

- **Context Map** -- shows Near / Anchor / Artifact tiers with admission reasons
  and eviction state.
- **Chat Stream** -- conversation with MiMo via OpenAI-compatible SSE streaming.
- **Agent Trace** -- goal / plan / action / observation steps from the agent loop.
- **Tool Cockpit** -- tool results, artifact IDs, exit codes, and rollback
  controls.

### Context Management

Three-tier context engine with governed admission:

- **Near** -- evictable working memory (85% budget cap).
- **Anchor** -- pinned reference material (never evicted during session).
- **Artifact** -- raw output storage that bypasses admission.

Supports `AutoBudget`, `SelectionReason` on every context item, and
`ReplacedBy` lineage tracking for context compression.

### Context Oracle

A HySparse-inspired oracle that reviews context items using keyword-based
scoring and heuristic promote / demote logic. Triggered via `Ctrl+R` in the
TUI or invoked automatically during the agent loop.

Note: the oracle currently uses keyword matching, not embedding-based semantic
similarity. See Known Limitations below.

### Tool Safety System

12 built-in tools with four safety grades:

| Grade | Behavior |
|-------|----------|
| `read_only` | Allowed by default |
| `workspace_mutation` | Requires approval |
| `shell_mutation` | Requires approval |
| `destructive` | Denied by default |

Policy is configured via `.mimo/policy.toml` with allowlist, denylist, and
`require_confirm` rules. Shell commands are automatically classified by
`detectShellRisk()` pattern matching.

### Secret Redaction and Input Sanitization

- `redactSecrets()` scrubs API keys, tokens, and bearer tokens from `shell`,
  `run_test`, `rg`, `read_file`, `git_status`, `git_diff`, and `git_log`
  output before artifact storage.
- `redactInput()` replaces large `content` and `patch` fields with byte counts
  in artifact storage.
- No hardcoded API keys in source; all credentials come from environment
  variables or local config files.

### Session Persistence and Resume

All events are written to `.mimo/sessions/<id>.jsonl` as NDJSON. Sessions can
be resumed with `-resume-latest`, which reconstructs context items, trace
updates, and recent conversation history from the event log.

### Rollback Snapshots

Mutating tools automatically take workspace snapshots before execution.
Rollback is available via CLI:

```sh
mimo -rollback-list
mimo -rollback-show <artifact-id>
mimo -rollback-apply <artifact-id>                # dry-run
mimo -rollback-apply <artifact-id> -rollback-confirm  # apply
```

### Model Registry

Three-channel model governance:

- **default** -- production model, always available.
- **candidate** -- evaluated against golden sessions via replay gate.
- **labs** -- gated behind `MIMO_LABS=1` environment variable.

Model changes persist across restarts via `.mimo/models.toml`. Candidates are
promoted with `-model-accept` after passing the replay gate.

### Multi-Language Test Detection

`run_test` automatically detects and runs the correct test command for Go, npm,
pnpm, yarn, Python, and Rust projects.

### Benchmark and Endurance Testing

A benchmark harness with 5 coding task definitions, long-run endurance testing,
and 1M context pressure tests.

---

## Getting Started

See the [Quickstart Guide](QUICKSTART.md) for build instructions, mock mode,
real MiMo configuration, TUI controls, session management, and rollback usage.

Build and run in under 5 minutes:

```sh
git clone <repo-url>
cd mimo-TUI
MIMO_MOCK=1 go run ./cmd/mimo
```

---

## Known Limitations

**MCP / Sub-Agent Support** -- Design only. MCP and sub-agent orchestration are
architected but not wired into the runtime. Cannot connect to external tool
servers or spawn sub-agents.

**LSP Diagnostics** -- Go diagnostics are available through the `diagnostics`
tool, but there is no persistent LSP client yet. Node.js, Python, and Rust
diagnostics remain placeholder follow-ups.

**Language-Specific Diagnostics** -- No inline diagnostics for Node.js
(TypeScript, ESLint), Python (mypy, pylint), or Rust (cargo check). Agent
relies on test output and manual inspection.

**No Remote MCP** -- Only local tool execution. Remote MCP server connections
over HTTP/SSE are not supported.

**Context Oracle is Keyword-Based** -- The oracle uses keyword matching, not
embedding-based semantic similarity. It may miss semantically relevant context
items that lack keyword overlap.

**Session Resume Does Not Restore Agent State** -- Resume reconstructs context
and history but does not restore in-progress tool executions or partial
reasoning chains. The agent starts fresh with historical context injected.

**No Streaming Token Count** -- Token usage is not displayed in real time
during streaming. Estimates are only available after context admission.

**Approval Timeout is Global** -- The `approval_timeout` setting applies to
all tool approvals. No per-tool or per-safety-grade timeout configuration.

**Benchmark Tasks are Go-Focused** -- The 5 benchmark coding tasks are defined
for Go projects only.

---

## Safety

### Tool Safety Grades

Every tool invocation is classified into one of four safety grades. The default
policy allows reads, asks for mutations, and denies destructive operations.

### Policy Configuration

`.mimo/policy.toml` provides three rule types:

- **allowlist** -- auto-approve specific tools.
- **denylist** -- auto-deny specific tools or patterns.
- **require_confirm** -- force approval prompt for specific tools or patterns.

Policy precedence: allowlist > denylist > require_confirm > safety grade default.

### Approval Flow

Workspace and shell mutations trigger an in-TUI approval prompt with a
configurable timeout (default 30 seconds). `y` or `Enter` approves; `n` or
`Esc` denies.

### Shell Risk Detection

`detectShellRisk()` classifies shell commands by pattern matching destructive
markers (`rm`, `sudo`, `git reset --hard`, `curl | sh`) and mutation markers
(`mv`, `cp`, `git`). Commands are labeled with risk levels in the Tool Cockpit.

### Secret Redaction

`redactSecrets()` pattern-matches API keys, tokens, and bearer tokens across
command output, search results, file reads, and git output before artifact
storage. `redactInput()` strips large content fields from artifact storage.

---

## Breaking Changes

None. This is the initial 1.0 release.

---

## Acknowledgments

MiMo-TUI is built for the MiMo model family by the MiMo team. The project
draws architecture lessons from the broader terminal agent ecosystem while
maintaining a clean-room implementation.

Special thanks to the MiMo model team for the 1M context window, HySparse
thinking, MTP streaming, and agentic RL capabilities that this TUI is designed
to amplify.
