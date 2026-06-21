---
name: plan
description: Turn a fuzzy request into a small, ordered, verifiable plan before editing
triggers: plan, design, approach, how should, spec, break down
---
Planning playbook — apply before making changes.

1. Restate the goal in one sentence and list the explicit acceptance criteria. If the request is ambiguous, state the assumption you are proceeding on.
2. Map the territory first: identify the files, functions, and data flows the change touches. Prefer reading over guessing.
3. Decompose into the smallest ordered steps that each leave the project building and testable. Call out the riskiest step and how you will de-risk it.
4. Name what could go wrong (edge cases, backwards compatibility, concurrency) and how each step's verification catches it.
5. Present the plan as a short numbered list before editing. Keep raw exploration output in observations, not the plan itself.
