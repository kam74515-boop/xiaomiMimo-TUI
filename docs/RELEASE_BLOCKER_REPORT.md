# Release Blocker Report -- MiMo-TUI 1.0

Full audit of the MiMo-TUI codebase for 1.0 release readiness.
Audit date: 2026-05-08. Auditor: automated codebase audit.

---

## Summary

| Severity | Count | Status |
|----------|-------|--------|
| P0 (must fix) | 0 | -- |
| P1 (should fix) | 2 | open |
| P2 (nice to have) | 8 | open |

**Verdict: No P0 blockers. Two P1 issues should be addressed before tagging 1.0.**

All build/test/vet/smoke validation passes cleanly:

```
go build ./...         -- PASS
go test ./...          -- PASS (16 packages, ~26s)
go vet ./...           -- PASS (0 diagnostics)
gofmt -l .             -- PASS (0 files)
MIMO_MOCK=1 smoke      -- PASS (events=16)
```

---

## P1 Issues (Should Fix for 1.0)

### P1-1: Incomplete secret redaction across tool outputs

**Location:** `internal/tools/readtools.go` (rgTool, gitDiffTool, gitLogTool),
`internal/tools/builtins.go` (readFileTool)

**Description:** Only `shellTool.runCommand()` calls `redactSecrets()` on
stdout/stderr before storing artifacts. The following tools store raw bytes
directly without redaction:

- `rg` -- search results may contain API keys from config files
- `git_diff` -- diffs may show API key changes
- `git_log` -- commit messages may reference secrets
- `read_file` -- file content stored verbatim
- `list_dir` -- directory listings (low risk, filenames only)

If the agent searches or reads files containing secrets, those secrets will be
stored in `.mimo/artifacts/` and potentially visible to anyone with filesystem
access.

**Impact:** API keys or tokens could leak into artifact storage if the agent
reads files containing them (e.g., `.env`, `config.toml` with embedded keys).

**Recommended fix:** Apply `redactSecrets()` to stdout/stderr bytes in
`runGitCommand()` (covers `git_diff`, `git_log`, `git_status`) and in each
tool's `Run()` method. Alternatively, apply redaction at the artifact store
write boundary.

---

### P1-2: Config documentation URL inconsistency

**Location:** `docs/QUICKSTART.md` line 129 vs `docs/CONFIGURATION.md` line 13

**Description:** QUICKSTART.md documents the default `MIMO_BASE_URL` as
`https://token-plan-cn.xiaomimimo.com/v1` in the environment variable table,
while CONFIGURATION.md and the actual code default to
`https://api.xiaomimimo.com/v1`. The "Run with Real MiMo" section in
QUICKSTART.md uses the token-plan URL as an example export, which is correct
for that context, but the env var table's "Default" column is misleading.

**Impact:** Users following the env var table may configure the wrong endpoint.

**Recommended fix:** Change QUICKSTART.md env var table default to
`https://api.xiaomimimo.com/v1` to match CONFIGURATION.md and code. Keep the
token-plan URL in the "Run with Real MiMo" example section.

---

## P2 Issues (Nice to Have)

### P2-1: Ctrl+C not documented as quit key in QUICKSTART.md

**Location:** `docs/QUICKSTART.md` TUI Controls table, `README.md` TUI Controls

**Description:** The TUI handles both `q` and `ctrl+c` as quit keys (confirmed
in `internal/tui/model.go` line 141). QUICKSTART.md only lists `q`. README.md
lists `q or Ctrl+C` which is correct. QUICKSTART should match.

**Recommended fix:** Add `Ctrl+C` to the QUICKSTART.md TUI controls table as an
alternative quit key.

---

### P2-2: Ctrl+G interrupt not documented in QUICKSTART.md

**Location:** `docs/QUICKSTART.md` TUI Controls table

**Description:** `Ctrl+G` sends an interrupt signal to cancel a running agent
run (`internal/tui/model.go` line 283). This is documented in the TUI help
overlay (`?` key) but not in QUICKSTART.md or CONFIGURATION.md keybinding
tables.

**Recommended fix:** Add `Ctrl+G` to the QUICKSTART.md and CONFIGURATION.md
TUI controls tables.

---

### P2-3: KNOWN_LIMITATIONS.md contradicts implemented code for model persistence

**Location:** `docs/KNOWN_LIMITATIONS.md` lines 80-98

