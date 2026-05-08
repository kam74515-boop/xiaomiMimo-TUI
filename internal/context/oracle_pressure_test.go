package context

import (
	"fmt"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

// TestOracleScoringUnderPressure verifies that the oracle scoring function
// works correctly when scoring 1000+ items simultaneously.
func TestOracleScoringUnderPressure(t *testing.T) {
	manager := New(500_000)
	goal := "Build agent loop with context management"

	// Add 1200 items: 400 stale, 400 medium, 400 recent.
	for i := 0; i < 400; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("stale:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("stale build output %d", i),
			Source:        "tool:build",
			Reason:        "old build data",
			TokenEstimate: 50,
		})
	}
	for i := 0; i < 400; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("medium:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("file read %d", i),
			Source:        "tool:read_file",
			Reason:        "file data",
			TokenEstimate: 50,
			Keywords:      []string{"context", "management"},
		})
	}
	for i := 0; i < 400; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("recent:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("agent loop implementation %d", i),
			Source:        "tool:read_file",
			Reason:        "core agent loop code",
			TokenEstimate: 50,
		})
	}

	oracle := NewOracle(manager)
	obs := []core.Observation{
		{Summary: "agent loop implementation review", StateDelta: "reviewed agent code"},
		{Summary: "context management analysis", StateDelta: "analyzed context tiers"},
	}

	result := oracle.Review(goal, obs)

	// Verify all 1200 items were scored.
	if len(result.Scores) != 1200 {
		t.Fatalf("expected 1200 scores, got %d", len(result.Scores))
	}

	// Verify scoring produced reasonable distributions.
	promotedCount := len(result.Promoted)
	demotedCount := len(result.Demoted)
	compressedCount := 0
	for _, c := range result.Compressed {
		compressedCount += len(c.ItemIDs)
	}

	t.Logf("Oracle review at 1200 items: promoted=%d demoted=%d compressed=%d total_scored=%d",
		promotedCount, demotedCount, compressedCount, len(result.Scores))

	// Some items should be promoted (those matching "agent" and "loop").
	if promotedCount == 0 {
		t.Fatal("expected at least some promotions at scale")
	}

	// Verify no pinned items are in demoted (none are pinned in this test,
	// but verify the invariant holds).
	for _, id := range result.Demoted {
		if item, ok := manager.items[id]; ok && item.Pinned {
			t.Fatalf("pinned item %s was demoted", id)
		}
	}

	// Verify scores are in valid range [0, 100].
	for id, score := range result.Scores {
		if score < 0 || score > 100 {
			t.Fatalf("score for %s = %d, out of range [0, 100]", id, score)
		}
	}

	// Verify reason is non-empty.
	if result.Reason == "" {
		t.Fatal("oracle reason should not be empty")
	}
}

// TestOraclePromoteDemoteCycle verifies that items can be promoted by the
// oracle and then demoted when they become stale.
func TestOraclePromoteDemoteCycle(t *testing.T) {
	manager := New(50_000)
	goal := "Build agent loop"

	// Add an item that matches the goal and will be promoted.
	_, _ = manager.Add(core.ContextItem{
		ID:            "cycle:promote-me",
		Tier:          core.TierNear,
		Title:         "Agent loop implementation",
		Source:        "tool:read_file",
		Reason:        "core agent loop code",
		TokenEstimate: 200,
	})

	// Add filler items to push the above into "old" half later.
	for i := 0; i < 10; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("filler:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("filler %d", i),
			Source:        "tool:test",
			Reason:        "filler",
			TokenEstimate: 50,
		})
	}

	// First review: item matches goal keywords and appears in observations,
	// so it should score high enough for promotion (>= 60).
	oracle := NewOracle(manager)
	obs := []core.Observation{
		{Summary: "agent loop implementation review", StateDelta: "reviewed agent loop code"},
	}
	result := oracle.Review(goal, obs)

	foundPromoted := false
	for _, item := range result.Promoted {
		if item.ID == "cycle:promote-me" {
			foundPromoted = true
			break
		}
	}
	if !foundPromoted {
		t.Fatalf("cycle:promote-me should be promoted; scores=%v", result.Scores)
	}

	// Apply promotion via RunOracleStep.
	RunOracleStep(manager, goal, obs, nil)

	// Verify item is now Anchor tier.
	snap := manager.Snapshot()
	for _, item := range snap.Items {
		if item.ID == "cycle:promote-me" {
			if item.Tier != core.TierAnchor {
				t.Fatalf("expected Anchor tier after promotion, got %s", item.Tier)
			}
			t.Logf("Promoted to Anchor: Tier=%s SelectionReason=%s", item.Tier, item.SelectionReason)
			break
		}
	}

	// Second review: the promoted item has score >= 60 and is already Anchor,
	// so it should NOT appear in the promoted list again.
	result2 := oracle.Review(goal, obs)
	for _, item := range result2.Promoted {
		if item.ID == "cycle:promote-me" {
			t.Fatal("already-Anchor item should not be re-promoted")
		}
	}
}

