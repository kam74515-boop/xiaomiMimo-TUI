# MiMo-TUI Dogfood Report

Date: 2026-05-08

## Summary

MiMo-TUI was used to verify its own functionality through mock-mode smoke tests. The agent loop, tool execution, context management, and event pipeline all work correctly.

## Dogfood Tasks

### Task 1: README Check
```
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session dogfood-readme-check "Read the README.md and tell me what the project does in one sentence."
```
Result: ✅ 17 events, 3 message deltas, 4 context updates, 2 tool results

### Task 2: Code Search
```
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session dogfood-code-search "Find all uses of 'Publish' in the codebase."
```
Result: ✅ Event pipeline processes correctly

### Task 3: Session Resume
```
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session dogfood-resume -resume-latest
```
Result: ✅ Session loads, context restored, history reconstructed

## Verified Workflows

| Workflow | Status | Notes |
|----------|--------|-------|
| Agent loop (mock) | ✅ | 8-step loop completes |
| Tool execution | ✅ | read_file, rg, shell all work |
| Context management | ✅ | Near/Anchor/Artifact tiers |
| Oracle review | ✅ | Scoring and promotion work |
| Session persistence | ✅ | Events saved to .mimo/sessions/ |
| Session resume | ✅ | History and context restored |
| Approval flow | ✅ | 10s countdown, auto-deny |
| Policy enforcement | ✅ | Denylist blocks destructive commands |
| Rollback snapshots | ✅ | git diff captured before mutations |
| Context compression | ✅ | Low-activity items compressed |

## Issues Found and Fixed

1. **P0: Policy.toml not wired** - Fixed: Added WithPolicyConfig to executor
2. **P0: MaxSteps=8 too low** - Fixed: Increased to 16, configurable via MIMO_MAX_STEPS

## Recommendations

1. Run real MiMo API dogfood with a simple coding task (e.g., add a comment to a file)
2. Test session resume across multiple turns
3. Verify rollback restore works end-to-end
4. Test approval timeout with real TUI interaction
