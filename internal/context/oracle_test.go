package context

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func TestOracleScoresGoalKeywordMatch(t *testing.T) {
	// Directly exercise the scoring function with controlled inputs.
	tests := []struct {
		name            string
		item            core.ContextItem
		goalWords       []string
		isRecent        bool
		isOld           bool
		windowTokens    int
		hasObservations bool
		wantMin         int
		wantMax         int
	}{
		{
			name: "pinned anchor with full relevance",
			item: core.ContextItem{
				ID: "a", Tier: core.TierAnchor, Pinned: true,
				Title:  "Build agent loop",
				Source: "task-goal",
				Reason: "core objective",
			},
			goalWords:    []string{"build", "agent", "loop"},
			isRecent:     true,
			isOld:        false,
			windowTokens: 10000,
			wantMin:      100, // 100 (pinned) + 20 (anchor) + 90 (3 words) + 25 (recent) = 235 → clamped to 100
			wantMax:      100,
		},
		{
			name: "near item with two goal matches and recency",
			item: core.ContextItem{
				ID: "b", Tier: core.TierNear, Pinned: false,
				Title:  "Read file contents for agent",
				Source: "tool:read_file",
				Reason: "agent needs file data",
			},
			goalWords:    []string{"agent", "file"},
			isRecent:     true,
			isOld:        false,
			windowTokens: 10000,
			wantMin:      85, // 60 (2 word matches) + 25 (recent) = 85
			wantMax:      85,
		},
		{
			name: "stale near item with no goal matches",
			item: core.ContextItem{
				ID: "c", Tier: core.TierNear, Pinned: false,
				Title: "Old build output", Source: "tool:build",
				Reason: "was useful", TokenEstimate: 600,
			},
			goalWords:    []string{"agent", "context"},
			isRecent:     false,
			isOld:        true,
			windowTokens: 1000, // 600 > 500 → token penalty
			wantMin:      0,    // 0 - 20 (old) - 10 (token) = -30 → 0
			wantMax:      0,
		},
		{
			name: "pinned near with no relevance — still safe from demotion",
			item: core.ContextItem{
				ID: "d", Tier: core.TierNear, Pinned: true,
				Title: "Irrelevant log", Source: "tool:log",
				Reason: "noisy",
			},
			goalWords:    []string{"agent"},
			isRecent:     false,
			isOld:        true,
			windowTokens: 10000,
			wantMin:      80, // 100 (pinned) - 20 (old) = 80
			wantMax:      80,
		},
		{
			name: "anchor base bonus without keyword match",
			item: core.ContextItem{
				ID: "e", Tier: core.TierAnchor, Pinned: false,
				Title: "Project map", Source: "seed",
				Reason: "structure",
			},
			goalWords:    []string{"completely", "unrelated"},
			isRecent:     false,
			isOld:        false,
			windowTokens: 10000,
			wantMin:      20, // just anchor base
			wantMax:      20,
		},
		{
			name: "token budget penalty applies",
			item: core.ContextItem{
				ID: "f", Tier: core.TierNear, Pinned: false,
				Title: "Huge file", Source: "tool:read",
				Reason: "big", TokenEstimate: 600,
			},
			goalWords:    []string{"unrelated"},
			isRecent:     false,
			isOld:        false,
			windowTokens: 1000, // 600 > 500 → penalty
			wantMin:      0,    // 0 - 10 = -10 → 0
			wantMax:      0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			score := computeOracleScore(test.item, test.goalWords, test.isRecent, test.isOld, test.windowTokens, test.hasObservations)
			if score < test.wantMin || score > test.wantMax {
				t.Fatalf("score = %d, want [%d, %d]", score, test.wantMin, test.wantMax)
			}
		})
	}
}

