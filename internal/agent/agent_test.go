package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	contextmap "mimo-tui/internal/context"
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
	if !strings.Contains(client.req.Messages[0].Content, "Tools are available and will be executed") {
		t.Fatalf("system prompt does not enable tool execution: %q", client.req.Messages[0].Content)
	}
	if !strings.Contains(client.req.Messages[0].Content, "Keep raw tool output out of context") {
		t.Fatalf("system prompt does not protect context from raw tool output: %q", client.req.Messages[0].Content)
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

func TestRunOncePublishesToolCallEvents(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(20)
	client := &fakeClient{
		events: []core.ModelEvent{
			{
				ToolCalls: []core.ToolCall{
					{
						ID:    "call_1",
						Name:  "read_file",
						Input: core.ToolInput{"path": "README.md"},
						Raw:   `{"path":"README.md"}`,
					},
				},
			},
			{Done: true},
		},
	}

	if err := RunOnce(context.Background(), "inspect README", client, bus); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	events := drainBus(sub)
	toolStart := findToolStart(events)
	if toolStart == nil {
		t.Fatalf("missing tool start event: %#v", events)
	}
	if toolStart.ToolName != "read_file" || toolStart.ToolCall == nil || toolStart.ToolCall.ID != "call_1" {
		t.Fatalf("tool start event = %#v, want read_file call_1", toolStart)
	}
	if toolStart.ToolCall.Input["path"] != "README.md" {
		t.Fatalf("tool call input = %#v, want README path", toolStart.ToolCall.Input)
	}
	if !hasToolTraceUpdate(events, "read_file") {
		t.Fatalf("missing visible tool-call trace update: %#v", events)
	}
	if hasEvent(events, core.EventToolResult) {
		t.Fatalf("agent published tool result without executor: %#v", events)
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
	streamFn func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error)
}

func (f *fakeClient) ChatStream(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
	f.req = req
	if f.streamFn != nil {
		return f.streamFn(ctx, req)
	}
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

func findToolStart(events []core.AgentEvent) *core.AgentEvent {
	for i := range events {
		if events[i].Type == core.EventToolStart {
			return &events[i]
		}
	}
	return nil
}

func hasToolTraceUpdate(events []core.AgentEvent, name string) bool {
	for _, event := range events {
		if event.Type != core.EventTraceUpdate || event.Trace == nil {
			continue
		}
		if event.Trace.Action == "tool_call.requested" &&
			strings.Contains(event.Trace.Observation, name) &&
			strings.Contains(event.Trace.Risk, "no tool execution") {
			return true
		}
	}
	return false
}

func hasObservationContaining(events []core.AgentEvent, text string) bool {
	for _, event := range events {
		if event.Type != core.EventObservation || event.Observation == nil {
			continue
		}
		if strings.Contains(event.Observation.Summary, text) {
			return true
		}
	}
	return false
}

// ---- Loop tests ----

type fakeExecutor struct {
	results []struct {
		result      core.ToolResult
		observation core.Observation
	}
	executed []core.ToolCall
}

func (e *fakeExecutor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, core.Observation) {
	e.executed = append(e.executed, call)
	if len(e.results) == 0 {
		return core.ToolResult{Content: "done", ArtifactID: "art-default"}, core.Observation{Summary: "default", ContextPlacement: core.TierNear}
	}
	r := e.results[0]
	e.results = e.results[1:]
	return r.result, r.observation
}

type fakeContextManager struct {
	items    []core.ContextItem
	upserted []core.ContextItem
	admitted []core.ContextItem
	admitErr error
}

func (m *fakeContextManager) Add(item core.ContextItem) (core.ContextSnapshot, error) {
	m.items = append(m.items, item)
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
}

func (m *fakeContextManager) Update(item core.ContextItem) (core.ContextSnapshot, error) {
	for i, existing := range m.items {
		if existing.ID == item.ID {
			m.items[i] = item
			return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
		}
	}
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, contextmap.ErrItemNotFound
}

func (m *fakeContextManager) Upsert(item core.ContextItem) (core.ContextSnapshot, error) {
	m.upserted = append(m.upserted, item)
	for i, existing := range m.items {
		if existing.ID == item.ID {
			m.items[i] = item
			return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
		}
	}
	m.items = append(m.items, item)
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
}

func (m *fakeContextManager) Remove(id string) (core.ContextSnapshot, error) {
	for i, item := range m.items {
		if item.ID == id {
			m.items = append(m.items[:i], m.items[i+1:]...)
			return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
		}
	}
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, contextmap.ErrItemNotFound
}

func (m *fakeContextManager) Pin(id string) (core.ContextSnapshot, error) {
	for i, item := range m.items {
		if item.ID == id {
			m.items[i].Pinned = true
			return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
		}
	}
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, contextmap.ErrItemNotFound
}

func (m *fakeContextManager) Unpin(id string) (core.ContextSnapshot, error) {
	for i, item := range m.items {
		if item.ID == id {
			m.items[i].Pinned = false
			return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, nil
		}
	}
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}, contextmap.ErrItemNotFound
}

func (m *fakeContextManager) Snapshot() core.ContextSnapshot {
	return core.ContextSnapshot{WindowTokens: 1000000, Items: m.items}
}

func (m *fakeContextManager) AutoBudget() contextmap.AutoBudgetResult {
	// By default, no evictions in tests.
	return contextmap.AutoBudgetResult{}
}

func (m *fakeContextManager) Admit(item core.ContextItem) (core.ContextSnapshot, error) {
	m.admitted = append(m.admitted, item)
	if m.admitErr != nil {
		return m.Snapshot(), m.admitErr
	}
	return m.Upsert(item)
}

func TestLoopSingleStep(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(50)
	client := &fakeClient{
		events: []core.ModelEvent{
			{Delta: "here is your answer"},
			{Done: true},
		},
	}
	executor := &fakeExecutor{}
	ctxMgr := &fakeContextManager{}
	config := DefaultLoopConfig()

	_, err := Loop(context.Background(), "hello", client, executor, ctxMgr, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if client.req.Messages[0].Role != "system" {
		t.Fatalf("system message not set")
	}
	if client.req.Messages[1].Role != "user" || client.req.Messages[1].Content != "hello" {
		t.Fatalf("user message = %#v", client.req.Messages[1])
	}
	events := drainBus(sub)
	if !hasEvent(events, core.EventDone) {
		t.Fatal("missing done event")
	}
	if len(executor.executed) != 0 {
		t.Fatalf("executor was called %d times, want 0", len(executor.executed))
	}
	if got := joinedDeltas(events); got != "here is your answer" {
		t.Fatalf("message deltas = %q, want 'here is your answer'", got)
	}
}

func TestLoopMultiStep(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(50)

	// Step 1: model requests a tool call, then we feed back a tool result.
	// Step 2: model gives final answer.
	callCount := 0
	client := &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			callCount++
			ch := make(chan core.ModelEvent, 4)
			if callCount == 1 {
				ch <- core.ModelEvent{
					Delta: "let me check that",
					ToolCalls: []core.ToolCall{
						{ID: "call_1", Name: "read_file", Input: core.ToolInput{"path": "README.md"}},
					},
				}
				ch <- core.ModelEvent{Done: true}
			} else {
				ch <- core.ModelEvent{Delta: "the file says hello"}
				ch <- core.ModelEvent{Done: true}
			}
			close(ch)
			return ch, nil
		},
	}

	executor := &fakeExecutor{
		results: []struct {
			result      core.ToolResult
			observation core.Observation
		}{
			{core.ToolResult{Content: "# README\nhello", ArtifactID: "art-1"}, core.Observation{Summary: "read README.md", ContextPlacement: core.TierArtifact}},
		},
	}
	ctxMgr := &fakeContextManager{}
	config := DefaultLoopConfig()

	_, err := Loop(context.Background(), "whats in README", client, executor, ctxMgr, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("ChatStream called %d times, want 2", callCount)
	}
	if len(executor.executed) != 1 {
		t.Fatalf("executor called %d times, want 1", len(executor.executed))
	}
	if executor.executed[0].Name != "read_file" {
		t.Fatalf("executed tool = %q, want read_file", executor.executed[0].Name)
	}
	if len(ctxMgr.upserted) != 1 {
		t.Fatalf("context upserted %d times, want 1", len(ctxMgr.upserted))
	}
	if len(ctxMgr.admitted) != 1 {
		t.Fatalf("context admitted %d times, want 1", len(ctxMgr.admitted))
	}

	events := drainBus(sub)
	if !hasEvent(events, core.EventDone) {
		t.Fatal("missing done event")
	}
	if !hasEvent(events, core.EventToolResult) {
		t.Fatal("missing tool result event")
	}
}

