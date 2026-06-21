---
name: tdd
description: Test-driven loop — write a failing test first, then the minimal code to pass it
triggers: test, tdd, unit test, red green, coverage
---
Test-driven development playbook.

1. Before writing implementation, add or identify a test that captures the desired behavior and currently fails for the right reason. Run it and confirm the failure.
2. Write the minimal code to make that test pass — no speculative extras.
3. Run the test. If it passes, run the surrounding suite to confirm nothing regressed.
4. Refactor only with green tests, re-running after each refactor.
5. Repeat per behavior. Keep each red→green→refactor cycle small enough to reason about. Report the exact test command and its result, never claim a test passed without running it.