func TestOraclePromotesRelevantEvidence(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop with context management"

	// Add a Near item that matches the goal well and appears in observations.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:agent-loop", Tier: core.TierNear, Pinned: false,
		Title: "Agent loop implementation", Source: "tool:read_file",
		Reason: "core agent loop code", TokenEstimate: 200,
	})
	// Add another item that also matches.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:context-mgr", Tier: core.TierNear, Pinned: false,
		Title: "Context manager with tiers", Source: "tool:read_file",
		Reason: "context management implementation", TokenEstimate: 200,
	})
	// Add an anchor that should stay (but not in promoted since it's already anchor).
	_, _ = manager.Add(core.ContextItem{
		ID: "anchor:task-goal", Tier: core.TierAnchor, Pinned: true,
		Title: "Task goal", Source: "seed",
		Reason: "Build agent loop", TokenEstimate: 50,
	})

	oracle := NewOracle(manager)
	recentObs := []core.Observation{
		{Summary: "agent loop implementation read", StateDelta: "captured loop code"},
		{Summary: "context manager near anchor artifact tiers", StateDelta: "context management captured"},
	}

	result := oracle.Review(goal, recentObs)

	// near:agent-loop should be promoted: matches "agent" + "loop" from goal (+60),
	// and appears in recent observations (+25) → score >= 85 → promoted.
	foundLoop := false
	for _, item := range result.Promoted {
		if item.ID == "near:agent-loop" {
			foundLoop = true
			break
		}
	}
	if !foundLoop {
		t.Fatalf("near:agent-loop not in promoted list; promoted=%v, scores=%v", result.Promoted, result.Scores)
	}

	// near:context-mgr should also be promoted: matches "context" + "agent" (goal has "agent")
	// + "management" (goal has "management") → 90 + recency 25 → 115 clamped to 100.
	foundCtx := false
	for _, item := range result.Promoted {
		if item.ID == "near:context-mgr" {
			foundCtx = true
			break
		}
	}
	if !foundCtx {
		t.Fatalf("near:context-mgr not in promoted list; promoted=%v, scores=%v", result.Promoted, result.Scores)
	}

	// anchor:task-goal should NOT be in promoted (already Anchor tier).
	for _, item := range result.Promoted {
		if item.ID == "anchor:task-goal" {
			t.Fatal("anchor:task-goal should not be promoted (already anchor)")
		}
	}

	// Reason should mention the promotions.
	if !strings.Contains(result.Reason, "promoted") {
		t.Fatalf("reason should mention promotions: %q", result.Reason)
	}
}

func TestOracleDemotesStaleEvidence(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop"

	// Add stale items first (they become "old" in insertion order).
	_, _ = manager.Add(core.ContextItem{
		ID: "near:stale-log", Tier: core.TierNear, Pinned: false,
		Title: "Build output log", Source: "tool:build",
		Reason: "raw build output", TokenEstimate: 50,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:stale-coverage", Tier: core.TierNear, Pinned: false,
		Title: "Coverage report", Source: "tool:test",
		Reason: "old test coverage", TokenEstimate: 50,
	})
	// Add some recent items to push the stale ones into the old half.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-1", Tier: core.TierNear, Pinned: false,
		Title: "Recent git status", Source: "tool:git_status",
		Reason: "current state", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-2", Tier: core.TierNear, Pinned: false,
		Title: "Recent list dir", Source: "tool:list_dir",
		Reason: "current files", TokenEstimate: 30,
	})

	// Neither stale item appears in recent observations.
	oracle := NewOracle(manager)
	result := oracle.Review(goal, nil)

	// Under revised thresholds (no observations, no recency decay):
	//   near:stale-coverage: score 0 (< 5) → demoted
	//   near:stale-log: score 10 (1 keyword match - 20 old = 10) → outside 5-9, stays
	//   near:recent-1, near:recent-2: score 0 (< 5) → demoted

	// Verify near:stale-coverage is demoted (score < 5).
	staleDemoted := false
	for _, id := range result.Demoted {
		if id == "near:stale-coverage" {
			staleDemoted = true
			break
		}
	}
	if !staleDemoted {
		t.Fatalf("near:stale-coverage should be demoted; demoted=%v, scores=%v", result.Demoted, result.Scores)
	}

	// Verify near:stale-log is NOT compressed (score 10, outside 5-9 threshold).
	for _, candidate := range result.Compressed {
		for _, id := range candidate.ItemIDs {
			if id == "near:stale-log" {
				t.Fatal("near:stale-log (score 10) should not be compressed with threshold 5-9")
			}
		}
	}

	// Verify near:stale-log is NOT demoted (score >= 5).
	for _, id := range result.Demoted {
		if id == "near:stale-log" {
			t.Fatal("near:stale-log (score 10) should not be demoted")
		}
	}

	// Stale item scores should be low.
	for _, id := range []string{"near:stale-log", "near:stale-coverage"} {
		if score, ok := result.Scores[id]; ok && score >= 20 {
			t.Fatalf("stale item %s score = %d, want < 20", id, score)
		}
	}

	if !strings.Contains(result.Reason, "demoted") {
		t.Fatalf("reason should mention demotions: %q", result.Reason)
	}
}

