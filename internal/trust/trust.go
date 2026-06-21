// Package trust implements MiMo-TUI's verification ledger.
//
// Coding agents fail most often by claiming success they did not verify ("tests
// pass", "it compiles", "fixed"). This package cross-checks such claims in the
// agent's answer against the tools that actually ran successfully in the
// session, and flags claims that no tool backs — so the user sees what is
// verified vs merely asserted before accepting a change.
//
// Detection is deterministic (keyword + evidence cross-check), not model-based.
package trust

import (
	"fmt"
	"strings"
)

// Action is a tool invocation outcome used as evidence.
type Action struct {
	Tool       string
	OK         bool
	ArtifactID string
}

// Claim is a success assertion found in the answer and its verification status.
type Claim struct {
	Label    string
	Verified bool
	Note     string
}

// Ledger is the verification result for one answer.
type Ledger struct {
	Actions []Action
	Claims  []Claim
}

// UnverifiedClaims returns the claims that no successful tool backs.
func (l Ledger) UnverifiedClaims() []Claim {
	var out []Claim
	for _, c := range l.Claims {
		if !c.Verified {
			out = append(out, c)
		}
	}
	return out
}

type rule struct {
	label    string
	all      []string // every token must be present (lowercased substring)
	any      []string // at least one must be present (optional)
	verifies []string // tool names that, if any ran OK, verify the claim
}

// rules are intentionally conservative to limit false positives.
var rules = []rule{
	{label: "tests pass", all: []string{"test"}, any: []string{"pass", "passing", "green", "succeed"}, verifies: []string{"run_test"}},
	{label: "code compiles / builds", any: []string{"compiles", "compile cleanly", "builds", "build succeeds", "build passes"}, verifies: []string{"run_test", "diagnostics"}},
	{label: "no errors / lint clean", any: []string{"no errors", "no compile error", "lint clean", "vet clean", "no warnings"}, verifies: []string{"diagnostics", "run_test"}},
	{label: "fix verified", any: []string{"fixed", "resolved the", "works now", "it works", "now works", "verified that"}, verifies: []string{"run_test", "diagnostics"}},
}

// Analyze builds a ledger from the answer text and the session's tool actions.
func Analyze(answer string, actions []Action) Ledger {
	l := Ledger{Actions: actions}
	lower := strings.ToLower(answer)
	for _, r := range rules {
		if !ruleMatches(lower, r) {
			continue
		}
		verified := anyOK(actions, r.verifies...)
		note := ""
		if !verified {
			note = "no successful " + strings.Join(r.verifies, "/") + " run in this session"
		}
		l.Claims = append(l.Claims, Claim{Label: r.label, Verified: verified, Note: note})
	}
	return l
}

func ruleMatches(lower string, r rule) bool {
	for _, tok := range r.all {
		if !strings.Contains(lower, tok) {
			return false
		}
	}
	if len(r.any) == 0 {
		return len(r.all) > 0
	}
	for _, tok := range r.any {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func anyOK(actions []Action, tools ...string) bool {
	want := map[string]bool{}
	for _, t := range tools {
		want[t] = true
	}
	for _, a := range actions {
		if a.OK && want[a.Tool] {
			return true
		}
	}
	return false
}

// Format renders a human-readable ledger.
func Format(l Ledger) string {
	var b strings.Builder
	ok, fail := 0, 0
	for _, a := range l.Actions {
		if a.OK {
			ok++
		} else {
			fail++
		}
	}
	b.WriteString(fmt.Sprintf("actions: %d ok, %d failed\n", ok, fail))
	if len(l.Claims) == 0 {
		b.WriteString("claims: none detected in the answer\n")
		return b.String()
	}
	for _, c := range l.Claims {
		mark := "✓ verified"
		if !c.Verified {
			mark = "⚠ unverified"
		}
		line := fmt.Sprintf("  %s — %s", mark, c.Label)
		if c.Note != "" {
			line += " (" + c.Note + ")"
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