func TestLoopMaxSteps(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(50)

	// Always return a tool call so the loop never terminates naturally.
	client := &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			ch := make(chan core.ModelEvent, 2)
			ch <- core.ModelEvent{
				ToolCalls: []core.ToolCall{
					{ID: "loop_call", Name: "list_dir", Input: core.ToolInput{}},
				},
			}
			ch <- core.ModelEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}

	executor := &fakeExecutor{}
	ctxMgr := &fakeContextManager{}
	config := LoopConfig{MaxSteps: 3, StepTimeout: 10 * time.Second, TotalTimeout: 30 * time.Second}

	_, err := Loop(context.Background(), "loop", client, executor, ctxMgr, nil, bus, config, nil)
	if err == nil || !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("Loop error = %v, want max steps error", err)
	}
	if len(executor.executed) != 3 {
		t.Fatalf("executor called %d times, want 3", len(executor.executed))
	}
	events := drainBus(sub)
	if !hasEvent(events, core.EventError) {
		t.Fatal("missing error event")
	}
}

func TestLoopPublishesContextAdmissionRejection(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(80)

	callCount := 0
	client := &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			callCount++
			ch := make(chan core.ModelEvent, 2)
			if callCount == 1 {
				ch <- core.ModelEvent{
					ToolCalls: []core.ToolCall{
						{ID: "call_1", Name: "read_file", Input: core.ToolInput{"path": "README.md"}},
					},
				}
				ch <- core.ModelEvent{Done: true}
			} else {
				ch <- core.ModelEvent{Delta: "done"}
				ch <- core.ModelEvent{Done: true}
			}
			close(ch)
			return ch, nil
		},
	}
	executor := &fakeExecutor{
		results: []struct {
			result      core.ToolResult
			observation core.Observation
		}{
			{core.ToolResult{Content: "content", ArtifactID: "art-1"}, core.Observation{Summary: "read README.md", ContextPlacement: core.TierNear}},
		},
	}
	ctxMgr := &fakeContextManager{admitErr: contextmap.ErrAdmitOverWindow}

	_, err := Loop(context.Background(), "read", client, executor, ctxMgr, nil, bus, DefaultLoopConfig(), nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if len(ctxMgr.admitted) != 1 {
		t.Fatalf("context admitted %d times, want 1", len(ctxMgr.admitted))
	}
	if len(ctxMgr.upserted) != 0 {
		t.Fatalf("context upserted %d times after admission rejection, want 0", len(ctxMgr.upserted))
	}

	events := drainBus(sub)
	if !hasObservationContaining(events, "context admission rejected") {
		t.Fatalf("missing admission rejection observation: %#v", events)
	}
}

