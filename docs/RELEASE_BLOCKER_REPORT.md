# Release Blocker Report -- MiMo-TUI 1.0

Full audit of the MiMo-TUI codebase for 1.0 release readiness.
Audit date: 2026-05-08. Auditor: automated codebase audit.

---

## Summary

| Severity | Count | Status |
|----------|-------|--------|
| P0 (must fix) | 0 | none |
| P1 (should fix) | 0 | fixed |
| P2 (nice to have) | 1 | deferred |

**Verdict: No P0 or P1 blockers remain. 1.0 is release-ready from this audit.**

All build/test/vet/smoke validation passes cleanly:

```
go build ./...         -- PASS
go test ./...          -- PASS (16 packages, ~26s)
go vet ./...           -- PASS (0 diagnostics)
gofmt -l .             -- PASS (0 files)
MIMO_MOCK=1 smoke      -- PASS (events=16)
```

---

## P1 Issues (Fixed)

### P1-1: Incomplete secret redaction across tool outputs -- FIXED

**Location:** `internal/tools/builtins.go`, `internal/tools/readtools.go`,
`internal/tools/redact_test.go`

**Resolution:** `redactSecrets()` now covers artifact payloads for:

- `shell`
- `run_test`
- `rg`
- `read_file`
- `git_status`
- `git_diff`
- `git_log`

`internal/tools/redact_test.go` verifies representative secrets are redacted
before storage.

---

### P1-2: Config documentation URL inconsistency -- FIXED

**Location:** `internal/config/config.go`, `internal/provider/mimo/client.go`,
`docs/QUICKSTART.md`, `docs/CONFIGURATION.md`

**Resolution:** Code and documentation now use
`https://token-plan-cn.xiaomimimo.com/v1` as the default endpoint.

---

## P2 Issues (Nice to Have)

### P2-1: Ctrl+C not documented as quit key in QUICKSTART.md -- FIXED

**Location:** `docs/QUICKSTART.md` TUI Controls table, `README.md` TUI Controls

**Description:** The TUI handles both `q` and `ctrl+c` as quit keys (confirmed
in `internal/tui/model.go` line 141). QUICKSTART.md only lists `q`. README.md
lists `q or Ctrl+C` which is correct. QUICKSTART should match.

**Resolution:** QUICKSTART documents `q` and `Ctrl+C` as quit keys.

---

### P2-2: Ctrl+G interrupt not documented in QUICKSTART.md -- FIXED

**Location:** `docs/QUICKSTART.md` TUI Controls table

**Description:** `Ctrl+G` sends an interrupt signal to cancel a running agent
run (`internal/tui/model.go` line 283). This is documented in the TUI help
overlay (`?` key) but not in QUICKSTART.md or CONFIGURATION.md keybinding
tables.

**Resolution:** QUICKSTART documents `Ctrl+G` as the running-agent interrupt.

---

### P2-3: KNOWN_LIMITATIONS.md contradicts implemented code for model persistence -- FIXED

**Location:** `docs/KNOWN_LIMITATIONS.md` lines 80-98

**Description:** The "Model Persistence Requires Manual Config" section states:
"Model registry changes made via `-model-accept` are stored in memory and do
not persist across restarts." However, `cmd/mimo/main.go` line 737 calls
`config.SaveModelsConfig(registry)` which writes to `.mimo/models.toml`, and
`config.LoadModelsConfig()` reads from that file on startup. The persistence
is implemented.

**Impact:** Users are told a feature is missing when it actually works.

**Resolution:** `docs/KNOWN_LIMITATIONS.md` now marks model registry
persistence as implemented.

---

### P2-4: ARCHITECTURE.md tool count is wrong -- FIXED

**Location:** `docs/ARCHITECTURE.md` line 14

**Description:** States "11 built-in tools" but the actual count is 12:
shell, rg, read_file, list_dir, git_diff, git_log, artifact_read, write_file,
apply_patch, git_status, run_test, diagnostics.

**Resolution:** `docs/ARCHITECTURE.md` now says 12 built-in tools.

---

### P2-5: Policy config load error silently ignored -- FIXED

**Location:** `cmd/mimo/main.go` line 340

**Description:** `config.LoadPolicy()` returns an error, but main.go discards
it: `policyCfg, _ := config.LoadPolicy()`. If `.mimo/policy.toml` exists but
contains invalid TOML, the error is silently swallowed and the default policy
is used without warning.

**Impact:** Users may think their custom policy is active when it is not.

**Resolution:** `LoadPolicy()` logs a warning and safely falls back to defaults
when a policy file has invalid syntax.

---

### P2-6: `detectShellRisk` false positives for curl/wget -- FIXED

**Location:** `internal/tools/builtins.go` lines 633-651

**Description:** Any shell command containing `curl ` or `wget ` is classified
as `SafetyDestructive`, even benign read-only fetches like
`curl https://example.com/status`. Only piped-to-shell patterns
(`curl ... | sh`) should be destructive. Simple `curl`/`wget` should be
`SafetyShellMutation` at most.

**Impact:** Benign HTTP fetch commands are blocked by default policy
(destructive = deny).

**Resolution:** Plain `curl`/`wget` are `shell_mutation`; `curl | sh` and
similar piped installer patterns remain destructive.

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

### P2-8: QUICKSTART.md policy.toml path ordering is backwards -- FIXED

**Location:** `docs/QUICKSTART.md` lines 106-108 vs `internal/config/policy.go`
lines 68-74

**Description:** QUICKSTART.md states config file loading order as:
1. `~/.mimo-tui/config.toml` (user-global)
2. `.mimo/config.toml` (project-local)

For the main config (`config.Load()`), this is correct. But QUICKSTART
implies the same ordering applies to policy.toml, while the code checks
`.mimo/policy.toml` first (project-local wins). CONFIGURATION.md correctly
documents both orderings separately.

**Resolution:** QUICKSTART now documents config layering separately from
policy first-found precedence.

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
| TUI keybindings match docs | PASS |
| Artifact store write/read | PASS |
| Event bus pub/sub | PASS |
| Mock mode smoke test | PASS |
| Real mode auto-fallback to mock | PASS |
| Binary produces working output | PASS |
| No orphan flags | PASS |
| No hardcoded API keys | PASS |
