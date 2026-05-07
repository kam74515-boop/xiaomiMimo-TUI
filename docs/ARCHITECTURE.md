# Architecture

The first implementation is a Go single-binary TUI.

## Packages

- `cmd/mimo`: CLI entrypoint.
- `internal/tui`: Bubble Tea views for chat, context map, trace, and tools.
- `internal/provider/mimo`: MiMo OpenAI-compatible streaming client and mock.
- `internal/agent`: agent loop and critical thinking policy.
- `internal/context`: Near/Anchor/Artifact context manager.
- `internal/tools`: tool registry and built-ins.
- `internal/artifact`: raw output persistence.
- `internal/replay`: event replay.
- `internal/config`: user and project config.

## Event Flow

User input enters the agent loop. The agent builds a context frame, calls the
model stream, executes tools, stores raw outputs as artifacts, emits observations,
updates context, and publishes events to the TUI.

The event log is a product surface, not only debugging infrastructure.
