package context

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

// TestAdmissionRequiresSource verifies that items without a Source field
// are rejected by Admit.
func TestAdmissionRequiresSource(t *testing.T) {
	m := New(100_000)

	item := core.ContextItem{
		ID:            "no-source",
		Tier:          core.TierNear,
		Title:         "missing source",
		Source:        "", // empty
		TokenEstimate: 50,
		Reason:        "test",
	}
	_, err := m.Admit(item)

	if err != ErrAdmitNoSource {
		t.Fatalf("expected ErrAdmitNoSource, got %v", err)
	}
}

// TestAdmissionRequiresReason verifies that items without a Reason field
// are rejected by Admit.
func TestAdmissionRequiresReason(t *testing.T) {
	m := New(100_000)

	item := core.ContextItem{
		ID:            "no-reason",
		Tier:          core.TierNear,
		Title:         "missing reason",
		Source:        "tool:test",
		TokenEstimate: 50,
		Reason:        "", // empty
	}
	_, err := m.Admit(item)

	if err != ErrAdmitNoReason {
		t.Fatalf("expected ErrAdmitNoReason, got %v", err)
	}
}

// TestAdmissionBypassesForArtifact verifies that Artifact-tier items
// are always accepted, even without Source or Reason, and even when
// the context is over_window.
func TestAdmissionBypassesForArtifact(t *testing.T) {
	m := New(10_000) // Small window to force over_window easily.

	// Fill context to capacity.
	for i := 0; i < 25; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            string(rune('a'+i%26)) + strings.Repeat("x", i),
			Tier:          core.TierNear,
			Title:         "fill",
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "fill",
		})
	}

	// Artifact without Source or Reason -- should still be accepted.
	artifact := core.ContextItem{
		ID:            "artifact:bypass",
		Tier:          core.TierArtifact,
		Title:         "raw output",
		Source:        "", // no source
		TokenEstimate: 500,
		Reason:        "", // no reason
	}
	_, err := m.Admit(artifact)
	if err != nil {
		t.Fatalf("artifact should bypass admission; got error: %v", err)
	}

	// Verify it's in the snapshot.
	snap := m.Snapshot()
	found := false
	for _, item := range snap.Items {
		if item.ID == "artifact:bypass" {
			found = true
			if item.SelectionReason == "" {
				t.Fatal("artifact item should have SelectionReason set")
			}
			t.Logf("Artifact SelectionReason: %s", item.SelectionReason)
			break
		}
	}
	if !found {
		t.Fatal("artifact item not found in snapshot")
	}
}

// TestAdmissionTruncatesOversized verifies that items with Source longer
// than 2000 characters are forced to Artifact tier with a truncated Source.
func TestAdmissionTruncatesOversized(t *testing.T) {
	m := New(1_000_000)

	// Create an item with a 3000-char Source.
	longSource := strings.Repeat("x", 3000)
	item := core.ContextItem{
		ID:            "oversized:source",
		Tier:          core.TierNear,
		Title:         "oversized item",
		Source:        longSource,
		TokenEstimate: core.EstimateTokens(longSource),
		Reason:        "testing truncation",
	}

	snap, err := m.Admit(item)
	if err != nil {
		t.Fatalf("Admit oversized item failed: %v", err)
	}

	// Find the admitted item.
	var admitted core.ContextItem
	for _, it := range snap.Items {
		if it.ID == "oversized:source" {
			admitted = it
			break
		}
	}

	if admitted.ID == "" {
		t.Fatal("oversized item not found in snapshot")
	}

	// Should have been forced to artifact tier.
	if admitted.Tier != core.TierArtifact {
		t.Fatalf("expected tier %q, got %q", core.TierArtifact, admitted.Tier)
	}

	// Source should be truncated to 2000 chars + "...[truncated]" suffix.
	if !strings.HasSuffix(admitted.Source, "...[truncated]") {
		t.Fatalf("source should end with '...[truncated]', got suffix: %q",
			admitted.Source[len(admitted.Source)-20:])
	}

	if len(admitted.Source) != 2000+len("...[truncated]") {
		t.Fatalf("source length = %d, want %d", len(admitted.Source), 2000+len("...[truncated]"))
	}

	// SelectionReason should mention the truncation / size limit.
	if !strings.Contains(admitted.SelectionReason, "exceeded 2000 chars") {
		t.Fatalf("SelectionReason should mention size limit: %q", admitted.SelectionReason)
	}

	t.Logf("Oversized item: Tier=%s SourceLen=%d Reason=%s",
		admitted.Tier, len(admitted.Source), admitted.SelectionReason)
}