func TestOracleNeverDemotesPinned(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop"

	// Create a pinned item that would otherwise be a demotion candidate.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:pinned-stale", Tier: core.TierNear, Pinned: true,
		Title: "Old irrelevant log", Source: "tool:log",
		Reason: "user pinned this", TokenEstimate: 50,
	})
	// Also add an unpinned stale item as a control.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:unpinned-stale", Tier: core.TierNear, Pinned: false,
		Title: "Another old log", Source: "tool:log",
		Reason: "not pinned", TokenEstimate: 50,
	})
	// Add recent items to make the above "old".
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-a", Tier: core.TierNear, Pinned: false,
		Title: "Recent list dir", Source: "tool:list_dir",
		Reason: "files", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-b", Tier: core.TierNear, Pinned: false,
		Title: "Recent git status", Source: "tool:git_status",
		Reason: "status", TokenEstimate: 30,
	})

	oracle := NewOracle(manager)
	result := oracle.Review(goal, nil)

	// Pinned item must never appear in demoted.
	for _, id := range result.Demoted {
		if id == "near:pinned-stale" {
			t.Fatal("pinned item near:pinned-stale was demoted; must never demote pinned items")
		}
	}

	// The unpinned stale item should be demoted.
	unpinnedDemoted := false
	for _, id := range result.Demoted {
		if id == "near:unpinned-stale" {
			unpinnedDemoted = true
			break
		}
	}
	if !unpinnedDemoted {
		t.Fatal("unpinned stale item should be demoted as a control")
	}

	// Pinned item score should be high (>= 80 due to pin bonus).
	if score, ok := result.Scores["near:pinned-stale"]; ok && score < 60 {
		t.Fatalf("pinned item score = %d, want >= 60", score)
	}
}

// Test that low-activity items (score 5-9) are compressed rather than demoted.
func TestOracleCompressesLowActivityItems(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop with context management"

	// Add old items first (they become "old" in insertion order).
	_, _ = manager.Add(core.ContextItem{
		ID: "near:old-1", Tier: core.TierNear, Pinned: false,
		Title: "Old stuff", Source: "tool:old",
		Reason: "old", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:old-2", Tier: core.TierNear, Pinned: false,
		Title: "Old stuff 2", Source: "tool:old",
		Reason: "old", TokenEstimate: 30,
	})

	// Item with keyword overlap -> score 5 (15 overlap - 10 recency decay).
	_, _ = manager.Add(core.ContextItem{
		ID: "near:build-overlap", Tier: core.TierNear, Pinned: false,
		Title: "Some data", Source: "tool:read_file",
		Reason: "data", TokenEstimate: 50,
		Keywords: []string{"build"},
	})

	// Item with no overlap -> score 0 (demoted).
	_, _ = manager.Add(core.ContextItem{
		ID: "near:no-overlap", Tier: core.TierNear, Pinned: false,
		Title: "Random data dump", Source: "tool:dump",
		Reason: "unrelated", TokenEstimate: 50,
	})

	// Provide observations that don't reference the items (triggers recency decay).
	obs := []core.Observation{
		{Summary: "recent file scan", StateDelta: "scanned files"},
	}

	oracle := NewOracle(manager)
	result := oracle.Review(goal, obs)

	// near:build-overlap: keyword overlap +15, recency decay -10 = 5 -> compressed.
	for _, id := range result.Demoted {
		if id == "near:build-overlap" {
			t.Fatal("near:build-overlap (score 5) should be compressed, not demoted")
		}
	}
	foundInCompressed := false
	for _, candidate := range result.Compressed {
		for _, id := range candidate.ItemIDs {
			if id == "near:build-overlap" {
				foundInCompressed = true
				break
			}
		}
	}
	if !foundInCompressed {
		t.Fatalf("near:build-overlap should be in compressed list; compressed=%v, scores=%v", result.Compressed, result.Scores)
	}

	// near:no-overlap (score 0, < 5) should be demoted.
	foundInDemoted := false
	for _, id := range result.Demoted {
		if id == "near:no-overlap" {
			foundInDemoted = true
			break
		}
	}
	if !foundInDemoted {
		t.Fatalf("near:no-overlap (score 0) should be demoted; demoted=%v", result.Demoted)
	}
}

