package context

import (
	"errors"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

func TestNewSeededDefaultsWindowAndAnchors(t *testing.T) {
	manager := NewSeeded(0, "cmd/mimo, internal/core", "implement context manager")

	snapshot := manager.Snapshot()
	if snapshot.WindowTokens != DefaultWindowTokens {
		t.Fatalf("window tokens = %d, want %d", snapshot.WindowTokens, DefaultWindowTokens)
	}
	if len(snapshot.Items) != 2 {
		t.Fatalf("snapshot items = %d, want 2", len(snapshot.Items))
	}
	for _, item := range snapshot.Items {
		if item.Tier != core.TierAnchor {
			t.Fatalf("seed item tier = %q, want %q", item.Tier, core.TierAnchor)
		}
		if !item.Pinned {
			t.Fatalf("seed item %q should be pinned", item.ID)
		}
	}
}

func TestAddUpdatePinRemoveAndTotals(t *testing.T) {
	manager := New(100)

	if _, err := manager.Add(core.ContextItem{ID: "near-1", TokenEstimate: 20}); err != nil {
		t.Fatalf("add near: %v", err)
	}
	if _, err := manager.Add(core.ContextItem{ID: "artifact-1", Tier: core.TierArtifact, TokenEstimate: 30}); err != nil {
		t.Fatalf("add artifact: %v", err)
	}
	if _, err := manager.Pin("artifact-1"); err != nil {
		t.Fatalf("pin artifact: %v", err)
	}
	if _, err := manager.Update(core.ContextItem{ID: "near-1", Tier: core.TierNear, TokenEstimate: 25}); err != nil {
		t.Fatalf("update near: %v", err)
	}

	totals := manager.Totals()
	if totals.UsedTokens != 55 || totals.NearTokens != 25 || totals.ArtifactTokens != 30 || totals.PinnedTokens != 30 {
		t.Fatalf("unexpected totals: %+v", totals)
	}

	snapshot, err := manager.Remove("near-1")
	if err != nil {
		t.Fatalf("remove near: %v", err)
	}
	if snapshot.UsedTokens != 30 {
		t.Fatalf("used tokens after remove = %d, want 30", snapshot.UsedTokens)
	}
	if _, err := manager.Unpin("artifact-1"); err != nil {
		t.Fatalf("unpin artifact: %v", err)
	}
	if manager.Totals().PinnedTokens != 0 {
		t.Fatalf("pinned tokens = %d, want 0", manager.Totals().PinnedTokens)
	}
}

func TestErrorsAndPollutionWarning(t *testing.T) {
	manager := New(100)
	if _, err := manager.Add(core.ContextItem{}); !errors.Is(err, ErrInvalidItemID) {
		t.Fatalf("empty id error = %v, want %v", err, ErrInvalidItemID)
	}
	if _, err := manager.Add(core.ContextItem{ID: "big", TokenEstimate: 85}); err != nil {
		t.Fatalf("add big: %v", err)
	}
	if risk := manager.Snapshot().PollutionRisk; risk != PollutionWarning {
		t.Fatalf("risk = %q, want %q", risk, PollutionWarning)
	}
	if _, err := manager.Add(core.ContextItem{ID: "big", TokenEstimate: 1}); !errors.Is(err, ErrDuplicateItem) {
		t.Fatalf("duplicate error = %v, want %v", err, ErrDuplicateItem)
	}
	if _, err := manager.Update(core.ContextItem{ID: "missing", TokenEstimate: 1}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("missing update error = %v, want %v", err, ErrItemNotFound)
	}
}

func TestExpiredUnpinnedItemsDropFromSnapshot(t *testing.T) {
	manager := New(100)
	expiredAt := time.Now().Add(-time.Minute)

	if _, err := manager.Add(core.ContextItem{ID: "near-expired", TokenEstimate: 10, ExpiresAt: expiredAt}); err != nil {
		t.Fatalf("add expired item: %v", err)
	}
	if snapshot := manager.Snapshot(); len(snapshot.Items) != 0 || snapshot.UsedTokens != 0 {
		t.Fatalf("expired item should be omitted: %+v", snapshot)
	}

	if _, err := manager.Upsert(core.ContextItem{ID: "near-expired", TokenEstimate: 10, Pinned: true, ExpiresAt: expiredAt}); err != nil {
		t.Fatalf("upsert pinned expired item: %v", err)
	}
	if snapshot := manager.Snapshot(); len(snapshot.Items) != 1 || snapshot.UsedTokens != 10 {
		t.Fatalf("pinned expired item should remain visible: %+v", snapshot)
	}
}