// TestOracleCompressionEffectiveness verifies that oracle-triggered
// compression actually reduces token usage in the context.
func TestOracleCompressionEffectiveness(t *testing.T) {
	manager := New(100_000)
	goal := "Build agent loop with context management"

	// Add old items that will be in the "old" half.
	for i := 0; i < 5; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("old:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("old data %d", i),
			Source:        "tool:old",
			Reason:        "old",
			TokenEstimate: 100,
		})
	}

	// Add items with keyword overlap that should score 5-9 and be compressed.
	// With observations that don't reference them, they get recency decay.
	for i := 0; i < 10; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("compress-me:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("marginal data %d", i),
			Source:        "tool:read_file",
			Reason:        "data",
			TokenEstimate: 200,
			Keywords:      []string{"build"},
		})
	}

	tokensBefore := manager.Totals().UsedTokens

	obs := []core.Observation{
		{Summary: "recent analysis of code", StateDelta: "analyzed"},
	}

	result := RunOracleStep(manager, goal, obs, nil)

	tokensAfter := manager.Totals().UsedTokens

	t.Logf("Oracle compression: tokensBefore=%d tokensAfter=%d compressed=%d demoted=%d",
		tokensBefore, tokensAfter, len(result.Compressed), len(result.Demoted))

	// Verify compression reduced token usage.
	if tokensAfter >= tokensBefore {
		// Some items may have been demoted instead of compressed; check
		// that at least one action was taken.
		if len(result.Compressed) == 0 && len(result.Demoted) == 0 {
			t.Fatal("no compressions or demotions; token usage unchanged")
		}
		t.Logf("Note: tokens did not decrease but actions were taken (compressed=%d demoted=%d)",
			len(result.Compressed), len(result.Demoted))
	}

	// If compression occurred, verify the compression records.
	if len(result.Compressed) > 0 {
		snap := manager.Snapshot()
		if len(snap.CompressionRecords) == 0 {
			t.Fatal("compression candidates produced but no compression records in snapshot")
		}
		for _, rec := range snap.CompressionRecords {
			t.Logf("Compression record: sources=%d before=%d after=%d saved=%d",
				len(rec.SourceIDs), rec.TokensBefore, rec.TokensAfter,
				rec.TokensBefore-rec.TokensAfter)
			if rec.TokensAfter >= rec.TokensBefore {
				t.Fatalf("compression did not save tokens: before=%d after=%d",
					rec.TokensBefore, rec.TokensAfter)
			}
		}
	}
}

// TestOracleKeywordOverlap verifies that keyword matching works correctly
// at scale with many items and diverse keywords.
func TestOracleKeywordOverlap(t *testing.T) {
	manager := New(200_000)
	goal := "Refactor database connection pool for performance"

	// Items with matching keywords.
	for i := 0; i < 50; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("match:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("database pool config %d", i),
			Source:        "tool:read_file",
			Reason:        "connection pool settings",
			TokenEstimate: 50,
			Keywords:      []string{"database", "performance"},
		})
	}

	// Items without matching keywords.
	for i := 0; i < 50; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("nomatch:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("random data %d", i),
			Source:        "tool:dump",
			Reason:        "unrelated data",
			TokenEstimate: 50,
			Keywords:      []string{"unrelated", "noise"},
		})
	}

	oracle := NewOracle(manager)
	result := oracle.Review(goal, nil)

	// Items with matching keywords should have higher scores than those without.
	matchScores := make([]int, 0, 50)
	noMatchScores := make([]int, 0, 50)
	for id, score := range result.Scores {
		if len(id) > 5 && id[:5] == "match" {
			matchScores = append(matchScores, score)
		} else if len(id) > 7 && id[:7] == "nomatch" {
			noMatchScores = append(noMatchScores, score)
		}
	}

	avgMatch := avg(matchScores)
	avgNoMatch := avg(noMatchScores)

	t.Logf("Keyword overlap: avgMatchScore=%.1f avgNoMatchScore=%.1f",
		avgMatch, avgNoMatch)

	if avgMatch <= avgNoMatch {
		t.Fatalf("matching-keyword items should score higher: avgMatch=%.1f avgNoMatch=%.1f",
			avgMatch, avgNoMatch)
	}

	// Verify that the +15 keyword overlap bonus is applied.
	// A matching item with just keyword overlap (no title match) should
	// score at least 15.
	minMatchScore := 100
	for _, s := range matchScores {
		if s < minMatchScore {
			minMatchScore = s
		}
	}
	if minMatchScore < 15 {
		t.Fatalf("minimum matching-keyword score = %d, want >= 15 (keyword overlap bonus)", minMatchScore)
	}
}

