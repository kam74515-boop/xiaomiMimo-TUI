# MiMo Value Amplifier TUI

A MiMo-first terminal coding agent that makes MiMo capabilities visible,
usable, and controllable.

The first build focuses on:

- Context Map for Near / Anchor / Artifact state.
- Agent Trace for goal, plan, action, observation, and revision.
- Tool Cockpit for tool-native execution and raw artifact storage.
- MiMo streaming with mock fallback when no API key is configured.

## Local Run

```sh
go run ./cmd/mimo
```

Headless smoke mode:

```sh
MIMO_MOCK=1 go run ./cmd/mimo -smoke
```

With MiMo Token Plan credentials:

```sh
export MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
export MIMO_API_KEY="..."
export MIMO_MODEL="mimo-v2.5-pro[1m]"
go run ./cmd/mimo
```

Never commit API keys. Keep credentials in environment variables or local,
ignored config files.
