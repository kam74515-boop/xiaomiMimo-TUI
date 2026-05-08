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
| `internal/provider/mimo/edge_case_test.go` | Provider edge case tests (empty/malformed/timeout) |

## E2E Tasks

| Task | Prompt | Min Tool Calls | Max Steps | Timeout |
|------|--------|----------------|-----------|---------|
| `simple_question` | "What is Go?" | 0 | 4 | 60s |
| `read_file` | "Read go.mod..." | 1 | 6 | 90s |
| `search_code` | "Search for 'func New'..." | 1 | 6 | 90s |
| `generate_patch` | "Create e2e_marker.txt..." | 1 | 6 | 90s |
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

When `MIMO_API_KEY` is not set (and no API key is passed in `E2EConfig`), `RunE2E` returns `nil, nil` -- no error, no results. This allows CI pipelines to conditionally run E2E tests without failure.

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
# Run E2E tests against the real API (requires MIMO_API_KEY)
MIMO_API_KEY="your-key" go test ./internal/eval/benchmark/ -run TestRunE2E -v -timeout 10m

# Run with custom base URL
MIMO_API_KEY="your-key" MIMO_BASE_URL="https://custom.api.com/v1" go test ./internal/eval/benchmark/ -run TestRunE2E -v

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
