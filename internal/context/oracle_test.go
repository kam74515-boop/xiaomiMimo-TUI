package context

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func TestOracleScoresGoalKeywordMatch(t *testing.T) {
	// Directly exercise the scoring function with controlled inputs.
	tests := []struct {
		name         string
		item         core.ContextItem
		goalWords    []string
		isRecent     bool
		isOld        bool
		windowTokens int
		wantMin      int
		wantMax      int
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
			score := computeOracleScore(test.item, test.goalWords, test.isRecent, test.isOld, test.windowTokens)
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

	// The stale items (first two in order) should be demoted.
	staleDemoted := 0
	for _, id := range result.Demoted {
		if id == "near:stale-log" || id == "near:stale-coverage" {
			staleDemoted++
		}
	}
	if staleDemoted < 2 {
		t.Fatalf("expected both stale items demoted; demoted=%v, scores=%v", result.Demoted, result.Scores)
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
