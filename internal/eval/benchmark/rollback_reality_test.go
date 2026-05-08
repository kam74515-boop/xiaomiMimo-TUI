package benchmark

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
	"mimo-tui/internal/tools"
)

// TestRollbackRealityMutatingToolCreatesSnapshot verifies that running a
// mutating tool (write_file) through the executor creates a rollback artifact
// containing the pre-state git diff.
func TestRollbackRealityMutatingToolCreatesSnapshot(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	// Create and commit a file.
	path := filepath.Join(workspace, "hello.txt")
	rollbackWriteFile(t, path, "original content\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial commit")

	// Modify the file to create dirty state (pre-tool diff).
	rollbackWriteFile(t, path, "original content\nmodified by user\n")

	// Set up executor with artifact store.
	store := artifact.NewStore(workspace)
	registry := tools.NewDefaultRegistry(workspace)
	executor := tools.NewExecutor(registry, nil,
		tools.WithArtifactStore(store, workspace),
		tools.WithAllowedAskTools("write_file"),
	)

	// Run write_file through executor.
	result, observation := executor.Execute(context.Background(), core.ToolCall{
		ID:   "call-write-1",
		Name: "write_file",
		Input: core.ToolInput{
			"path":    "hello.txt",
			"content": "new content from tool\n",
		},
	})
	if result.ExitCode != 0 {
		t.Fatalf("write_file failed: %+v", result)
	}

	// Verify the tool created a rollback artifact.
	if observation.RollbackArtifactID == "" {
		t.Fatal("write_file should have created a rollback artifact")
	}

	// Verify the rollback artifact exists and has the correct kind.
	rollbackMeta, err := store.ReadMetadata(observation.RollbackArtifactID)
	if err != nil {
		t.Fatalf("read rollback metadata: %v", err)
	}
	if rollbackMeta.Kind != "rollback" {
		t.Fatalf("rollback kind = %q, want rollback", rollbackMeta.Kind)
	}
	if rollbackMeta.Tool != "write_file" {
		t.Fatalf("rollback tool = %q, want write_file", rollbackMeta.Tool)
	}

	// Verify the rollback artifact contains the pre-state diff.
	payloads, err := store.ReadPayloads(observation.RollbackArtifactID)
	if err != nil {
		t.Fatalf("read rollback payloads: %v", err)
	}
	var hasPreState bool
	for _, p := range payloads {
		if p.Metadata.Name == "pre_state" {
			hasPreState = true
			diff := string(p.Data)
			// The diff should show the dirty state (user modification).
			if len(p.Data) > 0 && !strings.Contains(diff, "modified by user") {
				t.Fatalf("pre_state diff = %q, expected to contain 'modified by user'", diff)
			}
		}
	}
	if !hasPreState {
		t.Fatal("rollback artifact missing pre_state payload")
	}

	t.Logf("Rollback artifact created: id=%s diff_size=%d",
		observation.RollbackArtifactID, len(payloads[0].Data))
}

// TestRollbackRealityNonMutatingNoSnapshot verifies that running a
// read-only tool (read_file) through the executor does NOT create a
// rollback artifact.
func TestRollbackRealityNonMutatingNoSnapshot(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	path := filepath.Join(workspace, "hello.txt")
	rollbackWriteFile(t, path, "content\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial")

	// Set up executor with artifact store.
	store := artifact.NewStore(workspace)
	registry := tools.NewDefaultRegistry(workspace)
	executor := tools.NewExecutor(registry, nil,
		tools.WithArtifactStore(store, workspace),
	)

	// Run read_file through executor.
	result, observation := executor.Execute(context.Background(), core.ToolCall{
		ID:    "call-read-1",
		Name:  "read_file",
		Input: core.ToolInput{"path": "hello.txt"},
	})
	if result.ExitCode != 0 {
		t.Fatalf("read_file failed: %+v", result)
	}

	// Verify NO rollback artifact was created.
	if observation.RollbackArtifactID != "" {
		t.Fatalf("read_file should NOT create rollback artifact, got %q", observation.RollbackArtifactID)
	}

	// Verify no rollback artifacts exist in the store.
	rollbacks, err := artifact.ListRollbacks(workspace)
	if err != nil {
		t.Fatalf("ListRollbacks: %v", err)
	}
	if len(rollbacks) != 0 {
		t.Fatalf("expected 0 rollback artifacts, got %d", len(rollbacks))
	}
}

