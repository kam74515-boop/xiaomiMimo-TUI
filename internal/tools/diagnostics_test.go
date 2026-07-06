package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

func TestDiagnosticsToolNameSchemaSafetyPermission(t *testing.T) {
	tool := NewDiagTool(t.TempDir(), nil, nil)

	if tool.Name() != "diagnostics" {
		t.Fatalf("name = %q, want %q", tool.Name(), "diagnostics")
	}

	schema := tool.Schema()
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
	if _, ok := schema["description"]; !ok {
		t.Fatal("schema missing description")
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema properties is not a map")
	}
	if _, ok := props["project_path"]; !ok {
		t.Fatal("schema missing project_path property")
	}
	if _, ok := props["language"]; !ok {
		t.Fatal("schema missing language property")
	}

	if safety := tool.Safety(core.ToolInput{}); safety != core.SafetyReadOnly {
		t.Fatalf("safety = %s, want SafetyReadOnly", safety)
	}

	perm := tool.Permission(core.ToolInput{})
	if perm.Behavior != core.PermissionAllow {
		t.Fatalf("permission behavior = %s, want PermissionAllow", perm.Behavior)
	}
}

func TestParseGoDiagnostics_VetOutput(t *testing.T) {
	input := `./main.go:10:2: fmt.Println call has possible formatting directive %s
./util/helper.go:25:5: unused variable 'count'
./main.go:10:2: fmt.Println call has possible formatting directive %s
`

	result := parseGoDiagnostics(input)

	if result.Errors != 0 {
		t.Fatalf("errors = %d, want 0", result.Errors)
	}
	if result.Warnings != 2 {
		t.Fatalf("warnings = %d, want 2 (deduplicated from 3 lines)", result.Warnings)
	}
	if len(result.Issues) != 2 {
		t.Fatalf("issues = %d, want 2 (deduplicated)", len(result.Issues))
	}

	// Verify deduplication: the duplicate main.go:10:2 line should be removed.
	mainCount := 0
	for _, issue := range result.Issues {
		if issue.File == "./main.go" && issue.Line == 10 {
			mainCount++
		}
	}
	if mainCount != 1 {
		t.Fatalf("main.go:10:2 appears %d times, want 1 (deduplicated)", mainCount)
	}

	// Verify severity.
	for _, issue := range result.Issues {
		if issue.Severity != "warning" {
			t.Fatalf("vet issue severity = %q, want warning", issue.Severity)
		}
	}
}

func TestParseGoDiagnostics_BuildErrors(t *testing.T) {
	input := `./main.go:15:5: undefined: undefinedVar
./main.go:20:10: cannot use x (type int) as type string
./lib/foo.go:8:1: syntax error: unexpected }
`

	result := parseGoDiagnostics(input)

	if result.Errors != 3 {
		t.Fatalf("errors = %d, want 3", result.Errors)
	}
	if result.Warnings != 0 {
		t.Fatalf("warnings = %d, want 0", result.Warnings)
	}
	if len(result.Issues) != 3 {
		t.Fatalf("issues = %d, want 3", len(result.Issues))
	}

	for _, issue := range result.Issues {
		if issue.Severity != "error" {
			t.Fatalf("build issue severity = %q, want error", issue.Severity)
		}
	}
}

func TestParseGoDiagnostics_MixedVetAndBuild(t *testing.T) {
	input := `./main.go:10:2: fmt.Println call has possible formatting directive %s
./main.go:15:5: undefined: myVar
./util.go:3:1: vet: unreachable code
`

	result := parseGoDiagnostics(input)

	if result.Errors != 1 {
		t.Fatalf("errors = %d, want 1", result.Errors)
	}
	if result.Warnings != 2 {
		t.Fatalf("warnings = %d, want 2", result.Warnings)
	}
	if len(result.Issues) != 3 {
		t.Fatalf("issues = %d, want 3", len(result.Issues))
	}
}

