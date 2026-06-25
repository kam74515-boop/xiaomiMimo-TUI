package tools

import (
	"context"
	"testing"
	"time"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

// TestShellCancellationNotBlockedByGrandchild verifies that when a command
// backgrounds a long-lived grandchild that inherits stdout, a context timeout
// still returns promptly (the group is killed) rather than blocking on the
// grandchild's full lifetime — keeping the agent interruptible.
func TestShellCancellationNotBlockedByGrandchild(t *testing.T) {
	ws := t.TempDir()
	tool := NewShellTool(ws, artifact.NewStore(ws), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	done := make(chan core.ToolResult, 1)
	go func() {
		// sh exits immediately but the backgrounded `sleep 30` inherits stdout.
		done <- tool.Run(ctx, core.ToolInput{"command": "sleep 30 & echo started"})
	}()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("shell run blocked on grandchild for %s (should be killed near the 1s timeout)", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("shell run did not return after the context timeout (grandchild kept it blocked)")
	}
}