// TestRollbackRealityDryRunShowsChanges verifies that dry-run rollback
// shows what would change without modifying files.
func TestRollbackRealityDryRunShowsChanges(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	path := filepath.Join(workspace, "file.txt")
	rollbackWriteFile(t, path, "original\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial")

	// Modify to create dirty state.
	rollbackWriteFile(t, path, "original\nadded line\n")

	// Capture diff and create rollback artifact.
	diffData := rollbackGitDiff(t, workspace)
	store := artifact.NewStore(workspace)
	record, err := store.Write(artifact.WriteRequest{
		Tool:     "write_file",
		Kind:     "rollback",
		ExitCode: 1,
		Inputs:   map[string]any{"tool": "write_file"},
		Payloads: []artifact.Payload{
			{Name: "pre_state.diff", Data: diffData},
			{Name: "stderr.txt", Data: []byte{}},
		},
	})
	if err != nil {
		t.Fatalf("write rollback artifact: %v", err)
	}

	// Verify file content before dry-run.
	contentBefore := rollbackReadFile(t, path)
	if !strings.Contains(contentBefore, "added line") {
		t.Fatalf("file should contain 'added line' before dry-run: %q", contentBefore)
	}

	// Run dry-run rollback.
	output, err := artifact.ApplyRollback(workspace, record.ID, true)
	if err != nil {
		t.Fatalf("ApplyRollback dry-run: %v", err)
	}
	if !strings.Contains(output, "dry-run") {
		t.Fatalf("dry-run output = %q, expected to contain 'dry-run'", output)
	}

	// Verify file is NOT modified by dry-run.
	contentAfter := rollbackReadFile(t, path)
	if contentAfter != contentBefore {
		t.Fatalf("dry-run modified file: before=%q, after=%q", contentBefore, contentAfter)
	}
}

// TestRollbackRealityConfirmRestoresFile verifies that a confirmed rollback
// restores the file from a dirty state back to the committed state. The
// rollback artifact captures the pre-state diff; applying it reverses those
// changes.
func TestRollbackRealityConfirmRestoresFile(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	path := filepath.Join(workspace, "file.txt")
	rollbackWriteFile(t, path, "original content\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial")

	// Modify to create dirty state (simulating user edits before tool runs).
	rollbackWriteFile(t, path, "original content\nmodified by user\n")

	// Capture diff and create rollback artifact (what the executor does).
	diffData := rollbackGitDiff(t, workspace)
	if len(diffData) == 0 {
		t.Fatal("expected non-empty diff after file modification")
	}
	store := artifact.NewStore(workspace)
	record, err := store.Write(artifact.WriteRequest{
		Tool:     "write_file",
		Kind:     "rollback",
		ExitCode: 1,
		Inputs:   map[string]any{"tool": "write_file"},
		Payloads: []artifact.Payload{
			{Name: "pre_state.diff", Data: diffData},
			{Name: "stderr.txt", Data: []byte{}},
		},
	})
	if err != nil {
		t.Fatalf("write rollback artifact: %v", err)
	}

	// Verify file is still in dirty state before rollback.
	contentBefore := rollbackReadFile(t, path)
	if !strings.Contains(contentBefore, "modified by user") {
		t.Fatalf("file should be dirty before rollback: %q", contentBefore)
	}

	// Apply the rollback (restores to committed state).
	output, err := artifact.ApplyRollback(workspace, record.ID, false)
	if err != nil {
		t.Fatalf("ApplyRollback: %v", err)
	}
	if !strings.Contains(output, "applied") {
		t.Fatalf("apply output = %q, expected 'applied'", output)
	}

	// Verify file was restored to committed state.
	restored := rollbackReadFile(t, path)
	if restored != "original content\n" {
		t.Fatalf("file not restored to committed state: %q", restored)
	}
}

