package tools

import (
	"context"
	"testing"

	"mimo-tui/internal/core"
)

type fakeCaller struct {
	gotName string
	gotArgs map[string]any
	out     string
	err     error
}

func (f *fakeCaller) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	f.gotName = name
	f.gotArgs = args
	return f.out, f.err
}

func TestExternalToolDispatchesToCaller(t *testing.T) {
	tool := NewExternalTool("github", "create_issue", "Create an issue", nil, ".")
	if !tool.Stubbed() {
		t.Fatal("tool without a caller should be stubbed")
	}
	caller := &fakeCaller{out: "issue #42 created"}
	tool.SetCaller(caller)
	if tool.Stubbed() {
		t.Fatal("tool with a caller should not be stubbed")
	}

	res := tool.Run(context.Background(), core.ToolInput{"title": "bug"})
	if res.Error != "" || res.Content != "issue #42 created" {
		t.Fatalf("Run via caller = %+v", res)
	}
	if caller.gotName != "create_issue" || caller.gotArgs["title"] != "bug" {
		t.Fatalf("caller received name=%q args=%v", caller.gotName, caller.gotArgs)
	}
}

func TestExternalToolCallerError(t *testing.T) {
	tool := NewExternalTool("s", "t", "", nil, ".")
	tool.SetCaller(&fakeCaller{err: context.DeadlineExceeded, out: "partial"})
	res := tool.Run(context.Background(), core.ToolInput{})
	if res.Error == "" || res.ExitCode == 0 {
		t.Fatalf("caller error should surface: %+v", res)
	}
}
