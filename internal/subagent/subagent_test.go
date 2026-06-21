package subagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mimo-tui/internal/agent"
	"mimo-tui/internal/core"
	"mimo-tui/internal/provider/mimo"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	steps := [][]string{
		{"init"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
	}
	for _, args := range steps {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func drainBus(ch <-chan core.AgentEvent) []core.AgentEvent {
	var out []core.AgentEvent
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestSpawnRunsAndPublishesActivity(t *testing.T) {
	repo := gitRepo(t)
	bus := core.NewBus()
	sub := bus.Subscribe(128)

	cfg := agent.DefaultLoopConfig()
	cfg.MaxSteps = 3
	cfg.StepTimeout = 10 * time.Second
	cfg.TotalTimeout = 20 * time.Second

	sp := NewSpawner(Deps{
		RepoRoot:   repo,
		Client:     mimo.NewMock("test-model"),
		ParentBus:  bus,
		LoopConfig: cfg,
	})

	res, err := sp.Spawn(context.Background(), "summarize the repo")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Steps < 1 {
		t.Fatalf("expected at least 1 step, got %d", res.Steps)
	}
	if res.Summary == "" {
		t.Fatal("expected a non-empty summary")
	}

	events := drainBus(sub)
	var running, done bool
	for _, e := range events {
		if e.Type == core.EventActivityUpdate && e.Activity != nil && e.Activity.Kind == core.ActivitySubAgent {
			switch e.Activity.Status {
			case core.ActivityRunning:
				running = true
			case core.ActivityDone:
				done = true
			}
		}
	}
	if !running || !done {
		t.Fatalf("expected sub-agent running+done activity (running=%v done=%v)", running, done)
	}

	// The worktree must be cleaned up: no stray worktrees registered.
	out, _ := exec.Command("git", "-C", repo, "worktree", "list").Output()
	if n := len(splitNonEmptyLines(string(out))); n != 1 {
		t.Fatalf("expected only the main worktree to remain, git worktree list:\n%s", out)
	}
}

func TestSpawnNonRepoErrors(t *testing.T) {
	sp := NewSpawner(Deps{
		RepoRoot:   t.TempDir(), // not a git repo
		Client:     mimo.NewMock("test-model"),
		ParentBus:  core.NewBus(),
		LoopConfig: agent.DefaultLoopConfig(),
	})
	if _, err := sp.Spawn(context.Background(), "x"); err == nil {
		t.Fatal("Spawn in a non-repo should error")
	}
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range filepathSplitLines(s) {
		if len(line) > 0 {
			out = append(out, line)
		}
	}
	return out
}

func filepathSplitLines(s string) []string {
	var lines []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
