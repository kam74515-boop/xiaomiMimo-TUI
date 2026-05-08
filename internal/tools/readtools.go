package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

const (
	defaultListEntries       = 200
	maxListEntries           = 1000
	defaultGitLogLimit       = 20
	maxGitLogLimit           = 100
	artifactSmallPayloadSize = 4 * 1024
	artifactPreviewChars     = 360
	artifactPreviewLines     = 3
)

type listDirTool struct {
	baseTool
}

func NewListDirTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return listDirTool{baseTool: newBase("list_dir", workspace, store, s)}
}

func (t listDirTool) Schema() core.JSONSchema {
	return objectSchema("List direct directory entries.", map[string]any{
		"path":        stringSchema("Optional directory path. Defaults to the workspace root."),
		"max_entries": core.JSONSchema{"type": "integer", "description": "Maximum entries to include in the artifact."},
	})
}

func (t listDirTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "list_dir only reads directory metadata"}
}

func (t listDirTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	path := resolveToolPath(t.workspace, defaultStringInput(input, "path", "."))
	entries, err := os.ReadDir(path)
	if err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error()}
	}

	limit := intInput(input, "max_entries", defaultListEntries, 1, maxListEntries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var listing strings.Builder
	dirs := 0
	files := 0
	written := 0
	for _, entry := range entries {
		if written >= limit {
			break
		}
		info, infoErr := entry.Info()
		kind := "other"
		size := int64(0)
		if infoErr == nil {
			size = info.Size()
		}
		if entry.IsDir() {
			kind = "dir"
			dirs++
		} else if entry.Type().IsRegular() {
			kind = "file"
			files++
		}
		fmt.Fprintf(&listing, "%s\t%d\t%s\n", kind, size, entry.Name())
		written++
	}

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "directory",
		ExitCode: 0,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{{Name: "listing.txt", Data: []byte(listing.String())}},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}

	content := fmt.Sprintf("listed %d entries in %s (%d dirs, %d files; saved %d); artifact=%s", len(entries), displayPath(t.workspace, path), dirs, files, written, artifactID)
	if len(entries) > written {
		content = fmt.Sprintf("%s; %d entries omitted by max_entries", content, len(entries)-written)
	}
	return core.ToolResult{Content: content, ExitCode: 0, ArtifactID: artifactID}
}

func (t listDirTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t listDirTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("list_dir", result, core.TierArtifact)
}

type gitDiffTool struct {
	baseTool
}

func NewGitDiffTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return gitDiffTool{baseTool: newBase("git_diff", workspace, store, s)}
}

func (t gitDiffTool) Schema() core.JSONSchema {
	return objectSchema("Read git diff output.", map[string]any{
		"path":   stringSchema("Optional pathspec to diff."),
		"ref":    stringSchema("Optional ref or range to diff against."),
		"staged": core.JSONSchema{"type": "boolean", "description": "Read staged changes instead of unstaged changes."},
	})
}

func (t gitDiffTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "git_diff only reads repository state"}
}

func (t gitDiffTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	args := []string{"git", "diff", "--no-color"}
	if boolInput(input, "staged") {
		args = append(args, "--staged")
	}
	if ref := strings.TrimSpace(stringInput(input, "ref")); ref != "" {
		args = append(args, ref)
	}
	if path := strings.TrimSpace(stringInput(input, "path")); path != "" {
		args = append(args, "--", path)
	}
	return t.runGitCommand(ctx, input, args, func(stdout string, code int, artifactID string) string {
		return fmt.Sprintf("git diff exited %d with %d files and %d hunks; artifact=%s", code, countDiffFiles(stdout), countDiffHunks(stdout), artifactID)
	})
}

func (t gitDiffTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t gitDiffTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("git_diff", result, core.TierArtifact)
}

type gitLogTool struct {
	baseTool
}

func NewGitLogTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return gitLogTool{baseTool: newBase("git_log", workspace, store, s)}
}

func (t gitLogTool) Schema() core.JSONSchema {
	return objectSchema("Read recent git commit history.", map[string]any{
		"limit": core.JSONSchema{"type": "integer", "description": "Maximum commits to include in the artifact."},
		"path":  stringSchema("Optional pathspec to filter commit history."),
	})
}

func (t gitLogTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "git_log only reads repository state"}
}

func (t gitLogTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	limit := intInput(input, "limit", defaultGitLogLimit, 1, maxGitLogLimit)
	args := []string{"git", "log", "--date=short", "--pretty=format:%h%x09%ad%x09%an%x09%s", "--max-count=" + strconv.Itoa(limit)}
	if path := strings.TrimSpace(stringInput(input, "path")); path != "" {
		args = append(args, "--", path)
	}
	return t.runGitCommand(ctx, input, args, func(stdout string, code int, artifactID string) string {
		return fmt.Sprintf("git log exited %d with %d commits; artifact=%s", code, countNonEmptyLines(stdout), artifactID)
	})
}

func (t gitLogTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t gitLogTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("git_log", result, core.TierArtifact)
}

type artifactReadTool struct {
	baseTool
}

func NewArtifactReadTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return artifactReadTool{baseTool: newBase("artifact_read", workspace, store, s)}
}

func (t artifactReadTool) Schema() core.JSONSchema {
	return objectSchema("Summarize files stored in an artifact.", map[string]any{
		"artifact_id": stringSchema("Artifact id to read."),
		"file":        stringSchema("Optional artifact file name or path to summarize."),
	})
}

func (t artifactReadTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "artifact_read only reads artifact files"}
}

