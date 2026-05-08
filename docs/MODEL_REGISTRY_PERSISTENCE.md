# Model Registry Persistence

## Overview

The model registry persists model configuration to disk as TOML files so that
model changes (accepting candidates, setting defaults) survive restarts.

## File Format

Models are stored in `models.toml` with this structure:

```toml
version = 1
default = "mimo-v2.5-pro"

[[models]]
id = "mimo-v2.5-pro"
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
channel = "default"
description = "Primary MiMo model"
context_limit = 1000000
accepted = true

[[models]]
id = "mimo-v2.5-flash"
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
channel = "candidate"
description = "Fast MiMo candidate"
context_limit = 128000
accepted = false
```

## Config Precedence

Model configuration is loaded with the following precedence (highest wins):

1. **Built-in defaults** -- `DefaultRegistry()` in `internal/model/registry.go`
2. **Global config** -- `~/.mimo-tui/models.toml`
3. **Project config** -- `.mimo/models.toml` (wins on conflict)

When the same model ID appears in multiple layers, the higher-precedence layer
completely replaces the entry. The default model ID is also taken from the
highest-precedence layer that sets one.

## CLI Commands

### List models

```bash
mimo -list-models
```

Prints all registered models (merged from defaults + config files).

### Accept a candidate model

```bash
mimo -model-accept mimo-v2.5-flash \
     -golden-session <golden-id> \
     -candidate-session <candidate-id>
```

After the replay gate passes, the candidate is promoted and the updated
registry is saved to `.mimo/models.toml` in the current directory.

## File Locations

| Scope | Path | Purpose |
|-------|------|---------|
| Global | `~/.mimo-tui/models.toml` | User-wide model overrides |
| Project | `.mimo/models.toml` | Per-project model overrides |

## Key Functions

- `model.SaveToFile(path)` -- serialize a Registry to TOML
- `model.LoadFromFile(path)` -- deserialize a Registry from TOML (nil if missing)
- `config.LoadModelsConfig()` -- merge defaults + global + project configs
- `config.SaveModelsConfig(registry)` -- save to `.mimo/models.toml`
