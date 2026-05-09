# Runtime Integration Audit Report

Date: 2026-05-08

## Summary

| Feature | Status | Notes |
|---------|--------|-------|
| Provider regression (Phase 8) | ✅ integrated | Tests cover real MiMo format, serializer/parser correct |
| Session Resume (Phase 9) | ✅ integrated | `-resume-latest` restores history/context/trace |
| Acceptance Tests (Phase 10) | ⚠️ partial | Package tests only, no CLI entry point |
| Rollback Restore (Phase 11) | ✅ integrated | CLI flags work, executor creates rollback artifacts |
| Approval UX (Phase 12) | ✅ integrated | TUI shows tool name, safety, reason, countdown |
| Policy.toml (Phase 12) | ✅ integrated | **FIXED**: Now wired into executor via `WithPolicyConfig` |
| Context Compression (Phase 13) | ✅ integrated | Oracle calls compression, TUI shows ReplacedBy |
| Prompt Queue / Interrupt (Phase 2) | ✅ integrated | FIFO queue, Ctrl+C cancels current turn |
| Oracle (Phase 3) | ✅ integrated | Ctrl+r triggers review, auto-review on bootstrap |
| Model Registry (Phase 1) | ✅ integrated | DefaultRegistry seeded, channel gating works |
| MaxSteps | ✅ fixed | **FIXED**: Increased from 8 to 16, configurable via MIMO_MAX_STEPS |

## Detailed Findings

### 1. Provider Regression (Phase 8) — ✅ integrated

**Evidence:**
- `internal/provider/mimo/client_test.go`: 21 tests covering ToolCall serialization, Message content field, malformed args, HTTP errors
- Runtime uses correct OpenAI-compatible serializer in `client.go`
- `toolCallAccumulator` correctly handles streaming tool_call deltas

### 2. Session Resume (Phase 9) — ✅ integrated

**Evidence:**
- `cmd/mimo/main.go:publishResumeSummary()`: Loads session, restores context items, publishes trace updates
- `internal/session/summary.go:ExtractHistory()`: Reconstructs conversation from events
- `-resume-latest` flag passes history to `agent.Loop()`
- TUI displays "resumed session <id>"

### 3. Acceptance Tests (Phase 10) — ⚠️ partial

**Issue:** Tests exist in `internal/eval/tasks/` but are only runnable as `go test`. No CLI entry point like `-acceptance-test`.

**Fix task:** Add `-acceptance` flag to CLI that runs the eval tasks.

### 4. Rollback Restore (Phase 11) — ✅ integrated

**Evidence:**
- `internal/tools/executor.go:167-180`: Captures `git diff` before mutating tools
- `cmd/mimo/main.go`: `-rollback-list`, `-rollback-show`, `-rollback-apply`, `-rollback-confirm` flags
- `internal/artifact/rollback.go`: `ListRollbacks`, `ShowRollback`, `ApplyRollback`
- TUI shows `RollbackArtifactID` in Tool Cockpit panel

### 5. Approval UX (Phase 12) — ✅ integrated

**Evidence:**
- `internal/tui/model.go:renderApprovalPanel()`: Shows tool name, safety level (color-coded), reason, input summary, affected paths, countdown
- 10-second countdown with auto-deny
- `y/n/esc` keybindings

### 6. Policy.toml (Phase 12) — ✅ integrated (was ❌, now fixed)

**Previous issue:** `config/policy.go` had `LoadPolicy()` and `EvaluatePolicy()` but neither was called from main.go or executor.

**Fix applied:**
- `internal/tools/executor.go`: Added `WithPolicyConfig(cfg)` option, uses `EvaluatePolicy` when policy is set
- `cmd/mimo/main.go`: Loads policy via `config.LoadPolicy()` and passes to executor
- Tests added: `TestPolicyDenyBlocksExecution`, `TestPolicyAllowBypassesApproval`

### 7. Context Compression (Phase 13) — ✅ integrated

**Evidence:**
- `internal/context/oracle.go:163`: Calls `manager.CompressItems()` for low-activity items
- `internal/context/manager.go:278-290`: Includes compression records in snapshot
- `internal/tui/model.go:997-1004`: Shows ReplacedBy and SelectionReason in Context Map

### 8. Prompt Queue / Interrupt (Phase 2) — ✅ integrated

**Evidence:**
- `cmd/mimo/main.go`: Event loop handles `EventUserPrompt` (queues if agent running), `EventInterrupt` (cancels via context)
- TUI `ctrl+g` sends interrupt event
- Queue is FIFO, dispatches on `EventDone`

### 9. Oracle (Phase 3) — ✅ integrated

**Evidence:**
- `internal/context/oracle.go:RunOracleStep()`: Reviews all items, applies promotions/demotions/compressions
- `internal/tui/model.go`: `ctrl+r` sends `EventOracleReview`
- Bootstrap runs oracle after initial tools
- Auto-review possible every N steps (configurable)

### 10. Model Registry (Phase 1) — ✅ integrated

**Evidence:**
- `internal/model/registry.go:DefaultRegistry()`: Seeds mimo-v2.5-pro (default), mimo-v2-flash (candidate), mimo-v2.5-pro-max (candidate)
- `-list-models` flag prints registered models
- Channel gating: candidates log warning, labs require `MIMO_LABS=1`

### 11. MaxSteps — ✅ fixed (was P0)

**Previous issue:** `MaxSteps = 8` was too low for real coding tasks.

**Fix applied:**
- `internal/agent/agent.go`: Default increased to 16
- Configurable via `MIMO_MAX_STEPS` environment variable

## P0 Issues Found and Fixed

| Issue | Severity | Fix |
|-------|----------|-----|
| Policy.toml not wired into executor | P0 | Added `WithPolicyConfig` to executor, loaded in main.go |
| MaxSteps=8 too low for real tasks | P0 | Increased to 16, configurable via env var |

## Follow-Up Issues

| Issue | Severity | Status |
|-------|----------|--------|
| Acceptance tests not CLI-accessible | P2 | Optional `-acceptance` flag remains a post-1.0 convenience |
| Approval timeout mismatch (executor 30s vs TUI 10s) | Fixed | TUI now honors `ApprovalRequest.TimeoutSeconds` from executor/policy |
| Model registry persistence gap | Fixed | `-model-accept` persists to `.mimo/models.toml`; startup loads global then project overrides |
