package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mimo-tui/internal/core"
)

const SessionsDir = ".mimo/sessions"

var (
	ErrInvalidSessionID = errors.New("session id is required")
	ErrNoSessions       = errors.New("no usable replay sessions")
	ErrWriterClosed     = errors.New("replay writer is closed")
)

type Writer struct {
	mu   sync.Mutex
	file *os.File
	path string
}

type SessionInfo struct {
	ID            string
	Path          string
	Size          int64
	ModTime       time.Time
	EventCount    int
	StartedAt     time.Time
	LastEventAt   time.Time
	LastEventType core.EventType
	Err           error
}

func SessionPath(workspace, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", ErrInvalidSessionID
	}
	if workspace == "" {
		workspace = "."
	}
	name := filepath.Base(sessionID)
	if !strings.HasSuffix(name, ".jsonl") {
		name += ".jsonl"
	}
	return filepath.Join(workspace, SessionsDir, name), nil
}

func ListSessions(workspace string) ([]SessionInfo, error) {
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, SessionsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	sessions := make([]SessionInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fileInfo, err := entry.Info()
		session := SessionInfo{
			ID:   strings.TrimSuffix(entry.Name(), ".jsonl"),
			Path: path,
		}
		if err != nil {
			session.Err = err
			sessions = append(sessions, session)
			continue
		}
		session.Size = fileInfo.Size()
		session.ModTime = fileInfo.ModTime()
		events, err := ReadFile(path)
		if err != nil {
			session.Err = err
			sessions = append(sessions, session)
			continue
		}
		session.EventCount = len(events)
		for _, event := range events {
			if session.StartedAt.IsZero() && !event.Time.IsZero() {
				session.StartedAt = event.Time
			}
			if !event.Time.IsZero() {
				session.LastEventAt = event.Time
			}
			session.LastEventType = event.Type
		}
		sessions = append(sessions, session)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		left := sessions[i].activityTime()
		right := sessions[j].activityTime()
		if !left.Equal(right) {
			return left.After(right)
		}
		if !sessions[i].ModTime.Equal(sessions[j].ModTime) {
			return sessions[i].ModTime.After(sessions[j].ModTime)
		}
		return sessions[i].ID > sessions[j].ID
	})
	return sessions, nil
}

func LatestSession(workspace string) (SessionInfo, error) {
	sessions, err := ListSessions(workspace)
	if err != nil {
		return SessionInfo{}, err
	}
	for _, session := range sessions {
		if session.Err == nil && session.EventCount > 0 {
			return session, nil
		}
	}
	return SessionInfo{}, ErrNoSessions
}

func NewWriter(workspace, sessionID string) (*Writer, error) {
	path, err := SessionPath(workspace, sessionID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{file: file, path: path}, nil
}

func (w *Writer) Path() string {
	return w.path
}

func (w *Writer) Write(event core.AgentEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return ErrWriterClosed
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := w.file.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func Read(workspace, sessionID string) ([]core.AgentEvent, error) {
	path, err := SessionPath(workspace, sessionID)
	if err != nil {
		return nil, err
	}
	return ReadFile(path)
}

func ReadFile(path string) ([]core.AgentEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return Decode(file)
}

func Decode(r io.Reader) ([]core.AgentEvent, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var events []core.AgentEvent
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var event core.AgentEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("decode event line %d: %w", line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func Replay(ctx context.Context, events []core.AgentEvent, out chan<- core.AgentEvent) error {
	for _, event := range events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- event:
		}
	}
	return nil
}

func ReplayFile(ctx context.Context, path string, out chan<- core.AgentEvent) error {
	events, err := ReadFile(path)
	if err != nil {
		return err
	}
	return Replay(ctx, events, out)
}

func (s SessionInfo) activityTime() time.Time {
	if !s.LastEventAt.IsZero() {
		return s.LastEventAt
	}
	return s.ModTime
}
