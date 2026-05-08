package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"mimo-tui/internal/core"
)

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()

	if p.Defaults.ReadOnly != "allow" {
		t.Fatalf("read_only default = %q, want allow", p.Defaults.ReadOnly)
	}
	if p.Defaults.WorkspaceMutation != "ask" {
		t.Fatalf("workspace_mutation default = %q, want ask", p.Defaults.WorkspaceMutation)
	}
	if p.Defaults.ShellMutation != "ask" {
		t.Fatalf("shell_mutation default = %q, want ask", p.Defaults.ShellMutation)
	}
	if p.Defaults.Destructive != "deny" {
		t.Fatalf("destructive default = %q, want deny", p.Defaults.Destructive)
	}
}

func TestEvaluatePolicyDefaultsBySafetyGrade(t *testing.T) {
	p := DefaultPolicy()

	tests := []struct {
		name     string
		tool     string
		grade    core.SafetyGrade
		expected core.PermissionBehavior
	}{
		{"read_only tools allowed", "rg", core.SafetyReadOnly, core.PermissionAllow},
		{"workspace mutation asks", "write_file", core.SafetyWorkspaceMutation, core.PermissionAsk},
		{"shell mutation asks", "run_test", core.SafetyShellMutation, core.PermissionAsk},
		{"destructive denied", "shell", core.SafetyDestructive, core.PermissionDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EvaluatePolicy(p, tt.tool, nil, tt.grade)
			if result.Behavior != tt.expected {
				t.Fatalf("behavior = %q, want %q", result.Behavior, tt.expected)
			}
			if result.Reason == "" {
				t.Fatal("reason is empty")
			}
		})
	}
}

func TestEvaluatePolicyAllowlistOverrides(t *testing.T) {
	p := DefaultPolicy()
	p.Allowlist = []PolicyEntry{
		{Tool: "shell"},
		{Tool: "write_file"},
	}

	// Even destructive shell commands are allowed when allowlisted.
	result := EvaluatePolicy(p, "shell", nil, core.SafetyDestructive)
	if result.Behavior != core.PermissionAllow {
		t.Fatalf("allowlisted shell behavior = %q, want allow", result.Behavior)
	}
	if !strings.Contains(result.Reason, "allowlist") {
		t.Fatalf("reason = %q, want to contain allowlist", result.Reason)
	}

	// write_file (workspace mutation) also allowed.
	result = EvaluatePolicy(p, "write_file", nil, core.SafetyWorkspaceMutation)
	if result.Behavior != core.PermissionAllow {
		t.Fatalf("allowlisted write_file behavior = %q, want allow", result.Behavior)
	}
}

func TestEvaluatePolicyDenylistOverrides(t *testing.T) {
	p := DefaultPolicy()
	p.Denylist = []PolicyEntry{
		{Tool: "shell", Pattern: "rm -rf"},
	}

	// rm -rf denied even for shell_mutation grade.
	input := core.ToolInput{"command": "rm -rf /tmp/test"}
	result := EvaluatePolicy(p, "shell", input, core.SafetyShellMutation)
	if result.Behavior != core.PermissionDeny {
		t.Fatalf("denylisted rm -rf behavior = %q, want deny", result.Behavior)
	}
	if !strings.Contains(result.Reason, "denylist") {
		t.Fatalf("reason = %q, want to contain denylist", result.Reason)
	}

	// Non-matching command follows default (shell_mutation -> ask).
	input2 := core.ToolInput{"command": "echo hello"}
	result2 := EvaluatePolicy(p, "shell", input2, core.SafetyShellMutation)
	if result2.Behavior != core.PermissionAsk {
		t.Fatalf("non-matching shell behavior = %q, want ask", result2.Behavior)
	}
}

