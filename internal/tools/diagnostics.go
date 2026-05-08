package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

// Diagnostic represents a single diagnostic message from a language toolchain.
type Diagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"` // "error" or "warning"
	Message  string `json:"message"`
}

// DiagnosticsResult holds the parsed output from a diagnostics run.
type DiagnosticsResult struct {
	Language  string       `json:"language"`
	Errors    int          `json:"errors"`
	Warnings  int          `json:"warnings"`
	Issues    []Diagnostic `json:"issues"`
	RawOutput string       `json:"-"`
}

// diagnosticsTool runs language-specific lint/build checks and parses
// the output into structured diagnostics.
type diagnosticsTool struct {
	baseTool
}

// NewDiagTool creates a diagnostics tool wired to the given workspace,
// artifact store, and optional summarizer.
func NewDiagTool(workspace string, store *artifact.Store, s Summarizer) core.Tool {
	return diagnosticsTool{baseTool: newBase("diagnostics", workspace, store, s)}
}

func (t diagnosticsTool) Schema() core.JSONSchema {
	return objectSchema(
		"Run language diagnostics (compile errors, type errors, lint warnings) on the project. "+
			"Defaults to Go (go vet + go build). Returns structured issues and a compressed summary.",
		map[string]any{
			"project_path": stringSchema("Optional project root path. Defaults to the workspace."),
			"language":     stringSchema("Language to check: 'go' (default), 'node', 'python'. Node/Python are placeholders."),
		},
	)
}

func (t diagnosticsTool) Safety(input core.ToolInput) core.SafetyGrade {
	return core.SafetyReadOnly
}

func (t diagnosticsTool) Permission(input core.ToolInput) core.PermissionRequest {
	return core.PermissionRequest{
		Behavior: core.PermissionAllow,
		Reason:   "diagnostics only reads and analyzes the project; it does not mutate files",
	}
}

func (t diagnosticsTool) Run(ctx context.Context, input core.ToolInput) core.ToolResult {
	lang := strings.TrimSpace(stringInput(input, "language"))
	if lang == "" {
		lang = "go"
	}

	projectPath := strings.TrimSpace(stringInput(input, "project_path"))
	if projectPath == "" {
		projectPath = t.workspace
	}

	switch lang {
	case "go":
		return t.runGoDiagnostics(ctx, projectPath, input)
	case "node", "python":
		// Placeholder: return a helpful message until implemented.
		return t.placeholderResult(lang, input)
	default:
		return core.ToolResult{
			ExitCode: 2,
			Error:    fmt.Sprintf("unsupported language %q; supported: go, node (placeholder), python (placeholder)", lang),
		}
	}
}

func (t diagnosticsTool) Summarize(result core.ToolResult) core.Observation {
	return t.SummarizeWithBudget(result, BudgetSafe)
}

func (t diagnosticsTool) SummarizeWithBudget(result core.ToolResult, budget BudgetLevel) core.Observation {
	if t.summarizer != nil {
		return t.summarizer.Summarize(result, budget)
	}
	return summarizeResult("diagnostics", result, core.TierNear)
}

// runGoDiagnostics executes go vet and go build, merges the output, and
// returns structured diagnostics.
func (t diagnosticsTool) runGoDiagnostics(ctx context.Context, projectPath string, input core.ToolInput) core.ToolResult {
	vetOut, vetCode := t.runCommand(ctx, projectPath, "go", "vet", "./...")
	buildOut, buildCode := t.runCommand(ctx, projectPath, "go", "build", "./...")

	combined := vetOut
	if buildOut != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += buildOut
	}

	result := parseGoDiagnostics(combined)
	result.Language = "go"

	// Store the raw combined output as an artifact.
	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "diagnostics",
		ExitCode: maxExitCode(vetCode, buildCode),
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{
			{Name: "diagnostics.txt", Data: []byte(combined)},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}

	content := formatDiagnosticsSummary(result)
	exitCode := 0
	var errMsg string
	if result.Errors > 0 {
		exitCode = 1
		errMsg = fmt.Sprintf("%d compile/type errors detected", result.Errors)
	}

	return core.ToolResult{
		Content:    content,
		ExitCode:   exitCode,
		ArtifactID: artifactID,
		Error:      errMsg,
	}
}