// TestAdmissionDeduplication verifies that re-admitting an item with the
// same ID within a short window updates (upserts) the existing item
// rather than creating a duplicate entry.  Items with different IDs but
// identical content are treated as distinct.
func TestAdmissionDeduplication(t *testing.T) {
	m := New(1_000_000)

	item1 := core.ContextItem{
		ID:            "dedup:same-id",
		Tier:          core.TierNear,
		Title:         "duplicate test",
		Source:        "tool:test",
		TokenEstimate: 100,
		Reason:        "first admission",
	}

	// First admission.
	snap1, err := m.Admit(item1)
	if err != nil {
		t.Fatalf("first Admit failed: %v", err)
	}
	count1 := len(snap1.Items)

	// Second admission with same ID but different Reason (within 60s).
	item2 := item1
	item2.Reason = "second admission (update)"
	snap2, err := m.Admit(item2)
	if err != nil {
		t.Fatalf("second Admit failed: %v", err)
	}
	count2 := len(snap2.Items)

	// Should not increase item count (upsert, not duplicate).
	if count2 != count1 {
		t.Fatalf("item count changed from %d to %d; expected upsert (no new item)", count1, count2)
	}

	// Verify the item was updated with the new reason.
	for _, item := range snap2.Items {
		if item.ID == "dedup:same-id" {
			if item.Reason != "second admission (update)" {
				t.Fatalf("item reason not updated: got %q", item.Reason)
			}
			break
		}
	}

	// Now admit a different ID with the same content -- should be a new item.
	item3 := core.ContextItem{
		ID:            "dedup:different-id",
		Tier:          core.TierNear,
		Title:         "duplicate test", // same title
		Source:        "tool:test",      // same source
		TokenEstimate: 100,
		Reason:        "first admission", // same reason
	}
	snap3, err := m.Admit(item3)
	if err != nil {
		t.Fatalf("third Admit (different ID) failed: %v", err)
	}

	// Should have one more item now.
	if len(snap3.Items) != count2+1 {
		t.Fatalf("expected %d items after admitting different-ID duplicate, got %d",
			count2+1, len(snap3.Items))
	}

	t.Logf("Dedup test: same-ID upsert preserved count=%d; different-ID added count=%d",
		count2, len(snap3.Items))
}

// TestAdmissionSetsSelectionReason verifies that Admit sets SelectionReason
// on all admitted items.
func TestAdmissionSetsSelectionReason(t *testing.T) {
	m := New(100_000)

	tests := []struct {
		name string
		item core.ContextItem
	}{
		{
			name: "near item under budget",
			item: core.ContextItem{
				ID: "reason:near", Tier: core.TierNear,
				Title: "test", Source: "tool:test",
				TokenEstimate: 50, Reason: "test reason",
			},
		},
		{
			name: "anchor item",
			item: core.ContextItem{
				ID: "reason:anchor", Tier: core.TierAnchor,
				Title: "test", Source: "tool:test",
				TokenEstimate: 50, Reason: "test reason",
			},
		},
		{
			name: "artifact item",
			item: core.ContextItem{
				ID: "reason:artifact", Tier: core.TierArtifact,
				Title: "test", Source: "tool:test",
				TokenEstimate: 50, Reason: "test reason",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := m.Admit(tc.item)
			if err != nil {
				t.Fatalf("Admit failed: %v", err)
			}
			for _, item := range snap.Items {
				if item.ID == tc.item.ID {
					if item.SelectionReason == "" {
						t.Fatalf("SelectionReason is empty for %s", tc.name)
					}
					t.Logf("SelectionReason: %s", item.SelectionReason)
					break
				}
			}
		})
	}
}
