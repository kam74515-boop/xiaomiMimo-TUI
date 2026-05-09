# Known Limitations

Honest list of current limitations in MiMo-TUI 1.0 RC.
These are documented so users know what to expect and what is planned for future releases.

---

## MCP / Sub-Agent Support

**Status:** Design only, not functional.

MCP (Model Context Protocol) and sub-agent orchestration are architected but not
yet wired into the runtime. The agent loop currently runs tools locally via the
built-in registry. Remote MCP servers and delegated sub-agents are not available.

**Impact:** Cannot connect to external tool servers or spawn sub-agents for
parallel task execution.

**Planned:** After the local tool loop is stable and proven in real coding sessions.

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

## No Node.js / Python / Rust Diagnostics

**Status:** Not implemented.

Beyond the absence of LSP support, there are no language-specific diagnostic
tools for Node.js (TypeScript compiler, ESLint), Python (mypy, pylint), or
Rust (cargo check). The `run_test` tool can execute test commands, but inline
diagnostics are not available.

**Impact:** Agent must rely on test output and manual inspection for error
detection in non-Go projects.

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

## Approval Timeout is Global

**Status:** Current implementation.

The `approval_timeout` in `policy.toml` applies to all tool approvals. There is
no per-tool or per-safety-grade timeout configuration.

**Impact:** Destructive operations and read-only operations share the same
timeout window.

---

## Benchmark Tasks are Go-Focused

**Status:** Current implementation.

The 5 benchmark coding tasks are defined for Go projects. There are no benchmark
tasks for Python, JavaScript, Rust, or other languages.

**Impact:** Benchmark harness does not measure agent performance on non-Go
coding tasks.
