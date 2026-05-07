package replay

import (
	"context"
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
