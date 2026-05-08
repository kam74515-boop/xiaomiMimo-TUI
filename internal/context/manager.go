package context

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"mimo-tui/internal/core"
)

const (
	DefaultWindowTokens = 1_000_000

	ProjectMapAnchorID = "anchor:project-map"
	TaskGoalAnchorID   = "anchor:task-goal"

	PollutionLow        = "low"
	PollutionWarning    = "warning"
	PollutionOverWindow = "over_window"
)

var (
	ErrDuplicateItem = errors.New("context item already exists")
	ErrItemNotFound  = errors.New("context item not found")
	ErrInvalidItemID = errors.New("context item id is required")

	SafeBudgetRatio = 0.85
)

type TokenTotals struct {
	WindowTokens   int
	UsedTokens     int
	NearTokens     int
	AnchorTokens   int
	ArtifactTokens int
	PinnedTokens   int
}

type Manager struct {
	mu           sync.RWMutex
	windowTokens int
	items        map[string]core.ContextItem
	order        []string
	now          func() time.Time
}

func New(windowTokens int) *Manager {
	if windowTokens <= 0 {
		windowTokens = DefaultWindowTokens
	}
	return &Manager{
		windowTokens: windowTokens,
		items:        make(map[string]core.ContextItem),
		now:          time.Now,
	}
}

func NewSeeded(windowTokens int, projectMapSource, taskGoalSource string) *Manager {
	m := New(windowTokens)
	m.SeedAnchors(projectMapSource, taskGoalSource)
	return m
}

func (m *Manager) Add(item core.ContextItem) (core.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item = normalizeItem(item)
	if item.ID == "" {
		return m.snapshotLocked(), ErrInvalidItemID
	}
	if _, ok := m.items[item.ID]; ok {
		return m.snapshotLocked(), ErrDuplicateItem
	}
	m.items[item.ID] = item
	m.order = append(m.order, item.ID)
	return m.snapshotLocked(), nil
}

func (m *Manager) Update(item core.ContextItem) (core.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item = normalizeItem(item)
	if item.ID == "" {
		return m.snapshotLocked(), ErrInvalidItemID
	}
	if _, ok := m.items[item.ID]; !ok {
		return m.snapshotLocked(), ErrItemNotFound
	}
	m.items[item.ID] = item
	return m.snapshotLocked(), nil
}

func (m *Manager) Upsert(item core.ContextItem) (core.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item = normalizeItem(item)
	if item.ID == "" {
		return m.snapshotLocked(), ErrInvalidItemID
	}
	if _, ok := m.items[item.ID]; !ok {
		m.order = append(m.order, item.ID)
	}
	m.items[item.ID] = item
	return m.snapshotLocked(), nil
}

func (m *Manager) Remove(id string) (core.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" {
		return m.snapshotLocked(), ErrInvalidItemID
	}
	if _, ok := m.items[id]; !ok {
		return m.snapshotLocked(), ErrItemNotFound
	}
	delete(m.items, id)
	for i, itemID := range m.order {
		if itemID == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return m.snapshotLocked(), nil
}

func (m *Manager) Pin(id string) (core.ContextSnapshot, error) {
	return m.setPinned(id, true)
}

func (m *Manager) Unpin(id string) (core.ContextSnapshot, error) {
	return m.setPinned(id, false)
}

func (m *Manager) Snapshot() core.ContextSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshotLocked()
}

func (m *Manager) Totals() TokenTotals {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalsLocked()
}

func (m *Manager) SeedAnchors(projectMapSource, taskGoalSource string) core.ContextSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.seedAnchorLocked(core.ContextItem{
		ID:            ProjectMapAnchorID,
		Tier:          core.TierAnchor,
		Title:         "Project map",
		Source:        projectMapSource,
		TokenEstimate: core.EstimateTokens(projectMapSource),
		Pinned:        true,
		Reason:        "Seed anchor for project structure and important files",
	})
	m.seedAnchorLocked(core.ContextItem{
		ID:            TaskGoalAnchorID,
		Tier:          core.TierAnchor,
		Title:         "Task goal",
		Source:        taskGoalSource,
		TokenEstimate: core.EstimateTokens(taskGoalSource),
		Pinned:        true,
		Reason:        "Seed anchor for the active task objective",
	})
	return m.snapshotLocked()
}

func (m *Manager) setPinned(id string, pinned bool) (core.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" {
		return m.snapshotLocked(), ErrInvalidItemID
	}
	item, ok := m.items[id]
	if !ok {
		return m.snapshotLocked(), ErrItemNotFound
	}
	item.Pinned = pinned
	m.items[id] = item
	return m.snapshotLocked(), nil
}

func (m *Manager) seedAnchorLocked(item core.ContextItem) {
	item = normalizeItem(item)
	if _, ok := m.items[item.ID]; !ok {
		m.order = append(m.order, item.ID)
	}
	m.items[item.ID] = item
}

func (m *Manager) snapshotLocked() core.ContextSnapshot {
	items := m.activeItemsLocked()
	totals := totalsFor(m.windowTokens, items)
	return core.ContextSnapshot{
		WindowTokens:  m.windowTokens,
		UsedTokens:    totals.UsedTokens,
		Items:         items,
		PollutionRisk: pollutionRisk(m.windowTokens, totals.UsedTokens),
	}
}

func (m *Manager) totalsLocked() TokenTotals {
	return totalsFor(m.windowTokens, m.activeItemsLocked())
}

