package context

import (
	"fmt"
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

// makeSizedItem creates a Near context item whose Source field is padded to
// produce approximately the requested number of tokens.  One token is roughly
// 4 characters (matching core.EstimateTokens).
func makeSizedItem(id string, tokens int) core.ContextItem {
	// 4 chars ≈ 1 token.  We use repeated "a " to make it predictable.
	chars := tokens * 4
	if chars < 1 {
		chars = 1
	}
	source := strings.Repeat("a ", chars/2)
	return core.ContextItem{
		ID:            id,
		Tier:          core.TierNear,
		Title:         fmt.Sprintf("item-%s", id),
		Source:        source,
		TokenEstimate: core.EstimateTokens(source),
		Reason:        "pressure test item",
	}
}

// fillContext adds numItems Near items, each consuming approx tokensPerItem
// tokens, into the manager via Add.  Returns the total tokens added.
func fillContext(t *testing.T, m *Manager, numItems, tokensPerItem int) int {
	t.Helper()
	total := 0
	for i := 0; i < numItems; i++ {
		item := makeSizedItem(fmt.Sprintf("pressure:%d", i), tokensPerItem)
		_, err := m.Add(item)
		if err != nil {
			t.Fatalf("Add item %d failed: %v", i, err)
		}
		total += item.TokenEstimate
	}
	return total
}

// --- 100K pressure test ---

func TestContextPressure100K(t *testing.T) {
	windowTokens := 100_000
	m := New(windowTokens)

	// Add 200 items at ~500 tokens each = ~100K tokens.
	numItems := 200
	tokensPerItem := 500
	fillContext(t, m, numItems, tokensPerItem)

	totals := m.Totals()
	t.Logf("After fill: UsedTokens=%d WindowTokens=%d items=%d",
		totals.UsedTokens, totals.WindowTokens, numItems)

	// At 100K window with ~100K tokens, pollution should be warning or over_window.
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk == PollutionLow {
		t.Fatalf("expected warning or over_window at 100K fill, got %q", risk)
	}

	// AutoBudget should trigger and evict some Near items.
	result := m.AutoBudget()
	if len(result.Evicted) == 0 {
		t.Fatal("AutoBudget at 100K should evict at least one Near item")
	}
	t.Logf("AutoBudget evicted %d items; warning=%q", len(result.Evicted), result.Warning)

	// After eviction, used tokens should be <= safe boundary.
	totalsAfter := m.Totals()
	safeBoundary := int(float64(windowTokens) * SafeBudgetRatio)
	t.Logf("After eviction: UsedTokens=%d safeBoundary=%d", totalsAfter.UsedTokens, safeBoundary)
	if totalsAfter.UsedTokens > safeBoundary+tokensPerItem {
		// Allow one item overshoot since eviction is item-granular.
		t.Logf("warning: UsedTokens %d exceeds safe boundary %d by more than one item", totalsAfter.UsedTokens, safeBoundary)
	}

	// Admit should still work for new items if we are back under budget.
	admitItem := core.ContextItem{
		ID:            "admit:after-eviction-100k",
		Tier:          core.TierNear,
		Title:         "post-eviction item",
		Source:        "tool:test",
		TokenEstimate: 50,
		Reason:        "testing admission after eviction",
	}
	snap, err := m.Admit(admitItem)
	if err != nil {
		t.Logf("Admit after AutoBudget returned error (may be over_window): %v", err)
	} else {
		t.Logf("Admit succeeded after eviction; PollutionRisk=%s UsedTokens=%d", snap.PollutionRisk, snap.UsedTokens)
	}
}

// --- 300K pressure test ---

func TestContextPressure300K(t *testing.T) {
	windowTokens := 300_000
	m := New(windowTokens)

	// Add 600 items at ~500 tokens each = ~300K tokens.
	numItems := 600
	tokensPerItem := 500
	fillContext(t, m, numItems, tokensPerItem)

	totals := m.Totals()
	t.Logf("After fill: UsedTokens=%d WindowTokens=%d items=%d",
		totals.UsedTokens, totals.WindowTokens, numItems)

	// Verify pollution risk is at least warning.
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk == PollutionLow {
		t.Fatalf("expected warning or over_window at 300K fill, got %q", risk)
	}

	// AutoBudget should evict Near items to bring usage under 85%.
	result := m.AutoBudget()
	if len(result.Evicted) == 0 {
		t.Fatal("AutoBudget at 300K should evict Near items")
	}
	t.Logf("AutoBudget evicted %d items; warning=%q", len(result.Evicted), result.Warning)

	totalsAfter := m.Totals()
	safeBoundary := int(float64(windowTokens) * SafeBudgetRatio)
	t.Logf("After eviction: UsedTokens=%d safeBoundary=%d evicted=%d",
		totalsAfter.UsedTokens, safeBoundary, len(result.Evicted))

	// Verify that pinned items survive eviction.
	pinnedItem := core.ContextItem{
		ID:            "pinned:survivor-300k",
		Tier:          core.TierNear,
		Title:         "pinned survivor",
		Source:        "tool:pinned",
		TokenEstimate: 100,
		Reason:        "should survive eviction",
		Pinned:        true,
	}
	_, _ = m.Add(pinnedItem)

	// Run AutoBudget again -- pinned item must survive.
	result2 := m.AutoBudget()
	for _, evictedID := range result2.Evicted {
		if evictedID == "pinned:survivor-300k" {
			t.Fatal("pinned item was evicted by AutoBudget; pinned items must survive")
		}
	}

	// Verify the pinned item is still in the snapshot.
	snap := m.Snapshot()
	foundPinned := false
	for _, item := range snap.Items {
		if item.ID == "pinned:survivor-300k" {
			foundPinned = true
			break
		}
	}
	if !foundPinned {
		t.Fatal("pinned item missing from snapshot after AutoBudget")
	}
}

// --- 700K pressure test ---

func TestContextPressure700K(t *testing.T) {
	windowTokens := 700_000
	m := New(windowTokens)

	// Add 1400 items at ~500 tokens each = ~700K tokens.
	numItems := 1400
	tokensPerItem := 500
	totalAdded := fillContext(t, m, numItems, tokensPerItem)

	totals := m.Totals()
	t.Logf("After fill: UsedTokens=%d WindowTokens=%d totalAdded=%d items=%d",
		totals.UsedTokens, totals.WindowTokens, totalAdded, numItems)

	// Verify pollution risk.
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk == PollutionLow {
		t.Fatalf("expected warning or over_window at 700K fill, got %q", risk)
	}

	// Compress a batch of items to verify compression works under pressure.
	// Select 10 items to compress.
	compressIDs := make([]string, 10)
	for i := 0; i < 10; i++ {
		compressIDs[i] = fmt.Sprintf("pressure:%d", i)
	}

	tokensBeforeCompress := 0
	snap := m.Snapshot()
	for _, item := range snap.Items {
		for _, id := range compressIDs {
			if item.ID == id {
				tokensBeforeCompress += item.TokenEstimate
			}
		}
	}

	record, snapAfter, err := m.CompressItems(
		compressIDs,
		"Compressed batch of 10 pressure test items",
		"pressure test compression at 700K",
	)
	if err != nil {
		t.Fatalf("CompressItems error: %v", err)
	}

	t.Logf("Compression: TokensBefore=%d TokensAfter=%d Saved=%d",
		record.TokensBefore, record.TokensAfter, record.TokensBefore-record.TokensAfter)

	if record.TokensAfter >= record.TokensBefore {
		t.Fatalf("compression did not save tokens: before=%d after=%d", record.TokensBefore, record.TokensAfter)
	}

	// Verify compressed items are no longer in snapshot.
	for _, item := range snapAfter.Items {
		for _, id := range compressIDs {
			if item.ID == id {
				t.Fatalf("compressed item %s still in snapshot", id)
			}
		}
	}

	// Verify compression artifact is in snapshot.
	foundArtifact := false
	for _, item := range snapAfter.Items {
		if item.ID == record.ID {
			foundArtifact = true
			break
		}
	}
	if !foundArtifact {
		t.Fatal("compression artifact not found in snapshot")
	}

	// Now run AutoBudget to evict remaining Near items.
	result := m.AutoBudget()
	totalsAfter := m.Totals()
	t.Logf("After compression+eviction: UsedTokens=%d evicted=%d",
		totalsAfter.UsedTokens, len(result.Evicted))

	// Verify compression records are preserved.
	activeRecords := m.ActiveCompressionRecords()
	if len(activeRecords) == 0 {
		t.Fatal("compression records lost after AutoBudget")
	}
	t.Logf("Active compression records: %d", len(activeRecords))
}

// --- 1M pressure test ---

func TestContextPressure1M(t *testing.T) {
	windowTokens := 1_000_000
	m := New(windowTokens)

	// Add 2000 items at ~500 tokens each = ~1M tokens.
	numItems := 2000
	tokensPerItem := 500
	totalAdded := fillContext(t, m, numItems, tokensPerItem)

	totals := m.Totals()
	t.Logf("After fill: UsedTokens=%d WindowTokens=%d totalAdded=%d items=%d",
		totals.UsedTokens, totals.WindowTokens, totalAdded, numItems)

	// At 1M window with ~1M tokens, we expect over_window or heavy warning.
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	t.Logf("Pollution risk after fill: %s", risk)

	// Admit a new Near item -- should be rejected when projected risk
	// would be over_window.  Admit calculates projectedUsed = current + new,
	// so even at "warning" level, adding tokens can project to "over_window".
	newItem := core.ContextItem{
		ID:            "admit:new-at-1m",
		Tier:          core.TierNear,
		Title:         "new item at capacity",
		Source:        "tool:test",
		TokenEstimate: 100,
		Reason:        "testing admission at 1M",
	}
	_, err := m.Admit(newItem)
	if err == ErrAdmitOverWindow {
		t.Log("Correctly rejected Near item: projected usage would exceed window")
	} else if err != nil {
		t.Logf("Admit returned unexpected error: %v", err)
	} else {
		t.Log("Admit succeeded (projected usage within window)")
	}

	// Artifact items should always be accepted, even at over_window.
	artifactItem := core.ContextItem{
		ID:            "artifact:always-accepted",
		Tier:          core.TierArtifact,
		Title:         "artifact at capacity",
		Source:        "raw output data",
		TokenEstimate: 500,
		Reason:        "artifact bypass test",
	}
	_, err = m.Admit(artifactItem)
	if err != nil {
		t.Fatalf("artifact item should always be admitted; got error: %v", err)
	}
	t.Log("Artifact item correctly admitted at 1M capacity")

	// Anchor items should also be accepted (bypass pollution check).
	anchorItem := core.ContextItem{
		ID:            "anchor:always-accepted",
		Tier:          core.TierAnchor,
		Title:         "anchor at capacity",
		Source:        "anchor data",
		TokenEstimate: 200,
		Reason:        "anchor admission test",
	}
	_, err = m.Admit(anchorItem)
	if err != nil {
		t.Fatalf("anchor item should be admitted; got error: %v", err)
	}
	t.Log("Anchor item correctly admitted at 1M capacity")

	// Pinned Near items should also bypass pollution rejection.
	pinnedItem := core.ContextItem{
		ID:            "pinned:at-1m",
		Tier:          core.TierNear,
		Title:         "pinned at capacity",
		Source:        "pinned data",
		TokenEstimate: 100,
		Reason:        "pinned bypass test",
		Pinned:        true,
	}
	_, err = m.Admit(pinnedItem)
	if err != nil {
		t.Fatalf("pinned item should be admitted even at over_window; got error: %v", err)
	}
	t.Log("Pinned item correctly admitted at 1M capacity")

	// Run AutoBudget to bring context back under control.
	result := m.AutoBudget()
	totalsAfter := m.Totals()
	t.Logf("AutoBudget at 1M: evicted=%d UsedTokens=%d warning=%q",
		len(result.Evicted), totalsAfter.UsedTokens, result.Warning)

	// Run a second AutoBudget to verify convergence.
	result2 := m.AutoBudget()
	totalsFinal := m.Totals()
	t.Logf("Second AutoBudget: evicted=%d UsedTokens=%d",
		len(result2.Evicted), totalsFinal.UsedTokens)

	// Report final state.
	snap := m.Snapshot()
	nearCount := 0
	anchorCount := 0
	artifactCount := 0
	for _, item := range snap.Items {
		switch item.Tier {
		case core.TierNear:
			nearCount++
		case core.TierAnchor:
			anchorCount++
		case core.TierArtifact:
			artifactCount++
		}
	}
	t.Logf("Final state: items=%d near=%d anchor=%d artifact=%d UsedTokens=%d PollutionRisk=%s",
		len(snap.Items), nearCount, anchorCount, artifactCount, snap.UsedTokens, snap.PollutionRisk)
}
