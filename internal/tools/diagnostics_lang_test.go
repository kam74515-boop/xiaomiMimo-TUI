package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTSCDiagnostics(t *testing.T) {
	out := `src/app.ts(12,5): error TS2304: Cannot find name 'foo'.
src/util.ts(3,1): warning TS6133: 'x' is declared but never used.
this is not a diagnostic line
`
	res := buildDiagnosticsResult("node", parseTSCDiagnostics(out))
	if res.Errors != 1 || res.Warnings != 1 || len(res.Issues) != 2 {
		t.Fatalf("tsc parse: errors=%d warnings=%d issues=%d", res.Errors, res.Warnings, len(res.Issues))
	}
	first := res.Issues[0]
	if first.File != "src/app.ts" || first.Line != 12 || first.Column != 5 || first.Severity != "error" {
		t.Fatalf("tsc first issue wrong: %+v", first)
	}
	if first.Message != "Cannot find name 'foo'." {
		t.Fatalf("tsc message = %q", first.Message)
	}
}

func TestParsePythonDiagnostics(t *testing.T) {
	out := `app.py:10: error: Incompatible return value type
app.py:12:5: F401 'os' imported but unused
app.py:1:1: E999 SyntaxError: invalid syntax
notes.py:3: note: consider annotating
`
	res := buildDiagnosticsResult("python", parsePythonDiagnostics(out))
	if res.Errors != 2 || res.Warnings != 2 {
		t.Fatalf("python parse: errors=%d warnings=%d (issues=%d)", res.Errors, res.Warnings, len(res.Issues))
	}
}

func TestParseCargoDiagnostics(t *testing.T) {
	out := "src/main.rs:2:5: error[E0425]: cannot find value `x` in this scope\n" +
		"src/lib.rs:10:1: warning: unused import: `std::fmt`\n" +
		"error: aborting due to previous error\n"
	res := buildDiagnosticsResult("rust", parseCargoDiagnostics(out))
	if res.Errors != 1 || res.Warnings != 1 || len(res.Issues) != 2 {
		t.Fatalf("cargo parse: errors=%d warnings=%d issues=%d", res.Errors, res.Warnings, len(res.Issues))
	}
	// Sorted: lib.rs before main.rs.
	if res.Issues[0].File != "src/lib.rs" || res.Issues[1].File != "src/main.rs" {
		t.Fatalf("cargo issues not sorted by file: %+v", res.Issues)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"go.mod":           "go",
		"tsconfig.json":    "node",
		"package.json":     "node",
		"Cargo.toml":       "rust",
		"pyproject.toml":   "python",
		"requirements.txt": "python",
	}
	for marker, want := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, marker), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := detectLanguage(dir); got != want {
			t.Fatalf("detectLanguage(%s) = %q, want %q", marker, got, want)
		}
	}
	if got := detectLanguage(t.TempDir()); got != "" {
		t.Fatalf("empty dir should detect no language, got %q", got)
	}
}

func TestBuildDiagnosticsResultDedupAndSort(t *testing.T) {
	dup := Diagnostic{File: "a.ts", Line: 5, Column: 1, Severity: "error", Message: "boom"}
	res := buildDiagnosticsResult("node", []Diagnostic{
		{File: "b.ts", Line: 2, Column: 1, Severity: "warning", Message: "w"},
		dup, dup, // duplicate
	})
	if len(res.Issues) != 2 {
		t.Fatalf("dedup failed: %d issues", len(res.Issues))
	}
	if res.Errors != 1 || res.Warnings != 1 {
		t.Fatalf("counts wrong: errors=%d warnings=%d", res.Errors, res.Warnings)
	}
	if res.Issues[0].File != "a.ts" {
		t.Fatalf("not sorted: %+v", res.Issues)
	}
}
