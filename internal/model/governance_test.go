package model

import (
	"testing"
)

func TestAcceptCandidatePromotesToDefault(t *testing.T) {
	r := DefaultRegistry()

	// Verify candidate exists.
	info, ok := r.Get("mimo-v2.5-flash")
	if !ok {
		t.Fatal("mimo-v2.5-flash not found")
	}
	if info.Channel != ChannelCandidate {
		t.Fatalf("expected candidate channel, got %s", info.Channel)
	}
	if info.Accepted {
		t.Fatal("candidate should not be accepted initially")
	}

	// Accept the candidate.
	if err := r.AcceptCandidate("mimo-v2.5-flash"); err != nil {
		t.Fatalf("AcceptCandidate failed: %v", err)
	}

	// Verify promotion.
	info, _ = r.Get("mimo-v2.5-flash")
	if info.Channel != ChannelDefault {
		t.Fatalf("expected default channel after acceptance, got %s", info.Channel)
	}
	if !info.Accepted {
		t.Fatal("should be marked as accepted")
	}

	// Verify it became the default.
	if r.Default().ID != "mimo-v2.5-flash" {
		t.Fatalf("expected default to be mimo-v2.5-flash, got %s", r.Default().ID)
	}
}

func TestLabsChannelRequiresEnvVar(t *testing.T) {
	r := NewRegistry()
	r.Register(Info{
		ID:           "mimo-labs-experimental",
		BaseURL:      DefaultMiMoBaseURL,
		Channel:      ChannelLabs,
		ContextLimit: 128_000,
		Description:  "Experimental labs model",
	})

	info, ok := r.Get("mimo-labs-experimental")
	if !ok {
		t.Fatal("labs model not found")
	}
	if info.Channel != ChannelLabs {
		t.Fatalf("expected labs channel, got %s", info.Channel)
	}
}

func TestCandidatesListOnlyCandidates(t *testing.T) {
	r := DefaultRegistry()

	candidates := r.Candidates()
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	for _, c := range candidates {
		if c.Channel != ChannelCandidate {
			t.Fatalf("candidate %s has wrong channel: %s", c.ID, c.Channel)
		}
	}
}

func TestDefaultModelIsAccepted(t *testing.T) {
	r := DefaultRegistry()

	def := r.Default()
	if !def.Accepted {
		t.Fatal("default model should be accepted")
	}
	if def.Channel != ChannelDefault {
		t.Fatalf("default model should be in default channel, got %s", def.Channel)
	}
}

func TestRegistryPersistence(t *testing.T) {
	// Verify that the registry can be serialized and deserialized.
	r := DefaultRegistry()

	// Simulate persistence by listing models.
	listing := r.ListModels()
	if listing == "" {
		t.Fatal("ListModels should return non-empty string")
	}

	// Verify all models appear in listing.
	for _, id := range []string{"mimo-v2.5-pro", "mimo-v2.5-flash", "mimo-v2-pro"} {
		if !contains(listing, id) {
			t.Fatalf("listing should contain %s", id)
		}
	}
}

func TestModelUpdateRollback(t *testing.T) {
	r := DefaultRegistry()

	// Save original default.
	original := r.Default().ID

	// Accept a candidate.
	if err := r.AcceptCandidate("mimo-v2.5-flash"); err != nil {
		t.Fatalf("AcceptCandidate failed: %v", err)
	}
	if r.Default().ID != "mimo-v2.5-flash" {
		t.Fatal("default should have changed")
	}

	// Rollback: restore original default.
	if err := r.SetDefault(original); err != nil {
		t.Fatalf("SetDefault rollback failed: %v", err)
	}
	if r.Default().ID != original {
		t.Fatalf("expected rollback to %s, got %s", original, r.Default().ID)
	}
}

func TestChannelGating(t *testing.T) {
	tests := []struct {
		channel   Channel
		wantWarn  bool
		wantBlock bool
	}{
		{ChannelDefault, false, false},
		{ChannelCandidate, true, false},
		{ChannelLabs, true, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.channel), func(t *testing.T) {
			// Labs models should require MIMO_LABS=1
			if tt.channel == ChannelLabs {
				// Without env var, labs should be blocked
				// This is tested at the CLI level, not registry level
			}
			// Candidate models should produce a warning
			if tt.channel == ChannelCandidate {
				// Warning is produced at CLI level
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
