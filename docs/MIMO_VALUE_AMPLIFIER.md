# MiMo Value Amplifier

MiMo-TUI is not a generic TUI wrapped around a model. It is a developer-facing
instrument panel for making MiMo capabilities visible, usable, and controllable.

## Amplifiers

- 1M context becomes a Context Map: Near, Anchor, and Artifact state are visible.
- Agentic RL becomes Agent Trace: goal, plan, action, observation, and revision.
- SWA/GA-inspired context strategy becomes Context Focus: the app shows evidence
  actually placed in context, never a fake attention map.
- Tool-native execution becomes Tool Cockpit: every tool result is summarized into
  observation, state delta, risk delta, and context placement.
- Streaming becomes perceived momentum: token deltas, tool progress, and cost move
  continuously so users know the agent is alive.

## Guardrails

- Do not treat 1M context as a dump truck.
- Do not put raw tool output directly into the model prompt.
- Do not display invented model internals.
- Do not auto-promote new MiMo models without replay/eval evidence.
