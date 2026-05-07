# Critical Thinking Agent Contract

The agent must treat critical thinking as runtime behavior, not prose garnish.

Each significant turn records:

- goal
- known facts
- assumptions
- uncertainties
- tool plan
- context budget
- cost budget
- risk
- verification
- revise or continue decision

## Required Loop

1. Question: what assumption is driving the next action?
2. Evidence: is the assumption backed by code, tool output, docs, or inference?
3. Risk: what can fail through cost, permission, context, tests, or intent?
4. Revise: continue, change plan, call a tool, or ask the user.

## Forbidden Claims

- Do not claim completion without validation.
- Do not treat raw tool output as context-ready.
- Do not display fake MiMo attention.
- Do not trust generated skills or model updates without replay/eval evidence.
