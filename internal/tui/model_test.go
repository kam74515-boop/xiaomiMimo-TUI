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

func TestInputPromptEnterAndCancel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	// Press 'i' to enter prompt mode.
	m = updateModel(t, m, runeKey('i'))
	if m.inputMode != InputPrompt {
		t.Fatalf("inputMode = %d, want InputPrompt", m.inputMode)
	}
	if m.textInput != "" {
		t.Fatalf("textInput = %q, want empty", m.textInput)
	}

	// Type some text.
	m = updateModel(t, m, runeKey('h'))
	m = updateModel(t, m, runeKey('e'))
	m = updateModel(t, m, runeKey('l'))
	m = updateModel(t, m, runeKey('l'))
	m = updateModel(t, m, runeKey('o'))
	if m.textInput != "hello" {
		t.Fatalf("textInput = %q, want hello", m.textInput)
	}

	// Press Enter to submit.
	m = updateModel(t, m, keyMsg(tea.KeyEnter))
	if m.inputMode != InputNone {
		t.Fatalf("inputMode after Enter = %d, want InputNone", m.inputMode)
	}
	if !strings.Contains(m.chat, "user> hello") {
		t.Fatalf("chat = %q, want to contain user> hello", m.chat)
	}

	// Test cancel: enter prompt mode again, type, Esc.
	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('w'))
	m = updateModel(t, m, runeKey('o'))
	m = updateModel(t, m, runeKey('r'))
	m = updateModel(t, m, runeKey('l'))
	m = updateModel(t, m, runeKey('d'))
	m = updateModel(t, m, keyMsg(tea.KeyEsc))
	if m.inputMode != InputNone {
		t.Fatalf("inputMode after Esc = %d, want InputNone", m.inputMode)
	}
	if m.textInput != "" {
		t.Fatalf("textInput after cancel = %q, want empty", m.textInput)
	}
}

func TestInputPromptEmptySubmit(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m = updateModel(t, m, runeKey('i'))
	chatBefore := m.chat
	// Press Enter with empty text.
	m = updateModel(t, m, keyMsg(tea.KeyEnter))
	if m.inputMode != InputNone {
		t.Fatalf("inputMode after Enter = %d, want InputNone", m.inputMode)
	}
	if m.chat != chatBefore {
		t.Fatalf("chat changed with empty prompt: %q -> %q", chatBefore, m.chat)
	}
}

func TestInputPromptPublishesUserPromptEvent(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.width = 80
	m.height = 24

	// Enter prompt mode, type, and submit.
	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('h'))
	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, keyMsg(tea.KeyEnter))

	if m.inputMode != InputNone {
		t.Fatalf("inputMode after Enter = %d, want InputNone", m.inputMode)
	}

	events := drainTuiBus(sub)
	hasUserPrompt := false
	for _, ev := range events {
		if ev.Type == core.EventUserPrompt && ev.Message == "hi" {
			hasUserPrompt = true
		}
	}
	if !hasUserPrompt {
		t.Fatalf("expected EventUserPrompt with message 'hi', got events: %#v", events)
	}
}

func TestInputPromptBackspace(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('a'))
	m = updateModel(t, m, runeKey('b'))
	m = updateModel(t, m, runeKey('c'))
	if m.textInput != "abc" {
		t.Fatalf("textInput = %q, want abc", m.textInput)
	}

	m = updateModel(t, m, keyMsg(tea.KeyBackspace))
	if m.textInput != "ab" {
		t.Fatalf("textInput after backspace = %q, want ab", m.textInput)
	}

	m = updateModel(t, m, keyMsg(tea.KeyBackspace))
	m = updateModel(t, m, keyMsg(tea.KeyBackspace))
	if m.textInput != "" {
		t.Fatalf("textInput after 3 backspaces = %q, want empty", m.textInput)
	}

	// Backspace on empty string should not crash.
	m = updateModel(t, m, keyMsg(tea.KeyBackspace))
	if m.textInput != "" {
		t.Fatalf("textInput after backspace on empty = %q, want empty", m.textInput)
	}
}

func TestInputPromptCursorMovement(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('a'))
	m = updateModel(t, m, runeKey('b'))
	m = updateModel(t, m, runeKey('c'))
	if m.cursorPos != 3 {
		t.Fatalf("cursorPos = %d, want 3", m.cursorPos)
	}

	// Left moves cursor back.
	m = updateModel(t, m, keyMsg(tea.KeyLeft))
	if m.cursorPos != 2 {
		t.Fatalf("cursorPos after left = %d, want 2", m.cursorPos)
	}

	// Right moves cursor forward.
	m = updateModel(t, m, keyMsg(tea.KeyRight))
	if m.cursorPos != 3 {
		t.Fatalf("cursorPos after right = %d, want 3", m.cursorPos)
	}

	// Home and End.
	m = updateModel(t, m, keyMsg(tea.KeyHome))
	if m.cursorPos != 0 {
		t.Fatalf("cursorPos after home = %d, want 0", m.cursorPos)
	}
	m = updateModel(t, m, keyMsg(tea.KeyEnd))
	if m.cursorPos != 3 {
		t.Fatalf("cursorPos after end = %d, want 3", m.cursorPos)
	}
}

