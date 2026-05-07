# Agent Contracts

## Event Contract

The TUI consumes `core.AgentEvent` only. All model deltas, tool calls, tool
results, context updates, risk updates, cost updates, and terminal states must
be represented as events.

## Model Contract

`core.ModelClient` streams `core.ModelEvent`.

Model events can contain:

- `Delta`: assistant-visible text.
- `ToolCalls`: structured requests for registered tools.
- `Usage`: token usage.
- `Done`: terminal stream marker.
- `Err`: provider or parsing failure.

## Tool Contract

Tools never inject raw stdout, stderr, file contents, or diffs directly into
model context. A tool run returns:

- compact `ToolResult.Content` for the cockpit,
- raw files in `internal/artifact`,
- an `Observation` that decides whether the result belongs in Near, Anchor, or
  Artifact context.

## Context Contract

The Context Map is a user-facing memory budget, not a dump of everything the
agent has seen. Context placement must explain why an item is present and where
it came from.

## Critical Thinking Contract

The agent must expose uncertainty, assumptions, verification status, and
revision decisions as trace state. It must not claim hidden chain-of-thought,
secret attention, or tool execution that did not happen.

