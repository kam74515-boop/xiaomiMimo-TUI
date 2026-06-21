---
name: verify
description: Prove a change works by building, testing, and observing real behavior before claiming done
triggers: verify, validate, confirm, does it work, prove, gate
---
Verification playbook — apply before declaring a task complete.

1. Build the project. A change is not done if it does not compile.
2. Run the relevant tests (and a focused subset for the changed area). Capture the actual command and result.
3. Exercise the real behavior where feasible — run the tool/app and observe the outcome, not just the unit tests.
4. Check the change against each acceptance criterion from the plan. Note anything unverified explicitly rather than implying full coverage.
5. Summarize what was verified, how, and any residual risk. Report failures faithfully with their output instead of asserting success.
