package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"mimo-tui/internal/core"
)

// PolicyConfig mirrors the .mimo/policy.toml structure.
type PolicyConfig struct {
	Defaults         Defaults      `toml:"defaults"`
	Allowlist        []PolicyEntry `toml:"allowlist"`
	Denylist         []PolicyEntry `toml:"denylist"`
	RequireConfirm   []PolicyEntry `toml:"require_confirm"`
	ApprovalTimeout  int           `toml:"approval_timeout"`  // seconds; 0 means default 30s
	ApprovalTimeouts GradeTimeouts `toml:"approval_timeouts"` // per-safety-grade overrides (seconds)
}

// GradeTimeouts holds per-safety-grade approval timeouts in seconds (0 = unset:
// falls back to the global approval_timeout, then the built-in default).
type GradeTimeouts struct {
	ReadOnly          int `toml:"read_only"`
	WorkspaceMutation int `toml:"workspace_mutation"`
	ShellMutation     int `toml:"shell_mutation"`
	Destructive       int `toml:"destructive"`
}

// TimeoutForGrade returns the configured per-grade approval timeout in seconds,
// or 0 when none is set for that grade.
func (c PolicyConfig) TimeoutForGrade(grade core.SafetyGrade) int {
	switch grade {
	case core.SafetyReadOnly:
		return c.ApprovalTimeouts.ReadOnly
	case core.SafetyWorkspaceMutation:
		return c.ApprovalTimeouts.WorkspaceMutation
	case core.SafetyShellMutation:
		return c.ApprovalTimeouts.ShellMutation
	case core.SafetyDestructive:
		return c.ApprovalTimeouts.Destructive
	}
	return 0
}

// Defaults maps safety grades to default permission behaviors.
type Defaults struct {
	ReadOnly          string `toml:"read_only"`
	WorkspaceMutation string `toml:"workspace_mutation"`
	ShellMutation     string `toml:"shell_mutation"`
	Destructive       string `toml:"destructive"`
}

// PolicyEntry matches a tool (and optionally an input pattern).
type PolicyEntry struct {
	Tool    string `toml:"tool"`
	Pattern string `toml:"pattern,omitempty"`
}

// LoadPolicy reads .mimo/policy.toml and returns a PolicyConfig.
// Returns the default policy if the file does not exist.
func LoadPolicy() (PolicyConfig, error) {
	cfg := DefaultPolicy()
	paths := policyCandidatePaths()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			log.Printf("warning: policy config %s has invalid syntax, using defaults: %v", path, err)
			return cfg, nil
		}
		return cfg, nil
	}
	return cfg, nil
}

// DefaultPolicy returns a PolicyConfig with safe defaults that require
// approval for any mutation and deny destructive operations.
func DefaultPolicy() PolicyConfig {
	return PolicyConfig{
		Defaults: Defaults{
			ReadOnly:          "allow",
			WorkspaceMutation: "ask",
			ShellMutation:     "ask",
			Destructive:       "deny",
		},
	}
}

func policyCandidatePaths() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(".mimo", "policy.toml"),
		filepath.Join(home, ".mimo-tui", "policy.toml"),
	}
}

// EvaluatePolicy determines the PermissionBehavior for a tool call.
//
// Precedence (highest first):
//  1. allowlist entry matches (tool + optional pattern) -> Allow
//  2. denylist entry matches (tool + optional pattern) -> Deny
//  3. require_confirm entry matches (tool + optional pattern) -> Ask
//  4. Default mapping from safety grade
func EvaluatePolicy(policy PolicyConfig, toolName string, input core.ToolInput, grade core.SafetyGrade) core.PermissionRequest {
	// 1. Check allowlist first (highest priority).
	if entry, ok := matchEntry(policy.Allowlist, toolName, input); ok {
		return core.PermissionRequest{
			Behavior: core.PermissionAllow,
			Reason:   formatPolicyReason("allowlist", entry, toolName),
		}
	}

	// 2. Check denylist.
	if entry, ok := matchEntry(policy.Denylist, toolName, input); ok {
		return core.PermissionRequest{
			Behavior: core.PermissionDeny,
			Reason:   formatPolicyReason("denylist", entry, toolName),
		}
	}

	// 3. Check require_confirm (forces ask even if defaults say allow).
	if entry, ok := matchEntry(policy.RequireConfirm, toolName, input); ok {
		return core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   formatPolicyReason("require_confirm", entry, toolName),
		}
	}

	// 4. Fall back to default mapping from safety grade.
	return defaultPermission(policy.Defaults, toolName, grade)
}

// matchEntry checks whether any entry in the list matches the given tool and input.
func matchEntry(entries []PolicyEntry, toolName string, input core.ToolInput) (PolicyEntry, bool) {
	for _, entry := range entries {
		if entry.Tool != toolName {
			continue
		}
		if entry.Pattern == "" {
			return entry, true
		}
		if inputMatchesPattern(input, entry.Pattern) {
			return entry, true
		}
	}
	return PolicyEntry{}, false
}

// inputMatchesPattern checks whether any string value in the input contains
// the given pattern (case-insensitive substring match).
func inputMatchesPattern(input core.ToolInput, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return true
	}
	for _, value := range input {
		str := strings.ToLower(fmt.Sprint(value))
		if strings.Contains(str, pattern) {
			return true
		}
	}
	return false
}

func formatPolicyReason(source string, entry PolicyEntry, toolName string) string {
	reason := fmt.Sprintf("%s: %s", source, toolName)
	if entry.Pattern != "" {
		reason += fmt.Sprintf(" (pattern %q)", entry.Pattern)
	}
	return reason
}

func defaultPermission(defaults Defaults, toolName string, grade core.SafetyGrade) core.PermissionRequest {
	behavior := behaviorForGrade(defaults, grade)
	reason := fmt.Sprintf("default policy for %s (%s)", toolName, grade)
	return core.PermissionRequest{
		Behavior: behavior,
		Reason:   reason,
	}
}

func behaviorForGrade(defaults Defaults, grade core.SafetyGrade) core.PermissionBehavior {
	var raw string
	switch grade {
	case core.SafetyReadOnly:
		raw = defaults.ReadOnly
	case core.SafetyWorkspaceMutation:
		raw = defaults.WorkspaceMutation
	case core.SafetyShellMutation:
		raw = defaults.ShellMutation
	case core.SafetyDestructive:
		raw = defaults.Destructive
	default:
		return core.PermissionAsk
	}
	return parseBehavior(raw)
}

func parseBehavior(raw string) core.PermissionBehavior {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow":
		return core.PermissionAllow
	case "deny":
		return core.PermissionDeny
	default:
		return core.PermissionAsk
	}
}
