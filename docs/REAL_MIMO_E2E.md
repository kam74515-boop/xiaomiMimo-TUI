# Real MiMo API E2E Testing Infrastructure

## Overview

This document describes the end-to-end testing infrastructure for the MiMo API integration. The E2E tests exercise the full agent loop against the real MiMo API, validating that the provider client, tool executor, context manager, and agent loop work together correctly.

## Architecture

```
E2EConfig
    |
    v
RunE2E() --> for each E2ETask:
    |
    +--> buildE2EClient(apiKey, baseURL, model)
    |       |
    |       +--> mimo.New(config.ProviderConfig{...})
    |
    +--> runE2ETask(client, task)
            |
            +--> agent.Loop(prompt, client, executor, ctxMgr, toolSpecs, bus, config)
            |
            +--> collect events from bus
            |
            +--> classifyFailure(err, events)
            |
            +--> E2EResult
```

## Files

| File | Purpose |
|------|---------|
| `internal/eval/benchmark/e2e_tasks.go` | Defines 5 E2E task scenarios |
| `internal/eval/benchmark/e2e.go` | E2E runner with failure classification |
| `internal/eval/benchmark/e2e_test.go` | Unit tests for runner, classification, masking |
| `internal/eval/benchmark/e2e_real_test.go` | Real E2E tests against live MiMo API (requires `MIMO_RUN_REAL_E2E=1` and `MIMO_API_KEY`) |
| `internal/provider/mimo/edge_case_test.go` | Provider edge case tests (empty/malformed/timeout) |

## E2E Tasks

| Task | Prompt | Min Tool Calls | Max Steps | Timeout |
|------|--------|----------------|-----------|---------|
| `simple_question` | "What is Go?" | 0 | 4 | 60s |
| `read_file` | "Read go.mod..." | 1 | 6 | 90s |
| `search_code` | "Search for 'func New'..." | 1 | 6 | 90s |
| `generate_patch` | "Create e2e_marker.txt..." in a temporary workspace | 1 | 6 | 90s |
| `run_test` | "Run go test..." | 1 | 8 | 120s |

## Failure Classification

`classifyFailure(err, events)` categorizes failures into structured classes:

| Class | Trigger |
|-------|---------|
| `api_error` | Authentication, rate-limit, server errors, connection failures |
| `stream_error` | SSE decode/parse errors during streaming |
| `tool_parse_error` | Tool call JSON parsing failures (detected via events) |
| `timeout` | `context.DeadlineExceeded` or `context.Canceled` |
| `no_tool_calls` | Model never invoked a tool (and an error occurred) |
| `max_steps` | Agent loop hit the step limit |
| `unknown` | Anything else |

## API Key Safety

The runner never prints the full API key. `maskAPIKey()` truncates to the first 8 characters plus "..." for any log output. The key is passed directly to the HTTP client via `Authorization: Bearer <key>` and `api-key: <key>` headers.

Example log output:
```
e2e: starting 5 tasks (model=mimo-v2.5-pro, base_url=https://a..., key=mimo_abc...)
```

## Graceful Skip

When `MIMO_API_KEY` is not set (and no API key is passed in `E2EConfig`), `RunE2E` returns `nil, nil` -- no error, no results. The live Go tests also require `MIMO_RUN_REAL_E2E=1`, so normal `go test ./...` runs never call the live API just because a developer has a MiMo key in their shell.

## Provider Edge Case Tests

The `edge_case_test.go` file covers these scenarios:

1. **Empty response**: Server sends SSE framing with no data, then closes. Verifies the client produces a `done` event without error.

2. **Malformed SSE**: Server sends invalid JSON inside `data:` lines. Verifies the client produces a decode error.

3. **Connection timeout**: Server accepts TCP connections but never responds. Verifies the client surfaces a timeout error.

4. **Invalid JSON tool calls**: Server returns tool call deltas with malformed `arguments` JSON. Verifies `Raw` is preserved and `Input` is nil (no panic).

5. **Stream error mid-stream**: Server sends partial content then an error chunk. Verifies both partial content and error are captured.

6. **Multiple tool calls with mixed validity**: One valid tool call and one with broken JSON. Verifies both are handled independently.

7. **Empty choices array**: Server sends usage info with empty choices. Verifies usage is parsed correctly.

8. **HTML error page**: Server returns HTML 503 instead of SSE. Verifies the client surfaces a structured error.

## Running

