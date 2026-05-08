package model

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultRegistrySeeded(t *testing.T) {
	r := DefaultRegistry()
	def := r.Default()
	if def.ID != "mimo-v2.5-pro" {
		t.Fatalf("default model = %q, want mimo-v2.5-pro", def.ID)
	}
	if def.Channel != ChannelDefault {
		t.Fatalf("default channel = %q, want default", def.Channel)
	}
	if def.ContextLimit != 1000000 {
		t.Fatalf("context limit = %d, want 1000000", def.ContextLimit)
	}
	if def.BaseURL != DefaultMiMoBaseURL {
		t.Fatalf("base URL = %q, want %q", def.BaseURL, DefaultMiMoBaseURL)
	}
	if !def.Accepted {
		t.Fatal("default model should be accepted")
	}
}

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(Info{
		ID:           "test-model",
		BaseURL:      "http://localhost:8080/v1",
		Channel:      ChannelLabs,
		ContextLimit: 128000,
		Description:  "A test model.",
	})

	info, ok := r.Get("test-model")
	if !ok {
		t.Fatal("test-model not found")
	}
	if info.Channel != ChannelLabs {
		t.Fatalf("channel = %q, want labs", info.Channel)
	}
	if info.ContextLimit != 128000 {
		t.Fatalf("context limit = %d, want 128000", info.ContextLimit)
	}
}

func TestGetMissingReturnsFalse(t *testing.T) {
	r := DefaultRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected false for missing model")
	}
}

func TestSetDefault(t *testing.T) {
	r := NewRegistry()
	r.Register(Info{ID: "a", Channel: ChannelDefault})
	r.Register(Info{ID: "b", Channel: ChannelDefault})

	// First registered becomes default automatically.
	if r.Default().ID != "a" {
		t.Fatalf("initial default = %q, want a", r.Default().ID)
	}

	if err := r.SetDefault("b"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if r.Default().ID != "b" {
		t.Fatalf("default after SetDefault = %q, want b", r.Default().ID)
	}
}

func TestSetDefaultUnregistered(t *testing.T) {
	r := NewRegistry()
	err := r.SetDefault("missing")
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("SetDefault missing error = %v, want ErrNotRegistered", err)
	}
}

func TestCandidates(t *testing.T) {
	r := NewRegistry()
	r.Register(Info{ID: "def", Channel: ChannelDefault})
	r.Register(Info{ID: "c1", Channel: ChannelCandidate})
	r.Register(Info{ID: "c2", Channel: ChannelCandidate})
	r.Register(Info{ID: "lab", Channel: ChannelLabs})

	candidates := r.Candidates()
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
	ids := make(map[string]bool)
	for _, c := range candidates {
		ids[c.ID] = true
	}
	if !ids["c1"] || !ids["c2"] {
		t.Fatalf("missing expected candidates in %#v", candidates)
	}
}

func TestAcceptCandidate(t *testing.T) {
	r := NewRegistry()
	r.Register(Info{ID: "def", Channel: ChannelDefault, Accepted: true})
	r.Register(Info{ID: "next-gen", Channel: ChannelCandidate, ContextLimit: 2000000})

	if err := r.AcceptCandidate("next-gen"); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	info, _ := r.Get("next-gen")
	if info.Channel != ChannelDefault {
		t.Fatalf("channel after accept = %q, want default", info.Channel)
	}
	if !info.Accepted {
		t.Fatal("Accepted should be true after promotion")
	}
	if r.Default().ID != "next-gen" {
		t.Fatalf("default after accept = %q, want next-gen", r.Default().ID)
	}
}

func TestAcceptCandidateRejectsNonCandidate(t *testing.T) {
	r := NewRegistry()
	r.Register(Info{ID: "def", Channel: ChannelDefault})

	if err := r.AcceptCandidate("def"); err == nil {
		t.Fatal("expected error when accepting a non-candidate")
	}

	r.Register(Info{ID: "lab", Channel: ChannelLabs})
	if err := r.AcceptCandidate("lab"); err == nil {
		t.Fatal("expected error when accepting a labs model")
	}
}

func TestAcceptCandidateRejectsUnregistered(t *testing.T) {
	r := NewRegistry()
	err := r.AcceptCandidate("ghost")
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("AcceptCandidate ghost error = %v, want ErrNotRegistered", err)
	}
}

func TestDefaultRegistryHasThreeModels(t *testing.T) {
	r := DefaultRegistry()
	if n := r.Len(); n != 3 {
		t.Fatalf("Len() = %d, want 3", n)
	}

	// mimo-v2.5-pro: default channel, accepted.
	pro, ok := r.Get("mimo-v2.5-pro")
	if !ok {
		t.Fatal("mimo-v2.5-pro not found")
	}
	if pro.Channel != ChannelDefault {
		t.Fatalf("mimo-v2.5-pro channel = %q, want default", pro.Channel)
	}
	if pro.ContextLimit != 1_000_000 {
		t.Fatalf("mimo-v2.5-pro context limit = %d, want 1000000", pro.ContextLimit)
	}
	if !pro.Accepted {
		t.Fatal("mimo-v2.5-pro should be accepted")
	}

	// mimo-v2.5-flash: candidate channel, not accepted.
	flash, ok := r.Get("mimo-v2.5-flash")
	if !ok {
		t.Fatal("mimo-v2.5-flash not found")
	}
	if flash.Channel != ChannelCandidate {
		t.Fatalf("mimo-v2.5-flash channel = %q, want candidate", flash.Channel)
	}
	if flash.ContextLimit != 128_000 {
		t.Fatalf("mimo-v2.5-flash context limit = %d, want 128000", flash.ContextLimit)
	}
	if flash.Accepted {
		t.Fatal("mimo-v2.5-flash should not be accepted")
	}

	// mimo-v2-pro: candidate channel, not accepted.
	pro2, ok := r.Get("mimo-v2-pro")
	if !ok {
		t.Fatal("mimo-v2-pro not found")
	}
	if pro2.Channel != ChannelCandidate {
		t.Fatalf("mimo-v2-pro channel = %q, want candidate", pro2.Channel)
	}
	if pro2.ContextLimit != 256_000 {
		t.Fatalf("mimo-v2-pro context limit = %d, want 256000", pro2.ContextLimit)
	}
	if pro2.Accepted {
		t.Fatal("mimo-v2-pro should not be accepted")
	}

	// Verify ListModels output contains all three model IDs.
	output := r.ListModels()
	for _, id := range []string{"mimo-v2.5-pro", "mimo-v2.5-flash", "mimo-v2-pro"} {
		if !strings.Contains(output, id) {
			t.Fatalf("ListModels() output does not contain %q", id)
		}
	}
}
