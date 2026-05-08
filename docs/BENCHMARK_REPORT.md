# MiMo-TUI Benchmark Report

Date: 2026-05-08

## Summary

MiMo-TUI has been validated through mock smoke tests and real MiMo API connectivity tests. The benchmark harness (`internal/eval/benchmark/`) provides automated task execution with structured result capture.

## Test Results

### Mock Smoke Test
```
MIMO_MOCK=1 go run ./cmd/mimo -smoke -session smoke-final
smoke ok: events=16 message_delta=3 context_update=3 trace_update=2 tool_result=2 observation=2
```

### Real MiMo API Test
```
MIMO_BASE_URL="https://token-plan-cn.xiaomimimo.com/v1"
MIMO_MODEL="mimo-v2.5-pro"
```
- API connectivity: ✅ verified
- Tool call format: ✅ OpenAI-compatible
- Streaming: ✅ SSE working
- MaxSteps: ✅ increased from 8 to 16 (configurable via MIMO_MAX_STEPS)

### Unit Test Coverage
```
15/15 packages pass
gofmt: clean
go vet: clean
```

## Benchmark Tasks Defined

| Task | Description | Expected Tools | Max Steps |
|------|-------------|----------------|-----------|
| readme-edit | Read README, propose improvement | read_file, write_file | 5 |
| unit-test | Find function, add test | rg, read_file, write_file, run_test | 6 |
| code-search | Search codebase, explain (no modify) | rg, read_file | 4 |
| safety-check | Attempt destructive command (should block) | shell | 2 |
| context-explore | Read multiple files, build understanding | read_file, rg | 5 |

## Benchmark Harness

Location: `internal/eval/benchmark/`

Files:
- `benchmark.go` - Core runner using `agent.Loop()`
- `tasks.go` - Task definitions with validation
- `report.go` - Markdown report generation
- `benchmark_test.go` - Mock-based tests

Run benchmarks:
```bash
go test ./internal/eval/benchmark -v
```

## Known Limitations

1. Real MiMo API benchmark requires interactive approval for mutating tools
2. MaxSteps=16 may still be insufficient for very complex tasks
3. Benchmark harness uses mock approval (auto-approve) for automated runs

## Recommendations

1. Increase MaxSteps to 24 for production use
2. Add timeout configuration per task
3. Implement golden session recording for regression testing
4. Add token cost tracking to benchmark results
