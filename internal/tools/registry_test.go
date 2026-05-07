package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDefaultRegistryExecutesFiveTools(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "go.mod"), "module smoke\n\ngo 1.26\n")
	writeFile(t, filepath.Join(workspace, "main.go"), "package main\n\nfunc main() {}\n")
	run(t, workspace, "git", "init")
	run(t, workspace, "git", "add", ".")

	registry := NewDefaultRegistry(workspace)
	cases := []struct {
		name  string
		input map[string]any
	}{
		{name: "read_file", input: map[string]any{"path": "main.go"}},
		{name: "rg", input: map[string]any{"pattern": "package", "path": "."}},
		{name: "git_status", input: map[string]any{}},
		{name: "shell", input: map[string]any{"command": "printf tool-smoke"}},
		{name: "run_test", input: map[string]any{"command": "go test ./..."}},
	}

	for _, tc := range cases {
		tool, ok := registry.Get(tc.name)
		if !ok {
			t.Fatalf("missing tool %s", tc.name)
		}
		result := tool.Run(context.Background(), tc.input)
		if result.ArtifactID == "" {
			t.Fatalf("%s did not write an artifact: %+v", tc.name, result)
		}
		if obs := tool.Summarize(result); obs.ArtifactID == "" || obs.Summary == "" {
			t.Fatalf("%s produced weak observation: %+v", tc.name, obs)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}
