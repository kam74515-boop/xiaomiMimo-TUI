package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

func TestWriteFileConfinedToWorkspace(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	tool := NewWriteFileTool(ws, artifact.NewStore(ws), nil)

	// In-workspace write succeeds.
	if res := tool.Run(context.Background(), core.ToolInput{"path": "ok.txt", "content": "hi"}); res.Error != "" {
		t.Fatalf("in-workspace write should succeed: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(ws, "ok.txt")); err != nil {
		t.Fatalf("in-workspace file missing: %v", err)
	}

	// Absolute path outside the workspace is rejected.
	abs := filepath.Join(outside, "escape.txt")
	res := tool.Run(context.Background(), core.ToolInput{"path": abs, "content": "x"})
	if res.Error == "" {
		t.Fatalf("absolute out-of-workspace write should be rejected: %+v", res)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatal("escape via absolute path wrote a file outside the workspace")
	}

	// Relative traversal is rejected.
	res = tool.Run(context.Background(), core.ToolInput{"path": "../escape2.txt", "content": "x"})
	if res.Error == "" {
		t.Fatalf("../ traversal write should be rejected: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escape2.txt")); !os.IsNotExist(err) {
		t.Fatal("escape via ../ wrote a file outside the workspace")
	}

	// Nested in-workspace path still works.
	if res := tool.Run(context.Background(), core.ToolInput{"path": "sub/dir/deep.txt", "content": "y"}); res.Error != "" {
		t.Fatalf("nested in-workspace write should succeed: %+v", res)
	}
}
