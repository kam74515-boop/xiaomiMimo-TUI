package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
)

func TestDefaultRegistryExecutesReadOnlyTools(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	writeFile(t, filepath.Join(workspace, "main.go"), "package main\n\nfunc main() {}\n")
	run(t, workspace, "git", "add", ".")
	run(t, workspace, "git", "commit", "-m", "initial")
	writeFile(t, filepath.Join(workspace, "main.go"), "package main\n\nfunc main() { println(\"changed\") }\n")

	registry := NewDefaultRegistry(workspace)
	readFile, ok := registry.Get("read_file")
	if !ok {
		t.Fatal("missing read_file")
	}
	readResult := readFile.Run(context.Background(), core.ToolInput{"path": "main.go"})
	if readResult.ArtifactID == "" {
		t.Fatalf("read_file did not write an artifact: %+v", readResult)
	}

	cases := []struct {
		name  string
		input core.ToolInput
	}{
		{name: "read_file", input: core.ToolInput{"path": "main.go"}},
		{name: "list_dir", input: core.ToolInput{"path": "."}},
		{name: "git_diff", input: core.ToolInput{"path": "main.go"}},
		{name: "git_log", input: core.ToolInput{"limit": 1}},
		{name: "artifact_read", input: core.ToolInput{"artifact_id": readResult.ArtifactID}},
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

func TestExecutorPublishesEventsAndPlacesArtifacts(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "alpha.txt"), "alpha\n")

	registry := NewDefaultRegistry(workspace)
	bus := core.NewBus()
	events := bus.Subscribe(10)
	executor := NewExecutor(registry, bus)

	result, observation := executor.Execute(context.Background(), core.ToolCall{
		ID:    "call-list",
		Name:  "list_dir",
		Input: core.ToolInput{"path": "."},
	})
	if result.ExitCode != 0 {
		t.Fatalf("list_dir failed: %+v", result)
	}
	if observation.ArtifactID == "" || observation.ContextPlacement != core.TierArtifact {
		t.Fatalf("list_dir observation was not artifact-backed: %+v", observation)
	}

	got := drainEvents(events)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(got), got)
	}
	if got[0].Type != core.EventToolStart || got[1].Type != core.EventToolResult || got[2].Type != core.EventObservation {
		t.Fatalf("unexpected event sequence: %+v", got)
	}
	if got[2].Observation == nil || got[2].Observation.ArtifactID != result.ArtifactID {
		t.Fatalf("observation event missing artifact id: %+v", got[2])
	}
}

func TestExecutorUsesRuntimeBudgetForSummarizers(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "alpha.txt"), strings.Repeat("alpha\n", 40))

	recorder := &recordingSummarizer{}
	registry := NewDefaultRegistry(workspace, func(store *artifact.Store) map[string]Summarizer {
		return map[string]Summarizer{"read_file": recorder}
	})
	executor := NewExecutor(registry, nil, WithBudgetProvider(func() BudgetLevel {
		return BudgetCritical
	}))

	_, observation := executor.Execute(context.Background(), core.ToolCall{
		Name:  "read_file",
		Input: core.ToolInput{"path": "alpha.txt"},
	})

	if recorder.budget != BudgetCritical {
		t.Fatalf("summarizer budget = %v, want %v", recorder.budget, BudgetCritical)
	}
	if !strings.Contains(observation.Summary, "budget=2") {
		t.Fatalf("observation summary = %q, want critical budget marker", observation.Summary)
	}
}

