package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

// detectLanguage guesses the project language from marker files. Returns "" when
// no known marker is present.
func detectLanguage(projectPath string) string {
	switch {
	case fileExists(projectPath, "go.mod"):
		return "go"
	case fileExists(projectPath, "tsconfig.json"), fileExists(projectPath, "package.json"):
		return "node"
	case fileExists(projectPath, "Cargo.toml"):
		return "rust"
	case fileExists(projectPath, "pyproject.toml"), fileExists(projectPath, "setup.py"), fileExists(projectPath, "requirements.txt"):
		return "python"
	}
	return ""
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// hasProjectMarker reports whether projectPath looks like a project of the given
// language (used to keep diagnostics deterministic: no marker => not applicable).
func hasProjectMarker(projectPath, lang string) bool {
	switch lang {
	case "node":
		return fileExists(projectPath, "tsconfig.json") || fileExists(projectPath, "package.json")
	case "python":
		return fileExists(projectPath, "pyproject.toml") || fileExists(projectPath, "setup.py") || fileExists(projectPath, "requirements.txt")
	case "rust":
		return fileExists(projectPath, "Cargo.toml")
	}
	return false
}

// langToolchains lists candidate diagnostics commands per language, in priority
// order. The first whose binary is on PATH is used.
func langToolchains(lang string) [][]string {
	switch lang {
	case "node":
		return [][]string{
			{"tsc", "--noEmit", "--pretty", "false"},
			{"npx", "--no-install", "tsc", "--noEmit", "--pretty", "false"},
		}
	case "python":
		return [][]string{
			{"ruff", "check", "--no-cache", "."},
			{"pyflakes", "."},
			{"python3", "-m", "pyflakes", "."},
			{"python", "-m", "pyflakes", "."},
		}
	case "rust":
		return [][]string{
			{"cargo", "check", "--message-format=short", "--quiet"},
		}
	}
	return nil
}

func firstAvailable(candidates [][]string) ([]string, bool) {
	for _, c := range candidates {
		if len(c) == 0 {
			continue
		}
		if _, err := exec.LookPath(c[0]); err == nil {
			return c, true
		}
	}
	return nil, false
}

func toolchainNames(candidates [][]string) string {
	names := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if len(c) > 0 {
			names = append(names, c[0])
		}
	}
	return strings.Join(names, ", ")
}

// runLangDiagnostics runs an external language toolchain and parses its output.
// It degrades gracefully (exit 0, 0/0 issues) when the project marker or the
// toolchain is absent, so it is safe to call in any environment.
func (t diagnosticsTool) runLangDiagnostics(ctx context.Context, projectPath, lang string, parser func(string) []Diagnostic, input core.ToolInput) core.ToolResult {
	if !hasProjectMarker(projectPath, lang) {
		return t.gracefulDiagnostics(lang, input, fmt.Sprintf("no %s project detected (no marker file); skipping", lang))
	}
	candidates := langToolchains(lang)
	cmdline, ok := firstAvailable(candidates)
	if !ok {
		return t.gracefulDiagnostics(lang, input, fmt.Sprintf("no %s diagnostics toolchain found on PATH (tried: %s)", lang, toolchainNames(candidates)))
	}

	out, code := t.runCommandCombined(ctx, projectPath, cmdline[0], cmdline[1:]...)
	result := buildDiagnosticsResult(lang, parser(out))

	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "diagnostics",
		ExitCode: code,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{{Name: "diagnostics.txt", Data: []byte(out)}},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}

	exitCode := 0
	var errMsg string
	if result.Errors > 0 {
		exitCode = 1
		errMsg = fmt.Sprintf("%d %s error(s) detected", result.Errors, lang)
	}
	return core.ToolResult{
		Content:    formatDiagnosticsSummary(result),
		ExitCode:   exitCode,
		ArtifactID: artifactID,
		Error:      errMsg,
	}
}

