package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	data := []byte("---\nname: tdd\ndescription: Test loop\ntriggers: test, tdd, coverage\n---\nStep 1. Write a failing test.\nStep 2. Make it pass.")
	sk, err := Parse(data, SourceBuiltin, "", "fallback")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if sk.Name != "tdd" || sk.Description != "Test loop" {
		t.Fatalf("frontmatter not parsed: %+v", sk)
	}
	if len(sk.Triggers) != 3 || sk.Triggers[0] != "test" {
		t.Fatalf("triggers = %v", sk.Triggers)
	}
	if !strings.HasPrefix(sk.Body, "Step 1.") || strings.Contains(sk.Body, "---") {
		t.Fatalf("body not extracted cleanly: %q", sk.Body)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	sk, err := Parse([]byte("just a body, no frontmatter"), SourceProject, "/p/x.md", "x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if sk.Name != "x" || sk.Body != "just a body, no frontmatter" {
		t.Fatalf("fallback parse wrong: %+v", sk)
	}
}

func TestBuiltinsLoaded(t *testing.T) {
	s := NewStore(t.TempDir())
	skills := s.List()
	if len(skills) < 5 {
		t.Fatalf("expected >=5 builtin skills, got %d", len(skills))
	}
	want := map[string]bool{"plan": false, "tdd": false, "review": false, "debug": false, "verify": false}
	for _, sk := range skills {
		if _, ok := want[sk.Name]; ok {
			want[sk.Name] = true
			if strings.TrimSpace(sk.Body) == "" {
				t.Fatalf("builtin %s has empty body", sk.Name)
			}
			if sk.Source != SourceBuiltin {
				t.Fatalf("builtin %s wrong source %s", sk.Name, sk.Source)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing builtin skill %q", name)
		}
	}
}

func TestProjectOverridesBuiltin(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".mimo", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "---\nname: tdd\ndescription: custom tdd\n---\nmy own tdd playbook"
	if err := os.WriteFile(filepath.Join(dir, "tdd.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(ws)
	sk, ok := s.Get("tdd")
	if !ok {
		t.Fatal("tdd not found")
	}
	if sk.Source != SourceProject || sk.Description != "custom tdd" {
		t.Fatalf("project skill did not override builtin: %+v", sk)
	}
}

func TestGetCaseInsensitive(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, ok := s.Get("PLAN"); !ok {
		t.Fatal("Get should be case-insensitive")
	}
	if _, ok := s.Get("does-not-exist"); ok {
		t.Fatal("Get should miss unknown skill")
	}
}

func TestParseMalformedFrontmatterNoLeak(t *testing.T) {
	// Opening fence but no closing fence: must not leak the raw "---" / metadata
	// lines as a fenced block, and must fall back to the filename for the name.
	data := []byte("---\nname: tdd\ndescription: x\n\nactual body here")
	sk, err := Parse(data, SourceProject, "/p/tdd.md", "tdd")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if strings.Contains(sk.Body, "---") {
		t.Fatalf("malformed body leaked the raw fence: %q", sk.Body)
	}
	if sk.Name != "tdd" {
		t.Fatalf("name should fall back to filename, got %q", sk.Name)
	}
}

func TestParseClosingFenceMustBeFullLine(t *testing.T) {
	// A "----" line is not a valid closing fence; body must not get a stray dash.
	data := []byte("---\nname: x\n----\nbody")
	sk, err := Parse(data, SourceProject, "", "x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, line := range strings.Split(sk.Body, "\n") {
		if strings.TrimSpace(line) == "-" {
			t.Fatalf("body contains a stray dash line: %q", sk.Body)
		}
	}
}

func TestListDedupsCaseInsensitive(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".mimo", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Project skill "TDD" must override builtin "tdd", not duplicate it.
	if err := os.WriteFile(filepath.Join(dir, "tdd.md"), []byte("---\nname: TDD\ndescription: custom\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(ws)
	count := 0
	for _, sk := range s.List() {
		if strings.EqualFold(sk.Name, "tdd") {
			count++
			if sk.Source != SourceProject {
				t.Fatalf("expected project override, got source %s", sk.Source)
			}
		}
	}
	if count != 1 {
		t.Fatalf("case-differing skill produced %d entries, want 1", count)
	}
}

func TestCombine(t *testing.T) {
	out := Combine([]Skill{
		{Name: "a", Description: "first", Body: "do a"},
		{Name: "b", Body: "do b"},
	})
	if !strings.Contains(out, "## Skill: a — first") || !strings.Contains(out, "do a") {
		t.Fatalf("combine missing skill a: %q", out)
	}
	if !strings.Contains(out, "## Skill: b") || !strings.Contains(out, "do b") {
		t.Fatalf("combine missing skill b: %q", out)
	}
	if Combine(nil) != "" {
		t.Fatal("Combine(nil) should be empty")
	}
}