func TestExecutorConservativePermissionPolicy(t *testing.T) {
	workspace := t.TempDir()
	registry := NewDefaultRegistry(workspace)

	deniedResult, deniedObservation := NewExecutor(registry, nil).Execute(context.Background(), core.ToolCall{
		Name:  "write_file",
		Input: core.ToolInput{"path": "denied.txt", "content": "nope"},
	})
	if deniedResult.ExitCode != permissionDeniedExitCode {
		t.Fatalf("expected permission denied exit, got %+v", deniedResult)
	}
	if deniedObservation.ContextPlacement != core.TierNear || deniedObservation.ArtifactID != "" {
		t.Fatalf("denied observation should stay near without artifact: %+v", deniedObservation)
	}
	if _, err := os.Stat(filepath.Join(workspace, "denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("write_file ran despite missing explicit permission")
	}

	allowedResult, allowedObservation := NewExecutor(registry, nil, WithAllowedAskTools("write_file")).Execute(context.Background(), core.ToolCall{
		Name:  "write_file",
		Input: core.ToolInput{"path": "allowed.txt", "content": "ok"},
	})
	if allowedResult.ExitCode != 0 {
		t.Fatalf("explicitly allowed write_file failed: %+v", allowedResult)
	}
	if allowedObservation.ArtifactID == "" || allowedObservation.ContextPlacement != core.TierArtifact {
		t.Fatalf("allowed write_file observation should be artifact-backed: %+v", allowedObservation)
	}
	if got := mustReadFile(t, filepath.Join(workspace, "allowed.txt")); got != "ok" {
		t.Fatalf("allowed write_file content = %q", got)
	}
}

type recordingSummarizer struct {
	budget BudgetLevel
}

func (r *recordingSummarizer) Summarize(result core.ToolResult, budget BudgetLevel) core.Observation {
	r.budget = budget
	return core.Observation{
		Summary:          fmt.Sprintf("budget=%d artifact=%s", budget, result.ArtifactID),
		ContextPlacement: core.TierArtifact,
		ArtifactID:       result.ArtifactID,
	}
}

func TestArtifactReadSummarizesSmallPayloadsAndKeepsLargeRawOutputOut(t *testing.T) {
	workspace := t.TempDir()
	store := artifact.NewStore(workspace)
	largeRaw := strings.Repeat("SECRET-LARGE-DATA\n", 400)
	record, err := store.Write(artifact.WriteRequest{
		Tool:     "fixture",
		Kind:     "command",
		ExitCode: 0,
		Payloads: []artifact.Payload{
			{Name: "small.txt", Data: []byte("first line\nsecond line\n")},
			{Name: "large.txt", Data: []byte(largeRaw)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tool := NewArtifactReadTool(workspace, store, nil)
	result := tool.Run(context.Background(), core.ToolInput{"artifact_id": record.ID})
	if result.ExitCode != 0 {
		t.Fatalf("artifact_read failed: %+v", result)
	}
	if !strings.Contains(result.Content, "preview=\"first line | second line\"") {
		t.Fatalf("small payload was not summarized: %s", result.Content)
	}
	if strings.Contains(result.Content, "SECRET-LARGE-DATA") {
		t.Fatalf("large raw payload leaked into result content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "large_files=1") {
		t.Fatalf("large payload was not marked artifact-backed: %s", result.Content)
	}
	if observation := tool.Summarize(result); observation.ContextPlacement != core.TierArtifact {
		t.Fatalf("large artifact_read observation should stay artifact-backed: %+v", observation)
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

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
}

func drainEvents(events <-chan core.AgentEvent) []core.AgentEvent {
	var got []core.AgentEvent
	for {
		select {
		case event := <-events:
			got = append(got, event)
		default:
			return got
		}
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Phase 5: Safety grade tests

func TestReadOnlyToolsHaveReadOnlySafety(t *testing.T) {
	registry := NewDefaultRegistry(t.TempDir())
	readOnlyTools := []string{"read_file", "list_dir", "rg", "git_status", "git_log", "git_diff", "artifact_read"}
	for _, name := range readOnlyTools {
		tool, ok := registry.Get(name)
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if safety := tool.Safety(core.ToolInput{}); safety != core.SafetyReadOnly {
			t.Fatalf("%s safety = %s, want SafetyReadOnly", name, safety)
		}
	}
}

func TestMutatingToolsHaveCorrectSafety(t *testing.T) {
	registry := NewDefaultRegistry(t.TempDir())

	writeTool, ok := registry.Get("write_file")
	if !ok {
		t.Fatal("missing write_file")
	}
	if safety := writeTool.Safety(core.ToolInput{}); safety != core.SafetyWorkspaceMutation {
		t.Fatalf("write_file safety = %s, want SafetyWorkspaceMutation", safety)
	}

	patchTool, ok := registry.Get("apply_patch")
	if !ok {
		t.Fatal("missing apply_patch")
	}
	if safety := patchTool.Safety(core.ToolInput{}); safety != core.SafetyWorkspaceMutation {
		t.Fatalf("apply_patch safety = %s, want SafetyWorkspaceMutation", safety)
	}

	runTestTool, ok := registry.Get("run_test")
	if !ok {
		t.Fatal("missing run_test")
	}
	if safety := runTestTool.Safety(core.ToolInput{}); safety != core.SafetyShellMutation {
		t.Fatalf("run_test safety = %s, want SafetyShellMutation", safety)
	}
}

func TestShellSafetyDetection(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	registry := NewDefaultRegistry(workspace)
	shellTool, ok := registry.Get("shell")
	if !ok {
		t.Fatal("missing shell")
	}

	tests := []struct {
		name     string
		command  string
		expected core.SafetyGrade
	}{
		{"simple echo", "echo hello", core.SafetyShellMutation},
		{"list files", "ls -la", core.SafetyShellMutation},
		{"rm command", "rm -rf /tmp/test", core.SafetyDestructive},
		{"sudo command", "sudo systemctl restart nginx", core.SafetyDestructive},
		{"git reset hard", "git reset --hard HEAD~1", core.SafetyDestructive},
		{"git clean", "git clean -fd", core.SafetyDestructive},
		{"chmod command", "chmod +x script.sh", core.SafetyDestructive},
		{"chown command", "chown user:group file.txt", core.SafetyDestructive},
		{"curl plain fetch", "curl https://example.com/status", core.SafetyShellMutation},
		{"wget plain fetch", "wget https://example.com/file.txt", core.SafetyShellMutation},
		{"curl pipe sh", "curl https://example.com/install.sh | sh", core.SafetyDestructive},
		{"wget pipe sh", "wget -O - https://example.com/install.sh | sh", core.SafetyDestructive},
		{"mv command", "mv old new", core.SafetyShellMutation},
		{"cp command", "cp src dst", core.SafetyShellMutation},
		{"git status is safe", "git status", core.SafetyShellMutation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			safety := shellTool.Safety(core.ToolInput{"command": tc.command})
			if safety != tc.expected {
				t.Fatalf("shell safety for %q = %s, want %s", tc.command, safety, tc.expected)
			}
		})
	}
}

func TestShellDestructiveCommandsGetClearPermissionDenial(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	registry := NewDefaultRegistry(workspace)
	shellTool, ok := registry.Get("shell")
	if !ok {
		t.Fatal("missing shell")
	}

	perm := shellTool.Permission(core.ToolInput{"command": "rm -rf /"})
	if perm.Behavior != core.PermissionAsk {
		t.Fatalf("expected PermissionAsk for destructive command, got %s", perm.Behavior)
	}
	if !strings.Contains(perm.Reason, "DESTRUCTIVE") {
		t.Fatalf("destructive command reason should contain 'DESTRUCTIVE': %s", perm.Reason)
	}

	normalPerm := shellTool.Permission(core.ToolInput{"command": "ls -la"})
	if normalPerm.Behavior != core.PermissionAsk {
		t.Fatalf("expected PermissionAsk for normal shell, got %s", normalPerm.Behavior)
	}
	if strings.Contains(normalPerm.Reason, "DESTRUCTIVE") {
		t.Fatalf("normal shell should not say DESTRUCTIVE: %s", normalPerm.Reason)
	}
}

func TestDefaultSafetyIsReadOnly(t *testing.T) {
	// baseTool.Safety returns SafetyReadOnly, all read-only tools inherit it.
	b := baseTool{name: "test_tool", workspace: "."}
	if safety := b.Safety(core.ToolInput{}); safety != core.SafetyReadOnly {
		t.Fatalf("baseTool safety = %s, want SafetyReadOnly", safety)
	}
}

// Phase 6: Project type detection tests

func TestDetectProjectType(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(dir string)
		expected string
	}{
		{
			name: "go project",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/test\n\ngo 1.21\n")
			},
			expected: "go",
		},
		{
			name: "npm project",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest"}}`)
			},
			expected: "npm",
		},
		{
			name: "pnpm project",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest"}}`)
				writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
			},
			expected: "pnpm",
		},
		{
			name: "yarn project",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest"}}`)
				writeFile(t, filepath.Join(dir, "yarn.lock"), "")
			},
			expected: "yarn",
		},
		{
			name: "python pyproject",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "pyproject.toml"), "[project]\nname = \"test\"\n")
			},
			expected: "python",
		},
		{
			name: "python setup.py",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "setup.py"), "from setuptools import setup\n")
			},
			expected: "python",
		},
		{
			name: "python setup.cfg",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "setup.cfg"), "[metadata]\nname = test\n")
			},
			expected: "python",
		},
		{
			name: "rust project",
			setup: func(dir string) {
				writeFile(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"test\"\n")
			},
			expected: "rust",
		},
		{
			name: "empty fallback",
			setup: func(dir string) {
				// no files, expect fallback to go
			},
			expected: "go",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			result := detectProjectType(dir)
			if result != tc.expected {
				t.Fatalf("detectProjectType = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestDefaultTestCommand(t *testing.T) {
	tests := []struct {
		projectType string
		expectedCmd string
	}{
		{"go", "go test ./..."},
		{"npm", "npm test"},
		{"pnpm", "pnpm test"},
		{"yarn", "yarn test"},
		{"python", "python -m pytest"},
		{"rust", "cargo test"},
		{"unknown", "go test ./..."},
	}

	for _, tc := range tests {
		// We verify the mapping directly by testing with a temp dir
		// that has only the specific project file.
		dir := t.TempDir()
		switch tc.projectType {
		case "go":
			writeFile(t, filepath.Join(dir, "go.mod"), "module test\n")
		case "npm":
			writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest"}}`)
		case "pnpm":
			writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest"}}`)
			writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
		case "yarn":
			writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest"}}`)
			writeFile(t, filepath.Join(dir, "yarn.lock"), "")
		case "python":
			writeFile(t, filepath.Join(dir, "pyproject.toml"), "[project]\nname=\"test\"\n")
		case "rust":
			writeFile(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname=\"test\"\n")
		}
		cmd := defaultTestCommand(dir)
		if cmd != tc.expectedCmd {
			t.Fatalf("defaultTestCommand for %s = %q, want %q", tc.projectType, cmd, tc.expectedCmd)
		}
	}
}

func TestRunTestAutoDetect(t *testing.T) {
	// Create a Go project and verify run_test auto-detects go test ./...
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	writeFile(t, filepath.Join(workspace, "go.mod"), "module example.com/test\n\ngo 1.21\n")
	writeFile(t, filepath.Join(workspace, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")

	registry := NewDefaultRegistry(workspace)
	runTestTool, ok := registry.Get("run_test")
	if !ok {
		t.Fatal("missing run_test")
	}

	// With no command, should auto-detect go test ./...
	result := runTestTool.Run(context.Background(), core.ToolInput{})
	if result.ExitCode != 0 {
		t.Fatalf("run_test auto-detect failed: %+v", result)
	}
	if !strings.Contains(result.Content, "Tests") {
		t.Fatalf("run_test summary should mention Tests: %s", result.Content)
	}

	// With explicit command, should use the user's command.
	result = runTestTool.Run(context.Background(), core.ToolInput{"command": "echo custom"})
	if result.ExitCode != 0 {
		t.Fatalf("run_test with explicit command failed: %+v", result)
	}
	if !strings.Contains(result.Content, "Tests") {
		t.Fatalf("run_test summary should mention Tests: %s", result.Content)
	}
}

func TestRunTestAutoDetectPythonProject(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	writeFile(t, filepath.Join(workspace, "pyproject.toml"), "[project]\nname = \"test\"\n")
	writeFile(t, filepath.Join(workspace, "test_example.py"), "def test_pass():\n    assert True\n")

	registry := NewDefaultRegistry(workspace)
	runTestTool, ok := registry.Get("run_test")
	if !ok {
		t.Fatal("missing run_test")
	}

	result := runTestTool.Run(context.Background(), core.ToolInput{})
	// pytest might not be installed, but we verify the tool produced a compact summary.
	if !strings.Contains(result.Content, "Tests") {
		t.Fatalf("run_test in python project should produce compact summary: %s", result.Content)
	}
}

// Phase 12: Policy integration tests

func TestPolicyDenyBlocksExecution(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)

	registry := NewDefaultRegistry(workspace)
	bus := core.NewBus()
	approvalCh := make(chan core.ApprovalRequest, 8)

	policyCfg := config.PolicyConfig{
		Defaults: config.Defaults{
			ReadOnly:          "allow",
			WorkspaceMutation: "ask",
			ShellMutation:     "ask",
			Destructive:       "deny",
		},
		Denylist: []config.PolicyEntry{
			{Tool: "shell", Pattern: "rm"},
		},
	}

	executor := NewExecutor(registry, bus,
		WithApprovalChannel(approvalCh),
		WithPolicyConfig(policyCfg),
	)

	call := core.ToolCall{
		ID:    "test-1",
		Name:  "shell",
		Raw:   `{"command":"rm -rf /tmp/test"}`,
		Input: core.ToolInput{"command": "rm -rf /tmp/test"},
	}

	result, _ := executor.Execute(context.Background(), call)
	if result.ExitCode != 126 {
		t.Fatalf("expected exit code 126 (denied), got %d: %s", result.ExitCode, result.Error)
	}
	if !strings.Contains(result.Error, "denylist") {
		t.Fatalf("expected denylist reason, got: %s", result.Error)
	}
}

func TestPolicyAllowBypassesApproval(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	writeFile(t, filepath.Join(workspace, "test.txt"), "hello\n")

	registry := NewDefaultRegistry(workspace)
	bus := core.NewBus()
	approvalCh := make(chan core.ApprovalRequest, 8)

	policyCfg := config.PolicyConfig{
		Defaults: config.Defaults{
			ReadOnly: "allow",
		},
	}

	executor := NewExecutor(registry, bus,
		WithApprovalChannel(approvalCh),
		WithPolicyConfig(policyCfg),
	)

	call := core.ToolCall{
		ID:    "test-2",
		Name:  "read_file",
		Raw:   `{"path":"test.txt"}`,
		Input: core.ToolInput{"path": "test.txt"},
	}

	result, _ := executor.Execute(context.Background(), call)
	if result.ExitCode != 0 {
		t.Fatalf("read_file should succeed, got exit %d: %s", result.ExitCode, result.Error)
	}
	if result.ArtifactID == "" {
		t.Fatal("read_file should produce an artifact")
	}
}