func (t artifactReadTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	if t.store == nil {
		return core.ToolResult{ExitCode: 1, Error: "artifact store is nil"}
	}
	artifactID := strings.TrimSpace(stringInput(input, "artifact_id"))
	if artifactID == "" {
		artifactID = strings.TrimSpace(stringInput(input, "id"))
	}
	if artifactID == "" {
		return core.ToolResult{ExitCode: 2, Error: "artifact_id is required"}
	}

	meta, err := t.store.ReadMetadata(artifactID)
	if err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error(), ArtifactID: artifactID}
	}
	payloads, err := t.store.ReadPayloads(artifactID)
	if err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error(), ArtifactID: artifactID}
	}
	payloads = filterArtifactPayloads(payloads, strings.TrimSpace(stringInput(input, "file")))
	if len(payloads) == 0 {
		return core.ToolResult{ExitCode: 1, Error: "artifact file not found", ArtifactID: artifactID}
	}

	var summary strings.Builder
	largeFiles := 0
	fmt.Fprintf(&summary, "artifact %s: tool=%s kind=%s exit=%d files=%d", artifactID, meta.Tool, meta.Kind, meta.ExitCode, len(payloads))
	for _, payload := range payloads {
		fmt.Fprint(&summary, "\n")
		fmt.Fprint(&summary, summarizeArtifactPayload(artifactID, payload))
		if len(payload.Data) > artifactSmallPayloadSize {
			largeFiles++
		}
	}
	if largeFiles > 0 {
		fmt.Fprintf(&summary, "\nlarge_files=%d remain artifact-backed", largeFiles)
	}

	return core.ToolResult{Content: summary.String(), ExitCode: 0, ArtifactID: artifactID}
}

func (t artifactReadTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t artifactReadTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	placement := core.TierNear
	state := "small artifact payloads summarized"
	if strings.Contains(result.Content, "large_files=") {
		placement = core.TierArtifact
		state = "large artifact payloads left in artifact storage"
	}
	observation := summarizeResult("artifact_read", result, placement)
	observation.StateDelta = state + " for artifact " + result.ArtifactID
	return observation
}

func (t baseTool) runGitCommand(ctx context.Context, input core.ToolInput, args []string, content func(stdout string, code int, artifactID string) string) core.ToolResult {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = t.workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := exitCode(err)

	redactedStdout := redactSecrets(stdout.Bytes())
	redactedStderr := redactSecrets(stderr.Bytes())

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "command",
		ExitCode: code,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{
			{Name: "stdout.txt", Data: redactedStdout},
			{Name: "stderr.txt", Data: redactedStderr},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: code, Error: artifactErr.Error()}
	}

	resultContent := content(stdout.String(), code, artifactID)
	if err != nil {
		return core.ToolResult{Content: resultContent, ExitCode: code, ArtifactID: artifactID, Error: err.Error()}
	}
	return core.ToolResult{Content: resultContent, ExitCode: code, ArtifactID: artifactID}
}

func resolveToolPath(workspace, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(workspace, path)
}

func defaultStringInput(input core.ToolInput, key, fallback string) string {
	value := strings.TrimSpace(stringInput(input, key))
	if value == "" {
		return fallback
	}
	return value
}

func boolInput(input core.ToolInput, key string) bool {
	value, ok := input[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(v))
		return parsed
	default:
		return false
	}
}

func intInput(input core.ToolInput, key string, fallback, min, max int) int {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	var parsed int
	switch v := value.(type) {
	case int:
		parsed = v
	case int64:
		parsed = int(v)
	case float64:
		parsed = int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fallback
		}
		parsed = n
	default:
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}

func countDiffFiles(diff string) int {
	return strings.Count(diff, "\ndiff --git ") + boolToInt(strings.HasPrefix(diff, "diff --git "))
}

func countDiffHunks(diff string) int {
	return strings.Count(diff, "\n@@ ") + boolToInt(strings.HasPrefix(diff, "@@ "))
}

func filterArtifactPayloads(payloads []artifact.PayloadRecord, selector string) []artifact.PayloadRecord {
	if selector == "" {
		return payloads
	}
	out := make([]artifact.PayloadRecord, 0, 1)
	for _, payload := range payloads {
		if artifactPayloadMatches(payload.Metadata, selector) {
			out = append(out, payload)
		}
	}
	return out
}

func artifactPayloadMatches(file artifact.FileMetadata, selector string) bool {
	selector = strings.TrimSpace(selector)
	base := filepath.Base(selector)
	return selector == file.Path ||
		selector == file.Name ||
		base == filepath.Base(file.Path) ||
		base == file.Name
}

func summarizeArtifactPayload(artifactID string, payload artifact.PayloadRecord) string {
	size := len(payload.Data)
	path := payload.Metadata.Path
	if size > artifactSmallPayloadSize {
		return fmt.Sprintf("%s: %d bytes; large payload kept in artifact %s", path, size, artifactID)
	}
	if !utf8.Valid(payload.Data) || bytes.Contains(payload.Data, []byte{0}) {
		return fmt.Sprintf("%s: %d bytes; binary payload", path, size)
	}
	text := string(payload.Data)
	lines := countLines(text)
	preview := compactPreview(text)
	if preview == "" {
		return fmt.Sprintf("%s: %d bytes, %d lines; empty text", path, size, lines)
	}
	return fmt.Sprintf("%s: %d bytes, %d lines; preview=%q", path, size, lines, preview)
}

func compactPreview(text string) string {
	lines := strings.Split(text, "\n")
	parts := make([]string, 0, artifactPreviewLines)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
		if len(parts) >= artifactPreviewLines {
			break
		}
	}
	preview := strings.Join(parts, " | ")
	runes := []rune(preview)
	if len(runes) > artifactPreviewChars {
		preview = string(runes[:artifactPreviewChars]) + "..."
	}
	return preview
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