func TestEvaluatePolicyAllowlistWinsOverDenylist(t *testing.T) {
	p := DefaultPolicy()
	// Both allowlist and denylist match the same tool.
	p.Allowlist = []PolicyEntry{
		{Tool: "shell"},
	}
	p.Denylist = []PolicyEntry{
		{Tool: "shell", Pattern: "rm -rf"},
	}

	input := core.ToolInput{"command": "rm -rf /"}
	result := EvaluatePolicy(p, "shell", input, core.SafetyDestructive)
	if result.Behavior != core.PermissionAllow {
		t.Fatalf("allowlist should win: behavior = %q, want allow", result.Behavior)
	}
	if !strings.Contains(result.Reason, "allowlist") {
		t.Fatalf("reason = %q, want to contain allowlist", result.Reason)
	}
}

func TestEvaluatePolicyRequireConfirm(t *testing.T) {
	p := DefaultPolicy()
	p.RequireConfirm = []PolicyEntry{
		{Tool: "shell", Pattern: "git push"},
	}

	// git push requires explicit confirmation.
	input := core.ToolInput{"command": "git push origin main"}
	result := EvaluatePolicy(p, "shell", input, core.SafetyShellMutation)
	if result.Behavior != core.PermissionAsk {
		t.Fatalf("require_confirm behavior = %q, want ask", result.Behavior)
	}
	if !strings.Contains(result.Reason, "require_confirm") {
		t.Fatalf("reason = %q, want to contain require_confirm", result.Reason)
	}

	// Non-matching command gets default.
	input2 := core.ToolInput{"command": "echo hi"}
	result2 := EvaluatePolicy(p, "shell", input2, core.SafetyShellMutation)
	if result2.Behavior != core.PermissionAsk {
		t.Fatalf("non-matching shell behavior = %q, want ask (default)", result2.Behavior)
	}
}

func TestEvaluatePolicyAllowlistPatternMatch(t *testing.T) {
	p := DefaultPolicy()
	p.Allowlist = []PolicyEntry{
		{Tool: "shell", Pattern: "echo"},
	}

	// echo matches allowlist pattern.
	input := core.ToolInput{"command": "echo hello world"}
	result := EvaluatePolicy(p, "shell", input, core.SafetyShellMutation)
	if result.Behavior != core.PermissionAllow {
		t.Fatalf("pattern-matched allowlisted behavior = %q, want allow", result.Behavior)
	}

	// Non-echo command falls through to default.
	input2 := core.ToolInput{"command": "ls -la"}
	result2 := EvaluatePolicy(p, "shell", input2, core.SafetyShellMutation)
	if result2.Behavior != core.PermissionAsk {
		t.Fatalf("non-matched behavior = %q, want ask", result2.Behavior)
	}
}

func TestEvaluatePolicyDenylistWithoutPattern(t *testing.T) {
	p := DefaultPolicy()
	p.Denylist = []PolicyEntry{
		{Tool: "shell"}, // blank pattern = always match
	}

	result := EvaluatePolicy(p, "shell", core.ToolInput{"command": "echo hi"}, core.SafetyShellMutation)
	if result.Behavior != core.PermissionDeny {
		t.Fatalf("blank-pattern denylist behavior = %q, want deny", result.Behavior)
	}
}

func TestEvaluatePolicyEmptyPolicy(t *testing.T) {
	p := PolicyConfig{} // all fields zero-valued, defaults empty.

	// Empty defaults should be treated as "ask" since parseBehavior("") returns ask.
	result := EvaluatePolicy(p, "read_file", nil, core.SafetyReadOnly)
	if result.Behavior != core.PermissionAsk {
		t.Fatalf("empty policy read_only behavior = %q, want ask (safe fallback)", result.Behavior)
	}
}

func TestEvaluatePolicyUnknownSafetyGrade(t *testing.T) {
	p := DefaultPolicy()
	result := EvaluatePolicy(p, "unknown_tool", nil, core.SafetyGrade("unknown"))
	if result.Behavior != core.PermissionAsk {
		t.Fatalf("unknown grade behavior = %q, want ask (safe fallback)", result.Behavior)
	}
}

