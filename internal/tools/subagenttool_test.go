package tools

import (
	"context"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

type fakeSpawner struct {
	res     SubAgentResult
	err     error
	gotGoal string
}

func (f *fakeSpawner) Spawn(ctx context.Context, goal string) (SubAgentResult, error) {
	f.gotGoal = goal
	return f.res, f.err
}

func newSubAgentToolForTest(t *testing.T) (*subAgentTool, *artifact.Store) {
	t.Helper()
	ws := t.TempDir()
	store := artifact.NewStore(ws)
	return NewSubAgentTool(ws, store, nil).(*subAgentTool), store
}

func TestSubAgentToolStubWithoutSpawner(t *testing.T) {
	tool, _ := newSubAgentToolForTest(t)
	if !tool.Stubbed() {
		t.Fatal("tool without spawner should be stubbed")
	}
	res := tool.Run(context.Background(), core.ToolInput{"goal": "do x"})
	if !strings.Contains(res.Content, "not connected") {
		t.Fatalf("stub run = %+v", res)
	}
}

func TestSubAgentToolDelegates(t *testing.T) {
	tool, store := newSubAgentToolForTest(t)
	sp := &fakeSpawner{res: SubAgentResult{
		Summary: "implemented the helper",
		Diff:    "diff --git a/x.go b/x.go\n+func X() {}\n",
		Steps:   3,
	}}
	tool.SetSpawner(sp)
	if tool.Stubbed() {
		t.Fatal("tool with spawner should not be stubbed")
	}

	res := tool.Run(context.Background(), core.ToolInput{"goal": "add helper X"})
	if res.Error != "" {
		t.Fatalf("Run error: %+v", res)
	}
	if sp.gotGoal != "add helper X" {
		t.Fatalf("spawner received goal %q", sp.gotGoal)
	}
	if !strings.Contains(res.Content, "implemented the helper") || !strings.Contains(res.Content, "3 step") {
		t.Fatalf("content missing summary/steps: %q", res.Content)
	}
	if res.ArtifactID == "" {
		t.Fatal("diff should be stored as an artifact")
	}
	if _, err := store.ReadMetadata(res.ArtifactID); err != nil {
		t.Fatalf("artifact not readable: %v", err)
	}
}

func TestSubAgentToolMissingGoal(t *testing.T) {
	tool, _ := newSubAgentToolForTest(t)
	tool.SetSpawner(&fakeSpawner{})
	res := tool.Run(context.Background(), core.ToolInput{})
	if res.Error == "" {
		t.Fatalf("missing goal should error: %+v", res)
	}
}

func TestSubAgentToolRegistered(t *testing.T) {
	reg := NewDefaultRegistry(t.TempDir())
	if _, ok := reg.Get("subagent"); !ok {
		t.Fatal("subagent tool not registered in default registry")
	}
}
