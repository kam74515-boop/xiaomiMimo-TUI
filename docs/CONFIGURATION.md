# Configuration Reference

Complete reference for all MiMo-TUI configuration options.

---

## Environment Variables

Environment variables always override config file values.

### `MIMO_BASE_URL`

- **Default:** `https://token-plan-cn.xiaomimimo.com/v1`
- **Description:** MiMo API endpoint URL. Must be an OpenAI-compatible `/v1` endpoint.

```sh
export MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
```

### `MIMO_API_KEY`

- **Default:** (empty)
- **Description:** API key for MiMo authentication. When empty, mock mode activates automatically.

```sh
export MIMO_API_KEY="sk-..."
```

### `MIMO_MODEL`

- **Default:** `mimo-v2.5-pro`
- **Description:** Model ID to use for completions. Must be registered in the model registry or accepted via `-model-accept`.

```sh
export MIMO_MODEL="mimo-v2.5-pro"
```

### `MIMO_MOCK`

- **Default:** (unset, false)
- **Description:** Force mock mode regardless of API key presence. Accepts `1`, `true`, `yes`, `on`.

```sh
export MIMO_MOCK=1
```

### `MIMO_LABS`

- **Default:** (unset, false)
- **Description:** Unlock labs-channel models. Without this, labs models fall back to the registry default. Accepts `1`, `true`, `yes`, `on`.

```sh
export MIMO_LABS=1
```

### `MIMO_MAX_STEPS`

- **Default:** (unset, uses built-in default)
- **Description:** Override the maximum number of agent loop steps per turn.

```sh
export MIMO_MAX_STEPS=50
```

---

## CLI Flags

All flags are passed to `go run ./cmd/mimo [flags] [prompt]`.

### General

| Flag | Type | Description |
|------|------|-------------|
| `-workspace <dir>` | string | Workspace directory. Overrides `runtime.workspace` in config. Default: `.` |
| `-session <id>` | string | Session ID for the `.mimo/sessions/<id>.jsonl` event log. Default: timestamp |
| `-smoke` | bool | Run the event pipeline in headless mode without launching the TUI. |
| `-smoke-timeout <duration>` | duration | Maximum time to wait in smoke mode. Default: `10s` |

### Session Resume

| Flag | Type | Description |
|------|------|-------------|
| `-resume-latest` | bool | Load a compact summary of the latest session into the startup Context Map. |

### Evaluation

| Flag | Type | Description |
|------|------|-------------|
| `-eval` | bool | Extract and print trajectory info for a session. |
| `-eval-session <id>` | string | Evaluate a specific session ID. Default: latest session |

### Model Management

| Flag | Type | Description |
|------|------|-------------|
| `-list-models` | bool | Print all registered models and exit. |
| `-golden-session <id>` | string | Mark a session as golden (used as replay gate reference). |
| `-candidate-session <id>` | string | Candidate session for replay gate evaluation. |
| `-model-accept <model>` | string | Accept a candidate model if the replay gate passes. Requires `-golden-session` and `-candidate-session`. |

### Rollback

| Flag | Type | Description |
|------|------|-------------|
| `-rollback-list` | bool | List all rollback artifacts in the workspace. |
| `-rollback-show <id>` | string | Show what a rollback artifact will restore. |
| `-rollback-apply <id>` | string | Apply a rollback artifact. Dry-run by default. |
| `-rollback-confirm` | bool | Confirm actual rollback apply. Required with `-rollback-apply` to commit changes. |

---

## Config File

### Location

Config files are loaded in order (first found wins):

1. `~/.mimo-tui/config.toml` (user-global)
2. `.mimo/config.toml` (project-local)

### Format

```toml
[provider]
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
api_key = ""                    # prefer MIMO_API_KEY env var
model = "mimo-v2.5-pro"
mock = false

[runtime]
workspace = "."
context_window = 1000000
```

### Fields

#### `[provider]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `base_url` | string | `https://token-plan-cn.xiaomimimo.com/v1` | MiMo API endpoint |
| `api_key` | string | (empty) | API key. Prefer `MIMO_API_KEY` env var |
| `model` | string | `mimo-v2.5-pro` | Model ID |
| `mock` | bool | `false` | Force mock mode |