func TestLoopToolError(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(50)

	callCount := 0
	client := &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			callCount++
			ch := make(chan core.ModelEvent, 2)
			if callCount == 1 {
				ch <- core.ModelEvent{
					ToolCalls: []core.ToolCall{
						{ID: "bad_call", Name: "shell", Input: core.ToolInput{"command": "rm -rf /"}},
					},
				}
				ch <- core.ModelEvent{Done: true}
			} else {
				ch <- core.ModelEvent{Delta: "I cannot run that command"}
				ch <- core.ModelEvent{Done: true}
			}
			close(ch)
			return ch, nil
		},
	}

	executor := &fakeExecutor{
		results: []struct {
			result      core.ToolResult
			observation core.Observation
		}{
			{core.ToolResult{Content: "denied", ExitCode: 126, Error: "tool requires explicit permission"}, core.Observation{Summary: "shell denied", ContextPlacement: core.TierNear}},
		},
	}
	ctxMgr := &fakeContextManager{}
	config := DefaultLoopConfig()

	_, err := Loop(context.Background(), "run bad cmd", client, executor, ctxMgr, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("Loop returned unexpected error: %v", err)
	}
	// The tool "error" should be fed back to the model, and the loop should continue.
	if callCount != 2 {
		t.Fatalf("ChatStream called %d times, want 2", callCount)
	}
	events := drainBus(sub)
	if !hasEvent(events, core.EventDone) {
		t.Fatal("missing done event")
	}
}

