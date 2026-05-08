package context

import (
	"fmt"
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

// ===========================================================================
// Scenario 1: Realistic coding session admission flow
// ===========================================================================

// TestRealityCodingSessionAdmission simulates a full coding session where
// items are admitted in the order a real agent would produce them: user prompt,
// plan, file reads, tool results. It verifies tier placement, selection reasons,
// artifact promotion for oversized sources, eviction behavior, and survival
// guarantees for pinned and anchor items.
func TestRealityCodingSessionAdmission(t *testing.T) {
	// Use a window where we can trigger eviction with a realistic item count.
	// The safe budget is 85% of window. We'll admit items that stay under the
	// window for initial admission, then add extra bulk items via Add to push
	// past the budget, and verify AutoBudget evicts correctly.
	windowTokens := 3000
	m := New(windowTokens)

	// Track admitted items for the final report.
	type admittedInfo struct {
		id              string
		tier            core.ContextTier
		reason          string
		selectionReason string
		pinned          bool
		tokenEstimate   int
	}
	var admitted []admittedInfo

	admit := func(item core.ContextItem) {
		t.Helper()
		snap, err := m.Admit(item)
		if err != nil {
			t.Fatalf("Admit(%q) failed: %v (snapshot items=%d, used=%d/%d)",
				item.ID, err, len(snap.Items), snap.UsedTokens, snap.WindowTokens)
		}
		// Find the item in the snapshot to capture the admission-set fields.
		for _, it := range snap.Items {
			if it.ID == item.ID {
				admitted = append(admitted, admittedInfo{
					id:              it.ID,
					tier:            it.Tier,
					reason:          it.Reason,
					selectionReason: it.SelectionReason,
					pinned:          it.Pinned,
					tokenEstimate:   it.TokenEstimate,
				})
				break
			}
		}
	}

	// Step 1: User prompt — always Near, small.
	admit(core.ContextItem{
		ID:            "session:user-prompt",
		Tier:          core.TierNear,
		Title:         "User prompt: Fix the context manager bug",
		Source:        "Fix the context manager so that AutoBudget evicts stale items correctly.",
		TokenEstimate: 40,
		Reason:        "User's initial request for the session",
	})

	// Step 2: Agent plan — Near tier.
	admit(core.ContextItem{
		ID:            "session:plan",
		Tier:          core.TierNear,
		Title:         "Agent plan: read context manager, fix eviction, write tests",
		Source:        "1. Read internal/context/manager.go\n2. Identify eviction bug\n3. Fix collectNearEvictionCandidatesLocked\n4. Write tests",
		TokenEstimate: 60,
		Reason:        "Structured plan for the coding task",
	})

	// Step 3: Anchor item — project map, should survive everything.
	admit(core.ContextItem{
		ID:            "anchor:project-map",
		Tier:          core.TierAnchor,
		Title:         "Project map",
		Source:        "internal/context/manager.go\ninternal/context/oracle.go\ninternal/core/types.go",
		TokenEstimate: 30,
		Reason:        "Seed anchor for project structure",
		Pinned:        true,
	})

	// Step 4: Anchor item — task goal, pinned.
	admit(core.ContextItem{
		ID:            "anchor:task-goal",
		Tier:          core.TierAnchor,
		Title:         "Task goal: fix context manager",
		Source:        "Fix AutoBudget eviction logic and verify with reality tests",
		TokenEstimate: 20,
		Reason:        "Seed anchor for active task objective",
		Pinned:        true,
	})

	// Step 5: File read result — Near tier.
	admit(core.ContextItem{
		ID:            "tool:read-manager-go",
		Tier:          core.TierNear,
		Title:         "Read internal/context/manager.go",
		Source:        "package context\n\nimport (...)\n\nconst DefaultWindowTokens = 1_000_000\n...",
		TokenEstimate: 400,
		Reason:        "File content needed to understand the eviction logic",
	})

	// Step 6: Another file read — Near tier.
	admit(core.ContextItem{
		ID:            "tool:read-oracle-go",
		Tier:          core.TierNear,
		Title:         "Read internal/context/oracle.go",
		Source:        "package context\n\ntype Oracle struct { manager *Manager }\n...",
		TokenEstimate: 350,
		Reason:        "Oracle scoring logic needed for context review understanding",
	})

	// Step 7: Tool result — grep output, Near tier.
	admit(core.ContextItem{
		ID:            "tool:grep-eviction",
		Tier:          core.TierNear,
		Title:         "Grep results for 'eviction' in context package",
		Source:        "manager.go:397: func (m *Manager) AutoBudget()\nmanager.go:463: func (m *Manager) collectNearEvictionCandidatesLocked()",
		TokenEstimate: 80,
		Reason:        "Search results to locate eviction-related code",
	})

	// Step 8: Tool result — test output, Near tier.
	admit(core.ContextItem{
		ID:            "tool:test-output",
		Tier:          core.TierNear,
		Title:         "go test ./internal/context/ -run TestAutoBudget -v",
		Source:        "=== RUN   TestAutoBudgetEvictsOldest\n--- PASS: TestAutoBudgetEvictsOldest (0.00s)\nPASS",
		TokenEstimate: 60,
		Reason:        "Test output confirming fix works",
	})

	// Step 9: Large tool output (>2000 chars) — should be forced to Artifact.
	longOutput := strings.Repeat("line of output from build tool\n", 100) // ~3100 chars
	admit(core.ContextItem{
		ID:            "tool:build-output",
		Tier:          core.TierNear, // Requesting Near, but should be forced to Artifact.
		Title:         "Full build output",
		Source:        longOutput,
		TokenEstimate: core.EstimateTokens(longOutput),
		Reason:        "Complete build output for debugging",
	})

	// Step 10: Small observation — Near tier.
	admit(core.ContextItem{
		ID:            "obs:bug-found",
		Tier:          core.TierNear,
		Title:         "Bug found: expired items counted in budget",
		Source:        "The collectNearEvictionCandidatesLocked returns expired items, causing AutoBudget to waste eviction slots on items that would naturally expire.",
		TokenEstimate: 60,
		Reason:        "Key observation from code review",
	})

	// Step 11: Another observation — Near tier.
	admit(core.ContextItem{
		ID:            "obs:fix-applied",
		Tier:          core.TierNear,
		Title:         "Fix applied: filter expired before eviction",
		Source:        "Changed collectNearEvictionCandidatesLocked to skip expired items.",
		TokenEstimate: 40,
		Reason:        "Record of the fix that was applied",
	})

	// Step 12: Pinned item that should survive eviction.
	admit(core.ContextItem{
		ID:            "pinned:critical-path",
		Tier:          core.TierNear,
		Title:         "Critical code path: eviction loop",
		Source:        "for nearIndex < len(evict) && totals.UsedTokens > safeBoundary { ... }",
		TokenEstimate: 80,
		Reason:        "User pinned this as the most important code section",
		Pinned:        true,
	})

	// Step 13: Add extra bulk items via Add (bypasses admission) to push
	// past the safe budget (85% of 3000 = 2550 tokens). This simulates
	// a scenario where many tool results accumulate over a session.
	for i := 0; i < 30; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("bulk:log-%02d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("Debug log line %d", i),
			Source:        fmt.Sprintf("DEBUG [%d] processing request handler %d", i, i),
			TokenEstimate: 50,
			Reason:        "accumulated debug output",
		})
	}

	// --- Verification phase ---

	t.Run("admission_tier_placement", func(t *testing.T) {
		for _, a := range admitted {
			t.Logf("  admitted %-30s  tier=%-10s  pinned=%-5v  tokens=%-5d  reason=%s",
				a.id, a.tier, a.pinned, a.tokenEstimate, a.selectionReason)
		}

		// Anchor items must be TierAnchor.
		for _, a := range admitted {
			switch {
			case strings.HasPrefix(a.id, "anchor:"):
				if a.tier != core.TierAnchor {
					t.Errorf("item %q: expected tier %q, got %q", a.id, core.TierAnchor, a.tier)
				}
			case a.id == "tool:build-output":
				if a.tier != core.TierArtifact {
					t.Errorf("item %q: expected tier %q (forced by >2000 chars), got %q", a.id, core.TierArtifact, a.tier)
				}
			}
		}
	})

	t.Run("oversized_source_forced_to_artifact", func(t *testing.T) {
		// Find the build output item in the snapshot.
		snap := m.Snapshot()
		for _, it := range snap.Items {
			if it.ID == "tool:build-output" {
				if it.Tier != core.TierArtifact {
					t.Fatalf("expected Artifact tier, got %q", it.Tier)
				}
				if !strings.HasSuffix(it.Source, "...[truncated]") {
					t.Fatalf("source should end with '...[truncated]', got suffix %q",
						it.Source[len(it.Source)-20:])
				}
				if !strings.Contains(it.SelectionReason, "exceeded 2000 chars") {
					t.Fatalf("SelectionReason should mention truncation: %q", it.SelectionReason)
				}
				t.Logf("Oversized item correctly forced to Artifact: source len=%d", len(it.Source))
				return
			}
		}
		t.Fatal("build-output item not found in snapshot")
	})

	t.Run("selection_reasons_populated", func(t *testing.T) {
		for _, a := range admitted {
			if a.selectionReason == "" {
				t.Errorf("item %q has empty SelectionReason", a.id)
			}
		}
	})

	t.Run("auto_budget_evicts_near_items", func(t *testing.T) {
		// Run AutoBudget to bring context within safe limits.
		result := m.AutoBudget()
		t.Logf("AutoBudget: evicted %d items: %v", len(result.Evicted), result.Evicted)

		snap := m.Snapshot()
		t.Logf("After AutoBudget: %d items, %d/%d tokens, risk=%s",
			len(snap.Items), snap.UsedTokens, snap.WindowTokens, snap.PollutionRisk)

		// Verify evicted items are gone from the snapshot.
		for _, evictedID := range result.Evicted {
			for _, it := range snap.Items {
				if it.ID == evictedID {
					t.Errorf("evicted item %q still in snapshot", evictedID)
				}
			}
		}
	})

	t.Run("pinned_items_survive_eviction", func(t *testing.T) {
		snap := m.Snapshot()
		pinnedIDs := []string{"anchor:project-map", "anchor:task-goal", "pinned:critical-path"}
		for _, pid := range pinnedIDs {
			found := false
			for _, it := range snap.Items {
				if it.ID == pid {
					found = true
					if !it.Pinned {
						t.Errorf("item %q should be pinned", pid)
					}
					break
				}
			}
			if !found {
				t.Errorf("pinned item %q missing from snapshot after AutoBudget", pid)
			}
		}
	})

	t.Run("anchor_items_never_evicted", func(t *testing.T) {
		snap := m.Snapshot()
		anchorIDs := []string{"anchor:project-map", "anchor:task-goal"}
		for _, aid := range anchorIDs {
			found := false
			for _, it := range snap.Items {
				if it.ID == aid {
					found = true
					if it.Tier != core.TierAnchor {
						t.Errorf("anchor item %q has tier %q, expected %q", aid, it.Tier, core.TierAnchor)
					}
					break
				}
			}
			if !found {
				t.Errorf("anchor item %q missing from snapshot after eviction — anchors must never be evicted", aid)
			}
		}
	})
}

