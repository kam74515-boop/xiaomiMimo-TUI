package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

type baseTool struct {
	name       string
	workspace  string
	store      *artifact.Store
	summarizer Summarizer
}

func (t baseTool) Name() string {
	return t.name
}

func (t baseTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyReadOnly
}

func (t baseTool) path(input core.ToolInput) (string, error) {
	path := strings.TrimSpace(stringInput(input, "path"))
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Join(t.workspace, path), nil
}

func (t baseTool) writeArtifact(req artifact.WriteRequest) (string, error) {
	if t.store == nil {
		return "", fmt.Errorf("artifact store is nil")
	}
	record, err := t.store.Write(req)
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

type shellTool struct {
	baseTool
}

func NewShellTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return shellTool{baseTool: newBase("shell", workspace, store, s)}
}

func (t shellTool) Schema() core.JSONSchema {
	return objectSchema("Run a shell command.", map[string]any{
		"command": stringSchema("Command to run with sh -c."),
	})
}

func (t shellTool) Safety(input core.ToolInput) core.SafetyGrade {
	command := strings.TrimSpace(stringInput(input, "command"))
	if command == "" {
		return core.SafetyShellMutation
	}
	return detectShellRisk(command)
}

func (t shellTool) Permission(input core.ToolInput) core.PermissionRequest {
	command := strings.TrimSpace(stringInput(input, "command"))
	if command != "" && detectShellRisk(command) == core.SafetyDestructive {
		return core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "DESTRUCTIVE: " + command + " - requires explicit approval"}
	}
	return core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "shell commands can mutate the workspace"}
}

func (t shellTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	command := strings.TrimSpace(stringInput(input, "command"))
	if command == "" {
		return core.ToolResult{ExitCode: 2, Error: "command is required"}
	}
	return t.runCommand(ctx, "shell", command, []string{"sh", "-c", command}, input)
}

func (t shellTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t shellTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("shell", result, core.TierArtifact)
}

func (t shellTool) runCommand(ctx context.Context, toolName, command string, args []string, input core.ToolInput) core.ToolResult {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = t.workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := exitCode(err)

	// Redact secrets from output before storing as artifact.
	redactedStdout := redactSecrets(stdout.Bytes())
	redactedStderr := redactSecrets(stderr.Bytes())

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     toolName,
		Kind:     "command",
		ExitCode: exitCode,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{
			{Name: "stdout.txt", Data: redactedStdout},
			{Name: "stderr.txt", Data: redactedStderr},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: exitCode, Error: artifactErr.Error()}
	}

	var content string
	if toolName == "run_test" {
		passed, failed := parseTestResults(stdout.String())
		if passed > 0 || failed > 0 {
			content = fmt.Sprintf("Tests: %d passed, %d failed (exit %d)", passed, failed, exitCode)
		} else {
			lines := countNonEmptyLines(stdout.String())
			content = fmt.Sprintf("Tests (exit %d, %d lines output)", exitCode, lines)
		}
	} else {
		lines := countNonEmptyLines(stdout.String())
		risk := detectShellRisk(command)
		riskLabel := riskLevelLabel(risk)
		content = fmt.Sprintf("Shell: %s (exit %d, %d lines output) [risk: %s]", command, exitCode, lines, riskLabel)
	}
	if err != nil {
		return core.ToolResult{Content: content, ExitCode: exitCode, ArtifactID: artifactID, Error: err.Error()}
	}
	return core.ToolResult{Content: content, ExitCode: exitCode, ArtifactID: artifactID}
}

type rgTool struct {
	baseTool
}

func NewRGTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return rgTool{baseTool: newBase("rg", workspace, store, s)}
}

func (t rgTool) Schema() core.JSONSchema {
	return objectSchema("Search text with ripgrep.", map[string]any{
		"pattern": stringSchema("Pattern to search for."),
		"path":    stringSchema("Optional path to search."),
	})
}

func (t rgTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "rg only reads files"}
}

func (t rgTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	pattern := strings.TrimSpace(stringInput(input, "pattern"))
	if pattern == "" {
		return core.ToolResult{ExitCode: 2, Error: "pattern is required"}
	}
	args := []string{"rg", "--line-number", "--color", "never", pattern}
	if path := strings.TrimSpace(stringInput(input, "path")); path != "" {
		args = append(args, path)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = t.workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := exitCode(err)

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "command",
		ExitCode: code,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{
			{Name: "stdout.txt", Data: stdout.Bytes()},
			{Name: "stderr.txt", Data: stderr.Bytes()},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: code, Error: artifactErr.Error()}
	}

	matches := countNonEmptyLines(stdout.String())
	content := fmt.Sprintf("rg exited %d with %d matching lines; artifact=%s", code, matches, artifactID)
	if err != nil && code != 1 {
		return core.ToolResult{Content: content, ExitCode: code, ArtifactID: artifactID, Error: err.Error()}
	}
	return core.ToolResult{Content: content, ExitCode: code, ArtifactID: artifactID}
}

