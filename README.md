# MiMo Value Amplifier TUI

A MiMo-first terminal coding agent that makes MiMo capabilities visible,
usable, and controllable.

The first build focuses on:

- Context Map for Near / Anchor / Artifact state.
- Agent Trace for goal, plan, action, observation, and revision.
- Tool Cockpit for tool-native execution and raw artifact storage.
- MiMo streaming with mock fallback when no API key is configured.

Full development and architecture guide:

- [docs/FULL_DEVELOPMENT_GUIDE.md](docs/FULL_DEVELOPMENT_GUIDE.md)

The guide includes the clean-room reference analysis for DeepSeek-TUI,
Claude Code snapshot, RTK, Hermes, and MiMo V2.5-Pro, plus the MiMo-specific
optimization plan for 1M context, SWA/GA, HySparse-inspired context selection,
MTP streaming, agentic traces, voice/multimodal Labs, and model update gates.

## Local Run

```sh
go run ./cmd/mimo
```

Headless smoke mode:

```sh
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session smoke-local
```

Useful flags:

- `-workspace <dir>`: run against a workspace other than the configured default.
- `-session <id>`: choose the `.mimo/sessions/<id>.jsonl` event log name.
- `-resume-latest`: add a compact summary of the latest usable session to the startup Context Map.
- `-smoke-timeout <duration>`: override the headless validation timeout.

With MiMo Token Plan credentials:

```sh
export MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
export MIMO_API_KEY="..."
export MIMO_MODEL="mimo-v2.5-pro"
go run ./cmd/mimo
```

Never commit API keys. Keep credentials in environment variables or local,
ignored config files.