// Test that high-value items (score >= 60) are promoted, not compressed.
func TestOracleDoesNotCompressHighValueItems(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop with context management"

	// Item with multiple keyword matches -> high score, gets promoted.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:agent-context", Tier: core.TierNear, Pinned: false,
		Title: "Agent context manager code", Source: "tool:read_file",
		Reason: "context management implementation", TokenEstimate: 200,
	})
	// Recent items to push the above into "old" half.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-x", Tier: core.TierNear, Pinned: false,
		Title: "Recent file", Source: "tool:read_file",
		Reason: "current", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-y", Tier: core.TierNear, Pinned: false,
		Title: "Recent status", Source: "tool:git_status",
		Reason: "current", TokenEstimate: 30,
	})

	oracle := NewOracle(manager)
	result := oracle.Review(goal, nil)

	// near:agent-context: "agent" + "context" + "management" -> 3 keyword matches
	// score = 90 - 20 (old) = 70. Score >= 60 -> promoted, not compressed.
	for _, candidate := range result.Compressed {
		for _, id := range candidate.ItemIDs {
			if id == "near:agent-context" {
				t.Fatal("near:agent-context (score >= 60) should be promoted, not compressed")
			}
		}
	}

	// Should be in promoted list.
	foundInPromoted := false
	for _, item := range result.Promoted {
		if item.ID == "near:agent-context" {
			foundInPromoted = true
			break
		}
	}
	if !foundInPromoted {
		t.Fatalf("near:agent-context should be promoted; promoted=%v, scores=%v", result.Promoted, result.Scores)
	}
}

// Test that CompressItems correctly sets ReplacedBy and creates the compression artifact.
func TestCompressItemsSetsReplacedBy(t *testing.T) {
	manager := New(10000)

	_, _ = manager.Add(core.ContextItem{
		ID: "near:item-1", Tier: core.TierNear, Pinned: false,
		Title: "Build output log", Source: "tool:build",
		Reason: "build results", TokenEstimate: 100,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:item-2", Tier: core.TierNear, Pinned: false,
		Title: "Test coverage report", Source: "tool:test",
		Reason: "coverage data", TokenEstimate: 80,
	})

	record, snapshot, err := manager.CompressItems(
		[]string{"near:item-1", "near:item-2"},
		"Compressed: Build output and test coverage",
		"low-activity items unified",
	)
	if err != nil {
		t.Fatalf("CompressItems error: %v", err)
	}

	// Check the compression record.
	if record.TokensBefore != 180 {
		t.Fatalf("TokensBefore = %d, want 180", record.TokensBefore)
	}
	if record.TokensAfter <= 0 {
		t.Fatalf("TokensAfter = %d, want > 0", record.TokensAfter)
	}
	if len(record.SourceIDs) != 2 {
		t.Fatalf("SourceIDs len = %d, want 2", len(record.SourceIDs))
	}

	// Compression artifact should be in the snapshot.
	foundInSnapshot := false
	for _, item := range snapshot.Items {
		if item.ID == record.ID {
			foundInSnapshot = true
			if item.Source != "compression" {
				t.Fatalf("compression artifact source = %q, want 'compression'", item.Source)
			}
		}
		// Expired items should NOT appear in the snapshot.
		if item.ID == "near:item-1" || item.ID == "near:item-2" {
			t.Fatalf("source item %s should not appear in snapshot after compression", item.ID)
		}
	}
	if !foundInSnapshot {
		t.Fatal("compression artifact not in snapshot")
	}

	// Verify compression record is in the snapshot.
	if len(snapshot.CompressionRecords) != 1 {
		t.Fatalf("CompressionRecords len = %d, want 1", len(snapshot.CompressionRecords))
	}
	if snapshot.CompressionRecords[0].TokensBefore != 180 {
		t.Fatalf("snapshot record TokensBefore = %d, want 180", snapshot.CompressionRecords[0].TokensBefore)
	}
}