func TestInputMatchesPattern(t *testing.T) {
	tests := []struct {
		name    string
		input   core.ToolInput
		pattern string
		want    bool
	}{
		{"exact match", core.ToolInput{"cmd": "rm -rf /"}, "rm -rf", true},
		{"substring", core.ToolInput{"cmd": "sudo rm -rf /tmp"}, "rm -rf", true},
		{"case insensitive", core.ToolInput{"cmd": "RM -RF /"}, "rm -rf", true},
		{"no match", core.ToolInput{"cmd": "echo hello"}, "rm -rf", false},
		{"empty pattern", core.ToolInput{"cmd": "anything"}, "", true},
		{"multiple fields", core.ToolInput{"path": "/tmp", "content": "rm -rf test"}, "rm -rf", true},
		{"empty input", core.ToolInput{}, "pattern", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inputMatchesPattern(tt.input, tt.pattern)
			if got != tt.want {
				t.Fatalf("inputMatchesPattern(%v, %q) = %v, want %v", tt.input, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestLoadPolicyReadsFile(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.toml")

	content := `[defaults]
read_only = "allow"
workspace_mutation = "deny"
shell_mutation = "deny"
destructive = "deny"

[[allowlist]]
tool = "read_file"

[[denylist]]
tool = "shell"
pattern = "sudo"
`
	if err := os.WriteFile(policyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Temporarily create .mimo dir in temp dir and use it.
	// We can't easily redirect the candidate paths, so test LoadPolicyFromPath.
	cfg, err := loadPolicyFromPath(policyPath)
	if err != nil {
		t.Fatalf("LoadPolicyFromPath: %v", err)
	}

	// Verify overrides were applied.
	result := EvaluatePolicy(cfg, "shell", core.ToolInput{"command": "sudo rm -rf /"}, core.SafetyDestructive)
	if result.Behavior != core.PermissionDeny {
		t.Fatalf("sudo shell should be denied by denylist, got %q", result.Behavior)
	}

	result = EvaluatePolicy(cfg, "read_file", nil, core.SafetyReadOnly)
	if result.Behavior != core.PermissionAllow {
		t.Fatalf("allowlisted read_file should be allowed, got %q", result.Behavior)
	}

	result = EvaluatePolicy(cfg, "write_file", nil, core.SafetyWorkspaceMutation)
	if result.Behavior != core.PermissionDeny {
		t.Fatalf("workspace_mutation should be deny per config, got %q", result.Behavior)
	}
}

func TestLoadPolicyFileNotFoundReturnsDefaults(t *testing.T) {
	cfg, err := loadPolicyFromPath("/nonexistent/path/policy.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Defaults.ReadOnly != "allow" {
		t.Fatalf("read_only = %q, want allow", cfg.Defaults.ReadOnly)
	}
}

func TestLoadPolicyInvalidToml(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.toml")
	if err := os.WriteFile(policyPath, []byte("{{{invalid toml"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadPolicyFromPath(policyPath)
	if err == nil {
		t.Fatal("expected error for invalid toml")
	}
}

func TestParseBehavior(t *testing.T) {
	tests := []struct {
		raw  string
		want core.PermissionBehavior
	}{
		{"allow", core.PermissionAllow},
		{"ALLOW", core.PermissionAllow},
		{"Allow", core.PermissionAllow},
		{"deny", core.PermissionDeny},
		{"DENY", core.PermissionDeny},
		{"ask", core.PermissionAsk},
		{"ASK", core.PermissionAsk},
		{"", core.PermissionAsk},
		{"unknown", core.PermissionAsk},
	}

	for _, tt := range tests {
		got := parseBehavior(tt.raw)
		if got != tt.want {
			t.Fatalf("parseBehavior(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// loadPolicyFromPath reads a policy file from an explicit path (used in tests).
func loadPolicyFromPath(path string) (PolicyConfig, error) {
	cfg := DefaultPolicy()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
