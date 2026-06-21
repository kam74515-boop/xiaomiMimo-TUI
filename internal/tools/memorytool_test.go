package tools

import (
	"context"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

func newMemoryToolForTest(t *testing.T) core.Tool {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	return NewMemoryTool(ws, artifact.NewStore(ws), nil)
}

func TestMemoryToolAddThenShow(t *testing.T) {
	tool := newMemoryToolForTest(t)

	// show on empty
	res := tool.Run(context.Background(), core.ToolInput{"action": "show"})
	if res.Error != "" || !strings.Contains(res.Content, "empty") {
		t.Fatalf("show empty: %+v", res)
	}

	// add
	res = tool.Run(context.Background(), core.ToolInput{"action": "add", "note": "MiMo-TUI stays clean-room"})
	if res.Error != "" || !strings.Contains(res.Content, "recorded memory") {
		t.Fatalf("add: %+v", res)
	}

	// show reflects the new fact
	res = tool.Run(context.Background(), core.ToolInput{"action": "show"})
	if res.Error != "" || !strings.Contains(res.Content, "clean-room") {
		t.Fatalf("show after add: %+v", res)
	}
}

func TestMemoryToolAddRequiresNote(t *testing.T) {
	tool := newMemoryToolForTest(t)
	res := tool.Run(context.Background(), core.ToolInput{"action": "add"})
	if res.Error == "" {
		t.Fatalf("expected error when note is missing, got %+v", res)
	}
}

func TestMemoryToolDefaultsAction(t *testing.T) {
	tool := newMemoryToolForTest(t)
	// note present, no action -> treated as add
	res := tool.Run(context.Background(), core.ToolInput{"note": "default to add when note present"})
	if res.Error != "" || !strings.Contains(res.Content, "recorded memory") {
		t.Fatalf("default add: %+v", res)
	}
}

func TestMemoryToolPermissionAndSafety(t *testing.T) {
	tool := newMemoryToolForTest(t)
	if perm := tool.Permission(core.ToolInput{"action": "add"}); perm.Behavior != core.PermissionAllow {
		t.Fatalf("memory permission = %v, want allow", perm.Behavior)
	}
	if g := tool.Safety(core.ToolInput{"action": "add"}); g != core.SafetyWorkspaceMutation {
		t.Fatalf("add safety = %v, want workspace_mutation", g)
	}
	if g := tool.Safety(core.ToolInput{"action": "show"}); g != core.SafetyReadOnly {
		t.Fatalf("show safety = %v, want read_only", g)
	}
}

func TestMemoryToolRegistered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reg := NewDefaultRegistry(t.TempDir())
	if _, ok := reg.Get("memory"); !ok {
		t.Fatal("memory tool not registered in default registry")
	}
}
