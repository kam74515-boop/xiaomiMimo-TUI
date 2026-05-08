package context

import (
	"errors"
	"strings"
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

func TestPromoteObservationSetsPlacementSourceTokensAndReason(t *testing.T) {
	obs := core.Observation{
		Summary:          "read target files",
		StateDelta:       "captured current implementation",
		RiskDelta:        "no new risk",
		NextAffordances:  []string{"write focused tests", "apply small patch"},
		ContextPlacement: core.TierArtifact,
		ArtifactID:       "artifact-123",
	}

	item := PromoteObservation(" obs-1 ", obs)
	if item.ID != "obs-1" {
		t.Fatalf("id = %q, want obs-1", item.ID)
	}
	if item.Tier != core.TierArtifact {
		t.Fatalf("tier = %q, want %q", item.Tier, core.TierArtifact)
	}
	if item.Source != ArtifactSource+"artifact-123" {
		t.Fatalf("source = %q, want artifact source", item.Source)
	}
	if item.Title != obs.Summary {
		t.Fatalf("title = %q, want %q", item.Title, obs.Summary)
	}
	wantTokens := core.EstimateTokens("read target files\ncaptured current implementation\nno new risk\nwrite focused tests\napply small patch")
	if item.TokenEstimate != wantTokens {
		t.Fatalf("token estimate = %d, want %d", item.TokenEstimate, wantTokens)
	}
	if !strings.Contains(item.Reason, "artifact context") || !strings.Contains(item.Reason, "artifact-123") {
		t.Fatalf("reason = %q, want placement and artifact id", item.Reason)
	}
}

func TestAutoBudgetLowRiskNoEviction(t *testing.T) {
	manager := New(100)
	_, _ = manager.Add(core.ContextItem{ID: "near-1", Tier: core.TierNear, TokenEstimate: 30})
	_, _ = manager.Add(core.ContextItem{ID: "near-2", Tier: core.TierNear, TokenEstimate: 30})

	// 60/100 = 60% -> low risk, no eviction expected.
	result := manager.AutoBudget()
	if len(result.Evicted) != 0 {
		t.Fatalf("evicted = %d, want 0 (low risk)", len(result.Evicted))
	}
	if len(manager.Snapshot().Items) != 2 {
		t.Fatalf("items after auto budget = %d, want 2", len(manager.Snapshot().Items))
	}
}

func TestAutoBudgetWarningEvictsNonPinnedNear(t *testing.T) {
	manager := New(100)
	_, _ = manager.Add(core.ContextItem{ID: "near-1", Tier: core.TierNear, TokenEstimate: 40})
	_, _ = manager.Add(core.ContextItem{ID: "near-2", Tier: core.TierNear, TokenEstimate: 30})
	_, _ = manager.Add(core.ContextItem{ID: "near-3", Tier: core.TierNear, TokenEstimate: 25})

	// 95/100 = 95% -> warning, should evict.
	result := manager.AutoBudget()
	if len(result.Evicted) == 0 {
		t.Fatal("evicted = 0, want some evictions at warning risk")
	}
	snapshot := manager.Snapshot()
	if snapshot.PollutionRisk == PollutionWarning || snapshot.PollutionRisk == PollutionOverWindow {
		t.Fatalf("pollution risk after eviction = %q, want low", snapshot.PollutionRisk)
	}
}

func TestAutoBudgetPreservesPinnedAndAnchor(t *testing.T) {
	manager := New(100)
	_, _ = manager.Add(core.ContextItem{ID: "near-1", Tier: core.TierNear, TokenEstimate: 40})
	_, _ = manager.Add(core.ContextItem{ID: "near-pinned", Tier: core.TierNear, TokenEstimate: 40, Pinned: true})
	_, _ = manager.Add(core.ContextItem{ID: "anchor-1", Tier: core.TierAnchor, TokenEstimate: 20, Pinned: true})

	// 100/100 = 100% -> over window.
	result := manager.AutoBudget()
	if len(result.Evicted) == 0 {
		t.Fatal("evicted = 0, want near-1 evicted")
	}

	// near-pinned and anchor-1 should still be present.
	ids := idSet(manager.Snapshot().Items)
	if !ids["near-pinned"] {
		t.Fatal("near-pinned was evicted, want preserved")
	}
	if !ids["anchor-1"] {
		t.Fatal("anchor-1 was evicted, want preserved")
	}
	if ids["near-1"] {
		t.Fatal("near-1 was not evicted, want evicted")
	}
}

func TestAutoBudgetEvictsExpiredFirst(t *testing.T) {
	manager := New(100)
	manager.now = func() time.Time { return time.Now() }

	expiredAt := time.Now().Add(-time.Minute)
	// near-old and near-extra are non-expired (90 tokens -> warning).
	// near-expired is expired and does not count toward the budget.
	_, _ = manager.Add(core.ContextItem{ID: "near-old", Tier: core.TierNear, TokenEstimate: 45})
	_, _ = manager.Add(core.ContextItem{ID: "near-expired", Tier: core.TierNear, TokenEstimate: 45, ExpiresAt: expiredAt})
	_, _ = manager.Add(core.ContextItem{ID: "near-extra", Tier: core.TierNear, TokenEstimate: 45})

	// Active items: near-old (45) + near-extra (45) = 90 / 100 = 90% -> warning.
	// Phase 1: near-expired should be evicted (cleanup).
	// Phase 2: near-old should be evicted to get below 85%.
	result := manager.AutoBudget()
	evictedIDs := make(map[string]bool)
	for _, id := range result.Evicted {
		evictedIDs[id] = true
	}
	if !evictedIDs["near-expired"] {
		t.Fatalf("expired item not evicted first: evicted=%v", result.Evicted)
	}
	// near-old (first non-expired) should be evicted to bring budget under 85%.
	if !evictedIDs["near-old"] {
		t.Fatal("near-old should be evicted after expired items")
	}
	// near-extra should be preserved (45 + 10 = 55 < 85 after Phase 2 stops).
	if evictedIDs["near-extra"] {
		t.Fatal("near-extra should not be evicted; budget is already safe")
	}
}

func TestPinUnpinStateTransitions(t *testing.T) {
	manager := New(100)
	_, _ = manager.Add(core.ContextItem{ID: "near-1", Tier: core.TierNear, TokenEstimate: 10})

	if manager.Snapshot().Items[0].Pinned {
		t.Fatal("item should not be pinned initially")
	}

	_, err := manager.Pin("near-1")
	if err != nil {
		t.Fatalf("pin error: %v", err)
	}
	if !manager.Snapshot().Items[0].Pinned {
		t.Fatal("item should be pinned after Pin")
	}

	_, err = manager.Unpin("near-1")
	if err != nil {
		t.Fatalf("unpin error: %v", err)
	}
	if manager.Snapshot().Items[0].Pinned {
		t.Fatal("item should be unpinned after Unpin")
	}
}

func TestPinUnpinMissingItemReturnsError(t *testing.T) {
	manager := New(100)
	_, err := manager.Pin("does-not-exist")
	if !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("pin missing error = %v, want ErrItemNotFound", err)
	}
	_, err = manager.Unpin("does-not-exist")
	if !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("unpin missing error = %v, want ErrItemNotFound", err)
	}
}

func idSet(items []core.ContextItem) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item.ID] = true
	}
	return set
}

func TestPromoteObservationPlacementDefaultsAndPreservesKnownTiers(t *testing.T) {
	tests := []struct {
		name      string
		placement core.ContextTier
		want      core.ContextTier
	}{
		{name: "default near", want: core.TierNear},
		{name: "unknown near", placement: core.ContextTier("unknown"), want: core.TierNear},
		{name: "near", placement: core.TierNear, want: core.TierNear},
		{name: "anchor", placement: core.TierAnchor, want: core.TierAnchor},
		{name: "artifact", placement: core.TierArtifact, want: core.TierArtifact},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := PromoteObservation("obs", core.Observation{
				Summary:          "summary",
				ContextPlacement: test.placement,
			})
			if item.Tier != test.want {
				t.Fatalf("tier = %q, want %q", item.Tier, test.want)
			}
			if item.Source != ObservationSource {
				t.Fatalf("source = %q, want %q", item.Source, ObservationSource)
			}
		})
	}
}