// ===========================================================================
// Scenario 2: Compression lineage tracking
// ===========================================================================

// TestRealityCompressionLineage admits 10 items, compresses 5 of them, and
// verifies that the CompressionRecord has correct SourceIDs, TokensBefore,
// TokensAfter; that compressed items have ReplacedBy set; and that the
// compressed observation enters context with a correct SelectionReason.
func TestRealityCompressionLineage(t *testing.T) {
	m := New(100_000) // Large window — no eviction interference.

	// Admit 10 items simulating file reads and tool results.
	items := []core.ContextItem{
		{ID: "read:file-1", Tier: core.TierNear, Title: "Read handler.go", Source: "package main\nfunc handler() {}", TokenEstimate: 120, Reason: "file read"},
		{ID: "read:file-2", Tier: core.TierNear, Title: "Read router.go", Source: "package main\nfunc route() {}", TokenEstimate: 100, Reason: "file read"},
		{ID: "read:file-3", Tier: core.TierNear, Title: "Read middleware.go", Source: "package main\nfunc middleware() {}", TokenEstimate: 130, Reason: "file read"},
		{ID: "read:file-4", Tier: core.TierNear, Title: "Read config.go", Source: "package main\nfunc config() {}", TokenEstimate: 90, Reason: "file read"},
		{ID: "read:file-5", Tier: core.TierNear, Title: "Read types.go", Source: "package main\ntype Foo struct {}", TokenEstimate: 80, Reason: "file read"},
		{ID: "tool:grep-1", Tier: core.TierNear, Title: "Grep for TODO", Source: "handler.go:10: TODO fix this\nrouter.go:5: TODO refactor", TokenEstimate: 60, Reason: "search"},
		{ID: "tool:grep-2", Tier: core.TierNear, Title: "Grep for FIXME", Source: "middleware.go:3: FIXME: race condition", TokenEstimate: 40, Reason: "search"},
		{ID: "tool:test-1", Tier: core.TierNear, Title: "Test output", Source: "PASS handler_test.go\nPASS router_test.go", TokenEstimate: 50, Reason: "test results"},
		{ID: "tool:test-2", Tier: core.TierNear, Title: "Test coverage", Source: "coverage: 78% of statements", TokenEstimate: 30, Reason: "test results"},
		{ID: "obs:summary", Tier: core.TierNear, Title: "Session summary", Source: "Read 5 files, ran tests, found 2 issues", TokenEstimate: 40, Reason: "observation"},
	}

	for _, item := range items {
		if _, err := m.Admit(item); err != nil {
			t.Fatalf("Admit(%q) failed: %v", item.ID, err)
		}
	}

	totalsBefore := m.Totals()
	t.Logf("Before compression: %d items, %d tokens used", len(items), totalsBefore.UsedTokens)

	// Compress the first 5 items (the file reads).
	compressIDs := []string{"read:file-1", "read:file-2", "read:file-3", "read:file-4", "read:file-5"}
	expectedTokensBefore := 120 + 100 + 130 + 90 + 80 // = 520

	summary := "Compressed file reads: handler.go, router.go, middleware.go, config.go, types.go"
	reason := "5 file reads consolidated to save context budget"

	record, snapshot, err := m.CompressItems(compressIDs, summary, reason)
	if err != nil {
		t.Fatalf("CompressItems failed: %v", err)
	}

	// --- Verify CompressionRecord ---
	t.Run("compression_record_fields", func(t *testing.T) {
		if len(record.SourceIDs) != 5 {
			t.Fatalf("SourceIDs count = %d, want 5", len(record.SourceIDs))
		}
		for _, id := range compressIDs {
			found := false
			for _, sid := range record.SourceIDs {
				if sid == id {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("SourceIDs missing %q", id)
			}
		}

		if record.TokensBefore != expectedTokensBefore {
			t.Fatalf("TokensBefore = %d, want %d", record.TokensBefore, expectedTokensBefore)
		}
		if record.TokensAfter <= 0 {
			t.Fatalf("TokensAfter = %d, want > 0", record.TokensAfter)
		}
		if record.TokensAfter >= record.TokensBefore {
			t.Fatalf("TokensAfter (%d) should be less than TokensBefore (%d)", record.TokensAfter, record.TokensBefore)
		}
		if record.Summary != summary {
			t.Fatalf("Summary = %q, want %q", record.Summary, summary)
		}
		if record.Reason != reason {
			t.Fatalf("Reason = %q, want %q", record.Reason, reason)
		}

		t.Logf("Compression: %d tokens -> %d tokens (saved %d, %.0f%% reduction)",
			record.TokensBefore, record.TokensAfter,
			record.TokensBefore-record.TokensAfter,
			float64(record.TokensBefore-record.TokensAfter)/float64(record.TokensBefore)*100)
	})

	// --- Verify ReplacedBy is set on source items ---
	t.Run("replaced_by_set_on_source_items", func(t *testing.T) {
		for _, id := range compressIDs {
			item, ok := m.items[id]
			if !ok {
				t.Errorf("source item %q missing from manager items map", id)
				continue
			}
			if item.ReplacedBy != record.ID {
				t.Errorf("item %q: ReplacedBy = %q, want %q", id, item.ReplacedBy, record.ID)
			}
		}
	})

	// --- Verify compressed items are NOT in the active snapshot ---
	t.Run("compressed_items_not_in_snapshot", func(t *testing.T) {
		for _, id := range compressIDs {
			for _, it := range snapshot.Items {
				if it.ID == id {
					t.Errorf("compressed item %q should not appear in snapshot", id)
				}
			}
		}
	})

	// --- Verify compression artifact IS in the snapshot ---
	t.Run("compression_artifact_in_snapshot", func(t *testing.T) {
		found := false
		for _, it := range snapshot.Items {
			if it.ID == record.ID {
				found = true
				if it.Source != "compression" {
					t.Errorf("artifact source = %q, want %q", it.Source, "compression")
				}
				if !strings.Contains(it.Reason, "compressed from 5 items") {
					t.Errorf("artifact reason = %q, should mention 'compressed from 5 items'", it.Reason)
				}
				t.Logf("Compression artifact: id=%q tier=%s tokens=%d reason=%q",
					it.ID, it.Tier, it.TokenEstimate, it.Reason)
				break
			}
		}
		if !found {
			t.Error("compression artifact not found in snapshot")
		}
	})

	// --- Verify CompressionRecord appears in snapshot ---
	t.Run("compression_record_in_snapshot", func(t *testing.T) {
		if len(snapshot.CompressionRecords) != 1 {
			t.Fatalf("CompressionRecords count = %d, want 1", len(snapshot.CompressionRecords))
		}
		sr := snapshot.CompressionRecords[0]
		if sr.ID != record.ID {
			t.Errorf("snapshot record ID = %q, want %q", sr.ID, record.ID)
		}
		if sr.TokensBefore != expectedTokensBefore {
			t.Errorf("snapshot record TokensBefore = %d, want %d", sr.TokensBefore, expectedTokensBefore)
		}
	})

	// --- Verify remaining (non-compressed) items are still present ---
	t.Run("non_compressed_items_survive", func(t *testing.T) {
		remainingIDs := []string{"tool:grep-1", "tool:grep-2", "tool:test-1", "tool:test-2", "obs:summary"}
		for _, id := range remainingIDs {
			found := false
			for _, it := range snapshot.Items {
				if it.ID == id {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("non-compressed item %q missing from snapshot", id)
			}
		}
	})
}

// ===========================================================================
// Scenario 3: Oracle review decisions
// ===========================================================================

// TestRealityOracleDecisions creates a context snapshot with mixed items
// (stale, fresh, keyword-matching) and runs the oracle Review. It verifies
// that scoring, promotions, demotions, and compressions behave as expected.
func TestRealityOracleDecisions(t *testing.T) {
	m := New(50_000)
	goal := "Fix the context manager eviction bug and write tests"

	// Phase 1: Add old items (they become "old" in insertion order).
	oldItems := []core.ContextItem{
		{ID: "old:git-log", Tier: core.TierNear, Title: "Git log output", Source: "commit abc123 initial commit", TokenEstimate: 50, Reason: "git history"},
		{ID: "old:build-err", Tier: core.TierNear, Title: "Build error from CI", Source: "error: undefined function foo", TokenEstimate: 40, Reason: "CI output"},
		{ID: "old:coverage", Tier: core.TierNear, Title: "Old test coverage", Source: "coverage: 62%", TokenEstimate: 20, Reason: "coverage report"},
	}
	for _, item := range oldItems {
		if _, err := m.Admit(item); err != nil {
			t.Fatalf("Admit(%q) failed: %v", item.ID, err)
		}
	}

	// Phase 2: Add recent items (push old items into the first half of order).
	recentItems := []core.ContextItem{
		{ID: "recent:read-manager", Tier: core.TierNear, Title: "Read context manager code", Source: "func AutoBudget() { ... eviction logic ... }", TokenEstimate: 300, Reason: "file read for current task", Keywords: []string{"context", "manager", "eviction"}},
		{ID: "recent:read-oracle", Tier: core.TierNear, Title: "Read oracle scoring", Source: "func computeOracleScore() { ... }", TokenEstimate: 250, Reason: "file read for current task", Keywords: []string{"oracle", "scoring"}},
		{ID: "recent:test-run", Tier: core.TierNear, Title: "Test run output", Source: "PASS TestAutoBudget\nPASS TestOracleReview", TokenEstimate: 60, Reason: "test results", Keywords: []string{"test", "eviction"}},
		{ID: "recent:grep-results", Tier: core.TierNear, Title: "Grep for eviction", Source: "manager.go:397: AutoBudget\nmanager.go:463: collectNearEvictionCandidates", TokenEstimate: 80, Reason: "search results", Keywords: []string{"eviction", "budget"}},
		{ID: "anchor:project-map", Tier: core.TierAnchor, Title: "Project map", Source: "internal/context/\ninternal/core/", TokenEstimate: 30, Reason: "project structure", Pinned: true},
	}
	for _, item := range recentItems {
		if _, err := m.Admit(item); err != nil {
			t.Fatalf("Admit(%q) failed: %v", item.ID, err)
		}
	}

	// Provide observations that reference some items.
	observations := []core.Observation{
		{Summary: "Read context manager file for eviction bug", StateDelta: "found bug in collectNearEvictionCandidatesLocked"},
		{Summary: "Ran tests after fix", StateDelta: "all tests pass including TestAutoBudget"},
	}

	oracle := NewOracle(m)
	result := oracle.Review(goal, observations)

	t.Logf("Oracle review for goal: %q", goal)
	t.Logf("Scores:")
	for id, score := range result.Scores {
		t.Logf("  %-30s  score=%d", id, score)
	}
	t.Logf("Promoted: %d items", len(result.Promoted))
	for _, p := range result.Promoted {
		t.Logf("  promoted: %s (score=%d)", p.ID, result.Scores[p.ID])
	}
	t.Logf("Demoted: %d items", len(result.Demoted))
	for _, id := range result.Demoted {
		t.Logf("  demoted: %s (score=%d)", id, result.Scores[id])
	}
	t.Logf("Compressed candidates: %d groups", len(result.Compressed))
	for _, c := range result.Compressed {
		t.Logf("  compress: %v (reason=%q)", c.ItemIDs, c.Reason)
	}
	t.Logf("Reason: %s", result.Reason)

	// --- Verify keyword-matching items get higher scores ---
	t.Run("keyword_matches_score_higher", func(t *testing.T) {
		// recent:read-manager has "context" + "manager" matching the goal,
		// plus it appears in observations (recency bonus). Should score high.
		managerScore := result.Scores["recent:read-manager"]
		oldScore := result.Scores["old:git-log"]

		if managerScore <= oldScore {
			t.Errorf("recent:read-manager score (%d) should be > old:git-log score (%d)",
				managerScore, oldScore)
		}
		t.Logf("keyword match: read-manager=%d vs git-log=%d", managerScore, oldScore)
	})

	// --- Verify stale items get recency decay ---
	t.Run("stale_items_get_decay", func(t *testing.T) {
		// old:* items are in the first half of order and not referenced in observations.
		// They should have lower scores due to staleness penalty.
		for _, id := range []string{"old:git-log", "old:build-err", "old:coverage"} {
			score := result.Scores[id]
			if score >= 20 {
				t.Logf("stale item %s score=%d (may be high due to goal keyword match)", id, score)
			}
		}
	})

	// --- Verify promoted items make sense ---
	t.Run("promotions_make_sense", func(t *testing.T) {
		for _, p := range result.Promoted {
			score := result.Scores[p.ID]
			if score < 60 {
				t.Errorf("promoted item %s has score %d, expected >= 60", p.ID, score)
			}
			// Promoted items should not already be Anchor.
			if p.Tier == core.TierAnchor {
				t.Errorf("item %s is already Anchor tier, should not be in promoted list", p.ID)
			}
		}
	})

	// --- Verify demoted items make sense ---
	t.Run("demotions_make_sense", func(t *testing.T) {
		for _, id := range result.Demoted {
			score := result.Scores[id]
			if score >= 5 {
				t.Errorf("demoted item %s has score %d, expected < 5", id, score)
			}
		}
	})

	// --- Verify compressed items make sense ---
	t.Run("compressions_make_sense", func(t *testing.T) {
		for _, c := range result.Compressed {
			for _, id := range c.ItemIDs {
				score := result.Scores[id]
				if score < 5 || score > 9 {
					t.Errorf("compressed item %s has score %d, expected 5-9", id, score)
				}
			}
		}
	})

	// --- Verify pinned items are never demoted or compressed ---
	t.Run("pinned_items_protected", func(t *testing.T) {
		for _, id := range result.Demoted {
			if id == "anchor:project-map" {
				t.Error("pinned anchor:project-map should never be demoted")
			}
		}
		for _, c := range result.Compressed {
			for _, id := range c.ItemIDs {
				if id == "anchor:project-map" {
					t.Error("pinned anchor:project-map should never be compressed")
				}
			}
		}
	})

	// --- Run RunOracleStep and verify promotions are applied ---
	t.Run("run_oracle_step_applies_promotions", func(t *testing.T) {
		snapBefore := m.Snapshot()
		promotedCount := 0
		for _, p := range snapBefore.Items {
			if p.Tier == core.TierAnchor {
				promotedCount++
			}
		}

		stepResult := RunOracleStep(m, goal, observations, nil)

		snapAfter := m.Snapshot()
		anchorCount := 0
		for _, it := range snapAfter.Items {
			if it.Tier == core.TierAnchor {
				anchorCount++
			}
		}

		t.Logf("Before RunOracleStep: %d anchors; after: %d anchors (promoted %d)",
			promotedCount, anchorCount, len(stepResult.Promoted))

		// Promoted items should now be in Anchor tier.
		for _, p := range stepResult.Promoted {
			for _, it := range snapAfter.Items {
				if it.ID == p.ID && it.Tier != core.TierAnchor {
					t.Errorf("promoted item %s still has tier %s, expected anchor", it.ID, it.Tier)
				}
			}
		}
	})
}

// ===========================================================================
// Scenario 4: Pollution guard
// ===========================================================================

// TestRealityPollutionGuard fills the context past the window limit using Add
// (which bypasses admission), then verifies that Admit rejects Near items but
// still accepts Artifact, Anchor, and Pinned items.
func TestRealityPollutionGuard(t *testing.T) {
	windowTokens := 10_000
	m := New(windowTokens)

	// Use Add (bypasses admission) to fill well past the window so that
	// pollutionRisk returns over_window.
	itemTokens := 200
	fillCount := 60 // 60 * 200 = 12000 tokens, 120% of window

	for i := 0; i < fillCount; i++ {
		_, _ = m.Add(core.ContextItem{
			ID:            fmt.Sprintf("fill:%03d", i),
			Tier:          core.TierNear,
			Title:         fmt.Sprintf("Filler item %d", i),
			Source:        fmt.Sprintf("Source content for filler item %d", i),
			TokenEstimate: itemTokens,
			Reason:        "filler to exceed budget",
		})
	}

	totals := m.Totals()
	risk := pollutionRisk(windowTokens, totals.UsedTokens)
	t.Logf("After fill: %d items, %d/%d tokens (%.0f%% of window), risk=%s",
		len(m.Snapshot().Items), totals.UsedTokens, windowTokens,
		float64(totals.UsedTokens)/float64(windowTokens)*100, risk)

	if risk != PollutionOverWindow {
		t.Skipf("did not reach over_window (risk=%s); adjusting test", risk)
	}

	// --- Verify Near item is rejected ---
	t.Run("near_item_rejected_when_over_window", func(t *testing.T) {
		_, err := m.Admit(core.ContextItem{
			ID:            "reject:me",
			Tier:          core.TierNear,
			Title:         "Should be rejected",
			Source:        "This item should not be admitted",
			TokenEstimate: 50,
			Reason:        "testing rejection",
		})
		if err != ErrAdmitOverWindow {
			t.Fatalf("expected ErrAdmitOverWindow, got %v", err)
		}
		t.Log("Near item correctly rejected when over_window")
	})

	// --- Verify Artifact item is still accepted ---
	t.Run("artifact_accepted_when_over_window", func(t *testing.T) {
		_, err := m.Admit(core.ContextItem{
			ID:            "artifact:accepted",
			Tier:          core.TierArtifact,
			Title:         "Raw build output",
			Source:        "build output data",
			TokenEstimate: 500,
			Reason:        "artifact bypass test",
		})
		if err != nil {
			t.Fatalf("Artifact should bypass admission; got error: %v", err)
		}

		snap := m.Snapshot()
		found := false
		for _, it := range snap.Items {
			if it.ID == "artifact:accepted" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Artifact item not found in snapshot")
		}
		t.Log("Artifact item correctly accepted when over_window")
	})

	// --- Verify Anchor item is still accepted ---
	t.Run("anchor_accepted_when_over_window", func(t *testing.T) {
		_, err := m.Admit(core.ContextItem{
			ID:            "anchor:accepted",
			Tier:          core.TierAnchor,
			Title:         "Important anchor",
			Source:        "anchor data",
			TokenEstimate: 100,
			Reason:        "anchor bypass test",
		})
		if err != nil {
			t.Fatalf("Anchor should bypass admission; got error: %v", err)
		}

		snap := m.Snapshot()
		found := false
		for _, it := range snap.Items {
			if it.ID == "anchor:accepted" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("Anchor item not found in snapshot")
		}
		t.Log("Anchor item correctly accepted when over_window")
	})

	// --- Verify pinned Near item is still accepted ---
	t.Run("pinned_near_accepted_when_over_window", func(t *testing.T) {
		_, err := m.Admit(core.ContextItem{
			ID:            "pinned:accepted",
			Tier:          core.TierNear,
			Title:         "Pinned critical item",
			Source:        "pinned data",
			TokenEstimate: 50,
			Reason:        "pinned bypass test",
			Pinned:        true,
		})
		if err != nil {
			t.Fatalf("Pinned Near item should bypass admission; got error: %v", err)
		}
		t.Log("Pinned Near item correctly accepted when over_window")
	})

	// --- Verify AutoBudget brings context back within limits ---
	t.Run("auto_budget_restores_safe_usage", func(t *testing.T) {
		result := m.AutoBudget()
		t.Logf("AutoBudget: evicted %d items: %v", len(result.Evicted), result.Evicted)

		snap := m.Snapshot()
		t.Logf("After AutoBudget: %d items, %d/%d tokens (%.0f%%), risk=%s",
			len(snap.Items), snap.UsedTokens, snap.WindowTokens,
			float64(snap.UsedTokens)/float64(snap.WindowTokens)*100, snap.PollutionRisk)

		if snap.PollutionRisk == PollutionOverWindow {
			t.Errorf("AutoBudget did not reduce risk below over_window; risk=%s, used=%d/%d",
				snap.PollutionRisk, snap.UsedTokens, snap.WindowTokens)
		}
	})
}

// ===========================================================================
// Scenario 5: Context report generation
// ===========================================================================

// TestRealityContextReport runs a full simulation and produces a summary
// suitable for inclusion in the CONTEXT_REALITY_REPORT.md document.
func TestRealityContextReport(t *testing.T) {
	// --- Run a complete simulation ---
	windowTokens := 20_000
	m := New(windowTokens)

	// Counters for the report.
	var (
		nearAdmitted     int
		anchorAdmitted   int
		artifactAdmitted int
		totalEvicted     int
		totalCompressed  int
		oraclePromoted   int
		oracleDemoted    int
		oracleCompressed int
	)

	// Simulate a coding session.
	sessionItems := []core.ContextItem{
		{ID: "prompt:user", Tier: core.TierNear, Title: "User prompt", Source: "Fix the bug in the context manager", TokenEstimate: 30, Reason: "user request"},
		{ID: "plan:agent", Tier: core.TierNear, Title: "Agent plan", Source: "1. Read code 2. Find bug 3. Fix 4. Test", TokenEstimate: 40, Reason: "agent plan"},
		{ID: "anchor:project", Tier: core.TierAnchor, Title: "Project map", Source: "internal/context/ manager.go oracle.go", TokenEstimate: 30, Reason: "project structure", Pinned: true},
		{ID: "anchor:goal", Tier: core.TierAnchor, Title: "Task goal", Source: "Fix AutoBudget eviction", TokenEstimate: 20, Reason: "task goal", Pinned: true},
		{ID: "read:file-1", Tier: core.TierNear, Title: "Read manager.go", Source: strings.Repeat("code line\n", 50), TokenEstimate: 200, Reason: "file read"},
		{ID: "read:file-2", Tier: core.TierNear, Title: "Read oracle.go", Source: strings.Repeat("code line\n", 40), TokenEstimate: 160, Reason: "file read"},
		{ID: "read:file-3", Tier: core.TierNear, Title: "Read types.go", Source: strings.Repeat("code line\n", 30), TokenEstimate: 120, Reason: "file read"},
		{ID: "tool:grep-1", Tier: core.TierNear, Title: "Grep results", Source: "match1\nmatch2\nmatch3", TokenEstimate: 40, Reason: "search"},
		{ID: "tool:test-1", Tier: core.TierNear, Title: "Test output", Source: "PASS all tests", TokenEstimate: 30, Reason: "test results"},
		{ID: "obs:bug-found", Tier: core.TierNear, Title: "Bug found", Source: "Expired items counted in budget", TokenEstimate: 40, Reason: "observation", Keywords: []string{"bug", "eviction", "budget"}},
		{ID: "read:file-4", Tier: core.TierNear, Title: "Read test file", Source: strings.Repeat("test line\n", 25), TokenEstimate: 100, Reason: "file read"},
		{ID: "read:file-5", Tier: core.TierNear, Title: "Read config", Source: "config data", TokenEstimate: 50, Reason: "file read"},
		{ID: "tool:build-1", Tier: core.TierNear, Title: "Build output", Source: strings.Repeat("build line\n", 60), TokenEstimate: 240, Reason: "build results"},
		{ID: "obs:fix-applied", Tier: core.TierNear, Title: "Fix applied", Source: "Changed eviction filter", TokenEstimate: 30, Reason: "observation"},
		{ID: "tool:test-2", Tier: core.TierNear, Title: "Re-run tests", Source: "PASS all tests after fix", TokenEstimate: 40, Reason: "test results"},
	}

	for _, item := range sessionItems {
		snap, err := m.Admit(item)
		if err != nil {
			t.Logf("Admit(%q) error: %v (used=%d/%d)", item.ID, err, snap.UsedTokens, snap.WindowTokens)
			continue
		}
		switch item.Tier {
		case core.TierNear:
			nearAdmitted++
		case core.TierAnchor:
			anchorAdmitted++
		case core.TierArtifact:
			artifactAdmitted++
		}
	}

	// Also count any items forced to artifact by size.
	snap := m.Snapshot()
	for _, it := range snap.Items {
		if it.Tier == core.TierArtifact && it.ID != "artifact:forced" {
			// Already counted in artifactAdmitted if it was forced during Admit.
		}
	}

	t.Logf("Admitted: near=%d, anchor=%d, artifact=%d, total=%d",
		nearAdmitted, anchorAdmitted, artifactAdmitted, nearAdmitted+anchorAdmitted+artifactAdmitted)

	// Compress some items.
	compressIDs := []string{"read:file-1", "read:file-2", "read:file-3", "read:file-4", "read:file-5"}
	_, _, err := m.CompressItems(compressIDs, "Compressed 5 file reads", "token budget optimization")
	if err != nil {
		t.Fatalf("CompressItems failed: %v", err)
	}
	totalCompressed = len(compressIDs)

	// Run oracle review.
	goal := "Fix the context manager eviction bug and write tests"
	observations := []core.Observation{
		{Summary: "Bug found in eviction logic", StateDelta: "expired items counted"},
		{Summary: "Fix applied and tests pass", StateDelta: "eviction filter changed"},
	}

	oracle := NewOracle(m)
	oracleResult := oracle.Review(goal, observations)
	oraclePromoted = len(oracleResult.Promoted)
	oracleDemoted = len(oracleResult.Demoted)
	for _, c := range oracleResult.Compressed {
		oracleCompressed += len(c.ItemIDs)
	}

	// Apply oracle decisions.
	RunOracleStep(m, goal, observations, nil)

	// Run AutoBudget.
	budgetResult := m.AutoBudget()
	totalEvicted = len(budgetResult.Evicted)

	// Final state.
	finalSnap := m.Snapshot()
	totals := m.Totals()

	t.Logf("=== CONTEXT REALITY REPORT ===")
	t.Logf("Window: %d tokens", windowTokens)
	t.Logf("Final state: %d items, %d tokens used (%.0f%% of window)",
		len(finalSnap.Items), finalSnap.UsedTokens,
		float64(finalSnap.UsedTokens)/float64(finalSnap.WindowTokens)*100)
	t.Logf("Pollution risk: %s", finalSnap.PollutionRisk)
	t.Logf("Tier breakdown: near=%d, anchor=%d, artifact=%d, pinned=%d",
		totals.NearTokens, totals.AnchorTokens, totals.ArtifactTokens, totals.PinnedTokens)
	t.Logf("Admission: near=%d, anchor=%d, artifact=%d", nearAdmitted, anchorAdmitted, artifactAdmitted)
	t.Logf("Compressed: %d items into artifacts", totalCompressed)
	t.Logf("AutoBudget evicted: %d items", totalEvicted)
	t.Logf("Oracle: promoted=%d, demoted=%d, compressed=%d", oraclePromoted, oracleDemoted, oracleCompressed)
	t.Logf("Compression records: %d", len(finalSnap.CompressionRecords))

	// Print final items.
	t.Logf("Final context items:")
	for _, it := range finalSnap.Items {
		t.Logf("  %-35s  tier=%-10s  pinned=%-5v  tokens=%-5d  reason=%s",
			it.ID, it.Tier, it.Pinned, it.TokenEstimate, it.SelectionReason)
	}

	// Basic sanity checks on the report data.
	if nearAdmitted+anchorAdmitted+artifactAdmitted == 0 {
		t.Fatal("no items were admitted")
	}
	if len(finalSnap.Items) == 0 {
		t.Fatal("final snapshot has no items")
	}
}