func (t rgTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t rgTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("rg", result, core.TierArtifact)
}

type readFileTool struct {
	baseTool
}

func NewReadFileTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return readFileTool{baseTool: newBase("read_file", workspace, store, s)}
}

func (t readFileTool) Schema() core.JSONSchema {
	return objectSchema("Read a file into an artifact.", map[string]any{
		"path": stringSchema("File path to read."),
	})
}

func (t readFileTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "read_file only reads files"}
}

func (t readFileTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	path, err := t.path(input)
	if err != nil {
		return core.ToolResult{ExitCode: 2, Error: err.Error()}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error()}
	}
	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "content",
		ExitCode: 0,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{{Name: "content.txt", Data: data}},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}
	lineCount := strings.Count(string(data), "\n")
	return core.ToolResult{
		Content:    fmt.Sprintf("Read %s (%d lines, %d bytes)", displayPath(t.workspace, path), lineCount, len(data)),
		ExitCode:   0,
		ArtifactID: artifactID,
	}
}

func (t readFileTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t readFileTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("read_file", result, core.TierArtifact)
}

type writeFileTool struct {
	baseTool
}

func NewWriteFileTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return writeFileTool{baseTool: newBase("write_file", workspace, store, s)}
}

func (t writeFileTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyWorkspaceMutation
}

func (t writeFileTool) Schema() core.JSONSchema {
	return objectSchema("Write content to a file.", map[string]any{
		"path":    stringSchema("File path to write."),
		"content": stringSchema("Content to write."),
	})
}

func (t writeFileTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "write_file mutates the workspace"}
}

func (t writeFileTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	path, err := t.path(input)
	if err != nil {
		return core.ToolResult{ExitCode: 2, Error: err.Error()}
	}
	content := stringInput(input, "content")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error()}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return core.ToolResult{ExitCode: 1, Error: err.Error()}
	}
	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "content",
		ExitCode: 0,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{{Name: "content.txt", Data: []byte(content)}},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}
	return core.ToolResult{
		Content:    fmt.Sprintf("Wrote %s (%d bytes)", displayPath(t.workspace, path), len(content)),
		ExitCode:   0,
		ArtifactID: artifactID,
	}
}

func (t writeFileTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t writeFileTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("write_file", result, core.TierArtifact)
}

type applyPatchTool struct {
	baseTool
}

func NewApplyPatchTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return applyPatchTool{baseTool: newBase("apply_patch", workspace, store, s)}
}

func (t applyPatchTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyWorkspaceMutation
}

func (t applyPatchTool) Schema() core.JSONSchema {
	return objectSchema("Apply a patch with git apply.", map[string]any{
		"patch": stringSchema("Unified diff patch content."),
	})
}

func (t applyPatchTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "apply_patch mutates the workspace"}
}

func (t applyPatchTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	patch := stringInput(input, "patch")
	if strings.TrimSpace(patch) == "" {
		return core.ToolResult{ExitCode: 2, Error: "patch is required"}
	}

	cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = t.workspace
	cmd.Stdin = strings.NewReader(patch)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := exitCode(err)

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "patch",
		ExitCode: code,
		Inputs:   map[string]any{"patch_bytes": len(patch)},
		Payloads: []artifact.Payload{
			{Name: "patch.diff", Data: []byte(patch)},
			{Name: "stdout.txt", Data: stdout.Bytes()},
			{Name: "stderr.txt", Data: stderr.Bytes()},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: code, Error: artifactErr.Error()}
	}

	filesChanged, additions, removals := parseUnifiedDiff(patch)
	content := fmt.Sprintf("Applied patch: %d files changed, +%d -%d", filesChanged, additions, removals)
	if err != nil {
		return core.ToolResult{Content: content, ExitCode: code, ArtifactID: artifactID, Error: err.Error()}
	}
	return core.ToolResult{Content: content, ExitCode: code, ArtifactID: artifactID}
}

func (t applyPatchTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t applyPatchTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("apply_patch", result, core.TierArtifact)
}

type gitStatusTool struct {
	baseTool
}

func NewGitStatusTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return gitStatusTool{baseTool: newBase("git_status", workspace, store, s)}
}

func (t gitStatusTool) Schema() core.JSONSchema {
	return objectSchema("Read git status.", map[string]any{})
}

func (t gitStatusTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAllow, Reason: "git_status only reads repository state"}
}

func (t gitStatusTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	cmd := exec.CommandContext(ctx, "git", "status", "--short", "--branch")
	cmd.Dir = t.workspace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := exitCode(err)

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "command",
		ExitCode: code,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{
			{Name: "stdout.txt", Data: stdout.Bytes()},
			{Name: "stderr.txt", Data: stderr.Bytes()},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: code, Error: artifactErr.Error()}
	}

	entries := countStatusEntries(stdout.String())
	content := fmt.Sprintf("git status exited %d with %d changed entries; artifact=%s", code, entries, artifactID)
	if err != nil {
		return core.ToolResult{Content: content, ExitCode: code, ArtifactID: artifactID, Error: err.Error()}
	}
	return core.ToolResult{Content: content, ExitCode: code, ArtifactID: artifactID}
}

func (t gitStatusTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t gitStatusTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("git_status", result, core.TierArtifact)
}

type runTestTool struct {
	baseTool
}

func NewRunTestTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return runTestTool{baseTool: newBase("run_test", workspace, store, s)}
}

func (t runTestTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyShellMutation
}

func (t runTestTool) Schema() core.JSONSchema {
	return objectSchema("Run a test command.", map[string]any{
		"command": stringSchema("Optional test command. Defaults to auto-detected based on project type."),
	})
}

func (t runTestTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "tests can run arbitrary project code"}
}

func (t runTestTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	command := strings.TrimSpace(stringInput(input, "command"))
	if command == "" {
		command = defaultTestCommand(t.workspace)
	}
	return shellTool{baseTool: t.baseTool}.runCommand(ctx, t.Name(), command, []string{"sh", "-c", command}, input)
}

func (t runTestTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t runTestTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("run_test", result, core.TierArtifact)
}

func newBase(name, workspace string, store *artifact.Store, s Summarizer) baseTool {
	if workspace == "" {
		workspace = "."
	}
	return baseTool{name: name, workspace: workspace, store: store, summarizer: s}
}

func objectSchema(description string, properties map[string]any) core.JSONSchema {
	return core.JSONSchema{
		"type":        "object",
		"description": description,
		"properties":  properties,
	}
}

func stringSchema(description string) core.JSONSchema {
	return core.JSONSchema{"type": "string", "description": description}
}

func stringInput(input core.ToolInput, key string) string {
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func summarizeResult(name string, result core.ToolResult, placement core.ContextTier) core.Observation {
	summary := result.Content
	if summary == "" {
		summary = name + " completed"
	}
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
	} else if result.ExitCode != 0 {
		risk = "non-zero exit code " + strconv.Itoa(result.ExitCode)
	}
	return core.Observation{
		Summary:          summary,
		StateDelta:       artifactStateDelta(result.ArtifactID),
		RiskDelta:        risk,
		ContextPlacement: placement,
		ArtifactID:       result.ArtifactID,
	}
}

func artifactStateDelta(artifactID string) string {
	if artifactID == "" {
		return "no artifact was written"
	}
	return "raw output stored as artifact " + artifactID
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func redactInput(input core.ToolInput) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if key == "content" || key == "patch" {
			out[key+"_bytes"] = len(stringInput(input, key))
			continue
		}
		out[key] = value
	}
	return out
}

func countNonEmptyLines(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func countStatusEntries(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "##") {
			continue
		}
		count++
	}
	return count
}

