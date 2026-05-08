package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mimo-tui/internal/core"
)

func TestApplyEventUpdatesModelState(t *testing.T) {
	m := NewModel(nil, nil)

	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "hello"})
	if m.chat != "hello" {
		t.Fatalf("chat = %q, want hello", m.chat)
	}

	m.applyEvent(core.AgentEvent{
		Type: core.EventTraceUpdate,
		Trace: &core.TraceStep{
			ID:     "step-1",
			Goal:   "first goal",
			Status: core.TraceRunning,
		},
	})
	m.applyEvent(core.AgentEvent{
		Type: core.EventTraceUpdate,
		Trace: &core.TraceStep{
			ID:     "step-1",
			Goal:   "updated goal",
			Status: core.TraceDone,
		},
	})
	if len(m.trace) != 1 {
		t.Fatalf("trace length = %d, want 1", len(m.trace))
	}
	if m.trace[0].Goal != "updated goal" || m.trace[0].Status != core.TraceDone {
		t.Fatalf("trace[0] = %+v, want updated done step", m.trace[0])
	}

	m.applyEvent(core.AgentEvent{Type: core.EventObservation, Observation: &core.Observation{Summary: "evidence captured"}})
	if len(m.notes) != 1 || m.notes[0] != "evidence captured" {
		t.Fatalf("notes = %#v, want observation note", m.notes)
	}
}

func TestFocusedPanelScrollBounds(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 20
	m.focus = chatPanel
	m.chat = numberedLines(40)
	m.notes = strings.Split(numberedLines(30), "\n")

	maxChat := m.maxPanelScroll(chatPanel)
	if maxChat <= 0 {
		t.Fatalf("max chat scroll = %d, want scrollable content", maxChat)
	}

	m = updateModel(t, m, keyMsg(tea.KeyEnd))
	if m.scroll[chatPanel] != maxChat {
		t.Fatalf("chat scroll after end = %d, want %d", m.scroll[chatPanel], maxChat)
	}

	m = updateModel(t, m, keyMsg(tea.KeyDown))
	if m.scroll[chatPanel] != maxChat {
		t.Fatalf("chat scroll after down at bottom = %d, want %d", m.scroll[chatPanel], maxChat)
	}

	m = updateModel(t, m, keyMsg(tea.KeyHome))
	m = updateModel(t, m, keyMsg(tea.KeyUp))
	if m.scroll[chatPanel] != 0 {
		t.Fatalf("chat scroll after up at top = %d, want 0", m.scroll[chatPanel])
	}

	m = updateModel(t, m, keyMsg(tea.KeyDown))
	if m.scroll[chatPanel] != 1 {
		t.Fatalf("chat scroll after down = %d, want 1", m.scroll[chatPanel])
	}
	m.focus = tracePanel
	m = updateModel(t, m, keyMsg(tea.KeyDown))
	if m.scroll[tracePanel] != 1 {
		t.Fatalf("trace scroll after down = %d, want 1", m.scroll[tracePanel])
	}
	if m.scroll[chatPanel] != 1 {
		t.Fatalf("chat scroll changed while trace focused = %d, want 1", m.scroll[chatPanel])
	}

	m.chat = "short"
	m.clampAllScrolls()
	if m.scroll[chatPanel] != 0 {
		t.Fatalf("chat scroll after content shrink = %d, want 0", m.scroll[chatPanel])
	}
}

func TestHelpToggle(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.focus = chatPanel
	m.chat = numberedLines(20)

	m = updateModel(t, m, runeKey('?'))
	if !m.showHelp {
		t.Fatal("showHelp = false, want true")
	}
	if !strings.Contains(m.View(), "Controls") {
		t.Fatal("help view does not contain Controls")
	}

	m = updateModel(t, m, keyMsg(tea.KeyDown))
	if m.scroll[chatPanel] != 0 {
		t.Fatalf("scroll changed while help open = %d, want 0", m.scroll[chatPanel])
	}

	m = updateModel(t, m, keyMsg(tea.KeyEsc))
	if m.showHelp {
		t.Fatal("showHelp after esc = true, want false")
	}

	m = updateModel(t, m, runeKey('?'))
	m = updateModel(t, m, runeKey('?'))
	if m.showHelp {
		t.Fatal("showHelp after second ? = true, want false")
	}
}

func updateModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", next)
	}
	return updated
}

func keyMsg(key tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: key}
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestContextSelectionMovement(t *testing.T) {
	m := NewModel(nil, nil)
	m.hasContext = true
	m.context = core.ContextSnapshot{
		WindowTokens: 100,
		UsedTokens:   30,
		Items: []core.ContextItem{
			{ID: "near-1", Tier: core.TierNear, Title: "first", TokenEstimate: 10},
			{ID: "near-2", Tier: core.TierNear, Title: "second", TokenEstimate: 10},
			{ID: "anchor-1", Tier: core.TierAnchor, Title: "anchor", TokenEstimate: 10},
		},
	}
	m.focus = contextPanel

	// selectedContextItem starts at -1.
	if m.selectedContextItem != -1 {
		t.Fatalf("initial selection = %d, want -1", m.selectedContextItem)
	}

	// j moves to first item (wrap from -1).
	m = updateModel(t, m, runeKey('j'))
	if m.selectedContextItem != 0 {
		t.Fatalf("selection after j = %d, want 0", m.selectedContextItem)
	}

	// k wraps to last.
	m = updateModel(t, m, runeKey('k'))
	if m.selectedContextItem != 2 {
		t.Fatalf("selection after k = %d, want 2", m.selectedContextItem)
	}

	// k again -> 1.
	m = updateModel(t, m, runeKey('k'))
	if m.selectedContextItem != 1 {
		t.Fatalf("selection after k = %d, want 1", m.selectedContextItem)
	}
}

func TestContextPinToggle(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.hasContext = true
	m.context = core.ContextSnapshot{
		WindowTokens: 100,
		UsedTokens:   20,
		Items: []core.ContextItem{
			{ID: "near-1", Tier: core.TierNear, Title: "item", TokenEstimate: 20, Pinned: false},
		},
	}
	m.focus = contextPanel
	m.selectedContextItem = 0

	m = updateModel(t, m, runeKey('p'))

	events := drainTuiBus(sub)
	hasPin := false
	for _, ev := range events {
		if ev.Type == core.EventContextPin && ev.Message == "near-1" {
			hasPin = true
		}
	}
	if !hasPin {
		t.Fatalf("expected EventContextPin, got events: %#v", events)
	}
}

func TestContextRemoveItem(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.hasContext = true
	m.context = core.ContextSnapshot{
		WindowTokens: 100,
		UsedTokens:   20,
		Items: []core.ContextItem{
			{ID: "near-1", Tier: core.TierNear, Title: "removable", TokenEstimate: 20, Pinned: false},
		},
	}
	m.focus = contextPanel
	m.selectedContextItem = 0

	m = updateModel(t, m, runeKey('d'))

	events := drainTuiBus(sub)
	hasRemove := false
	for _, ev := range events {
		if ev.Type == core.EventContextRemove && ev.Message == "near-1" {
			hasRemove = true
		}
	}
	if !hasRemove {
		t.Fatalf("expected EventContextRemove, got events: %#v", events)
	}
}

func TestContextRemovePinnedItemBlocked(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.hasContext = true
	m.context = core.ContextSnapshot{
		WindowTokens: 100,
		UsedTokens:   20,
		Items: []core.ContextItem{
			{ID: "pinned-1", Tier: core.TierAnchor, Title: "pinned", TokenEstimate: 20, Pinned: true},
		},
	}
	m.focus = contextPanel
	m.selectedContextItem = 0

	m = updateModel(t, m, runeKey('d'))

	events := drainTuiBus(sub)
	for _, ev := range events {
		if ev.Type == core.EventContextRemove {
			t.Fatal("pinned item should not be removable")
		}
	}
}

func drainTuiBus(ch <-chan core.AgentEvent) []core.AgentEvent {
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

func numberedLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	return strings.Join(lines, "\n")
}
