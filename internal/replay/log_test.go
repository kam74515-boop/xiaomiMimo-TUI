package replay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

func TestWriterReaderUsesSessionsJSONL(t *testing.T) {
	workspace := t.TempDir()
	writer, err := NewWriter(workspace, "session-1")
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	events := []core.AgentEvent{
		{Type: core.EventMessageDelta, Time: time.Unix(1, 0), Message: "hello"},
		{Type: core.EventDone, Time: time.Unix(2, 0), Message: "done"},
	}
	for _, event := range events {
		if err := writer.Write(event); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	wantPath := filepath.Join(workspace, SessionsDir, "session-1.jsonl")
	if writer.Path() != wantPath {
		t.Fatalf("path = %q, want %q", writer.Path(), wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("stat log: %v", err)
	}

	got, err := Read(workspace, "session-1")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("events = %d, want %d", len(got), len(events))
	}
	for i := range events {
		if got[i].Type != events[i].Type || got[i].Message != events[i].Message || !got[i].Time.Equal(events[i].Time) {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], events[i])
		}
	}
}

func TestWriteFillsMissingTime(t *testing.T) {
	workspace := t.TempDir()
	writer, err := NewWriter(workspace, "session-1")
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if err := writer.Write(core.AgentEvent{Type: core.EventDone}); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	events, err := Read(workspace, "session-1")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 1 || events[0].Time.IsZero() {
		t.Fatalf("expected non-zero event time, got %+v", events)
	}
}

func TestListSessionsOrdersNewestUsableByEventTime(t *testing.T) {
	workspace := t.TempDir()
	oldTime := time.Unix(10, 0)
	newTime := time.Unix(20, 0)

	writeSessionFixture(t, workspace, "old", core.AgentEvent{Type: core.EventMessageDelta, Time: oldTime})
	writeSessionFixture(t, workspace, "new", core.AgentEvent{Type: core.EventDone, Time: newTime})
	if err := os.WriteFile(filepath.Join(workspace, SessionsDir, "ignore.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write ignored fixture: %v", err)
	}

	sessions, err := ListSessions(workspace)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2: %+v", len(sessions), sessions)
	}
	if sessions[0].ID != "new" || sessions[1].ID != "old" {
		t.Fatalf("session order = %q, %q; want new, old", sessions[0].ID, sessions[1].ID)
	}
	if sessions[0].EventCount != 1 || sessions[0].LastEventType != core.EventDone || !sessions[0].LastEventAt.Equal(newTime) {
		t.Fatalf("new session metadata = %+v", sessions[0])
	}

	latest, err := LatestSession(workspace)
	if err != nil {
		t.Fatalf("latest session: %v", err)
	}
	if latest.ID != "new" {
		t.Fatalf("latest session = %q, want new", latest.ID)
	}
}

func TestLatestSessionSkipsCorruptAndEmptyLogs(t *testing.T) {
	workspace := t.TempDir()
	writeSessionFixture(t, workspace, "usable", core.AgentEvent{Type: core.EventDone, Time: time.Unix(5, 0)})
	writeSessionFixture(t, workspace, "empty")
	corruptPath := filepath.Join(workspace, SessionsDir, "corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("not-json\n"), 0o644); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	sessions, err := ListSessions(workspace)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	seenCorrupt := false
	seenEmpty := false
	for _, session := range sessions {
		switch session.ID {
		case "corrupt":
			seenCorrupt = session.Err != nil
		case "empty":
			seenEmpty = session.Err == nil && session.EventCount == 0
		}
	}
	if !seenCorrupt {
		t.Fatalf("expected corrupt session to be listed with an error: %+v", sessions)
	}
	if !seenEmpty {
		t.Fatalf("expected empty session to be listed without events: %+v", sessions)
	}

	latest, err := LatestSession(workspace)
	if err != nil {
		t.Fatalf("latest session: %v", err)
	}
	if latest.ID != "usable" {
		t.Fatalf("latest session = %q, want usable", latest.ID)
	}

	workspace = t.TempDir()
	writeSessionFixture(t, workspace, "empty")
	corruptPath = filepath.Join(workspace, SessionsDir, "corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("not-json\n"), 0o644); err != nil {
		t.Fatalf("write corrupt-only fixture: %v", err)
	}
	if _, err := LatestSession(workspace); !errors.Is(err, ErrNoSessions) {
		t.Fatalf("latest empty/corrupt error = %v, want %v", err, ErrNoSessions)
	}
}

func TestReplaySendsEventsToChannel(t *testing.T) {
	events := []core.AgentEvent{
		{Type: core.EventObservation, Message: "one"},
		{Type: core.EventDone, Message: "two"},
	}
	out := make(chan core.AgentEvent, len(events))

	if err := Replay(context.Background(), events, out); err != nil {
		t.Fatalf("replay: %v", err)
	}
	for i, want := range events {
		got := <-out
		if got.Type != want.Type || got.Message != want.Message {
			t.Fatalf("event %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestDecodeReportsLineNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"done\"}\nnot-json\n"), 0o644); err != nil {
		t.Fatalf("write bad fixture: %v", err)
	}
	if _, err := ReadFile(path); err == nil {
		t.Fatal("expected decode error")
	}
}

func writeSessionFixture(t *testing.T, workspace, sessionID string, events ...core.AgentEvent) string {
	t.Helper()
	path, err := SessionPath(workspace, sessionID)
	if err != nil {
		t.Fatalf("session path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open session fixture: %v", err)
	}
	defer file.Close()
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event fixture: %v", err)
		}
		if _, err := file.Write(append(raw, '\n')); err != nil {
			t.Fatalf("write event fixture: %v", err)
		}
	}
	return path
}
