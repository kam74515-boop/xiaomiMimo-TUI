package context

import (
	"fmt"
	"strings"

	"mimo-tui/internal/core"
)

// Oracle periodically reviews context items and recommends which evidence
// deserves to stay in context, be promoted, or be demoted.
type Oracle struct {
	manager *Manager
}

// ReviewResult holds the outcome of an oracle review pass.
type ReviewResult struct {
	Promoted []core.ContextItem // Items recommended for promotion to Anchor
	Demoted  []string           // Item IDs recommended for demotion or removal
	Scores   map[string]int     // Score for each item, 0-100
	Reason   string             // Human-readable explanation
}

// NewOracle creates an Oracle that reviews the given Manager's context.
func NewOracle(manager *Manager) *Oracle {
	return &Oracle{manager: manager}
}

// Review examines all context items and recent observations, recommending
// which items should stay or go based on relevance scoring.
func (o *Oracle) Review(goal string, recentObservations []core.Observation) ReviewResult {
	o.manager.mu.RLock()
	items := o.manager.activeItemsLocked()
	// Capture insertion order for staleness detection.
	order := make([]string, len(o.manager.order))
	copy(order, o.manager.order)
	windowTokens := o.manager.windowTokens
	o.manager.mu.RUnlock()

	goalWords := tokenize(strings.ToLower(goal))

	// Build observation lookup: collect all meaningful words from recent
	// observations so we can detect which items are "referenced".
	obsWords := make(map[string]bool)
	for _, obs := range recentObservations {
		for _, w := range tokenize(strings.ToLower(obs.Summary + " " + obs.StateDelta)) {
			obsWords[w] = true
		}
	}

	// Build insertion-order position map.
	orderPos := make(map[string]int, len(order))
	for i, id := range order {
		orderPos[id] = i
	}

	// Compute old threshold: items in the first half of insertion order
	// (and not pinned) are candidates for the staleness penalty.
	oldThreshold := len(order) / 2
	if oldThreshold < 1 {
		oldThreshold = 1
	}

	// Score every active item.
	scores := make(map[string]int, len(items))
	for _, item := range items {
		isRecent := itemReferencedInObs(item, obsWords)
		isOld := !item.Pinned && orderPos[item.ID] < oldThreshold && !isRecent
		scores[item.ID] = computeOracleScore(item, goalWords, isRecent, isOld, windowTokens)
	}

	// Apply review thresholds.
	var promoted []core.ContextItem
	var demoted []string
	for _, item := range items {
		score := scores[item.ID]
		switch {
		case score >= 60 && item.Tier != core.TierAnchor && item.Tier != core.TierArtifact:
			promoted = append(promoted, item)
		case score < 20 && !item.Pinned:
			demoted = append(demoted, item.ID)
		}
	}

	reason := buildOracleReason(promoted, demoted, scores)

	return ReviewResult{
		Promoted: promoted,
		Demoted:  demoted,
		Scores:   scores,
		Reason:   reason,
	}
}

// RunOracleStep performs an oracle review and publishes results via the event
// bus. Call this after tool executions to re-evaluate context placement.
func RunOracleStep(manager *Manager, goal string, recentObservations []core.Observation, bus *core.Bus) ReviewResult {
	oracle := NewOracle(manager)
	result := oracle.Review(goal, recentObservations)

	if bus == nil {
		return result
	}

	// Apply promotions: update tier to Anchor.
	for _, item := range result.Promoted {
		promotedItem := item
		promotedItem.Tier = core.TierAnchor
		promotedItem.SelectionReason = "oracle promoted to anchor: " + result.Reason
		snapshot, err := manager.Update(promotedItem)
		if err == nil {
			publishContextUpdate(bus, snapshot)
		}
	}

	// Apply demotions: remove the item.
	for _, id := range result.Demoted {
		snapshot, err := manager.Remove(id)
		if err == nil {
			publishContextUpdate(bus, snapshot)
		}
	}

	return result
}

// computeOracleScore calculates a relevance score (0-100) for a single
// context item based on the specified rules.
func computeOracleScore(item core.ContextItem, goalWords []string, isRecent, isOld bool, windowTokens int) int {
	score := 0

	// Pinned items get a massive bonus and are never demoted.
	if item.Pinned {
		score += 100
	}

	// Anchor tier gets a base bonus.
	if item.Tier == core.TierAnchor {
		score += 20
	}

	// Goal keyword matching: +30 per matching word.
	itemText := strings.ToLower(item.Title + " " + item.Source + " " + item.Reason)
	for _, w := range goalWords {
		if w != "" && strings.Contains(itemText, w) {
			score += 30
		}
	}

	// Recency bonus from observations.
	if isRecent {
		score += 25
	}

	// Staleness penalty.
	if isOld {
		score -= 20
	}

	// Token budget penalty: items consuming >50% of the window.
	if windowTokens > 0 && item.TokenEstimate > windowTokens/2 {
		score -= 10
	}

	// Clamp to [0, 100].
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// itemReferencedInObs returns true when the item's title or source shares
// meaningful words with the recent-observation word set.
func itemReferencedInObs(item core.ContextItem, obsWords map[string]bool) bool {
	if len(obsWords) == 0 {
		return false
	}
	itemWords := tokenize(strings.ToLower(item.Title + " " + item.Source))
	for _, w := range itemWords {
		if obsWords[w] {
			return true
		}
	}
	return false
}

// tokenize splits text into lowercase words, dropping short or empty tokens.
func tokenize(text string) []string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		// Keep words with 3+ characters to avoid noise.
		if len(f) >= 3 {
			out = append(out, f)
		}
	}
	return out
}

// buildOracleReason produces a human-readable summary of the oracle's
// decisions.
func buildOracleReason(promoted []core.ContextItem, demoted []string, scores map[string]int) string {
	parts := make([]string, 0, 3)

	if len(promoted) > 0 {
		ids := make([]string, len(promoted))
		for i, item := range promoted {
			ids[i] = fmt.Sprintf("%s(%d)", item.ID, scores[item.ID])
		}
		parts = append(parts, fmt.Sprintf("promoted %d item(s) to anchor: %s", len(promoted), strings.Join(ids, ", ")))
	}

	if len(demoted) > 0 {
		ids := make([]string, len(demoted))
		for i, id := range demoted {
			ids[i] = fmt.Sprintf("%s(%d)", id, scores[id])
		}
		parts = append(parts, fmt.Sprintf("demoted %d item(s): %s", len(demoted), strings.Join(ids, ", ")))
	}

	if len(parts) == 0 {
		return "oracle review complete; no promotions or demotions recommended"
	}
	return strings.Join(parts, "; ")
}

// publishContextUpdate sends a context snapshot to the event bus.
func publishContextUpdate(bus *core.Bus, snapshot core.ContextSnapshot) {
	event := core.NewEvent(core.EventContextUpdate)
	event.Context = &snapshot
	bus.Publish(event)
}
