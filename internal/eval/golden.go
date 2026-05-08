package eval

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mimo-tui/internal/core"
	"mimo-tui/internal/replay"
)

// GoldenDir is the directory where golden session files are stored,
// relative to the workspace root.
const GoldenDir = ".mimo/golden"

// GoldenSession describes a golden session stored on disk.
type GoldenSession struct {
	SessionID   string
	Description string
	CreatedAt   time.Time
}

// MarkGolden copies the session file for sessionID into
// .mimo/golden/<id>.jsonl and records the description.
func MarkGolden(workspace, sessionID, description string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}

	srcPath, err := replay.SessionPath(workspace, sessionID)
	if err != nil {
		return fmt.Errorf("resolve session path: %w", err)
	}

	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("session file not found at %s: %w", srcPath, err)
	}

	dstDir := filepath.Join(workspace, GoldenDir)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create golden dir: %w", err)
	}

	dstName := filepath.Base(sessionID)
	if !strings.HasSuffix(dstName, ".jsonl") {
		dstName += ".jsonl"
	}
	dstPath := filepath.Join(dstDir, dstName)

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source session: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create golden file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy session data: %w", err)
	}

	// Write a minimal metadata sidecar so we can reconstruct GoldenSession
	// without storing a separate registry.
	metaPath := dstPath + ".meta"
	meta := fmt.Sprintf("description=%s\ncreated_at=%s\n", description, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(metaPath, []byte(meta), 0o644); err != nil {
		return fmt.Errorf("write golden metadata: %w", err)
	}

	return nil
}

// ListGolden returns all golden sessions found under .mimo/golden in the
// workspace, ordered by creation time descending (newest first).
func ListGolden(workspace string) ([]GoldenSession, error) {
	dir := filepath.Join(workspace, GoldenDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []GoldenSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		gs := GoldenSession{SessionID: id}
		gs.CreatedAt = entryModTime(entry)

		// Try to read sidecar metadata.
		metaPath := filepath.Join(dir, entry.Name()+".meta")
		if metaData, err := os.ReadFile(metaPath); err == nil {
			gs.Description = parseMetaField(string(metaData), "description")
			if t, err := time.Parse(time.RFC3339, parseMetaField(string(metaData), "created_at")); err == nil {
				gs.CreatedAt = t
			}
		}

		sessions = append(sessions, gs)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
	return sessions, nil
}

// LoadGolden reads the events for a golden session from .mimo/golden/<id>.jsonl.
func LoadGolden(workspace, sessionID string) ([]core.AgentEvent, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}

	dstName := filepath.Base(sessionID)
	if !strings.HasSuffix(dstName, ".jsonl") {
		dstName += ".jsonl"
	}
	path := filepath.Join(workspace, GoldenDir, dstName)
	return replay.ReadFile(path)
}

// entryModTime returns the modification time from a DirEntry, falling back
// to the zero time on error.
func entryModTime(entry os.DirEntry) time.Time {
	info, err := entry.Info()
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// parseMetaField extracts the value for a key from simple key=value metadata.
func parseMetaField(data, key string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	return ""
}