func TestLoopContextSummaryInjected(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(20)
	client := &fakeClient{
		events: []core.ModelEvent{
			{Delta: "ok"},
			{Done: true},
		},
	}
	ctxMgr := &fakeContextManager{
		items: []core.ContextItem{
			{ID: "anchor:project-map", Tier: core.TierAnchor, Title: "Project map", Pinned: true, TokenEstimate: 100},
			{ID: "near:status", Tier: core.TierNear, Title: "git status output", TokenEstimate: 50},
		},
	}
	executor := &fakeExecutor{}
	config := DefaultLoopConfig()

	_, _ = Loop(context.Background(), "test", client, executor, ctxMgr, nil, bus, config, nil)
	drainBus(sub)

	sysContent := client.req.Messages[0].Content
	if !strings.Contains(sysContent, "[Context Map Summary]") {
		t.Fatalf("system prompt missing context summary: %q", sysContent)
	}
	if !strings.Contains(sysContent, "Anchor: Project map (pinned)") {
		t.Fatalf("context summary missing anchor: %q", sysContent)
	}
	if !strings.Contains(sysContent, "Near: git status output") {
		t.Fatalf("context summary missing near: %q", sysContent)
	}
}

func TestLoopHistoryPropagation(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(50)

	// Step 1: first call returns some messages.
	callCount := 0
	client := &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			callCount++
			ch := make(chan core.ModelEvent, 2)
			ch <- core.ModelEvent{Delta: fmt.Sprintf("turn-%d", callCount)}
			ch <- core.ModelEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}
	executor := &fakeExecutor{}
	ctxMgr := &fakeContextManager{}
	config := DefaultLoopConfig()

	// First turn: no history.
	history, err := Loop(context.Background(), "first", client, executor, ctxMgr, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("first Loop returned error: %v", err)
	}
	drainBus(sub)

	// Verify the returned messages include system + user + assistant.
	if len(history) < 3 {
		t.Fatalf("history after first turn = %d messages, want 3", len(history))
	}
	if history[0].Role != "system" {
		t.Fatalf("first message role = %q, want system", history[0].Role)
	}
	if history[len(history)-1].Role != "assistant" {
		t.Fatalf("last message role = %q, want assistant", history[len(history)-1].Role)
	}

	// Second turn: pass history from first turn.
	history2, err := Loop(context.Background(), "second", client, executor, ctxMgr, nil, bus, config, history)
	if err != nil {
		t.Fatalf("second Loop returned error: %v", err)
	}
	drainBus(sub)

	// Should have grown: original messages + new user + new assistant.
	if len(history2) <= len(history) {
		t.Fatalf("history2 length = %d, want > %d", len(history2), len(history))
	}

	// The first message should still be the system prompt.
	if history2[0].Role != "system" || !strings.Contains(history2[0].Content, "MiMo inside") {
		t.Fatalf("system message lost after second turn: %#v", history2[0])
	}

	// The system prompt in the second request should contain context summary.
	sysContent := client.req.Messages[0].Content
	if !strings.Contains(sysContent, "[Context Map Summary]") {
		t.Fatalf("second turn system prompt missing context summary: %q", sysContent)
	}
}