// TestRollbackRealityFullFlow tests the complete rollback lifecycle:
// commit -> dirty state -> tool runs on different file (captures rollback) ->
// rollback restores the dirty file. The tool writes to a different file so
// the reverse diff can be applied cleanly.
func TestRollbackRealityFullFlow(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	// Step 1: Create and commit two files.
	dirtyPath := filepath.Join(workspace, "dirty.go")
	otherPath := filepath.Join(workspace, "other.go")
	rollbackWriteFile(t, dirtyPath, "package main\n\nfunc main() {}\n")
	rollbackWriteFile(t, otherPath, "package util\n\nfunc Helper() {}\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial commit")

	// Step 2: User makes local edits to dirty.go (dirty state).
	rollbackWriteFile(t, dirtyPath, "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n")

	// Step 3: Set up executor and run write_file on OTHER file.
	// The executor captures git diff (which shows dirty.go changes) before the tool runs.
	store := artifact.NewStore(workspace)
	registry := tools.NewDefaultRegistry(workspace)
	executor := tools.NewExecutor(registry, nil,
		tools.WithArtifactStore(store, workspace),
		tools.WithAllowedAskTools("write_file"),
	)

	result, observation := executor.Execute(context.Background(), core.ToolCall{
		ID:   "call-write-flow",
		Name: "write_file",
		Input: core.ToolInput{
			"path":    "other.go",
			"content": "package util\n\n// Agent's rewrite\nfunc Helper() { run() }\n",
		},
	})
	if result.ExitCode != 0 {
		t.Fatalf("write_file failed: %+v", result)
	}

	// Step 4: Verify rollback artifact was created.
	rollbackID := observation.RollbackArtifactID
	if rollbackID == "" {
		t.Fatal("expected rollback artifact ID")
	}

	// Step 5: List rollbacks and verify.
	rollbacks, err := artifact.ListRollbacks(workspace)
	if err != nil {
		t.Fatalf("ListRollbacks: %v", err)
	}
	if len(rollbacks) != 1 {
		t.Fatalf("expected 1 rollback, got %d", len(rollbacks))
	}
	if rollbacks[0].ID != rollbackID {
		t.Fatalf("rollback ID mismatch: got %s, want %s", rollbacks[0].ID, rollbackID)
	}

	// Step 6: Show rollback (dry-run).
	showOutput, err := artifact.ShowRollback(workspace, rollbackID)
	if err != nil {
		t.Fatalf("ShowRollback: %v", err)
	}
	if showOutput == "" {
		t.Fatal("ShowRollback returned empty")
	}
	t.Logf("ShowRollback: %s", showOutput)

	// Step 7: Verify dirty.go still has user edits before rollback.
	preRollback := rollbackReadFile(t, dirtyPath)
	if !strings.Contains(preRollback, "fmt.Println") {
		t.Fatalf("dirty.go should still have user edits: %q", preRollback)
	}

	// Step 8: Apply rollback to restore dirty.go to committed state.
	applyOutput, err := artifact.ApplyRollback(workspace, rollbackID, false)
	if err != nil {
		t.Fatalf("ApplyRollback: %v", err)
	}
	t.Logf("ApplyRollback: %s", applyOutput)

	// Step 9: Verify dirty.go restored to committed state (user edits removed).
	restored := rollbackReadFile(t, dirtyPath)
	if restored != "package main\n\nfunc main() {}\n" {
		t.Fatalf("dirty.go not restored to committed state: %q", restored)
	}

	// Step 10: Verify other.go still has agent's changes (not rolled back).
	otherContent := rollbackReadFile(t, otherPath)
	if !strings.Contains(otherContent, "Agent's rewrite") {
		t.Fatalf("other.go should still have agent's content: %q", otherContent)
	}
}

