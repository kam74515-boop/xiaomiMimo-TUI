# MiMo Value Amplifier Development Plan

## Product Thesis

MiMo Value Amplifier is not a generic chat TUI. It is an operating surface for
MiMo-specific strengths:

- 1M context becomes a visible, governable Context Map instead of an invisible
  prompt bucket.
- Agentic behavior becomes a replayable goal, plan, action, observation, and
  revision trace.
- SWA/GA-style focus is represented only as injected evidence and tool-derived
  context, never as fake attention.
- Tool results land in artifacts first, then enter context as compact
  observations.

## Current Mainline

- Go single-binary TUI using Bubble Tea and Lip Gloss.
- OpenAI-compatible MiMo streaming client with mock fallback.
- Core event bus with Context Map, Agent Trace, Tool Cockpit, replay log, and
  artifact store.
- Built-in tools for shell, rg, file read/write, patch, git status, and tests.

## Next Development Wave

1. TUI operation surface
   - Add command entry, panel scroll, help overlay, stronger empty states, and
     better tool/artifact rendering.
   - Keep TUI honest: it displays context evidence and tool traces, not hidden
     model attention.

2. Agent loop and model protocol
   - Add first-class tool-call parsing and publishing.
   - Support OpenAI-style tool specs in chat requests.
   - Keep a bounded max-step loop and visible critical-thinking revisions.

3. Tool executor
   - Centralize permission decisions, tool start/result/observation events, and
     artifact-backed summaries.
   - Add high-leverage coding tools: list_dir, git_diff, git_log, and
     artifact_read.

4. Context and replay
   - Promote observations into Near/Anchor/Artifact items deliberately.
   - Add latest-session discovery and replay summaries so interrupted work can
     resume with a skeleton rather than starting cold.

5. Provider validation
   - Keep mock smoke deterministic.
   - Allow a separate real MiMo connectivity smoke via environment variables
     without committing credentials or raw responses.

## Validation Gates

- `gofmt -w`
- `go test ./...`
- `go vet ./...`
- `MIMO_MOCK=1 go run ./cmd/mimo -smoke`
- Optional real-provider smoke only with environment-provided credentials.

