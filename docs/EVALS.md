# Evaluation Plan

The first eval suite is small and biased toward failures that matter for a
tool-native coding agent.

## MVP Gates

- TUI starts with no API key by using mock provider mode.
- MiMo streaming works when credentials are present.
- At least five tools execute through the registry.
- Raw tool output is persisted as artifacts.
- Context Map shows Near, Anchor, and Artifact items.
- Agent Trace shows a full goal-plan-action-observation-revision loop.
- Session event logs can be replayed.

## Regression Tracks

- Long-task reliability: multi-file edit, test failure, repair, re-test.
- Context discipline: large outputs become artifacts plus observations.
- Cost discipline: compressed observations are shorter than raw outputs.
- Model updates: replay saved traces before promoting a candidate MiMo model.
- Safety: mutating tools require explicit permission unless a policy allows them.