func displayPath(workspace, path string) string {
	if rel, err := filepath.Rel(workspace, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func detectShellRisk(command string) core.SafetyGrade {
	cmd := strings.TrimSpace(command)

	// Destructive patterns: rm/rrmdir/chmod/chown/sudo/git reset --hard/git clean
	// Also pipe-to-shell and /dev/null redirections that may indicate destructive intent.
	destructiveMarkers := []string{
		"rm ", "rmdir", "chmod", "chown", "sudo",
		"git reset --hard", "git clean",
		">/dev/null", ">/dev/null",
		"curl ", "wget ",
	}
	shellMutationMarkers := []string{
		"mv ", "cp ", "git ",
	}

	// Check for curl/wget piped to sh.
	lower := strings.ToLower(cmd)
	if (strings.Contains(lower, "curl ") || strings.Contains(lower, "wget ")) && strings.Contains(cmd, "| sh") {
		return core.SafetyDestructive
	}

	for _, marker := range destructiveMarkers {
		if strings.Contains(lower, marker) {
			return core.SafetyDestructive
		}
	}

	for _, marker := range shellMutationMarkers {
		if strings.Contains(lower, marker) {
			return core.SafetyShellMutation
		}
	}

	return core.SafetyShellMutation
}

var (
	secretKVPattern     = regexp.MustCompile(`(?i)(api[-_]?key|api[-_]?secret|secret[-_]?key|access[-_]?token|auth[-_]?token|private[-_]?key|token)\s*[=:]\s*\S+`)
	secretBearerPattern = regexp.MustCompile(`(?i)Bearer\s+\S+`)
)

// redactSecrets scans data for patterns that look like API keys, tokens, or
// other secrets and replaces their values with ***REDACTED***.
func redactSecrets(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	text := string(data)
	text = secretKVPattern.ReplaceAllStringFunc(text, func(match string) string {
		for i, c := range match {
			if c == '=' || c == ':' {
				return match[:i+1] + "***REDACTED***"
			}
		}
		return match
	})
	text = secretBearerPattern.ReplaceAllString(text, "Bearer ***REDACTED***")
	return []byte(text)
}

// riskLevelLabel maps a SafetyGrade to a human-readable risk level for shell summaries.
func riskLevelLabel(grade core.SafetyGrade) string {
	switch grade {
	case core.SafetyReadOnly:
		return "low"
	case core.SafetyWorkspaceMutation:
		return "low"
	case core.SafetyShellMutation:
		return "medium"
	case core.SafetyDestructive:
		return "high"
	default:
		return "unknown"
	}
}

func detectProjectType(workspace string) string {
	// Check for Go
	if _, err := os.Stat(filepath.Join(workspace, "go.mod")); err == nil {
		return "go"
	}

	// Check for Node.js (check package.json for test script)
	if data, err := os.ReadFile(filepath.Join(workspace, "package.json")); err == nil {
		// Quick check for "test" script in package.json
		if strings.Contains(string(data), "\"test\"") {
			// Determine package manager
			if _, err := os.Stat(filepath.Join(workspace, "pnpm-lock.yaml")); err == nil {
				return "pnpm"
			}
			if _, err := os.Stat(filepath.Join(workspace, "yarn.lock")); err == nil {
				return "yarn"
			}
			return "npm"
		}
		// package.json exists but no test script — still npm
		if _, err := os.Stat(filepath.Join(workspace, "pnpm-lock.yaml")); err == nil {
			return "pnpm"
		}
		if _, err := os.Stat(filepath.Join(workspace, "yarn.lock")); err == nil {
			return "yarn"
		}
		return "npm"
	}

	// Check for Python
	if _, err := os.Stat(filepath.Join(workspace, "pyproject.toml")); err == nil {
		return "python"
	}
	if _, err := os.Stat(filepath.Join(workspace, "setup.py")); err == nil {
		return "python"
	}
	if _, err := os.Stat(filepath.Join(workspace, "setup.cfg")); err == nil {
		return "python"
	}

	// Check for Rust
	if _, err := os.Stat(filepath.Join(workspace, "Cargo.toml")); err == nil {
		return "rust"
	}

	return "go" // fallback
}

func defaultTestCommand(workspace string) string {
	projectType := detectProjectType(workspace)
	switch projectType {
	case "go":
		return "go test ./..."
	case "npm":
		return "npm test"
	case "pnpm":
		return "pnpm test"
	case "yarn":
		return "yarn test"
	case "python":
		return "python -m pytest"
	case "rust":
		return "cargo test"
	default:
		return "go test ./..."
	}
}

// parseUnifiedDiff counts files changed, additions, and removals in a unified diff.
func parseUnifiedDiff(patch string) (files, additions, removals int) {
	seen := make(map[string]bool)
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			name := line[6:]
			if name != "/dev/null" && !seen[name] {
				seen[name] = true
				files++
			}
		case strings.HasPrefix(line, "--- a/"):
			name := line[6:]
			if name != "/dev/null" && !seen[name] {
				seen[name] = true
				files++
			}
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			additions++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removals++
		}
	}
	return
}

// parseTestResults counts passed and failed tests from common test output formats.
func parseTestResults(output string) (passed, failed int) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--- PASS:") {
			passed++
		} else if strings.HasPrefix(trimmed, "--- FAIL:") {
			failed++
		}
	}
	return
}