func TestParseGoDiagnostics_LineOnlyFormat(t *testing.T) {
	// Some tools produce "file:line: message" without a column.
	input := `./main.go:10: undefined: foo
`

	result := parseGoDiagnostics(input)

	if len(result.Issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(result.Issues))
	}
	if result.Issues[0].Line != 10 {
		t.Fatalf("line = %d, want 10", result.Issues[0].Line)
	}
	if result.Issues[0].Column != 0 {
		t.Fatalf("column = %d, want 0 (no column in input)", result.Issues[0].Column)
	}
}

func TestParseGoDiagnostics_EmptyOutput(t *testing.T) {
	result := parseGoDiagnostics("")

	if result.Errors != 0 || result.Warnings != 0 || len(result.Issues) != 0 {
		t.Fatalf("expected empty result, got errors=%d warnings=%d issues=%d",
			result.Errors, result.Warnings, len(result.Issues))
	}
}

func TestParseGoDiagnostics_NonDiagnosticLines(t *testing.T) {
	// Lines that don't match the pattern should be ignored.
	input := `# command-line-arguments
FAIL	go.example.com/test	0.123s
some random noise
./main.go:10:5: undefined: foo
`

	result := parseGoDiagnostics(input)

	if len(result.Issues) != 1 {
		t.Fatalf("issues = %d, want 1 (noise lines should be ignored)", len(result.Issues))
	}
}

func TestParseGoDiagnostics_Sorted(t *testing.T) {
	input := `./z.go:5:1: warning A
./a.go:10:3: warning B
./a.go:2:1: warning C
`

	result := parseGoDiagnostics(input)

	if len(result.Issues) != 3 {
		t.Fatalf("issues = %d, want 3", len(result.Issues))
	}

	// Should be sorted: a.go:2, a.go:10, z.go:5
	if result.Issues[0].File != "./a.go" || result.Issues[0].Line != 2 {
		t.Fatalf("first issue = %s:%d, want ./a.go:2", result.Issues[0].File, result.Issues[0].Line)
	}
	if result.Issues[1].File != "./a.go" || result.Issues[1].Line != 10 {
		t.Fatalf("second issue = %s:%d, want ./a.go:10", result.Issues[1].File, result.Issues[1].Line)
	}
	if result.Issues[2].File != "./z.go" || result.Issues[2].Line != 5 {
		t.Fatalf("third issue = %s:%d, want z.go:5", result.Issues[2].File, result.Issues[2].Line)
	}
}

func TestClassifySeverity(t *testing.T) {
	tests := []struct {
		line    string
		message string
		want    string
	}{
		{"./main.go:10:5: undefined: foo", "undefined: foo", "error"},
		{"./main.go:10:5: cannot use x as type string", "cannot use x as type string", "error"},
		{"./main.go:10:5: syntax error: unexpected }", "syntax error: unexpected }", "error"},
		{"./main.go:10:2: fmt.Println call has possible formatting directive", "fmt.Println call has possible formatting directive", "warning"},
		{"./main.go:10:1: vet: unreachable code", "unreachable code", "warning"},
		{"./main.go:5:1: some generic message", "some generic message", "warning"},
	}

	for _, tc := range tests {
		got := classifySeverity(tc.line, tc.message)
		if got != tc.want {
			t.Fatalf("classifySeverity(%q, %q) = %q, want %q", tc.line, tc.message, got, tc.want)
		}
	}
}

func TestFormatDiagnosticsSummary(t *testing.T) {
	result := &DiagnosticsResult{
		Language: "go",
		Errors:   3,
		Warnings: 5,
	}
	got := formatDiagnosticsSummary(result)
	want := "Diagnostics: 3 errors, 5 warnings (language: go)"
	if got != want {
		t.Fatalf("formatDiagnosticsSummary = %q, want %q", got, want)
	}
}

func TestDiagnosticsToolRunUnsupportedLanguage(t *testing.T) {
	tool := NewDiagTool(t.TempDir(), nil, nil)
	result := tool.Run(context.Background(), core.ToolInput{"language": "cobol"})

	if result.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Error, "unsupported language") {
		t.Fatalf("error = %q, want it to contain 'unsupported language'", result.Error)
	}
}