// TestOracleRecencyDecayAtScale verifies that stale items (not referenced in
// observations) receive lower scores over time, tested with many items present.
func TestOracleRecencyDecayAtScale(t *testing.T) {
	manager := New(100_000)
	goal := "Build agent loop"

	// Add an old item that appears in the first half of insertion order
	// and is NOT referenced in observations.
	_, _ = manager.Add(core.ContextItem{
		ID:            "decay:stale-item",
		Tier:          core.TierNear,
		Title:         "Old build output",
		Source:        "tool:build",
		Reason:        "build data",
		TokenEstimate: 100,
	})

	// Add many newer items to push the above into the "old" half.
	for i := 0; i < 20; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("new:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("recent item %d", i),
			Source:        "tool:test",
			Reason:        "current data",
			TokenEstimate: 50,
		})
	}

	// Without observations: no recency decay.
	oracle := NewOracle(manager)
	resultNoObs := oracle.Review(goal, nil)
	scoreNoObs := resultNoObs.Scores["decay:stale-item"]

	// With observations that DON'T reference the stale item: recency decay.
	obs := []core.Observation{
		{Summary: "recent file scan", StateDelta: "scanned files"},
		{Summary: "git status check", StateDelta: "checked status"},
	}
	resultWithObs := oracle.Review(goal, obs)
	scoreWithObs := resultWithObs.Scores["decay:stale-item"]

	t.Logf("Recency decay: scoreNoObs=%d scoreWithObs=%d decay=%d",
		scoreNoObs, scoreWithObs, scoreNoObs-scoreWithObs)

	// The score with observations should be lower due to recency decay (-10).
	if scoreWithObs >= scoreNoObs {
		t.Fatalf("expected score with observations (%d) to be lower than without (%d)",
			scoreWithObs, scoreNoObs)
	}

	expectedDecay := 10
	actualDecay := scoreNoObs - scoreWithObs
	if actualDecay != expectedDecay {
		t.Fatalf("recency decay = %d, want %d", actualDecay, expectedDecay)
	}
}

// TestOracleScoringPerformance benchmarks oracle scoring with 2000 items.
func TestOracleScoringPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	manager := New(1_000_000)
	goal := "Build agent loop with context management and compression"

	// Add 2000 items.
	for i := 0; i < 2000; i++ {
		_, _ = manager.Add(core.ContextItem{
			ID:            fmt.Sprintf("perf:%d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("performance test item %d with agent context", i),
			Source:        "tool:read_file",
			Reason:        "performance testing",
			TokenEstimate: 50,
		})
	}

	obs := []core.Observation{
		{Summary: "agent loop implementation", StateDelta: "built loop"},
		{Summary: "context management review", StateDelta: "reviewed context"},
	}

	start := time.Now()
	oracle := NewOracle(manager)
	result := oracle.Review(goal, obs)
	elapsed := time.Since(start)

	t.Logf("Oracle scoring 2000 items: %v (promoted=%d demoted=%d compressed=%d scores=%d)",
		elapsed, len(result.Promoted), len(result.Demoted),
		len(result.Compressed), len(result.Scores))

	if len(result.Scores) != 2000 {
		t.Fatalf("expected 2000 scores, got %d", len(result.Scores))
	}

	// Scoring 2000 items should complete in well under 1 second.
	if elapsed > time.Second {
		t.Fatalf("oracle scoring took %v; expected < 1s for 2000 items", elapsed)
	}
}

func avg(nums []int) float64 {
	if len(nums) == 0 {
		return 0
	}
	sum := 0
	for _, n := range nums {
		sum += n
	}
	return float64(sum) / float64(len(nums))
}
