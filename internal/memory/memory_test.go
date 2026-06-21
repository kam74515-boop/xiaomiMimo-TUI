package memory

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"mimo-tui/internal/core"
)

// isolate points global memory at a temp home so tests are deterministic
// regardless of the developer's real ~/.mimo-tui/MEMORY.md.
func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return t.TempDir()
}

func TestAppendAndLoadProject(t *testing.T) {
	ws := isolate(t)
	s := NewStore(ws)

	if got := s.Load(); got != "" {
		t.Fatalf("fresh Load() = %q, want empty", got)
	}

	if err := s.Append(ScopeProject, "use go-toml/v2 for config"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append("", "raw tool output stays artifact-backed"); err != nil {
		t.Fatalf("Append (default scope): %v", err)
	}

	got := s.Load()
	if !strings.Contains(got, "## Project") {
		t.Fatalf("Load() missing Project section: %q", got)
	}
	if !strings.Contains(got, "use go-toml/v2 for config") || !strings.Contains(got, "artifact-backed") {
		t.Fatalf("Load() missing recorded facts: %q", got)
	}
}

func TestAppendEmptyRejected(t *testing.T) {
	s := NewStore(isolate(t))
	if err := s.Append(ScopeProject, "   "); err == nil {
		t.Fatal("expected error for empty note")
	}
}

func TestGlobalAndProjectCombined(t *testing.T) {
	ws := isolate(t)
	s := NewStore(ws)
	if err := s.Append(ScopeGlobal, "prefer small reversible steps"); err != nil {
		t.Fatalf("Append global: %v", err)
	}
	if err := s.Append(ScopeProject, "this repo is clean-room"); err != nil {
		t.Fatalf("Append project: %v", err)
	}
	got := s.Load()
	if !strings.Contains(got, "## Global") || !strings.Contains(got, "## Project") {
		t.Fatalf("Load() missing a section: %q", got)
	}
	// Global is listed before project.
	if strings.Index(got, "## Global") > strings.Index(got, "## Project") {
		t.Fatalf("global should precede project: %q", got)
	}
}

func TestAnchorItem(t *testing.T) {
	ws := isolate(t)
	s := NewStore(ws)
	if _, ok := s.AnchorItem(); ok {
		t.Fatal("AnchorItem should be absent when memory is empty")
	}
	if err := s.Append(ScopeProject, "context map is a governed view, not a prompt bucket"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	item, ok := s.AnchorItem()
	if !ok {
		t.Fatal("AnchorItem should exist after append")
	}
	if item.ID != MemoryAnchorID || item.Tier != core.TierAnchor || !item.Pinned {
		t.Fatalf("anchor item not a pinned anchor: %+v", item)
	}
	if !strings.Contains(item.Source, "governed view") {
		t.Fatalf("anchor source missing fact: %q", item.Source)
	}
	if item.TokenEstimate <= 0 {
		t.Fatalf("anchor token estimate = %d, want > 0", item.TokenEstimate)
	}
}

func TestConcurrentAppendNoLostUpdates(t *testing.T) {
	ws := isolate(t)
	const writers = 8
	const perWriter = 5

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each goroutine uses its own Store instance, like the agent tool
			// and the runtime do, to exercise the process-wide file lock.
			s := NewStore(ws)
			for i := 0; i < perWriter; i++ {
				if err := s.Append(ScopeProject, fmt.Sprintf("fact w%d-i%d", w, i)); err != nil {
					t.Errorf("append: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()

	got := NewStore(ws).Load()
	count := strings.Count(got, "fact w")
	if count != writers*perWriter {
		t.Fatalf("recorded %d facts, want %d (lost updates)", count, writers*perWriter)
	}
}

func TestLoadBounded(t *testing.T) {
	ws := isolate(t)
	s := NewStore(ws)
	for i := 0; i < 200; i++ {
		if err := s.Append(ScopeProject, fmt.Sprintf("fact number %d with some padding text", i)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	full := s.Load()
	if len(full) <= 500 {
		t.Fatalf("expected a large memory file, got %d bytes", len(full))
	}
	bounded := s.LoadBounded(500)
	if len(bounded) > 600 { // 500 budget + small truncation marker
		t.Fatalf("LoadBounded len = %d, want <= ~600", len(bounded))
	}
	if !strings.Contains(bounded, "truncated") {
		t.Fatalf("bounded memory should carry a truncation marker: %q", bounded)
	}
	// A budget larger than the content returns it unchanged.
	if s.LoadBounded(len(full)+10) != full {
		t.Fatal("LoadBounded with a large budget should return full content")
	}
}

func TestNormalizeNote(t *testing.T) {
	cases := map[string]string{
		"  hello   world  ":  "hello world",
		"- already bulleted": "already bulleted",
		"line one\nline two": "line one line two",
		"":                   "",
	}
	for in, want := range cases {
		if got := normalizeNote(in); got != want {
			t.Errorf("normalizeNote(%q) = %q, want %q", in, got, want)
		}
	}
}
