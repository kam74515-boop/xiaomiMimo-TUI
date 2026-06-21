package trust

import (
	"strings"
	"testing"
)

func TestTestsPassVerifiedByRunTest(t *testing.T) {
	l := Analyze("All tests pass now.", []Action{{Tool: "run_test", OK: true}})
	if len(l.Claims) != 1 || !l.Claims[0].Verified {
		t.Fatalf("claim should be verified by a passing run_test: %+v", l.Claims)
	}
	if len(l.UnverifiedClaims()) != 0 {
		t.Fatalf("no unverified claims expected: %+v", l.UnverifiedClaims())
	}
}

func TestTestsPassUnverifiedWithoutRun(t *testing.T) {
	l := Analyze("All tests pass now.", []Action{{Tool: "read_file", OK: true}})
	unv := l.UnverifiedClaims()
	if len(unv) != 1 || unv[0].Label != "tests pass" {
		t.Fatalf("claim should be unverified without a run_test: %+v", l.Claims)
	}
	if !strings.Contains(unv[0].Note, "run_test") {
		t.Fatalf("note should explain what is missing: %q", unv[0].Note)
	}
}

func TestFailedRunDoesNotVerify(t *testing.T) {
	// A run_test that failed must NOT verify a "tests pass" claim.
	l := Analyze("Tests pass.", []Action{{Tool: "run_test", OK: false}})
	if len(l.UnverifiedClaims()) != 1 {
		t.Fatalf("failed run_test must not verify the claim: %+v", l.Claims)
	}
}

func TestCompilesVerifiedByDiagnostics(t *testing.T) {
	l := Analyze("The code compiles cleanly.", []Action{{Tool: "diagnostics", OK: true}})
	if len(l.Claims) != 1 || !l.Claims[0].Verified {
		t.Fatalf("compiles claim should be verified by diagnostics: %+v", l.Claims)
	}
}

func TestNoClaimsDetected(t *testing.T) {
	l := Analyze("I updated the README and added a helper function.", []Action{{Tool: "write_file", OK: true}})
	if len(l.Claims) != 0 {
		t.Fatalf("no success claims should be detected: %+v", l.Claims)
	}
	if !strings.Contains(Format(l), "none detected") {
		t.Fatalf("Format should report no claims: %q", Format(l))
	}
}

func TestFormatMarksUnverified(t *testing.T) {
	l := Analyze("tests pass and it compiles", nil)
	out := Format(l)
	if !strings.Contains(out, "unverified") {
		t.Fatalf("Format should mark unverified claims: %q", out)
	}
}
