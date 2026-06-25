package artifact

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollbackListShowApply(t *testing.T) {
	// Set up a temporary git workspace.
	dir := t.TempDir()
	gitInit(t, dir)

	// Create initial commit.
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("original content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitAddCommit(t, dir, path, "initial commit")

	// Modify file to create dirty state (simulating pre-tool diff).
	if err := os.WriteFile(path, []byte("original content\nmodified content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Generate diff.
	diffData := gitDiff(t, dir)
	if len(diffData) == 0 {
		t.Fatal("expected non-empty diff after file modification")
	}

	// Write a rollback artifact.
	store := NewStore(dir)
	record, err := store.Write(WriteRequest{
		Tool:     "shell",
		Kind:     "rollback",
		ExitCode: 1,
		Inputs:   map[string]any{"tool": "shell"},
		Payloads: []Payload{
			{Name: "pre_state.diff", Data: diffData},
			{Name: "stderr.txt", Data: []byte{}},
		},
	})
	if err != nil {
		t.Fatalf("write rollback artifact: %v", err)
	}

	// ---- List ----
	rollbacks, err := ListRollbacks(dir)
	if err != nil {
		t.Fatalf("ListRollbacks: %v", err)
	}
	if len(rollbacks) != 1 {
		t.Fatalf("expected 1 rollback, got %d", len(rollbacks))
	}
	if rollbacks[0].ID != record.ID {
		t.Fatalf("rollback ID mismatch: got %s, want %s", rollbacks[0].ID, record.ID)
	}
	if rollbacks[0].Tool != "shell" {
		t.Fatalf("rollback tool mismatch: got %s, want shell", rollbacks[0].Tool)
	}
	if rollbacks[0].DiffSize == 0 {
		t.Fatal("expected non-zero diff size")
	}
	t.Logf("ListRollbacks OK: id=%s tool=%s size=%d", rollbacks[0].ID, rollbacks[0].Tool, rollbacks[0].DiffSize)

	// ---- Show ----
	showOutput, err := ShowRollback(dir, record.ID)
	if err != nil {
		t.Fatalf("ShowRollback: %v", err)
	}
	if showOutput == "" {
		t.Fatal("ShowRollback returned empty output")
	}
	t.Logf("ShowRollback OK: %s", showOutput)

	// ---- Dry-run Apply ----
	dryOutput, err := ApplyRollback(dir, record.ID, true)
	if err != nil {
		t.Fatalf("ApplyRollback (dry-run): %v", err)
	}
	if dryOutput == "" {
		t.Fatal("ApplyRollback dry-run returned empty output")
	}
	t.Logf("ApplyRollback dry-run OK: %s", dryOutput)

	// Verify file is NOT modified by dry-run.
	contentAfterDry, _ := os.ReadFile(path)
	if string(contentAfterDry) != "original content\nmodified content\n" {
		t.Fatalf("dry-run modified file unexpectedly: %q", string(contentAfterDry))
	}

	// ---- Actual Apply ----
	applyOutput, err := ApplyRollback(dir, record.ID, false)
	if err != nil {
		t.Fatalf("ApplyRollback: %v", err)
	}
	t.Logf("ApplyRollback OK: %s", applyOutput)

	// Verify file WAS modified by apply (restored to original).
	contentAfterApply, _ := os.ReadFile(path)
	if string(contentAfterApply) != "original content\n" {
		t.Fatalf("apply did not restore file: got %q, want %q", string(contentAfterApply), "original content\n")
	}

	// ---- Record apply ----
	recordedID, err := RecordRollbackApply(dir, record.ID)
	if err != nil {
		t.Fatalf("RecordRollbackApply: %v", err)
	}
	if recordedID == "" {
		t.Fatal("RecordRollbackApply returned empty ID")
	}
	t.Logf("RecordRollbackApply OK: %s", recordedID)

	// Verify the record artifact exists.
	_, err = store.ReadMetadata(recordedID)
	if err != nil {
		t.Fatalf("record artifact not readable: %v", err)
	}

	// ---- Empty diff rollback ----
	emptyRecord, err := store.Write(WriteRequest{
		Tool:     "shell",
		Kind:     "rollback",
		ExitCode: 0,
		Inputs:   map[string]any{"tool": "shell"},
		Payloads: []Payload{
			{Name: "pre_state.diff", Data: []byte{}},
			{Name: "stderr.txt", Data: []byte{}},
		},
	})
	if err != nil {
		t.Fatalf("write empty rollback: %v", err)
	}

	showEmpty, err := ShowRollback(dir, emptyRecord.ID)
	if err != nil {
		t.Fatalf("ShowRollback empty: %v", err)
	}
	if showEmpty != "(empty diff — nothing to rollback)" {
		t.Fatalf("unexpected empty show output: %q", showEmpty)
	}

	applyEmpty, err := ApplyRollback(dir, emptyRecord.ID, false)
	if err != nil {
		t.Fatalf("ApplyRollback empty: %v", err)
	}
	if applyEmpty != "(empty diff — nothing to rollback)" {
		t.Fatalf("unexpected empty apply output: %q", applyEmpty)
	}
	t.Log("Empty diff rollback OK")
}

func TestRollbackDeletesCreatedFiles(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	seed := filepath.Join(dir, "seed.txt")
	if err := os.WriteFile(seed, []byte("seed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitAddCommit(t, dir, seed, "init")

	// Snapshot records the exact file the tool created (empty tracked diff).
	store := NewStore(dir)
	record, err := store.Write(WriteRequest{
		Tool: "write_file", Kind: "rollback", ExitCode: 0,
		Inputs: map[string]any{"tool": "write_file"},
		Payloads: []Payload{
			{Name: "pre_state.diff", Data: []byte{}},
			{Name: "created_files.txt", Data: []byte("created.go")},
		},
	})
	if err != nil {
		t.Fatalf("write rollback: %v", err)
	}

	// The created (untracked) file is present on disk.
	created := filepath.Join(dir, "created.go")
	if err := os.WriteFile(created, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Dry-run should report the deletion without performing it.
	dry, err := ApplyRollback(dir, record.ID, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(dry, "created.go") {
		t.Fatalf("dry-run should mention the created file: %q", dry)
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatal("dry-run must not delete the file")
	}

	// Apply: the created file must be removed.
	out, err := ApplyRollback(dir, record.ID, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !strings.Contains(out, "deleted") {
		t.Fatalf("apply output should note deletion: %q", out)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatal("rollback did not delete the tool-created file")
	}
	// The seed (pre-existing) must be untouched.
	if _, err := os.Stat(seed); err != nil {
		t.Fatal("rollback wrongly removed a pre-existing file")
	}
}

func TestListRollbacksEmpty(t *testing.T) {
	dir := t.TempDir()
	rollbacks, err := ListRollbacks(dir)
	if err != nil {
		t.Fatalf("ListRollbacks empty dir: %v", err)
	}
	if len(rollbacks) != 0 {
		t.Fatalf("expected 0 rollbacks in empty dir, got %d", len(rollbacks))
	}
}

func TestListRollbacksSkipsNonRollback(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Write a non-rollback artifact.
	_, err := store.Write(WriteRequest{
		Tool:     "read_file",
		Kind:     "content",
		ExitCode: 0,
		Inputs:   map[string]any{"path": "test.txt"},
		Payloads: []Payload{
			{Name: "content.txt", Data: []byte("hello")},
		},
	})
	if err != nil {
		t.Fatalf("write non-rollback: %v", err)
	}

	rollbacks, err := ListRollbacks(dir)
	if err != nil {
		t.Fatalf("ListRollbacks: %v", err)
	}
	if len(rollbacks) != 0 {
		t.Fatalf("expected 0 rollbacks (only non-rollback exists), got %d", len(rollbacks))
	}
}

func TestReadRollbackDiffErrors(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Write a non-rollback artifact.
	record, err := store.Write(WriteRequest{
		Tool:     "read_file",
		Kind:     "content",
		ExitCode: 0,
		Inputs:   map[string]any{"path": "test.txt"},
		Payloads: []Payload{
			{Name: "content.txt", Data: []byte("hello")},
		},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// ShowRollback should fail for non-rollback artifact.
	_, err = ShowRollback(dir, record.ID)
	if err == nil {
		t.Fatal("expected error for non-rollback artifact")
	}

	// ApplyRollback should fail for non-rollback artifact.
	_, err = ApplyRollback(dir, record.ID, false)
	if err == nil {
		t.Fatal("expected error for non-rollback artifact")
	}
}

// Helpers

func gitInit(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "test")
}

func gitAddCommit(t *testing.T, dir, path, msg string) {
	t.Helper()
	runCmd(t, dir, "git", "add", path)
	runCmd(t, dir, "git", "commit", "-m", msg)
}

func gitDiff(t *testing.T, dir string) []byte {
	t.Helper()
	cmd := exec.Command("git", "diff")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// git diff exits 1 when there are changes.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return out
		}
		t.Fatalf("git diff: %v", err)
	}
	return out
}

func runCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmd %v: %v\n%s", args, err, string(out))
	}
}
