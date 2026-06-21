---
name: review
description: Self-review a diff for correctness, simplicity, and honesty before declaring done
triggers: review, check, audit, lgtm, before commit, self review
---
Code-review playbook — apply to your own diff before finishing.

1. Re-read the full diff as if you did not write it. For each hunk ask: what breaks if this is wrong?
2. Correctness: off-by-one, nil/empty, error paths, concurrency (shared state, ordering), and edge inputs. Trace at least one realistic failing scenario per risky change.
3. Reuse and simplicity: is there existing code that already does this? Can the change be smaller or clearer with the same behavior?
4. Consistency: does it match surrounding naming, comment density, and idioms? Remove dead code and stale comments you introduced.
5. Honesty: did you actually run the build and tests? Report failures with their output; do not soften or assume. State residual risk plainly.