func TestDiagnosticsToolNodeGracefulWhenNoProject(t *testing.T) {
	workspace := t.TempDir() // no package.json/tsconfig => not a node project
	store := newTestStore(t, workspace)
	tool := NewDiagTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{"language": "node"})

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (graceful, no project)", result.ExitCode)
	}
	if !strings.Contains(result.Content, "0 errors, 0 warnings") || !strings.Contains(result.Content, "node") {
		t.Fatalf("content = %q, want graceful node summary", result.Content)
	}
	if result.ArtifactID == "" {
		t.Fatal("expected an artifact ID for graceful result")
	}
}

func TestDiagnosticsToolPythonGracefulWhenNoProject(t *testing.T) {
	workspace := t.TempDir() // no pyproject/setup.py/requirements => not a python project
	store := newTestStore(t, workspace)
	tool := NewDiagTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{"language": "python"})

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (graceful, no project)", result.ExitCode)
	}
	if !strings.Contains(result.Content, "python") {
		t.Fatalf("content = %q, should mention python", result.Content)
	}
}

func TestDiagnosticsToolSummarize(t *testing.T) {
	tool := NewDiagTool(t.TempDir(), nil, nil)

	result := core.ToolResult{
		Content:  "Diagnostics: 2 errors, 3 warnings (language: go)",
		ExitCode: 1,
	}
	obs := tool.Summarize(result)

	if obs.Summary != result.Content {
		t.Fatalf("observation summary = %q, want %q", obs.Summary, result.Content)
	}
	if obs.ContextPlacement != core.TierNear {
		t.Fatalf("context placement = %s, want TierNear", obs.ContextPlacement)
	}
}

func TestParseGoDiagnostics_JSONRoundTrip(t *testing.T) {
	input := `./main.go:10:5: undefined: foo
./main.go:20:1: vet: unreachable code
`

	result := parseGoDiagnostics(input)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var roundTrip DiagnosticsResult
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if roundTrip.Errors != 1 || roundTrip.Warnings != 1 {
		t.Fatalf("round trip: errors=%d warnings=%d, want 1,1", roundTrip.Errors, roundTrip.Warnings)
	}
	if len(roundTrip.Issues) != 2 {
		t.Fatalf("round trip: issues=%d, want 2", len(roundTrip.Issues))
	}
}

func TestParseIntOrDefault(t *testing.T) {
	tests := []struct {
		input string
		def   int
		want  int
	}{
		{"42", 0, 42},
		{"0", -1, 0},
		{"", 99, 99},
		{"abc", 7, 7},
		{"12x", 3, 3},
	}
	for _, tc := range tests {
		got := parseIntOrDefault(tc.input, tc.def)
		if got != tc.want {
			t.Fatalf("parseIntOrDefault(%q, %d) = %d, want %d", tc.input, tc.def, got, tc.want)
		}
	}
}

func TestMaxExitCode(t *testing.T) {
	if maxExitCode(0, 1) != 1 {
		t.Fatal("maxExitCode(0,1) should be 1")
	}
	if maxExitCode(3, 2) != 3 {
		t.Fatal("maxExitCode(3,2) should be 3")
	}
	if maxExitCode(0, 0) != 0 {
		t.Fatal("maxExitCode(0,0) should be 0")
	}
}

func TestDiagnosticsToolDefaultLanguage(t *testing.T) {
	// When language is not specified, it should default to "go".
	// We test this by verifying the tool attempts to run Go diagnostics
	// (which will fail gracefully outside a Go project but still show "go" in output).
	workspace := t.TempDir()
	store := newTestStore(t, workspace)
	tool := NewDiagTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{})
	// Should produce a diagnostics summary mentioning "go".
	if !strings.Contains(result.Content, "language: go") {
		t.Fatalf("content = %q, should mention 'language: go' as default", result.Content)
	}
}

func TestDiagnosticsToolRegisteredInBuiltins(t *testing.T) {
	workspace := t.TempDir()
	builtins := Builtins(workspace, nil, nil)
	found := false
	for _, tool := range builtins {
		if tool.Name() == "diagnostics" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("diagnostics tool not found in Builtins()")
	}
}

// newTestStore creates an artifact store in a temp directory for testing.
func newTestStore(t *testing.T, workspace string) *artifact.Store {
	t.Helper()
	return artifact.NewStore(workspace)
}