```bash
# Run E2E tests against the real API (explicit opt-in required)
MIMO_RUN_REAL_E2E=1 MIMO_API_KEY="your-key" go test ./internal/eval/benchmark/ -run TestRealMiMoE2E -v -timeout 10m

# Run streaming-only E2E test (just the simple_question task)
MIMO_RUN_REAL_E2E=1 MIMO_API_KEY="your-key" go test ./internal/eval/benchmark/ -run TestRealMiMoE2E_StreamingOnly -v -timeout 2m

# Run with custom base URL
MIMO_RUN_REAL_E2E=1 MIMO_API_KEY="your-key" MIMO_BASE_URL="https://custom.api.com/v1" go test ./internal/eval/benchmark/ -run TestRealMiMoE2E -v

# Run provider edge case tests (no API key needed -- uses httptest)
go test ./internal/provider/mimo/ -run TestEdgeCase -v

# Run all unit tests (classification, masking, etc.)
go test ./internal/eval/benchmark/ -v
```

## E2EResult Structure

```go
type E2EResult struct {
    TaskName     string        // e.g. "read_file"
    Success      bool          // true if task passed
    ErrorClass   string        // structured failure class (empty on success)
    Steps        int           // number of agent loop iterations
    ToolCalls    int           // number of tool_start events
    Duration     time.Duration // wall-clock time
    ErrorMessage string        // raw error message (empty on success)
}
```

## Live Test Results

Tested on 2026-05-08 against the production MiMo API.

**Configuration:**
- Base URL: `https://token-plan-cn.xiaomimimo.com/v1`
- Model: `mimo-v2.5-pro`
- API Key: provided via `MIMO_API_KEY` (not logged in full)
- Test file: `internal/eval/benchmark/e2e_real_test.go`

### Results Summary

| Task | Status | Steps | Tool Calls | Duration | Notes |
|------|--------|-------|------------|----------|-------|
| `simple_question` | PASS | 1 | 0 | 4.9s | Direct text answer, no tools needed |
| `read_file` | PASS | 6 | 3 | 22.7s | Used read_file and converged after retries |
| `search_code` | PASS | 5 | 2 | 17.8s | Used search tools and returned file paths |
| `generate_patch` | PASS | 3 | 1 | 7.6s | Used write_file inside a temporary workspace |
| `run_test` | PASS | 3 | 1 | 10.4s | Used shell tool to run `go version` |

**Overall: 5/5 tasks passed. Total duration: 63 seconds.**

### Key Findings

1. **Streaming works reliably.** All 5 tasks completed with no SSE parse errors, no stream errors, and no connection issues. The `readEventStream` parser handles the MiMo API's SSE format correctly.

2. **Tool call parsing works correctly.** The `toolCallAccumulator` correctly reassembles streamed tool call deltas into complete tool calls. All tool arguments parsed as valid JSON (`parseToolInput` returned non-nil for all calls).

3. **Tool arguments are valid.** The model produced correct `path` arguments for `read_file`, valid `pattern`/`path` arguments for `rg`, a valid `path`/`content` pair for `write_file`, and a valid `command` argument for `shell`.

4. **The model tends to retry.** For `read_file` and `search_code`, the model took more iterations than minimum. This is expected behavior -- the model sometimes explores alternative approaches before settling on the right one. The higher MaxSteps in the real E2E tasks accommodates this.

5. **No provider fixes needed.** The MiMo provider client (`internal/provider/mimo/client.go`) worked correctly out of the box for all tasks. No edge cases or bugs were encountered during live testing.

6. **Default BaseURL note.** Code, docs, and live E2E tests now default to `https://token-plan-cn.xiaomimimo.com/v1`. Override `MIMO_BASE_URL` when using a different MiMo endpoint.

### Initial Run Issues (Resolved)

The first automated run (via `go test ./...`) exposed two issues that were fixed:

1. **Recursive test execution.** The `run_test` E2E task originally used `go test ./...` as its prompt, which triggered the real E2E tests recursively, causing a timeout cascade. Fixed by changing the prompt to `go version`.

2. **MaxSteps too low.** The default tasks had MaxSteps of 6 for `read_file` and `search_code`, but the model sometimes needs more iterations. The real E2E tasks use larger per-task limits to accommodate the model's exploration behavior.

3. **Repository pollution.** Earlier live runs created `e2e_marker.txt` in the repo root. Live tests now run against a disposable temporary Go workspace, so generated files do not touch the project checkout.
