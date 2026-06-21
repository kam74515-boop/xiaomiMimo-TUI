---
name: debug
description: Diagnose a bug by forming and testing hypotheses before changing code
triggers: debug, bug, broken, failing, error, crash, why does, regression
---
Debugging playbook — diagnose before you patch.

1. Reproduce first. Establish the smallest reliable way to trigger the failure and capture the exact error/output as evidence.
2. Localize: use the stack trace, recent changes, and targeted reading to narrow to a suspect region. State what you expect vs what you observe.
3. Form a specific, falsifiable hypothesis ("X is nil because Y returns early"). Test it with the cheapest possible probe before editing.
4. Fix the root cause, not the symptom. Avoid speculative shotgun changes; change one thing at a time.
5. Verify the original reproduction now passes and add a regression test that would have caught it. Re-run the broader suite.