// Test that compression reduces token usage.
func TestCompressItemsReducesTokens(t *testing.T) {
	manager := New(10000)

	_, _ = manager.Add(core.ContextItem{
		ID: "near:big-1", Tier: core.TierNear, Pinned: false,
		Title: "Large file contents A", Source: "tool:read_file",
		Reason: "read file A", TokenEstimate: 500,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:big-2", Tier: core.TierNear, Pinned: false,
		Title: "Large file contents B", Source: "tool:read_file",
		Reason: "read file B", TokenEstimate: 500,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:big-3", Tier: core.TierNear, Pinned: false,
		Title: "Large file contents C", Source: "tool:read_file",
		Reason: "read file C", TokenEstimate: 500,
	})

	tokensBefore := manager.Totals().UsedTokens // 1500

	_, snapshot, err := manager.CompressItems(
		[]string{"near:big-1", "near:big-2", "near:big-3"},
		"Compressed summary of three large files",
		"token budget optimization",
	)
	if err != nil {
		t.Fatalf("CompressItems error: %v", err)
	}

	tokensAfter := snapshot.UsedTokens

	// After compression: 3 source items (1500 tokens) replaced by 1 compression
	// artifact (far fewer tokens since the summary is short).
	if tokensAfter >= tokensBefore {
		t.Fatalf("tokens after compression (%d) should be less than before (%d)", tokensAfter, tokensBefore)
	}
	if snapshot.CompressionRecords[0].TokensBefore != 1500 {
		t.Fatalf("record TokensBefore = %d, want 1500", snapshot.CompressionRecords[0].TokensBefore)
	}
	saved := snapshot.CompressionRecords[0].TokensBefore - snapshot.CompressionRecords[0].TokensAfter
	if saved <= 0 {
		t.Fatalf("tokens saved = %d, want > 0", saved)
	}
}

// Test that pinned items are never compressed.
func TestOracleNeverCompressesPinnedItems(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop"

	// Pinned item with low potential score -- but pin bonus keeps it high.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:pinned-low", Tier: core.TierNear, Pinned: true,
		Title: "Old irrelevant log", Source: "tool:log",
		Reason: "pinned by user", TokenEstimate: 50,
	})
	// Recent items to push pinned item into "old" half.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-1", Tier: core.TierNear, Pinned: false,
		Title: "Recent scan", Source: "tool:scan",
		Reason: "current", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-2", Tier: core.TierNear, Pinned: false,
		Title: "Recent list", Source: "tool:list_dir",
		Reason: "current", TokenEstimate: 30,
	})

	oracle := NewOracle(manager)
	result := oracle.Review(goal, nil)

	// Pinned item must never appear in compressed.
	for _, candidate := range result.Compressed {
		for _, id := range candidate.ItemIDs {
			if id == "near:pinned-low" {
				t.Fatal("pinned item near:pinned-low was compressed; must never compress pinned items")
			}
		}
	}

	// Pinned item must never appear in demoted.
	for _, id := range result.Demoted {
		if id == "near:pinned-low" {
			t.Fatal("pinned item near:pinned-low was demoted; must never demote pinned items")
		}
	}
}

