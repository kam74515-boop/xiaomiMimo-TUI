package tools

import (
	"testing"
	"time"

	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
)

func TestApprovalTimeoutPrecedence(t *testing.T) {
	policy := config.PolicyConfig{
		ApprovalTimeout:  30,
		ApprovalTimeouts: config.GradeTimeouts{Destructive: 5},
	}
	e := NewExecutor(NewRegistry(), nil, WithPolicyConfig(policy))

	if got := e.approvalTimeout(core.SafetyDestructive); got != 5*time.Second {
		t.Fatalf("destructive timeout = %v, want 5s (per-grade)", got)
	}
	if got := e.approvalTimeout(core.SafetyReadOnly); got != 30*time.Second {
		t.Fatalf("read_only timeout = %v, want 30s (global fallback)", got)
	}

	// No policy at all => built-in default.
	plain := NewExecutor(NewRegistry(), nil)
	if got := plain.approvalTimeout(core.SafetyShellMutation); got != 30*time.Second {
		t.Fatalf("default timeout = %v, want 30s", got)
	}
}