// TestRollbackRealityEmptyDiff verifies that a clean workspace produces
// an empty rollback diff, and applying it is a no-op.
func TestRollbackRealityEmptyDiff(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	path := filepath.Join(workspace, "file.txt")
	rollbackWriteFile(t, path, "content\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial")

	// Workspace is clean -- no dirty state.
	store := artifact.NewStore(workspace)
	registry := tools.NewDefaultRegistry(workspace)
	executor := tools.NewExecutor(registry, nil,
		tools.WithArtifactStore(store, workspace),
		tools.WithAllowedAskTools("write_file"),
	)

	result, observation := executor.Execute(context.Background(), core.ToolCall{
		ID:   "call-clean",
		Name: "write_file",
		Input: core.ToolInput{
			"path":    "file.txt",
			"content": "new content\n",
		},
	})
	if result.ExitCode != 0 {
		t.Fatalf("write_file failed: %+v", result)
	}

	// Rollback artifact should still be created (even with empty diff).
	rollbackID := observation.RollbackArtifactID
	if rollbackID == "" {
		t.Fatal("expected rollback artifact even with clean workspace")
	}

	// Show should indicate empty diff.
	showOutput, err := artifact.ShowRollback(workspace, rollbackID)
	if err != nil {
		t.Fatalf("ShowRollback: %v", err)
	}
	if !strings.Contains(showOutput, "empty") {
		t.Fatalf("ShowRollback = %q, expected 'empty'", showOutput)
	}

	// Apply should indicate nothing to rollback.
	applyOutput, err := artifact.ApplyRollback(workspace, rollbackID, false)
	if err != nil {
		t.Fatalf("ApplyRollback: %v", err)
	}
	if !strings.Contains(applyOutput, "empty") {
		t.Fatalf("ApplyRollback = %q, expected 'empty'", applyOutput)
	}
}

// TestRollbackRealityMultipleMutatingTools verifies that each mutating tool
// call creates its own rollback artifact.
func TestRollbackRealityMultipleMutatingTools(t *testing.T) {
	workspace := t.TempDir()
	rollbackGitInit(t, workspace)

	rollbackWriteFile(t, filepath.Join(workspace, "a.txt"), "a\n")
	rollbackWriteFile(t, filepath.Join(workspace, "b.txt"), "b\n")
	rollbackRun(t, workspace, "git", "add", ".")
	rollbackRun(t, workspace, "git", "commit", "-m", "initial")

	// Make some dirty changes.
	rollbackWriteFile(t, filepath.Join(workspace, "a.txt"), "a\nchanged\n")

	store := artifact.NewStore(workspace)
	registry := tools.NewDefaultRegistry(workspace)
	executor := tools.NewExecutor(registry, nil,
		tools.WithArtifactStore(store, workspace),
		tools.WithAllowedAskTools("write_file"),
	)

	// First mutating tool call.
	_, obs1 := executor.Execute(context.Background(), core.ToolCall{
		ID: "call-1", Name: "write_file",
		Input: core.ToolInput{"path": "a.txt", "content": "a\nfrom tool 1\n"},
	})
	if obs1.RollbackArtifactID == "" {
		t.Fatal("first write_file should create rollback artifact")
	}

	// Second mutating tool call.
	_, obs2 := executor.Execute(context.Background(), core.ToolCall{
		ID: "call-2", Name: "write_file",
		Input: core.ToolInput{"path": "b.txt", "content": "b\nfrom tool 2\n"},
	})
	if obs2.RollbackArtifactID == "" {
		t.Fatal("second write_file should create rollback artifact")
	}

	// Both rollback artifacts should be distinct.
	if obs1.RollbackArtifactID == obs2.RollbackArtifactID {
		t.Fatal("rollback artifact IDs should be distinct")
	}

	// List should show 2 rollbacks.
	rollbacks, err := artifact.ListRollbacks(workspace)
	if err != nil {
		t.Fatalf("ListRollbacks: %v", err)
	}
	if len(rollbacks) != 2 {
		t.Fatalf("expected 2 rollbacks, got %d", len(rollbacks))
	}
}

// Helpers

func rollbackGitInit(t *testing.T, dir string) {
	t.Helper()
	rollbackRun(t, dir, "git", "init")
	rollbackRun(t, dir, "git", "config", "user.email", "test@test.com")
	rollbackRun(t, dir, "git", "config", "user.name", "test")
}

func rollbackRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
}

func rollbackWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rollbackReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func rollbackGitDiff(t *testing.T, dir string) []byte {
	t.Helper()
	cmd := exec.Command("git", "diff")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return out
		}
		t.Fatalf("git diff: %v", err)
	}
	return out
}