// Test that compression lineage is available after oracle step.
func TestRunOracleStepAddsCompressionLineage(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop"

	// Add old items first (to push later items into non-old half).
	_, _ = manager.Add(core.ContextItem{
		ID: "near:old-1", Tier: core.TierNear, Pinned: false,
		Title: "Old stuff", Source: "tool:old",
		Reason: "old", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:old-2", Tier: core.TierNear, Pinned: false,
		Title: "Old stuff 2", Source: "tool:old",
		Reason: "old", TokenEstimate: 30,
	})

	// Add items with keyword overlap -> score 5 (compressed).
	_, _ = manager.Add(core.ContextItem{
		ID: "near:overlap-a", Tier: core.TierNear, Pinned: false,
		Title: "Some data A", Source: "tool:read_file",
		Reason: "data A", TokenEstimate: 50,
		Keywords: []string{"build"},
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:overlap-b", Tier: core.TierNear, Pinned: false,
		Title: "Some data B", Source: "tool:read_file",
		Reason: "data B", TokenEstimate: 50,
		Keywords: []string{"agent"},
	})

	// Provide observations that don't reference the overlap items.
	obs := []core.Observation{
		{Summary: "recent file scan", StateDelta: "scanned files"},
	}

	// Run oracle step (with nil bus -- no event publishing).
	result := RunOracleStep(manager, goal, obs, nil)

	// Verify compression candidates were produced.
	if len(result.Compressed) == 0 {
		t.Fatalf("expected compressed candidates; result=%+v, scores=%v", result, result.Scores)
	}

	// Snapshot should contain compression records.
	snapshot := manager.Snapshot()
	if len(snapshot.CompressionRecords) == 0 {
		t.Fatalf("expected compression records in snapshot; got %v", snapshot.CompressionRecords)
	}

	// Verify the compression record has meaningful data.
	rec := snapshot.CompressionRecords[0]
	if rec.TokensBefore <= rec.TokensAfter {
		t.Fatalf("tokens saved should be positive; before=%d after=%d", rec.TokensBefore, rec.TokensAfter)
	}
	if len(rec.SourceIDs) == 0 {
		t.Fatal("compression record should have source IDs")
	}
	if rec.Summary == "" {
		t.Fatal("compression record should have a summary")
	}
}

// Test that keyword overlap adds +15 to the score.
func TestOracleKeywordOverlapBonus(t *testing.T) {
	item := core.ContextItem{
		ID: "test:overlap", Tier: core.TierNear, Pinned: false,
		Title:    "Some data",
		Source:   "tool:read_file",
		Reason:   "data",
		Keywords: []string{"build", "unrelated"},
	}
	goalWords := []string{"build", "agent", "loop"}

	// Without keyword overlap (no Keywords field).
	itemNoKW := item
	itemNoKW.Keywords = nil
	scoreNoKW := computeOracleScore(itemNoKW, goalWords, false, false, 10000, false)

	// With keyword overlap.
	scoreWithKW := computeOracleScore(item, goalWords, false, false, 10000, false)

	if scoreWithKW-scoreNoKW != 15 {
		t.Fatalf("keyword overlap bonus = %d, want 15 (scoreWithKW=%d, scoreNoKW=%d)", scoreWithKW-scoreNoKW, scoreWithKW, scoreNoKW)
	}
}

// Test that recency decay applies when observations exist but item is not referenced.
func TestOracleRecencyDecay(t *testing.T) {
	// Use an item with a keyword match so it has a base score > 0.
	item := core.ContextItem{
		ID: "test:decay", Tier: core.TierNear, Pinned: false,
		Title:  "Build output",
		Source: "tool:build",
		Reason: "build data",
	}
	goalWords := []string{"build"}

	// Without observations (hasObservations=false) — no decay. Score = 30.
	scoreNoObs := computeOracleScore(item, goalWords, false, false, 10000, false)

	// With observations but not referenced (hasObservations=true, isRecent=false) — -10 decay. Score = 20.
	scoreWithObs := computeOracleScore(item, goalWords, false, false, 10000, true)

	if scoreNoObs-scoreWithObs != 10 {
		t.Fatalf("recency decay = %d, want 10 (scoreNoObs=%d, scoreWithObs=%d)", scoreNoObs-scoreWithObs, scoreNoObs, scoreWithObs)
	}
}

// Test that recency decay does NOT apply to pinned items.
func TestOracleRecencyDecaySkipsPinned(t *testing.T) {
	item := core.ContextItem{
		ID: "test:pinned", Tier: core.TierNear, Pinned: true,
		Title:  "Pinned data",
		Source: "tool:read_file",
		Reason: "pinned",
	}
	goalWords := []string{"build"}

	// Pinned item with observations but not referenced.
	scorePinned := computeOracleScore(item, goalWords, false, false, 10000, true)

	// Pinned item without observations.
	scorePinnedNoObs := computeOracleScore(item, goalWords, false, false, 10000, false)

	// Both should be the same — pinned items don't get recency decay.
	if scorePinned != scorePinnedNoObs {
		t.Fatalf("pinned item should not get recency decay: scorePinned=%d, scorePinnedNoObs=%d", scorePinned, scorePinnedNoObs)
	}
}

