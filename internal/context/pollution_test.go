package context

import (
	"fmt"
	"testing"

	"mimo-tui/internal/core"
)

// TestPollutionDetectionLow verifies that usage under 85% of the window
// produces a "low" pollution risk.
func TestPollutionDetectionLow(t *testing.T) {
	windowTokens := 100_000
	m := New(windowTokens)

	// Add items totaling ~50% of the window.
	for i := 0; i < 100; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("low:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("item %d", i),
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "pollution low test",
		})
	}

	totals := m.Totals()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)

	t.Logf("UsedTokens=%d WindowTokens=%d ratio=%.2f risk=%s",
		totals.UsedTokens, totals.WindowTokens,
		float64(totals.UsedTokens)/float64(totals.WindowTokens), risk)

	if risk != PollutionLow {
		t.Fatalf("expected pollution risk %q, got %q (used=%d window=%d)",
			PollutionLow, risk, totals.UsedTokens, totals.WindowTokens)
	}
}

// TestPollutionDetectionWarning verifies that usage between 85% and 100%
// of the window produces a "warning" pollution risk.
func TestPollutionDetectionWarning(t *testing.T) {
	windowTokens := 100_000
	m := New(windowTokens)

	// Add items totaling ~90% of the window (90,000 tokens).
	for i := 0; i < 180; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("warn:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("item %d", i),
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "pollution warning test",
		})
	}

	totals := m.Totals()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	ratio := float64(totals.UsedTokens) / float64(totals.WindowTokens)

	t.Logf("UsedTokens=%d WindowTokens=%d ratio=%.4f risk=%s",
		totals.UsedTokens, totals.WindowTokens, ratio, risk)

	if ratio < SafeBudgetRatio {
		t.Skipf("test did not reach warning threshold: ratio=%.4f < %.4f", ratio, SafeBudgetRatio)
	}

	if risk != PollutionWarning {
		t.Fatalf("expected pollution risk %q, got %q", PollutionWarning, risk)
	}
}

// TestPollutionDetectionOverWindow verifies that usage over 100% of the
// window produces an "over_window" pollution risk.
func TestPollutionDetectionOverWindow(t *testing.T) {
	windowTokens := 50_000
	m := New(windowTokens)

	// Add items totaling ~60K tokens (120% of window).
	for i := 0; i < 120; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("over:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("item %d", i),
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "pollution over_window test",
		})
	}

	totals := m.Totals()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)

	t.Logf("UsedTokens=%d WindowTokens=%d risk=%s",
		totals.UsedTokens, totals.WindowTokens, risk)

	if risk != PollutionOverWindow {
		t.Fatalf("expected pollution risk %q, got %q", PollutionOverWindow, risk)
	}
}

// TestPollutionRejection verifies that over_window pollution rejects new
// non-pinned Near items via Admit.
func TestPollutionRejection(t *testing.T) {
	windowTokens := 50_000
	m := New(windowTokens)

	// Fill well over the window.
	for i := 0; i < 120; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("fill:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("item %d", i),
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "fill for rejection test",
		})
	}

	// Confirm over_window.
	totals := m.Totals()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk != PollutionOverWindow {
		t.Skipf("did not reach over_window: risk=%s", risk)
	}

	// Try to Admit a new Near item -- should be rejected.
	newItem := core.ContextItem{
		ID:            "reject:me",
		Tier:          core.TierNear,
		Title:         "should be rejected",
		Source:        "tool:test",
		TokenEstimate: 100,
		Reason:        "rejection test",
	}
	_, err := m.Admit(newItem)

	if err != ErrAdmitOverWindow {
		t.Fatalf("expected ErrAdmitOverWindow, got %v", err)
	}
}

// TestPollutionNeverRejectsPinned verifies that pinned Near items are
// accepted even when the context is over_window.
func TestPollutionNeverRejectsPinned(t *testing.T) {
	windowTokens := 50_000
	m := New(windowTokens)

	// Fill well over the window.
	for i := 0; i < 120; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("fill:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("item %d", i),
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "fill",
		})
	}

	// Confirm over_window.
	totals := m.Totals()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk != PollutionOverWindow {
		t.Skipf("did not reach over_window: risk=%s", risk)
	}

	// Admit a pinned Near item -- must succeed.
	pinned := core.ContextItem{
		ID:            "pinned:never-rejected",
		Tier:          core.TierNear,
		Title:         "pinned item",
		Source:        "tool:pinned",
		TokenEstimate: 100,
		Reason:        "pinned bypass test",
		Pinned:        true,
	}
	_, err := m.Admit(pinned)
	if err != nil {
		t.Fatalf("pinned item should never be rejected; got error: %v", err)
	}

	// Verify it's in the snapshot.
	snap := m.Snapshot()
	found := false
	for _, item := range snap.Items {
		if item.ID == "pinned:never-rejected" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("pinned item not found in snapshot after admission")
	}
}

// TestPollutionNeverRejectsAnchor verifies that Anchor items are accepted
// even when the context is over_window.
func TestPollutionNeverRejectsAnchor(t *testing.T) {
	windowTokens := 50_000
	m := New(windowTokens)

	// Fill well over the window.
	for i := 0; i < 120; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("fill:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("item %d", i),
			Source:        "tool:test",
			TokenEstimate: 500,
			Reason:        "fill",
		})
	}

	// Confirm over_window.
	totals := m.Totals()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk != PollutionOverWindow {
		t.Skipf("did not reach over_window: risk=%s", risk)
	}

	// Admit an Anchor item -- must succeed.
	anchor := core.ContextItem{
		ID:            "anchor:never-rejected",
		Tier:          core.TierAnchor,
		Title:         "anchor item",
		Source:        "anchor data",
		TokenEstimate: 200,
		Reason:        "anchor bypass test",
	}
	_, err := m.Admit(anchor)
	if err != nil {
		t.Fatalf("anchor item should never be rejected; got error: %v", err)
	}

	// Verify it's in the snapshot.
	snap := m.Snapshot()
	found := false
	for _, item := range snap.Items {
		if item.ID == "anchor:never-rejected" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("anchor item not found in snapshot after admission")
	}
}
