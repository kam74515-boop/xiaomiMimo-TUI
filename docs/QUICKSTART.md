# Quickstart

Get MiMo-TUI running in under 5 minutes.

---

## Prerequisites

- Go 1.21 or later
- Git (for workspace operations and rollback)
- ripgrep (`rg`) for the search tool

---

## 1. Build

```sh
git clone <repo-url>
cd mimo-TUI
go build -o mimo ./cmd/mimo
```

Or run directly without building:

```sh
go run ./cmd/mimo
```

---

## 2. Run in Mock Mode (No API Key)

Mock mode runs the full agent loop with simulated MiMo responses. No API key required.

```sh
MIMO_MOCK=1 go run ./cmd/mimo
```

This launches the 4-panel TUI:
- **Context Map** (top-left): shows Near/Anchor/Artifact tiers
- **Chat Stream** (top-right): conversation with MiMo
- **Agent Trace** (bottom-left): goal/plan/action/observation steps
- **Tool Cockpit** (bottom-right): tool results and artifacts

Headless smoke test (no TUI):

```sh
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session smoke-local
```

---

## 3. Run with Real MiMo

Set your credentials as environment variables:

```sh
export MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
export MIMO_API_KEY="your-api-key-here"
export MIMO_MODEL="mimo-v2.5-pro"
```

Then launch:

```sh
go run ./cmd/mimo
```

Or pass a prompt directly:

```sh
go run ./cmd/mimo "Explain the context oracle architecture"
```

**Never commit API keys.** Keep them in environment variables or local config files.

---

## 4. TUI Controls

| Key | Action |
|-----|--------|
| `i` or `/` | Enter prompt input mode |
| `Enter` | Submit prompt |
| `Esc` | Cancel input, close help, dismiss approval |
| `Tab` | Switch panel focus |
| `Ctrl+L` | Clear chat display |
| `Ctrl+R` | Request context oracle review |
| `?` | Toggle help overlay |
| `Up/Down` | Scroll within focused panel |
| `PgUp/PgDn` | Page scroll |
| `Home/End` | Jump to top/bottom |
| `q` or `Ctrl+C` | Quit |
| `Ctrl+G` | Interrupt running agent |

When a tool requests approval:
- `y` or `Enter` to approve
- `n` or `Esc` to deny

---

## 5. Configuration

### Config File

MiMo-TUI loads configuration from (in order, first found wins):

1. `~/.mimo-tui/config.toml` (user-global)
2. `.mimo/config.toml` (project-local)

Example `config.toml`:

```toml
[provider]
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
api_key = ""       # prefer MIMO_API_KEY env var
model = "mimo-v2.5-pro"
mock = false

[runtime]
workspace = "."
context_window = 1000000
```

### Environment Variables

All environment variables override config file values:

| Variable | Default | Description |
|----------|---------|-------------|
| `MIMO_BASE_URL` | `https://token-plan-cn.xiaomimimo.com/v1` | MiMo API endpoint |
| `MIMO_API_KEY` | (empty) | API key; if empty, mock mode activates |
| `MIMO_MODEL` | `mimo-v2.5-pro` | Model ID |
| `MIMO_MOCK` | (unset) | Set to `1`/`true`/`yes` to force mock mode |
| `MIMO_LABS` | (unset) | Set to `1`/`true`/`yes` to unlock labs-channel models |
| `MIMO_MAX_STEPS` | (unset) | Override max agent loop steps |

### Policy File

Create `.mimo/policy.toml` to control tool permissions:

```toml
approval_timeout = 30  # seconds

[defaults]
read_only = "allow"
workspace_mutation = "ask"
shell_mutation = "ask"
destructive = "deny"

[[allowlist]]
tool = "rg"

[[allowlist]]
tool = "read_file"

[[denylist]]
tool = "shell"
pattern = "rm -rf"

[[require_confirm]]
tool = "shell"
pattern = "git push"
```

See [CONFIGURATION.md](CONFIGURATION.md) for the complete reference.

---

## 6. Session Management

### Session Logs

Every run creates a JSONL event log at `.mimo/sessions/<session-id>.jsonl`.

Default session ID is the current timestamp (e.g., `20260508T143022`).

Choose a custom session ID:

```sh
go run ./cmd/mimo -session my-feature-work
```

### Resume a Session

Resume the most recent session with context restoration:

```sh
go run ./cmd/mimo -resume-latest
```

This injects a compact summary of the previous session into the startup context,
including context items, trace updates, and recent conversation history.

### Evaluate a Session

Extract trajectory info from a session:

```sh
go run ./cmd/mimo -eval
go run ./cmd/mimo -eval-session my-feature-work
```

---

## 7. Rollback

If a tool mutates your workspace and you want to undo:

```sh
# List available rollback snapshots
go run ./cmd/mimo -rollback-list

# Preview what a rollback will restore
go run ./cmd/mimo -rollback-show <artifact-id>

# Dry-run (default)
go run ./cmd/mimo -rollback-apply <artifact-id>

# Actually apply the rollback
go run ./cmd/mimo -rollback-apply <artifact-id> -rollback-confirm
```

---

## 8. Model Management

List registered models:

```sh
go run ./cmd/mimo -list-models
```

Accept a candidate model after replay gate evaluation:

```sh
go run ./cmd/mimo \
  -golden-session <golden-id> \
  -candidate-session <candidate-id> \
  -model-accept <model-id>
```

Unlock labs-channel models:

```sh
MIMO_LABS=1 go run ./cmd/mimo -list-models
```

---

## Next Steps

- Read [CONFIGURATION.md](CONFIGURATION.md) for the full configuration reference.
- Read [KNOWN_LIMITATIONS.md](KNOWN_LIMITATIONS.md) to understand current constraints.
- Read [../README.md](../README.md) for architecture details and the development guide.
