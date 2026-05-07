package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mimo-tui/internal/core"
)

func TestRunOncePublishesModelEvents(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(20)
	client := &fakeClient{
		events: []core.ModelEvent{
			{Delta: "hel"},
			{Delta: "lo"},
			{Usage: &core.CostUpdate{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
			{Done: true},
		},
	}

	if err := RunOnce(context.Background(), "hello", client, bus); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !client.req.Stream {
		t.Fatal("agent did not request a stream")
	}
	if len(client.req.Messages) != 2 {
		t.Fatalf("messages = %d, want system and user", len(client.req.Messages))
	}
	if client.req.Messages[0].Role != "system" || !strings.Contains(client.req.Messages[0].Content, "Critical thinking policy") {
		t.Fatalf("system prompt = %#v", client.req.Messages[0])
	}
	if client.req.Messages[1].Role != "user" || client.req.Messages[1].Content != "hello" {
		t.Fatalf("user prompt = %#v", client.req.Messages[1])
	}

	events := drainBus(sub)
	if got := joinedDeltas(events); got != "hello" {
		t.Fatalf("message deltas = %q, want hello", got)
	}
	if !hasEvent(events, core.EventCostUpdate) {
		t.Fatal("missing cost update")
	}
	if !hasEvent(events, core.EventDone) {
		t.Fatal("missing done event")
	}
	if !hasTraceStatus(events, core.TraceRunning) || !hasTraceStatus(events, core.TraceDone) {
		t.Fatalf("trace statuses missing from events: %#v", events)
	}
}

func TestRunOncePublishesErrorEvents(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(20)
	client := &fakeClient{
		events: []core.ModelEvent{{Err: errors.New("boom")}},
	}

	err := RunOnce(context.Background(), "hello", client, bus)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("RunOnce error = %v, want boom", err)
	}

	events := drainBus(sub)
	if !hasEvent(events, core.EventError) {
		t.Fatal("missing error event")
	}
	if !hasEvent(events, core.EventDone) {
		t.Fatal("missing done event")
	}
	if !hasTraceStatus(events, core.TraceFailed) {
		t.Fatalf("missing failed trace status: %#v", events)
	}
}

type fakeClient struct {
	req      core.ChatRequest
	startErr error
	events   []core.ModelEvent
}

func (f *fakeClient) ChatStream(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
	f.req = req
	if f.startErr != nil {
		return nil, f.startErr
	}
	ch := make(chan core.ModelEvent, len(f.events))
	for _, event := range f.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func drainBus(ch <-chan core.AgentEvent) []core.AgentEvent {
	var events []core.AgentEvent
	for {
		select {
		case event := <-ch:
			events = append(events, event)
		default:
			return events
		}
	}
}

func joinedDeltas(events []core.AgentEvent) string {
	var b strings.Builder
	for _, event := range events {
		if event.Type == core.EventMessageDelta {
			b.WriteString(event.Message)
		}
	}
	return b.String()
}

func hasEvent(events []core.AgentEvent, eventType core.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func hasTraceStatus(events []core.AgentEvent, status core.TraceStepStatus) bool {
	for _, event := range events {
		if event.Type == core.EventTraceUpdate && event.Trace != nil && event.Trace.Status == status {
			return true
		}
	}
	return false
}
