package context

import (
	"fmt"
	"strings"
	"time"

	"mimo-tui/internal/core"
)

// CompressItems merges multiple low-activity context items into a single
// compressed summary artifact. Source items have their ReplacedBy field set
// to the new artifact ID and are marked as expired so they drop from active
// snapshots (but remain in the map for lineage tracking).
//
// Returns the compression record and the updated snapshot.
func (m *Manager) CompressItems(ids []string, summary string, reason string) (core.CompressionRecord, core.ContextSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(ids) == 0 {
		return core.CompressionRecord{}, m.snapshotLocked(), fmt.Errorf("compress requires at least one source item")
	}

	// Validate that all source items exist and collect token totals.
	tokensBefore := 0
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		item, ok := m.items[id]
		if !ok {
			continue
		}
		tokensBefore += item.TokenEstimate
		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		return core.CompressionRecord{}, m.snapshotLocked(), fmt.Errorf("none of the requested items exist")
	}

	// Build a short title from the summary.
	title := summary
	if len(title) > 80 {
		title = title[:80] + "..."
	}

	// Generate compression artifact ID.
	compressionID := fmt.Sprintf("compressed:%d", time.Now().UnixNano())

	tokensAfter := core.EstimateTokens(summary)

	// Create the compressed item (Near tier by default).
	compressedItem := core.ContextItem{
		ID:            compressionID,
		Tier:          core.TierNear,
		Title:         title,
		Source:        "compression",
		TokenEstimate: tokensAfter,
		Reason:        fmt.Sprintf("compressed from %d items: %s", len(validIDs), reason),
	}

	// Mark all source items as replaced and expired.
	past := time.Now().Add(-24 * time.Hour)
	for _, id := range validIDs {
		item := m.items[id]
		item.ReplacedBy = compressionID
		item.ExpiresAt = past
		m.items[id] = item
	}

	// Add the compressed item.
	m.items[compressionID] = compressedItem
	m.order = append(m.order, compressionID)

	// Store compression record.
	record := core.CompressionRecord{
		ID:           compressionID,
		SourceIDs:    validIDs,
		Summary:      summary,
		Reason:       reason,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}
	m.compressionRecords[compressionID] = record

	return record, m.snapshotLocked(), nil
}

// CompressionRecord returns the compression record for a given artifact ID,
// or nil if no such record exists.
func (m *Manager) CompressionRecord(id string) *core.CompressionRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if record, ok := m.compressionRecords[id]; ok {
		return &record
	}
	return nil
}

// ActiveCompressionRecords returns all compression records whose artifact
// is still present in the context map.
func (m *Manager) ActiveCompressionRecords() []core.CompressionRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	records := make([]core.CompressionRecord, 0, len(m.compressionRecords))
	for id, record := range m.compressionRecords {
		if _, ok := m.items[id]; ok {
			records = append(records, record)
		}
	}
	return records
}

// compressionSummary builds a human-readable summary from item titles.
func compressionSummary(items []core.ContextItem) string {
	titles := make([]string, len(items))
	for i, item := range items {
		titles[i] = fallback(item.Title, item.ID)
	}
	return "Compressed: " + strings.Join(titles, "; ")
}

func fallback(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