func (m *Manager) activeItemsLocked() []core.ContextItem {
	now := m.now()
	items := make([]core.ContextItem, 0, len(m.items))
	for _, id := range m.order {
		item, ok := m.items[id]
		if !ok || expired(item, now) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return tierRank(items[i].Tier) < tierRank(items[j].Tier)
	})
	return items
}

func totalsFor(windowTokens int, items []core.ContextItem) TokenTotals {
	totals := TokenTotals{WindowTokens: windowTokens}
	for _, item := range items {
		tokens := item.TokenEstimate
		if tokens < 0 {
			tokens = 0
		}
		totals.UsedTokens += tokens
		if item.Pinned {
			totals.PinnedTokens += tokens
		}
		switch item.Tier {
		case core.TierAnchor:
			totals.AnchorTokens += tokens
		case core.TierArtifact:
			totals.ArtifactTokens += tokens
		default:
			totals.NearTokens += tokens
		}
	}
	return totals
}

func normalizeItem(item core.ContextItem) core.ContextItem {
	if item.Tier == "" {
		item.Tier = core.TierNear
	}
	if item.TokenEstimate < 0 {
		item.TokenEstimate = 0
	}
	return item
}

func expired(item core.ContextItem, now time.Time) bool {
	return !item.Pinned && !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(now)
}

func tierRank(tier core.ContextTier) int {
	switch tier {
	case core.TierNear:
		return 0
	case core.TierAnchor:
		return 1
	case core.TierArtifact:
		return 2
	default:
		return 3
	}
}

func pollutionRisk(windowTokens, usedTokens int) string {
	if windowTokens <= 0 {
		return PollutionLow
	}
	switch {
	case usedTokens > windowTokens:
		return PollutionOverWindow
	case float64(usedTokens)/float64(windowTokens) >= SafeBudgetRatio:
		return PollutionWarning
	default:
		return PollutionLow
	}
}

// AutoBudgetResult holds the outcome of an automatic context budget enforcement pass.
type AutoBudgetResult struct {
	Evicted []string // IDs of items removed
	Warning string   // e.g. "Near tier at 95% - consider pinning critical items"
}

// AutoBudget evicts non-pinned Near items when pollution risk exceeds the safe
// budget ratio. Expired non-pinned Near items are evicted first, followed by the
// oldest non-pinned Near items by insertion order. Pinned items and Anchor items
// are never auto-evicted.
func (m *Manager) AutoBudget() AutoBudgetResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	totals := m.totalsLocked()
	risk := pollutionRisk(m.windowTokens, totals.UsedTokens)
	if risk == PollutionLow {
		return AutoBudgetResult{}
	}

	result := AutoBudgetResult{}
	safeBoundary := int(float64(m.windowTokens) * SafeBudgetRatio)
	if safeBoundary < 1 {
		safeBoundary = 1
	}

	evict := m.collectNearEvictionCandidatesLocked()
	// Phase 1: evict expired non-pinned Near items (cleanup; they do not
	// contribute to the budget so we do not subtract from totals).
	for i := 0; i < len(evict); {
		item := evict[i]
		if !expired(item, m.now()) {
			i++
			continue
		}
		result.Evicted = append(result.Evicted, item.ID)
		delete(m.items, item.ID)
		m.removeOrderEntryLocked(item.ID)
		evict = append(evict[:i], evict[i+1:]...)
	}

	// Recalculate totals after expired-item cleanup (in case pinned items
	// with the same IDs were removed, though that should not happen).
	totals = m.totalsLocked()

	// Phase 2: if still over safe boundary, evict oldest non-pinned Near items
	// by insertion order.
	nearIndex := 0
	for nearIndex < len(evict) && totals.UsedTokens > safeBoundary {
		item := evict[nearIndex]
		totals.UsedTokens -= item.TokenEstimate
		result.Evicted = append(result.Evicted, item.ID)
		delete(m.items, item.ID)
		m.removeOrderEntryLocked(item.ID)
		nearIndex++
	}

	if risk == PollutionOverWindow {
		pct := 0
		if m.windowTokens > 0 {
			pct = (totals.UsedTokens) * 100 / m.windowTokens
		}
		result.Warning = formatBudgetWarning(pct, len(result.Evicted))
	} else if risk == PollutionWarning {
		pct := 0
		if m.windowTokens > 0 {
			pct = (totals.UsedTokens) * 100 / m.windowTokens
		}
		result.Warning = formatBudgetWarning(pct, len(result.Evicted))
	}

	return result
}

// collectNearEvictionCandidatesLocked returns non-pinned Near items in insertion
// order. The caller must hold m.mu.
func (m *Manager) collectNearEvictionCandidatesLocked() []core.ContextItem {
	now := m.now()
	candidates := make([]core.ContextItem, 0)
	for _, id := range m.order {
		item, ok := m.items[id]
		if !ok || item.Pinned || item.Tier != core.TierNear {
			continue
		}
		// Skip items that are already expired (they would be dropped naturally
		// from the snapshot, but we also evict them explicitly in phase 1).
		_ = now // passed for clarity; expired check done in caller
		candidates = append(candidates, item)
	}
	return candidates
}

func (m *Manager) removeOrderEntryLocked(id string) {
	for i, entry := range m.order {
		if entry == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
}

func formatBudgetWarning(pctUsed int, evicted int) string {
	if evicted == 0 {
		return ""
	}
	if pctUsed >= 100 {
		return fmt.Sprintf("Near tier at %d%% - evicted %d item(s); consider pinning critical items", pctUsed, evicted)
	}
	return fmt.Sprintf("Near tier at %d%% - evicted %d item(s); consider pinning critical items", pctUsed, evicted)
}