func (t diagnosticsTool) placeholderResult(lang string, input core.ToolInput) core.ToolResult {
	msg := fmt.Sprintf("Diagnostics for %s is not yet implemented. Only Go is supported in this MVP.", lang)
	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "diagnostics",
		ExitCode: 0,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{
			{Name: "diagnostics.txt", Data: []byte(msg)},
		},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}
	return core.ToolResult{
		Content:    fmt.Sprintf("Diagnostics: 0 errors, 0 warnings (language: %s, placeholder)", lang),
		ExitCode:   0,
		ArtifactID: artifactID,
	}
}

// runCommand executes a command in the given directory and returns
// combined stderr+stdout and the exit code.
func (t diagnosticsTool) runCommand(ctx context.Context, dir string, name string, args ...string) (string, int) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// go vet and go build write diagnostics to stderr.
	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}

	code := exitCode(err)
	return output, code
}

// goDiagnosticPattern matches "file:line:col: message" and "file:line: message" formats
// produced by go vet and go build.
var goDiagnosticPattern = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?:\s+(.+)$`)

// parseGoDiagnostics parses go vet / go build stderr output into structured diagnostics.
// It deduplicates repeated messages.
func parseGoDiagnostics(output string) *DiagnosticsResult {
	result := &DiagnosticsResult{}
	seen := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := goDiagnosticPattern.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		file := matches[1]
		lineNum := parseIntOrDefault(matches[2], 0)
		col := parseIntOrDefault(matches[3], 0)
		message := matches[4]

		// Determine severity: build failures are errors, vet warnings are warnings.
		severity := classifySeverity(line, message)

		// Deduplicate.
		key := fmt.Sprintf("%s:%d:%d:%s", file, lineNum, col, message)
		if seen[key] {
			continue
		}
		seen[key] = true

		d := Diagnostic{
			File:     file,
			Line:     lineNum,
			Column:   col,
			Severity: severity,
			Message:  message,
		}
		result.Issues = append(result.Issues, d)

		if severity == "error" {
			result.Errors++
		} else {
			result.Warnings++
		}
	}

	// Sort by file, then line, then column for deterministic output.
	sort.Slice(result.Issues, func(i, j int) bool {
		a, b := result.Issues[i], result.Issues[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})

	return result
}

// classifySeverity determines whether a diagnostic line is an error or warning.
// go build output typically contains the word "undefined" or "cannot" for errors.
// go vet output is treated as warnings.
func classifySeverity(line, message string) string {
	lower := strings.ToLower(message)

	// Common go build error patterns.
	errorMarkers := []string{
		"undefined",
		"cannot",
		"cannot use",
		"cannot refer",
		"syntax error",
		"expected",
		"missing",
		"unexpected",
		"imported and not used",
		"redeclared",
		"no new variables",
		"not enough arguments",
		"too many arguments",
		"impossible type assertion",
	}
	for _, marker := range errorMarkers {
		if strings.Contains(lower, marker) {
			return "error"
		}
	}

	// go vet typically outputs from the "vet:" prefix or specific vet check names.
	if strings.Contains(line, "vet:") {
		return "warning"
	}

	// Default: treat as warning (go vet findings).
	return "warning"
}

func parseIntOrDefault(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	val := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return defaultVal
		}
		val = val*10 + int(c-'0')
	}
	return val
}

func maxExitCode(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func formatDiagnosticsSummary(result *DiagnosticsResult) string {
	return fmt.Sprintf("Diagnostics: %d errors, %d warnings (language: %s)",
		result.Errors, result.Warnings, result.Language)
}