// gracefulDiagnostics returns a non-error "nothing to report" result with a note,
// for when a language toolchain or project is unavailable.
func (t diagnosticsTool) gracefulDiagnostics(lang string, input core.ToolInput, note string) core.ToolResult {
	result := &DiagnosticsResult{Language: lang}
	artifactID, artifactErr := t.writeArtifact(artifact.WriteRequest{
		Tool:     t.Name(),
		Kind:     "diagnostics",
		ExitCode: 0,
		Inputs:   redactInput(input),
		Payloads: []artifact.Payload{{Name: "diagnostics.txt", Data: []byte(note)}},
	})
	if artifactErr != nil {
		return core.ToolResult{ExitCode: 1, Error: artifactErr.Error()}
	}
	return core.ToolResult{
		Content:    formatDiagnosticsSummary(result) + "\n" + note,
		ExitCode:   0,
		ArtifactID: artifactID,
	}
}

// runCommandCombined runs a command and returns stdout+stderr combined (some
// toolchains write diagnostics to stdout, others to stderr) and the exit code.
func (t diagnosticsTool) runCommandCombined(ctx context.Context, dir, name string, args ...string) (string, int) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), exitCode(err)
}

// buildDiagnosticsResult dedups, counts, and sorts a set of diagnostics.
func buildDiagnosticsResult(lang string, issues []Diagnostic) *DiagnosticsResult {
	result := &DiagnosticsResult{Language: lang}
	seen := make(map[string]bool)
	for _, d := range issues {
		key := fmt.Sprintf("%s:%d:%d:%s", d.File, d.Line, d.Column, d.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		result.Issues = append(result.Issues, d)
		if d.Severity == "error" {
			result.Errors++
		} else {
			result.Warnings++
		}
	}
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

// tsc --noEmit output: "src/app.ts(12,5): error TS2304: Cannot find name 'x'."
var tscPattern = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\):\s+(error|warning)\s+TS\d+:\s+(.+)$`)

func parseTSCDiagnostics(output string) []Diagnostic {
	var out []Diagnostic
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		m := tscPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, Diagnostic{
			File:     m[1],
			Line:     parseIntOrDefault(m[2], 0),
			Column:   parseIntOrDefault(m[3], 0),
			Severity: m[4],
			Message:  strings.TrimSpace(m[5]),
		})
	}
	return out
}

// mypy: "file.py:12: error: msg" / "file.py:12:5: error: msg" / "...: note: msg"
var pyMypyPattern = regexp.MustCompile(`^(.+?):(\d+):(?:(\d+):)?\s+(error|warning|note):\s+(.+)$`)

// pyflakes/ruff: "file.py:12:5: F401 msg" / "file.py:12:5: msg"
var pyColonPattern = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s+(.+)$`)

func parsePythonDiagnostics(output string) []Diagnostic {
	var out []Diagnostic
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := pyMypyPattern.FindStringSubmatch(line); m != nil {
			sev := m[4]
			if sev == "note" {
				sev = "warning"
			}
			out = append(out, Diagnostic{
				File:     m[1],
				Line:     parseIntOrDefault(m[2], 0),
				Column:   parseIntOrDefault(m[3], 0),
				Severity: sev,
				Message:  strings.TrimSpace(m[5]),
			})
			continue
		}
		if m := pyColonPattern.FindStringSubmatch(line); m != nil {
			msg := strings.TrimSpace(m[4])
			sev := "warning"
			// ruff E999 / pyflakes syntax errors are hard errors.
			if strings.Contains(msg, "E999") || strings.Contains(strings.ToLower(msg), "syntaxerror") {
				sev = "error"
			}
			out = append(out, Diagnostic{
				File:     m[1],
				Line:     parseIntOrDefault(m[2], 0),
				Column:   parseIntOrDefault(m[3], 0),
				Severity: sev,
				Message:  msg,
			})
		}
	}
	return out
}

// cargo check --message-format=short:
// "src/main.rs:2:5: error[E0425]: cannot find value `x`"
var cargoPattern = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s+(error|warning)(?:\[[^\]]+\])?:\s+(.+)$`)

func parseCargoDiagnostics(output string) []Diagnostic {
	var out []Diagnostic
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		m := cargoPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, Diagnostic{
			File:     m[1],
			Line:     parseIntOrDefault(m[2], 0),
			Column:   parseIntOrDefault(m[3], 0),
			Severity: m[4],
			Message:  strings.TrimSpace(m[5]),
		})
	}
	return out
}
