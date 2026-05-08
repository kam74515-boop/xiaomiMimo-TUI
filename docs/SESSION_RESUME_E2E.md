# Session Resume E2E Report

## Overview

End-to-end tests proving that session resume works correctly across realistic scenarios. Tests exercise `BuildResumeSummary` and `ExtractHistory` from `internal/session/summary.go` through six scenarios.

## Test Scenarios

### 1. Basic Resume (`TestE2EBasicResume`)

Single-turn session with user prompt, agent response, one tool call (read_file), context update, and trace update.

**Verified:**
- Event counts captured correctly (user_prompt=1, tool_start=1, tool_result=1, done=1)
- LatestContext preserves tier ordering (near, anchor) and pinned state
- RecentTraceUpdates captures stage (inspect) and status (done)
- ArtifactIDs deduplicates and captures "art-1"
- ExtractHistory reconstructs: system + user + assistant + tool messages
- Tool message carries correct ToolCallID linkage

### 2. Multi-Turn Resume (`TestE2EMultiTurnResume`)

3 turns, each with 2 tool calls (read_file, write_file). Uses `BuildTestEvents(3, 2)` helper.

**Verified:**
- 3 user prompts, 3 agent starts, 6 tool starts, 6 tool results, 3 dones
- ExtractHistory produces: system + 3 user + 3+ assistant + 6 tool messages
- Tool messages carry correct ToolCallIDs linking back to tool_start events
- Consecutive tool results (without intermediate deltas) are handled correctly
- Last tool call ID references the final turn's last tool

### 3. Interrupted Recovery (`TestE2EInterruptedRecovery`)

Session ending with `EventError` instead of `EventDone` (simulates crash/panic).

**Verified:**
- LastStatus = "error" (not "done")
- LastError captures the error message from EventError
- TraceFailed status and stage (test) preserved in RecentTraceUpdates
- Trace observation ("panic in TestFoo") captured
- LatestContext preserved from the interrupted session
- ExtractHistory reconstructs partial conversation (user + assistant + tool)

### 4. Context Restoration (`TestE2EContextRestoration`)

Two context updates: first with near+anchor, second adding artifact tier and a pinned item.

**Verified:**
- LatestContext is the second (most recent) update, not the first
- All three tiers present: near(2), anchor(1), artifact(1)
- Pinned item ("near:main.go") survives with reason "user selection"
- PollutionRisk preserved from context snapshot
- Context is cloned (not a pointer to the original event data)

### 5. Artifact References (`TestE2EArtifactReferences`)

Multiple observation events with overlapping artifact IDs.

**Verified:**
- ArtifactIDs deduplicates: "art-foo-test" appears in 3 events but listed once
- "art-bar-test" captured from second tool result
- Empty artifact IDs are ignored
- LastToolResults captures the last 5 tool result summaries

### 6. Trace Continuity (`TestE2ETraceContinuity`)

Four trace updates across stages: inspect -> plan -> patch -> test.

**Verified:**
- All 4 trace updates preserved in order (under default limit of 5)
- Each trace preserves: ID, Goal, Status, Stage
- LastStage = "test" (the final stage)
- LastStatus = "done" (from the EventDone after traces)

## Helper Function

`BuildTestEvents(turns, toolsPerTurn int) []core.AgentEvent` in `resume_e2e.go` generates realistic event sequences. Tested with 6 parameter combinations including edge cases (0 turns, 0 tools).

## Benchmark

`RunResumeBenchmark(eventCount)` in `internal/eval/benchmark/resume_benchmark.go` measures:
- `ExtractHistory` performance on N synthetic events
- `BuildResumeSummary` performance on N synthetic events

Event generation uses `GenerateResumeEvents(n)` which produces chronologically ordered events with realistic type distribution (~14 events per turn).

## Files Created

| File | Purpose |
|------|---------|
| `internal/session/resume_e2e.go` | `BuildTestEvents` helper |
| `internal/session/resume_e2e_test.go` | 7 E2E tests (6 scenarios + helper test) |
| `internal/eval/benchmark/resume_benchmark.go` | `GenerateResumeEvents`, `RunResumeBenchmark`, `FormatResumeReport` |
| `internal/eval/benchmark/resume_benchmark_test.go` | 5 benchmark tests + 2 Go benchmarks |

## Validation Results

```
go build ./...     -- PASS
go test ./...      -- PASS (all packages)
go vet ./...       -- PASS
gofmt -l .         -- clean
```
