package config

import (
	"testing"

	"mimo-tui/internal/core"
)

func TestTimeoutForGrade(t *testing.T) {
	c := PolicyConfig{
		ApprovalTimeout: 30,
		ApprovalTimeouts: GradeTimeouts{
			ReadOnly:    60,
			Destructive: 5,
		},
	}
	if got := c.TimeoutForGrade(core.SafetyReadOnly); got != 60 {
		t.Fatalf("read_only timeout = %d, want 60", got)
	}
	if got := c.TimeoutForGrade(core.SafetyDestructive); got != 5 {
		t.Fatalf("destructive timeout = %d, want 5", got)
	}
	// Unset grade returns 0 (caller falls back to the global default).
	if got := c.TimeoutForGrade(core.SafetyShellMutation); got != 0 {
		t.Fatalf("unset grade timeout = %d, want 0", got)
	}
}
