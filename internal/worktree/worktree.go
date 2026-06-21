// Package worktree provides isolated git worktrees so a delegated sub-agent can
// make file changes without touching the parent's working tree. The parent
// reviews the resulting diff and merges deliberately — delegation stays
// controllable and reversible, consistent with the project's philosophy.
package worktree

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Worktree is an isolated checkout rooted at Path, backed by the repo at
// repoRoot via `git worktree`.
type Worktree struct {
	Path     string
	repoRoot string
}

// IsGitRepo reports whether dir is inside a git work tree.
func IsGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Create adds a detached worktree at a fresh temp directory checked out at the
// repo's current HEAD. The caller must Remove it when done.
func Create(repoRoot, label string) (*Worktree, error) {
	if !IsGitRepo(repoRoot) {
		return nil, fmt.Errorf("worktree: %q is not a git repository", repoRoot)
	}
	dir, err := os.MkdirTemp("", "mimo-wt-"+sanitize(label)+"-")
	if err != nil {
		return nil, fmt.Errorf("worktree: temp dir: %w", err)
	}
	// --detach checks out HEAD without creating a branch (nothing to clean up).
	if out, err := run(repoRoot, "worktree", "add", "--detach", dir, "HEAD"); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("worktree: add: %w: %s", err, out)
	}
	return &Worktree{Path: dir, repoRoot: repoRoot}, nil
}

// Diff returns a unified diff of all changes made in the worktree, including new
// and .gitignore-matching files. The worktree starts as a clean checkout of
// HEAD, so --force only surfaces files the sub-agent itself wrote (otherwise a
// sub-agent writing to an ignored path would have its work silently dropped). It
// stages into the worktree's own index, which is discarded on Remove.
func (w *Worktree) Diff() (string, error) {
	if _, err := run(w.Path, "add", "-A", "--force"); err != nil {
		return "", fmt.Errorf("worktree: stage: %w", err)
	}
	out, err := run(w.Path, "diff", "--cached")
	if err != nil {
		return "", fmt.Errorf("worktree: diff: %w", err)
	}
	return out, nil
}

// Remove deletes the worktree and its directory.
func (w *Worktree) Remove() error {
	if w == nil || w.Path == "" {
		return nil
	}
	_, err := run(w.repoRoot, "worktree", "remove", "--force", w.Path)
	// Best-effort directory cleanup in case git left anything behind.
	_ = os.RemoveAll(w.Path)
	if err != nil {
		// Prune the registry entry so a leftover does not wedge future adds.
		_, _ = run(w.repoRoot, "worktree", "prune")
	}
	return err
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return strings.TrimSpace(stderr.String()), err
	}
	return stdout.String(), nil
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "task"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
		if b.Len() >= 24 {
			break
		}
	}
	return b.String()
}
