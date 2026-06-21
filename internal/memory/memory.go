// Package memory implements MiMo-TUI's persistent, cross-session memory.
//
// Memory is plain Markdown so it stays human-readable, reviewable, and
// version-controllable — consistent with the project's visibility philosophy.
// Two scopes are supported:
//
//   - project memory: <workspace>/.mimo/MEMORY.md (committed with the repo)
//   - global memory:  ~/.mimo-tui/MEMORY.md       (shared across workspaces)
//
// At session start the runtime loads the combined memory and injects it into
// the system prompt (so the model reads it every turn) and into the Context Map
// as a pinned Anchor item (so the user can see what is loaded). The agent can
// also record new facts during a run via the `memory` tool; those are picked up
// on the next turn.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mimo-tui/internal/core"
)

// fileMu serializes memory file access within this process. Both the agent's
// `memory` tool and the runtime's /memory handler append to the same MEMORY.md
// via distinct Store instances, so a per-Store lock is insufficient. Appends
// additionally use O_APPEND so concurrent mimo processes on the same repo do not
// clobber each other's bullets (the file lock alone is intra-process only).
var fileMu sync.RWMutex

// Scope selects which memory file an operation targets.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"

	// MemoryAnchorID is the stable Context Map item id used for the injected
	// memory anchor, so refreshes upsert rather than duplicate.
	MemoryAnchorID = "anchor:memory"

	header = "# MiMo-TUI Memory\n\nPersistent facts recorded across sessions. One bullet per fact.\n"

	maxContextSource = 4000

	// MaxModelMemoryBytes bounds how much memory is injected into the model
	// (system prompt every turn, and the `memory show` result) so an
	// ever-growing MEMORY.md cannot silently inflate cost and context use.
	MaxModelMemoryBytes = 6000
)

// Store reads and appends persistent memory for a workspace.
type Store struct {
	workspace string
	now       func() time.Time
}

// NewStore returns a memory store rooted at workspace. An empty workspace
// defaults to the current directory.
func NewStore(workspace string) *Store {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return &Store{workspace: workspace, now: time.Now}
}

// ProjectPath is the path to the workspace-scoped memory file.
func (s *Store) ProjectPath() string {
	return filepath.Join(s.workspace, ".mimo", "MEMORY.md")
}

// GlobalPath is the path to the user-global memory file. It returns "" when the
// user home directory cannot be resolved.
func (s *Store) GlobalPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".mimo-tui", "MEMORY.md")
}

func (s *Store) pathFor(scope Scope) string {
	if scope == ScopeGlobal {
		return s.GlobalPath()
	}
	return s.ProjectPath()
}

// Load returns the combined memory content (global first, then project),
// trimmed. It returns an empty string when no memory has been recorded.
func (s *Store) Load() string {
	var sections []string
	if global := s.readFile(s.GlobalPath()); global != "" {
		sections = append(sections, "## Global\n"+global)
	}
	if project := s.readFile(s.ProjectPath()); project != "" {
		sections = append(sections, "## Project\n"+project)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

// LoadBounded is Load capped to roughly maxBytes, keeping whole leading lines
// and appending a truncation marker. Use it on model-facing paths so memory
// injection stays bounded as MEMORY.md grows.
func (s *Store) LoadBounded(maxBytes int) string {
	content := s.Load()
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}
	cut := content[:maxBytes]
	if idx := strings.LastIndexByte(cut, '\n'); idx > 0 {
		cut = cut[:idx]
	} else {
		cut = strings.ToValidUTF8(cut, "")
	}
	return cut + "\n...[memory truncated; see .mimo/MEMORY.md]"
}

// readFile returns the bullet/body content of a memory file with the standard
// header stripped, or "" if the file is missing or empty.
func (s *Store) readFile(path string) string {
	if path == "" {
		return ""
	}
	fileMu.RLock()
	data, err := os.ReadFile(path)
	fileMu.RUnlock()
	if err != nil {
		return ""
	}
	body := strings.TrimSpace(string(data))
	body = strings.TrimPrefix(body, strings.TrimSpace(header))
	return strings.TrimSpace(body)
}

// Append records a new fact as a timestamped bullet in the given scope. An
// empty scope defaults to project memory. The note is normalized to a single
// line so the file stays a clean bullet list.
func (s *Store) Append(scope Scope, note string) error {
	note = normalizeNote(note)
	if note == "" {
		return fmt.Errorf("memory: note is empty")
	}
	if scope == "" {
		scope = ScopeProject
	}
	path := s.pathFor(scope)
	if path == "" {
		return fmt.Errorf("memory: cannot resolve %s memory path", scope)
	}
	fileMu.Lock()
	defer fileMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("memory: create dir: %w", err)
	}

	// Write the header only when the file is new/empty; otherwise append just the
	// bullet. O_APPEND keeps concurrent processes from clobbering each other.
	needHeader := true
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > 0 {
		needHeader = false
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("memory: open: %w", err)
	}
	defer f.Close()

	var b strings.Builder
	if needHeader {
		b.WriteString(header)
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("- [%s] %s\n", s.now().Format("2006-01-02"), note))

	if _, err := f.WriteString(b.String()); err != nil {
		return fmt.Errorf("memory: write: %w", err)
	}
	return nil
}

// AnchorItem builds a pinned Anchor Context Map item carrying the current
// memory so it is visible in the Context Map. The second return is false when
// there is no memory to show.
func (s *Store) AnchorItem() (core.ContextItem, bool) {
	content := s.Load()
	if content == "" {
		return core.ContextItem{}, false
	}
	source := content
	if len(source) > maxContextSource {
		source = source[:maxContextSource] + "\n...[truncated; see .mimo/MEMORY.md]"
	}
	return core.ContextItem{
		ID:            MemoryAnchorID,
		Tier:          core.TierAnchor,
		Title:         "Persistent memory",
		Source:        source,
		TokenEstimate: core.EstimateTokens(source),
		Pinned:        true,
		Reason:        "cross-session memory loaded at startup; injected into the system prompt every turn",
	}, true
}

func normalizeNote(note string) string {
	note = strings.TrimSpace(note)
	if note == "" {
		return ""
	}
	// Collapse to a single line so each fact is one bullet.
	fields := strings.Fields(note)
	note = strings.Join(fields, " ")
	note = strings.TrimPrefix(note, "- ")
	return strings.TrimSpace(note)
}