**Description:** The "Model Persistence Requires Manual Config" section states:
"Model registry changes made via `-model-accept` are stored in memory and do
not persist across restarts." However, `cmd/mimo/main.go` line 737 calls
`config.SaveModelsConfig(registry)` which writes to `.mimo/models.toml`, and
`config.LoadModelsConfig()` reads from that file on startup. The persistence
is implemented.

**Impact:** Users are told a feature is missing when it actually works.

**Recommended fix:** Update KNOWN_LIMITATIONS.md to reflect that model
persistence IS implemented. Change the status to "Implemented" or remove the
section entirely.

---

### P2-4: ARCHITECTURE.md tool count is wrong

**Location:** `docs/ARCHITECTURE.md` line 14

**Description:** States "11 built-in tools" but the actual count is 12:
shell, rg, read_file, list_dir, git_diff, git_log, artifact_read, write_file,
apply_patch, git_status, run_test, diagnostics.

**Recommended fix:** Change "11" to "12".

---

### P2-5: Policy config load error silently ignored

**Location:** `cmd/mimo/main.go` line 340

**Description:** `config.LoadPolicy()` returns an error, but main.go discards
it: `policyCfg, _ := config.LoadPolicy()`. If `.mimo/policy.toml` exists but
contains invalid TOML, the error is silently swallowed and the default policy
is used without warning.

**Impact:** Users may think their custom policy is active when it is not.

**Recommended fix:** Log a warning to stderr when `LoadPolicy()` returns an
error but the file exists.

---

### P2-6: `detectShellRisk` false positives for curl/wget

**Location:** `internal/tools/builtins.go` lines 633-651

**Description:** Any shell command containing `curl ` or `wget ` is classified
as `SafetyDestructive`, even benign read-only fetches like
`curl https://example.com/status`. Only piped-to-shell patterns
(`curl ... | sh`) should be destructive. Simple `curl`/`wget` should be
`SafetyShellMutation` at most.

**Impact:** Benign HTTP fetch commands are blocked by default policy
(destructive = deny).

**Recommended fix:** Move `curl`/`wget` from `destructiveMarkers` to
`shellMutationMarkers` unless they appear with `| sh` or `| bash` (which is
already handled separately).

---

### P2-7: No tests for `cmd/mimo` or `internal/core`

**Status:** DEFERRED -- too risky to add cmd/mimo tests this close to 1.0 tag.
The entry point has significant side-effectful logic (TUI launch, event bus
wiring, smoke validation) that would need substantial mocking infrastructure.
Post-1.0 follow-up.

**Description:** Both `cmd/mimo` and `internal/core` have `[no test files]`.
The `cmd/mimo` package contains significant logic (flag parsing, smoke
validation, rollback commands, eval commands, model resolution) that would
benefit from unit tests.

**Recommended fix:** Add unit tests for `parseFlags()`, `validateSmokeCounts()`,
`resolveModel()`, and the rollback/eval command functions.

---

### P2-8: QUICKSTART.md policy.toml path ordering is backwards

**Location:** `docs/QUICKSTART.md` lines 106-108 vs `internal/config/policy.go`
lines 68-74

**Description:** QUICKSTART.md states config file loading order as:
1. `~/.mimo-tui/config.toml` (user-global)
2. `.mimo/config.toml` (project-local)

For the main config (`config.Load()`), this is correct. But QUICKSTART
implies the same ordering applies to policy.toml, while the code checks
`.mimo/policy.toml` first (project-local wins). CONFIGURATION.md correctly
documents both orderings separately.

**Recommended fix:** In QUICKSTART.md, clarify that policy.toml loading order
differs from config.toml (project-local policy wins over user-global policy).

---

## Areas Audited with No Issues Found

| Area | Result |
|------|--------|
| Build path (`go build ./cmd/mimo`) | PASS |
| All CLI flags wired and functional | PASS |
| All env vars match os.Getenv calls | PASS |
| Config file loading with defaults | PASS |
| Policy evaluation precedence | PASS |
| Approval flow and timeout | PASS |
| Rollback snapshot and restore | PASS |
| Session resume and history | PASS |
| Context compression and oracle | PASS |
| Model registry and channel gating | PASS |
| TUI keybindings match docs | PASS (minor P2s) |
| Artifact store write/read | PASS |
| Event bus pub/sub | PASS |
| Mock mode smoke test | PASS |
| Real mode auto-fallback to mock | PASS |
| Binary produces working output | PASS |
| No orphan flags | PASS |
| No hardcoded API keys | PASS |