// Test that recency decay does NOT apply when item IS referenced in observations.
func TestOracleRecencyDecaySkipsReferenced(t *testing.T) {
	item := core.ContextItem{
		ID: "test:referenced", Tier: core.TierNear, Pinned: false,
		Title:  "Agent loop code",
		Source: "tool:read_file",
		Reason: "core implementation",
	}
	goalWords := []string{"build"}

	// Referenced in observations (isRecent=true) — no decay.
	scoreReferenced := computeOracleScore(item, goalWords, true, false, 10000, true)

	// Not referenced, no observations.
	scoreNoObs := computeOracleScore(item, goalWords, false, false, 10000, false)

	// Referenced should be higher by recency bonus (+25) minus no decay.
	// scoreReferenced = 0 + 25 = 25, scoreNoObs = 0.
	if scoreReferenced != 25 {
		t.Fatalf("referenced item score = %d, want 25", scoreReferenced)
	}
	if scoreNoObs != 0 {
		t.Fatalf("unreferenced no-obs score = %d, want 0", scoreNoObs)
	}
}

// Test that items with score 10-19 are NOT compressed with the new threshold.
func TestOracleDoesNotCompressScoreAboveNine(t *testing.T) {
	manager := New(10000)
	goal := "Build agent loop"

	// Item with 1 keyword match and old: score = 30 - 20 = 10.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:score-10", Tier: core.TierNear, Pinned: false,
		Title: "Build output log", Source: "tool:build",
		Reason: "build output", TokenEstimate: 50,
	})
	// Push into old half.
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-a", Tier: core.TierNear, Pinned: false,
		Title: "Recent file", Source: "tool:read_file",
		Reason: "current", TokenEstimate: 30,
	})
	_, _ = manager.Add(core.ContextItem{
		ID: "near:recent-b", Tier: core.TierNear, Pinned: false,
		Title: "Recent status", Source: "tool:git_status",
		Reason: "current", TokenEstimate: 30,
	})

	oracle := NewOracle(manager)
	result := oracle.Review(goal, nil)

	// Score 10 should NOT be compressed (threshold is 5-9).
	for _, candidate := range result.Compressed {
		for _, id := range candidate.ItemIDs {
			if id == "near:score-10" {
				t.Fatal("near:score-10 (score 10) should not be compressed with threshold 5-9")
			}
		}
	}

	// Score 10 should NOT be demoted (>= 5).
	for _, id := range result.Demoted {
		if id == "near:score-10" {
			t.Fatal("near:score-10 (score 10) should not be demoted")
		}
	}
}

// Test that removing a compression artifact cleans up its record.
func TestRemoveCompressionArtifactCleansRecord(t *testing.T) {
	manager := New(10000)

	_, _ = manager.Add(core.ContextItem{
		ID: "near:item-a", Tier: core.TierNear, Pinned: false,
		Title: "Item A", Source: "tool:scan",
		Reason: "data", TokenEstimate: 40,
	})

	record, _, err := manager.CompressItems(
		[]string{"near:item-a"},
		"Compressed item A",
		"compress single item",
	)
	if err != nil {
		t.Fatalf("CompressItems error: %v", err)
	}

	// Record should exist.
	if retrieved := manager.CompressionRecord(record.ID); retrieved == nil {
		t.Fatal("compression record should exist after compression")
	}

	// Remove the compression artifact.
	_, err = manager.Remove(record.ID)
	if err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	// Record should be cleaned up.
	if retrieved := manager.CompressionRecord(record.ID); retrieved != nil {
		t.Fatal("compression record should be cleaned up after artifact removal")
	}

	// Snapshot should have no compression records.
	snapshot := manager.Snapshot()
	if len(snapshot.CompressionRecords) != 0 {
		t.Fatalf("snapshot should have no compression records after removal; got %v", snapshot.CompressionRecords)
	}
}
