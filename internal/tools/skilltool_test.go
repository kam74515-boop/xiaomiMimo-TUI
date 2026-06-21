package tools

import (
	"context"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

func newSkillToolForTest(t *testing.T) core.Tool {
	t.Helper()
	ws := t.TempDir()
	return NewSkillTool(ws, artifact.NewStore(ws), nil)
}

func TestSkillToolList(t *testing.T) {
	tool := newSkillToolForTest(t)
	res := tool.Run(context.Background(), core.ToolInput{"action": "list"})
	if res.Error != "" {
		t.Fatalf("list error: %+v", res)
	}
	for _, name := range []string{"plan", "tdd", "review", "debug", "verify"} {
		if !strings.Contains(res.Content, name) {
			t.Fatalf("list missing builtin %q: %s", name, res.Content)
		}
	}
}

func TestSkillToolShow(t *testing.T) {
	tool := newSkillToolForTest(t)
	res := tool.Run(context.Background(), core.ToolInput{"action": "show", "name": "tdd"})
	if res.Error != "" || !strings.Contains(strings.ToLower(res.Content), "test") {
		t.Fatalf("show tdd: %+v", res)
	}
	res = tool.Run(context.Background(), core.ToolInput{"action": "show", "name": "nope"})
	if res.Error == "" {
		t.Fatalf("show unknown should error, got %+v", res)
	}
}

func TestSkillToolDefaultsAction(t *testing.T) {
	tool := newSkillToolForTest(t)
	// name present, no action -> show
	res := tool.Run(context.Background(), core.ToolInput{"name": "plan"})
	if res.Error != "" || !strings.Contains(strings.ToLower(res.Content), "plan") {
		t.Fatalf("default show: %+v", res)
	}
	// nothing -> list
	res = tool.Run(context.Background(), core.ToolInput{})
	if res.Error != "" || !strings.Contains(res.Content, "skill") {
		t.Fatalf("default list: %+v", res)
	}
}

func TestSkillToolRegistered(t *testing.T) {
	reg := NewDefaultRegistry(t.TempDir())
	if _, ok := reg.Get("skill"); !ok {
		t.Fatal("skill tool not registered in default registry")
	}
	if _, ok := reg.Get("memory"); !ok {
		t.Fatal("memory tool not registered in default registry")
	}
}
