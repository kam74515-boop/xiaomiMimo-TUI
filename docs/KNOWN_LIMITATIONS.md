# Known Limitations

Honest list of current limitations in MiMo-TUI 1.0 RC.
These are documented so users know what to expect and what is planned for future releases.

---

## Deferred Performance / Robustness Items

Surfaced by the deep-optimization audit and consciously deferred (with the
reasoning recorded here) rather than fixed under-verified:

- **TUI transcript re-wrap.** The wrapped transcript is now memoized in a
  shared-pointer cache keyed by content+width (+animation frame while streaming),
  so idle pulses no longer re-wrap and a streaming frame wraps once instead of
  once per measure/render call; the transcript is also capped (~256KB). What
  remains is `panelSize` re-rendering the header/status/footer/input bar purely
  to measure their heights on each call — a minor, bounded cost (single-line,
  width-bounded strings) left as-is to avoid coupling to layout assumptions.

- **Rollback covers tracked modifications and tool-created files; staged-only
  changes are a minor gap.** The snapshot now also records the pre-tool untracked
  set, and `ApplyRollback` deletes files that became untracked since (i.e. files
  the tool created) in addition to reverse-applying the tracked-file diff.
  Snapshot failures are surfaced. Remaining edge: changes a tool explicitly
  `git add`s (the built-in tools do not) are captured by the unstaged diff only.

---

## MCP / Sub-Agent Support

**Status:** MCP stdio transport functional; sub-agent execution functional
(worktree-isolated, no auto-merge).

MCP servers configured in `.mimo/mcp.toml` are connected over a newline-delimited
JSON-RPC stdio transport (initialize / tools/list / tools/call); their tools are
registered as `mcp__<server>__<tool>`, approval-gated, and shown in the activity
dashboard. Only local stdio servers are supported — remote HTTP/SSE transports
are not yet implemented.

Sub-agent delegation is wired: the `subagent` tool spawns a sub-agent that runs
in an isolated git worktree, with progress published to the activity dashboard
and its changes returned as a reviewable diff (artifact-backed). The sub-agent is
sandboxed — it may read and write files in its worktree but shell and destructive
tools are denied, it cannot delegate further (no nested sub-agents), and nothing
is merged into the parent tree automatically. Requires the workspace to be a git
repository; otherwise the tool stays an inert placeholder.

**Impact:** Can use local stdio MCP servers and delegate worktree-isolated
sub-tasks. Cannot use remote (HTTP/SSE) MCP servers; sub-agents cannot run shell
tools and their changes are not auto-merged (by design — the parent reviews the
diff and merges deliberately).

**Planned:** Remote MCP transports; optional shell access and merge assistance
for sub-agents once the isolated path is proven in real sessions.

**Configure** (`.mimo/mcp.toml`):

```toml
[[servers]]
name = "filesystem"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "."]
enabled = true
```

---

## LSP Diagnostics

**Status:** Go diagnostic tool implemented; no full LSP client.

MiMo-TUI includes a `diagnostics` tool that runs Go checks (`go vet` and
`go build`) and turns compiler output into structured diagnostics. It does not
yet run a persistent LSP server, so editor-grade incremental diagnostics,
cross-language symbol intelligence, and code action support are not available.

**Impact:** Go projects get basic after-edit validation, but diagnostics are
coarser than a real language server and still depend on explicit tool use.

**Planned:** Add a real LSP client after the Go diagnostic tool has proven
stable, then expand to other languages.

---

## Node.js / Python / Rust Diagnostics

**Status:** Implemented (toolchain-gated), no LSP.

The `diagnostics` tool now runs language-specific checks and parses them into
structured issues: Go (`go vet`+`go build`), Node/TypeScript (`tsc --noEmit`),
Python (`ruff` or `pyflakes`), and Rust (`cargo check`). The language is
auto-detected from project markers (go.mod / tsconfig.json|package.json /
pyproject.toml|setup.py|requirements.txt / Cargo.toml) when not specified. When
the project marker or the toolchain is absent, it degrades gracefully (0 issues,
exit 0) instead of failing — so it is safe to call anywhere.

**Impact:** These are batch (run-and-parse) diagnostics, not a persistent LSP, so
they lack incremental/editor-grade intelligence and code actions. Per-language
behavior depends on the chosen tool's output format.

---

## No Remote MCP

**Status:** Not implemented.

The MCP sub-agent design only covers local tool execution. Remote MCP server
connections (HTTP/SSE transport) are not supported.

**Impact:** Cannot use tools hosted on remote MCP servers.

---

## Context Oracle is Keyword-Based, Not Semantic

**Status:** Current implementation.

The Context Oracle uses keyword matching and heuristic scoring to promote and
demote context items. It does not use embedding-based semantic similarity or
vector search.

**Impact:** Oracle review may miss semantically relevant context items that
don't share keywords with the current goal. Promotion/demotion decisions are
approximate.

**Planned:** Semantic scoring using embeddings after the keyword-based oracle
is proven reliable.

---

## Model Registry Persistence

**Status:** Implemented.

Model registry changes made via `-model-accept` are persisted to `.mimo/models.toml`
(project-level). On startup, the registry loads from `~/.mimo-tui/models.toml` (global)
then `.mimo/models.toml` (project, overrides global). Accepted candidates and default
model changes survive restarts.

---

## Session Resume Does Not Restore Agent State

**Status:** Current implementation.

Session resume reconstructs context items, trace updates, and conversation
history from the JSONL event log. However, it does not restore the agent's
internal state (e.g., in-progress tool executions, partial reasoning chains).
The agent starts fresh with historical context injected.

**Impact:** Resumed sessions may re-execute tools or re-discover information
that was already processed in the previous session.

---

## No Streaming Token Count

**Status:** Not implemented.

The TUI does not display real-time token usage during streaming. Token estimates
are only available after context admission.

**Impact:** Users cannot monitor token consumption in real time during long
responses.

---

## Approval Timeout

**Status:** Per-safety-grade timeouts supported; per-tool not yet.

`policy.toml` supports a global `approval_timeout` plus per-safety-grade overrides
under `[approval_timeouts]` (`read_only` / `workspace_mutation` /
`shell_mutation` / `destructive`, in seconds). Resolution order is per-grade →
global → built-in default (30s). Per-individual-tool timeouts are not yet
configurable.

```toml
approval_timeout = 30
[approval_timeouts]
destructive = 10
shell_mutation = 20
```

---

## Benchmark Tasks are Go-Focused

**Status:** Current implementation.

The 5 benchmark coding tasks are defined for Go projects. There are no benchmark
tasks for Python, JavaScript, Rust, or other languages.

**Impact:** Benchmark harness does not measure agent performance on non-Go
coding tasks.
