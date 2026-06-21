package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestIsGitRepo(t *testing.T) {
	if IsGitRepo(t.TempDir()) {
		t.Fatal("empty dir should not be a git repo")
	}
	if !IsGitRepo(gitRepo(t)) {
		t.Fatal("initialized repo should be detected")
	}
}

func TestCreateDiffRemove(t *testing.T) {
	repo := gitRepo(t)

	wt, err := Create(repo, "my-task")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(filepath.Base(wt.Path), "my-task") {
		t.Logf("worktree path: %s", wt.Path)
	}
	// Seed file must be present (checked out at HEAD).
	if _, err := os.Stat(filepath.Join(wt.Path, "seed.txt")); err != nil {
		t.Fatalf("seed file missing in worktree: %v", err)
	}

	// Make changes in the worktree: a new file and a modification.
	if err := os.WriteFile(filepath.Join(wt.Path, "new.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "seed.txt"), []byte("seed\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := wt.Diff()
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "new.go") || !strings.Contains(diff, "package x") {
		t.Fatalf("diff missing new file: %q", diff)
	}
	if !strings.Contains(diff, "more") {
		t.Fatalf("diff missing modification: %q", diff)
	}

	// Parent tree must be untouched.
	if _, err := os.Stat(filepath.Join(repo, "new.go")); !os.IsNotExist(err) {
		t.Fatal("worktree change leaked into the parent repo")
	}
	parentSeed, _ := os.ReadFile(filepath.Join(repo, "seed.txt"))
	if string(parentSeed) != "seed\n" {
		t.Fatalf("parent seed.txt was modified: %q", parentSeed)
	}

	if err := wt.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("worktree dir still exists after Remove")
	}
}

func TestCreateNonRepoFails(t *testing.T) {
	if _, err := Create(t.TempDir(), "x"); err == nil {
		t.Fatal("Create on a non-repo should fail")
	}
}