#### `[runtime]`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workspace` | string | `.` | Workspace directory for tool execution |
| `context_window` | int | `1000000` | Context window size in tokens |

---

## Policy File

### Location

Policy files are loaded in order (first found wins):

1. `.mimo/policy.toml` (project-local)
2. `~/.mimo-tui/policy.toml` (user-global)

### Format

```toml
# Approval timeout in seconds. 0 means default (30s).
approval_timeout = 30

[defaults]
read_only = "allow"             # allow | ask | deny
workspace_mutation = "ask"      # allow | ask | deny
shell_mutation = "ask"          # allow | ask | deny
destructive = "deny"            # allow | ask | deny

# Allowlist: highest priority. Matching tool calls are auto-allowed.
[[allowlist]]
tool = "rg"

[[allowlist]]
tool = "read_file"

[[allowlist]]
tool = "git_status"

# Denylist: second priority. Matching tool calls are denied.
[[denylist]]
tool = "shell"
pattern = "rm -rf"

[[denylist]]
tool = "shell"
pattern = "sudo"

# Require confirm: third priority. Matching tool calls require user approval.
[[require_confirm]]
tool = "shell"
pattern = "git push"

[[require_confirm]]
tool = "shell"
pattern = "git reset"
```

### Evaluation Precedence

Tool permission is resolved in this order (first match wins):

1. **Allowlist** -- if the tool (and optional pattern) matches, the call is auto-allowed.
2. **Denylist** -- if the tool (and optional pattern) matches, the call is denied.
3. **Require confirm** -- if the tool (and optional pattern) matches, the call requires user approval.
4. **Default** -- the `[defaults]` mapping from the tool's safety grade.

### Safety Grades

Each tool reports a safety grade that maps to a default permission:

| Grade | Example Tools | Default |
|-------|---------------|---------|
| `read_only` | `rg`, `read_file`, `list_dir`, `git_status`, `git_diff`, `git_log`, `artifact_read` | `allow` |
| `workspace_mutation` | `write_file`, `apply_patch` | `ask` |
| `shell_mutation` | `shell` (non-destructive), `run_test` | `ask` |
| `destructive` | `shell` (rm, sudo, git reset --hard) | `deny` |

### Pattern Matching

The optional `pattern` field in policy entries is a case-insensitive substring
match against the tool call input values. For example:

```toml
[[denylist]]
tool = "shell"
pattern = "rm -rf"
```

This denies any shell command containing `rm -rf` anywhere in its arguments.

---

## Session Files

### Location

Session event logs are stored at:

```
.mimo/sessions/<session-id>.jsonl
```

Each line is a JSON-encoded `AgentEvent` containing the event type, timestamp,
message, tool calls, observations, and context snapshots.

### Golden Sessions

Golden sessions (used for replay gate evaluation) are stored at:

```
.mimo/golden/<session-id>.jsonl
```

### Artifacts

Tool output artifacts are stored under:

```
.mimo/artifacts/<artifact-id>/
```

Each artifact directory contains `metadata.json` and payload files (e.g.,
`stdout.txt`, `stderr.txt`, `content.txt`, `pre_state.diff`).

---

## TUI Key Bindings

| Key | Mode | Action |
|-----|------|--------|
| `i` | Normal | Enter prompt input mode |
| `/` | Normal | Enter prompt input mode |
| `Enter` | Input | Submit prompt |
| `Esc` | Input | Cancel input |
| `Esc` | Help | Close help |
| `Esc` | Approval | Deny tool call |
| `Tab` | Normal | Cycle panel focus |
| `Ctrl+L` | Normal | Clear chat display |
| `Ctrl+R` | Normal | Trigger context oracle review |
| `?` | Normal | Toggle help overlay |
| `y` / `Enter` | Approval | Approve tool call |
| `n` / `Esc` | Approval | Deny tool call |
| `Up` / `Down` | Normal | Scroll focused panel |
| `PgUp` / `PgDn` | Normal | Page scroll |
| `Home` / `End` | Normal | Jump to top / bottom |
| `q` | Normal | Quit |