func TestInputPromptInsertAtCursor(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('a'))
	m = updateModel(t, m, runeKey('c'))

	// Move cursor to beginning.
	m = updateModel(t, m, keyMsg(tea.KeyHome))
	// Insert 'x' at beginning.
	m = updateModel(t, m, runeKey('x'))
	if m.textInput != "xac" {
		t.Fatalf("textInput = %q, want xac", m.textInput)
	}
	if m.cursorPos != 1 {
		t.Fatalf("cursorPos = %d, want 1", m.cursorPos)
	}
}

func TestNavigationDisabledInInputModes(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	initialFocus := m.focus
	initialScroll := m.scroll[chatPanel]

	// Enter prompt mode.
	m = updateModel(t, m, runeKey('i'))
	if m.inputMode != InputPrompt {
		t.Fatalf("inputMode = %d, want InputPrompt", m.inputMode)
	}

	// Tab should not change focus in prompt mode.
	m = updateModel(t, m, keyMsg(tea.KeyTab))
	if m.focus != initialFocus {
		t.Fatalf("focus changed in prompt mode: %d -> %d", initialFocus, m.focus)
	}

	// Arrow keys should not scroll in prompt mode.
	m = updateModel(t, m, keyMsg(tea.KeyDown))
	if m.scroll[chatPanel] != initialScroll {
		t.Fatalf("scroll changed in prompt mode: %d -> %d", initialScroll, m.scroll[chatPanel])
	}
}

func TestApprovalNeededEventTransitionsToApproveMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	resp := make(chan core.ApprovalDecision, 1)
	m.applyEvent(core.AgentEvent{
		Type:     core.EventApprovalNeeded,
		ToolName: "shell",
		Message:  "Approval needed for tool shell",
		Approval: &core.ApprovalRequest{
			ToolCall:   core.ToolCall{Name: "shell"},
			Permission: core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "shell commands can mutate"},
			Response:   resp,
		},
	})

	if m.inputMode != InputApprove {
		t.Fatalf("inputMode = %d, want InputApprove", m.inputMode)
	}
	if m.pendingApproval.ToolCall.Name != "shell" {
		t.Fatalf("pending tool = %q, want shell", m.pendingApproval.ToolCall.Name)
	}
}

func TestApprovalApproveSendsDecision(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	resp := make(chan core.ApprovalDecision, 1)
	m.inputMode = InputApprove
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk},
		Response:   resp,
	}

	m = updateModel(t, m, runeKey('y'))
	if m.inputMode != InputNone {
		t.Fatalf("inputMode after y = %d, want InputNone", m.inputMode)
	}

	select {
	case decision := <-resp:
		if !decision.Allowed {
			t.Fatal("decision.Allowed = false, want true")
		}
	default:
		t.Fatal("no decision sent on response channel")
	}
}

func TestApprovalDenySendsDecision(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	resp := make(chan core.ApprovalDecision, 1)
	m.inputMode = InputApprove
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk},
		Response:   resp,
	}

	m = updateModel(t, m, runeKey('n'))
	if m.inputMode != InputNone {
		t.Fatalf("inputMode after n = %d, want InputNone", m.inputMode)
	}

	select {
	case decision := <-resp:
		if decision.Allowed {
			t.Fatal("decision.Allowed = true, want false")
		}
	default:
		t.Fatal("no decision sent on response channel")
	}
}

func TestApprovalEscCancels(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	resp := make(chan core.ApprovalDecision, 1)
	m.inputMode = InputApprove
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk},
		Response:   resp,
	}

	m = updateModel(t, m, keyMsg(tea.KeyEsc))
	if m.inputMode != InputNone {
		t.Fatalf("inputMode after esc = %d, want InputNone", m.inputMode)
	}

	select {
	case decision := <-resp:
		if decision.Allowed {
			t.Fatal("decision.Allowed = true, want false")
		}
	default:
		t.Fatal("no decision sent on response channel")
	}
}

func TestViewShowsInputBarInPromptMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	// Normal mode: no input bar.
	view := m.View()
	if strings.Contains(view, "PROMPT>") {
		t.Fatal("view should not contain PROMPT> in normal mode")
	}

	// Enter prompt mode.
	m.inputMode = InputPrompt
	m.textInput = "test"
	m.cursorPos = 4
	view = m.View()
	if !strings.Contains(view, "PROMPT>") {
		t.Fatal("view should contain PROMPT> in prompt mode")
	}
}

func TestViewShowsInputBarInApproveMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "write_file"},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk},
	}
	view := m.View()
	if !strings.Contains(view, "APPROVE?") {
		t.Fatal("view should contain APPROVE? in approve mode")
	}
	if !strings.Contains(view, "write_file") {
		t.Fatal("view should contain the tool name in approve mode")
	}
}

func numberedLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	return strings.Join(lines, "\n")
}
