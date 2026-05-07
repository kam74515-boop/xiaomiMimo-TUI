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
	"strings"
	"sync"
	"time"

	"mimo-tui/internal/core"
)

const SessionsDir = ".mimo/sessions"

var (
	ErrInvalidSessionID = errors.New("session id is required")
	ErrWriterClosed     = errors.New("replay writer is closed")
)

type Writer struct {
	mu   sync.Mutex
	file *os.File
	path string
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
