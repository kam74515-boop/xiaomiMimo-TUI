# LSP Diagnostics Tool

## Overview

The `diagnostics` tool lets the MiMo-TUI agent detect compile errors, type errors,
and lint issues after code modifications. It runs language-specific toolchains,
parses their output into structured diagnostics, and produces a compressed
observation suitable for the agent's context window.

## How It Works

1. The agent calls `diagnostics` with a `language` parameter (defaults to `"go"`).
2. For Go, the tool runs `go vet ./...` and `go build ./...` in the project root.
3. Stderr output from both commands is merged and parsed into structured
   `{file, line, column, severity, message}` records.
4. Raw output is stored as an artifact (via `artifact.Store`) so it can be
   retrieved later for detailed inspection.
5. The tool returns a compressed summary: `"Diagnostics: N errors, N warnings (language: go)"`.

## Go Diagnostics (MVP)

### Commands Executed

- `go vet ./...` -- static analysis for common mistakes (printf directives, unreachable code, etc.)
- `go build ./...` -- compile check for type errors, undefined references, syntax errors

### Output Parsing

Go toolchains emit diagnostics in the format:

```
file:line:col: message
file:line: message       (some tools omit column)
```

The parser handles both formats and classifies severity:

- **error**: build failures -- undefined symbols, type mismatches, syntax errors
- **warning**: vet findings -- printf issues, unreachable code, unused results

### Deduplication

Repeated diagnostic messages (same file, line, column, and message) are
deduplicated to keep the observation compact.

### Sorting

Issues are sorted by file path, then line number, then column for deterministic
output across runs.

## Artifact Storage

Raw diagnostic output is stored as an artifact with:

- `kind`: `"diagnostics"`
- `tool`: `"diagnostics"`
- Payload: `diagnostics.txt` containing the merged stderr from go vet and go build

The artifact ID is included in the `ToolResult` so the agent can retrieve the
full output later via `artifact_read`.

## Observation Compression

The observation placed into the agent's context is intentionally terse:

```
Diagnostics: 3 errors, 2 warnings (language: go)
```

This keeps context usage minimal while giving the agent a clear signal about
project health. The agent can use `artifact_read` to get the full diagnostic
details when needed.

## Future Languages

### Node.js (placeholder)

Will run `npx tsc --noEmit` (TypeScript) or `eslint .` (JavaScript) depending
on project configuration. Currently returns a placeholder message.

### Python (placeholder)

Will run `mypy .` or `pylint` depending on project configuration. Currently
returns a placeholder message.

## Usage

```json
{
  "language": "go",
  "project_path": "/path/to/project"
}
```

Both parameters are optional:
- `language` defaults to `"go"`
- `project_path` defaults to the workspace root

## Safety

The diagnostics tool is **read-only** (`SafetyReadOnly`) and **always allowed**
(`PermissionAllow`). It does not modify any files.

## Testing

Tests cover:

- Go diagnostics parser with sample `go vet` and `go build` output
- Deduplication of repeated messages
- Severity classification (error vs warning)
- Sorting of issues
- Empty/noise line handling
- Tool name, schema, safety, and permission verification
- Language placeholder behavior (node, python)
- JSON round-trip serialization of `DiagnosticsResult`
