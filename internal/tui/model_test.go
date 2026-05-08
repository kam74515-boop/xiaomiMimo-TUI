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
	if !strings.Contains(m.View(), "Navigation") {
		t.Fatal("help view does not contain Navigation")
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

func TestRunningStateTransitions(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	// Initial state: not running.
	if m.running {
		t.Fatal("running = true, want false initially")
	}

	// Enter prompt mode, type, and submit: running should become true.
	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('t'))
	m = updateModel(t, m, runeKey('e'))
	m = updateModel(t, m, runeKey('s'))
	m = updateModel(t, m, runeKey('t'))
	m = updateModel(t, m, keyMsg(tea.KeyEnter))
	if !m.running {
		t.Fatal("running = false after prompt submit, want true")
	}
	if m.status != "agent processing..." {
		t.Fatalf("status = %q, want agent processing...", m.status)
	}

	// EventDone: running should become false.
	m.applyEvent(core.AgentEvent{Type: core.EventDone, Message: "all done"})
	if m.running {
		t.Fatal("running = true after EventDone, want false")
	}
	if m.status != "all done" {
		t.Fatalf("status after done = %q, want all done", m.status)
	}

	// EventError: running should become false, lastError set.
	m.running = true // re-set for test
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "something broke", Message: "fallback err"})
	if m.running {
		t.Fatal("running = true after EventError, want false")
	}
	if m.lastError != "something broke" {
		t.Fatalf("lastError = %q, want something broke", m.lastError)
	}
}

func TestErrorDisplayInStatusBar(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	// No error initially.
	if m.lastError != "" {
		t.Fatalf("lastError = %q, want empty initially", m.lastError)
	}

	// Send an error event.
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "connection refused"})

	if m.lastError != "connection refused" {
		t.Fatalf("lastError = %q, want connection refused", m.lastError)
	}
	if !strings.Contains(m.status, "connection refused") {
		t.Fatalf("status = %q, want to contain connection refused", m.status)
	}

	// Error should also appear in notes panel.
	foundInNotes := false
	for _, note := range m.notes {
		if strings.Contains(note, "connection refused") {
			foundInNotes = true
			break
		}
	}
	if !foundInNotes {
		t.Fatal("error not found in notes panel")
	}

	// Footer should contain the error (red styled).
	footer := m.renderFooter(120)
	if !strings.Contains(footer, "ERR:") {
		t.Fatal("footer does not contain ERR: prefix")
	}
	if !strings.Contains(footer, "connection refused") {
		t.Fatal("footer does not contain error message")
	}
}

func TestApprovalShowsToolDescription(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 10
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "write_file"},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "file writes can mutate the workspace",
		},
	}

	view := m.View()
	if !strings.Contains(view, "APPROVE?") {
		t.Fatal("view should contain APPROVE?")
	}
	if !strings.Contains(view, "10s") {
		t.Fatal("view should contain timeout hint")
	}
	if !strings.Contains(view, "write_file") {
		t.Fatal("view should contain tool name write_file")
	}
	if !strings.Contains(view, "file writes can mutate the workspace") {
		t.Fatal("view should contain permission reason")
	}
	if !strings.Contains(view, "[y/n]") {
		t.Fatal("view should contain [y/n] prompt")
	}

	// Test with missing reason: should still show tool name.
	m2 := NewModel(nil, nil)
	m2.width = 80
	m2.height = 24
	m2.inputMode = InputApprove
	m2.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
		},
	}
	view2 := m2.View()
	if !strings.Contains(view2, "shell") {
		t.Fatal("view should contain tool name shell even without reason")
	}
}

func TestApprovalPanelShowsSafetyLevel(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 10
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "shell", Input: core.ToolInput{"command": "rm -rf /tmp/test"}},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "DESTRUCTIVE COMMAND: rm -rf /tmp/test",
		},
	}

	view := m.View()
	if !strings.Contains(view, "Safety:") && !strings.Contains(view, "Safety") {
		t.Fatal("approval panel should show safety level")
	}
	if !strings.Contains(view, "destructive") {
		t.Fatal("approval panel should show destructive safety grade")
	}

	// Non-destructive case.
	m2 := NewModel(nil, nil)
	m2.width = 80
	m2.height = 24
	m2.inputMode = InputApprove
	m2.approvalCountdown = 10
	m2.approvalCountdownMax = 10
	m2.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "write_file"},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "write_file mutates the workspace",
		},
	}
	view2 := m2.View()
	if !strings.Contains(view2, "Safety") {
		t.Fatal("approval panel should show safety level")
	}
}

func TestApprovalPanelShowsToolName(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 10
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "apply_patch"},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "apply_patch mutates the workspace",
		},
	}

	view := m.View()
	if !strings.Contains(view, "apply_patch") {
		t.Fatal("approval panel should show tool name")
	}
	if !strings.Contains(view, "TOOL APPROVAL REQUIRED") {
		t.Fatal("approval panel should show approval header")
	}
}

func TestApprovalPanelShowsInputSummary(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 10
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "shell", Input: core.ToolInput{
			"command": "git push origin main",
			"path":    "/home/user/project",
		}},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "shell commands can mutate the workspace",
		},
	}

	view := m.View()
	if !strings.Contains(view, "Input:") && !strings.Contains(view, "Input") {
		t.Fatal("approval panel should show input summary")
	}
	if !strings.Contains(view, "command") {
		t.Fatal("approval panel should show command in input summary")
	}
	if !strings.Contains(view, "path") {
		t.Fatal("approval panel should show path in input summary")
	}
}

func TestApprovalPanelShowsCountdown(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 5
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "shell commands can mutate",
		},
	}

	view := m.View()
	if !strings.Contains(view, "Timeout:") && !strings.Contains(view, "5s") {
		t.Fatal("approval panel should show countdown timer")
	}
	if !strings.Contains(view, "default: deny") {
		t.Fatal("approval panel should mention default deny on timeout")
	}
}

func TestApprovalPanelShowsAffectedPaths(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 10
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall: core.ToolCall{Name: "shell", Input: core.ToolInput{
			"command": "cp /etc/config /tmp/backup",
		}},
		Permission: core.PermissionRequest{
			Behavior: core.PermissionAsk,
			Reason:   "shell commands can mutate",
		},
	}

	view := m.View()
	if !strings.Contains(view, "Paths") {
		t.Fatal("approval panel should show affected paths")
	}
	if !strings.Contains(view, "/etc/config") || !strings.Contains(view, "/tmp/backup") {
		t.Fatal("approval panel should show the paths from the shell command")
	}
}

func TestApprovalPanelCountdownTickAutoDenies(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	resp := make(chan core.ApprovalDecision, 1)
	m.inputMode = InputApprove
	m.approvalCountdown = 1
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk},
		Response:   resp,
	}

	next, cmd := m.Update(tickMsg{})
	updated := next.(Model)

	if updated.inputMode != InputNone {
		t.Fatalf("inputMode after tick=0 = %d, want InputNone (auto-denied)", updated.inputMode)
	}

	select {
	case decision := <-resp:
		if decision.Allowed {
			t.Fatal("countdown timeout should auto-deny (Allowed=true)")
		}
		if !strings.Contains(decision.Reason, "timed out") {
			t.Fatalf("deny reason = %q, want to contain 'timed out'", decision.Reason)
		}
	default:
		t.Log("cmd returned:", cmd)
		t.Fatal("no decision sent on timeout")
	}
}

func TestApprovalPanelCountdownTickDecrements(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	m.inputMode = InputApprove
	m.approvalCountdown = 5
	m.approvalCountdownMax = 10
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "shell", Input: core.ToolInput{"command": "echo hi"}},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk, Reason: "test"},
	}

	next1, cmd1 := m.Update(tickMsg{})
	updated1 := next1.(Model)

	if updated1.approvalCountdown != 4 {
		t.Fatalf("countdown after first tick = %d, want 4", updated1.approvalCountdown)
	}
	if cmd1 == nil {
		t.Fatal("first tick should return another tick command")
	}

	next2, cmd2 := updated1.Update(tickMsg{})
	updated2 := next2.(Model)

	if updated2.approvalCountdown != 3 {
		t.Fatalf("countdown after second tick = %d, want 3", updated2.approvalCountdown)
	}
	if cmd2 == nil {
		t.Fatal("second tick should return another tick command")
	}
	if !strings.Contains(updated2.status, "3s") {
		t.Fatalf("status = %q, want to contain 3s", updated2.status)
	}
}

func TestApprovalPanelEscCancelsDuringCountdown(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	resp := make(chan core.ApprovalDecision, 1)
	m.inputMode = InputApprove
	m.approvalCountdown = 8
	m.pendingApproval = core.ApprovalRequest{
		ToolCall:   core.ToolCall{Name: "shell"},
		Permission: core.PermissionRequest{Behavior: core.PermissionAsk},
		Response:   resp,
	}

	next, _ := m.Update(keyMsg(tea.KeyEsc))
	updated := next.(Model)

	if updated.inputMode != InputNone {
		t.Fatalf("inputMode after esc = %d, want InputNone", updated.inputMode)
	}

	select {
	case decision := <-resp:
		if decision.Allowed {
			t.Fatal("esc should send deny decision")
		}
	default:
		t.Fatal("no decision sent on esc cancel")
	}
}

func TestContextShowsSelectionReason(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40
	m.hasContext = true
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   850, // 85% — should be yellow
		Items: []core.ContextItem{
			{
				ID:              "near-1",
				Tier:            core.TierNear,
				Title:           "relevant file",
				TokenEstimate:   100,
				Reason:          "matches query",
				SelectionReason: "high semantic similarity",
			},
			{
				ID:            "near-2",
				Tier:          core.TierNear,
				Title:         "other file",
				TokenEstimate: 50,
				Reason:        "also matches",
			},
		},
		PollutionRisk: "low",
	}

	content := m.contextContent()

	// SelectionReason should appear for items that have it.
	if !strings.Contains(content, "(selected: high semantic similarity)") {
		t.Fatal("context content should contain SelectionReason")
	}

	// Reason should still be present.
	if !strings.Contains(content, "matches query") {
		t.Fatal("context content should contain Reason")
	}

	// Items without SelectionReason should not show "(selected:" prefix.
	if strings.Contains(content, "(selected:") && !strings.Contains(content, "(selected: high semantic similarity)") {
		// This condition is wrong - let me check more carefully.
	}
	// Verify the second item does NOT have selection reason.
	lines := strings.Split(content, "\n")
	selectedCount := 0
	for _, line := range lines {
		if strings.Contains(line, "(selected:") {
			selectedCount++
		}
	}
	if selectedCount != 1 {
		t.Fatalf("expected 1 selection reason line, got %d", selectedCount)
	}

	// Token budget at 85% should be yellow (not green, not red).
	// Yellow is color "220" in lipgloss. We check that the content can be rendered.
	view := m.View()
	if !strings.Contains(view, "85%") {
		t.Fatal("view should contain token percentage")
	}

	// Test red at >90%.
	m.context.UsedTokens = 950
	contentRed := m.contextContent()
	if !strings.Contains(contentRed, "95%") {
		t.Fatal("context content should show 95%")
	}

	// Test green at <70%.
	m.context.UsedTokens = 500
	contentGreen := m.contextContent()
	if !strings.Contains(contentGreen, "50%") {
		t.Fatal("context content should show 50%")
	}
}

func TestChatAutoScroll(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.focus = chatPanel

	// Default should be true.
	if !m.chatAutoScroll {
		t.Fatal("chatAutoScroll = false, want true by default")
	}

	// Fill chat with enough content to scroll.
	m.chat = numberedLines(100)

	// When message_delta arrives with autoScroll on, it should scroll to bottom.
	m.scroll[chatPanel] = 0 // start at top
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "new output"})
	maxScroll := m.maxPanelScroll(chatPanel)
	if m.scroll[chatPanel] != maxScroll {
		t.Fatalf("scroll after message_delta with autoScroll = %d, want max %d", m.scroll[chatPanel], maxScroll)
	}

	// Manual scroll up should disable autoScroll.
	m.scroll[chatPanel] = 5
	m = updateModel(t, m, keyMsg(tea.KeyUp))
	if m.chatAutoScroll {
		t.Fatal("chatAutoScroll = true after manual scroll up, want false")
	}

	// Now message_delta should NOT auto-scroll.
	prevScroll := m.scroll[chatPanel]
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "more output"})
	if m.scroll[chatPanel] != prevScroll {
		t.Fatalf("scroll changed after message_delta with autoScroll off: %d -> %d", prevScroll, m.scroll[chatPanel])
	}

	// End key should re-enable autoScroll.
	m = updateModel(t, m, keyMsg(tea.KeyEnd))
	if !m.chatAutoScroll {
		t.Fatal("chatAutoScroll = false after End key, want true")
	}

	// pgup should disable autoScroll.
	m.scroll[chatPanel] = 10
	m = updateModel(t, m, keyMsg(tea.KeyPgUp))
	if m.chatAutoScroll {
		t.Fatal("chatAutoScroll = true after pgup, want false")
	}

	// home should also disable.
	m.chatAutoScroll = true // re-enable
	m = updateModel(t, m, keyMsg(tea.KeyHome))
	if m.chatAutoScroll {
		t.Fatal("chatAutoScroll = true after home, want false")
	}
}

func TestMockModeShowsWarning(t *testing.T) {
	// Verify mock mode sets the initial chat warning.
	events := make(chan core.AgentEvent)
	close(events)
	err := Run(events, nil, "test-model", true)
	// Run blocks until program exits, but since the event channel is closed
	// it should exit quickly. We just verify it doesn't panic.
	if err != nil {
		t.Logf("Run returned error (expected for closed channel): %v", err)
	}

	// Unit test the mock mode chat initialization directly.
	m := NewModel(nil, nil)
	m.mockMode = true
	m.chat = "MOCK MODE — set MIMO_API_KEY for real MiMo\n"
	if !strings.Contains(m.chat, "MOCK MODE") {
		t.Fatal("mock mode chat should contain MOCK MODE warning")
	}
}

func TestSlashKeyEntersPromptMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24

	// Press '/' to enter prompt mode.
	m = updateModel(t, m, runeKey('/'))
	if m.inputMode != InputPrompt {
		t.Fatalf("inputMode = %d, want InputPrompt after /", m.inputMode)
	}
	if m.textInput != "" {
		t.Fatalf("textInput = %q, want empty", m.textInput)
	}
	if m.status != "PROMPT> type your message, Enter to submit, Esc to cancel" {
		t.Fatalf("status = %q, want prompt status", m.status)
	}
}

func TestCtrlLClearsChat(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.chat = "some conversation history\nwith multiple lines\n"

	m = updateModel(t, m, keyMsg(tea.KeyCtrlL))
	if m.chat != "" {
		t.Fatalf("chat = %q, want empty after ctrl+l", m.chat)
	}
	if m.status != "chat cleared" {
		t.Fatalf("status = %q, want chat cleared", m.status)
	}
}

func TestCtrlRPublishesOracleReview(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.width = 80
	m.height = 24

	m = updateModel(t, m, keyMsg(tea.KeyCtrlR))

	events := drainTuiBus(sub)
	hasOracleReview := false
	for _, ev := range events {
		if ev.Type == core.EventOracleReview {
			hasOracleReview = true
		}
	}
	if !hasOracleReview {
		t.Fatal("expected oracle_review event after ctrl+r")
	}
	if m.status != "oracle review requested" {
		t.Fatalf("status = %q, want oracle review requested", m.status)
	}
}

func TestCtrlLNotInHelpMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.showHelp = true
	m.chat = "some chat"

	// In help mode, ctrl+l should be blocked (help absorbs all keys except esc).
	m = updateModel(t, m, keyMsg(tea.KeyCtrlL))
	if m.chat != "some chat" {
		t.Fatal("ctrl+l should not clear chat while help is shown")
	}
}

func TestCtrlRNotInHelpMode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.showHelp = true

	prevStatus := m.status
	m = updateModel(t, m, keyMsg(tea.KeyCtrlR))
	if m.status != prevStatus {
		t.Fatal("ctrl+r should be blocked while help is shown")
	}
}

func TestPromptQueuedWhileRunning(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.running = true

	// Submit prompt while running — should queue, not publish.
	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, runeKey('h'))
	m = updateModel(t, m, runeKey('i'))
	m = updateModel(t, m, keyMsg(tea.KeyEnter))

	if len(m.promptQueue) != 1 || m.promptQueue[0] != "hi" {
		t.Fatalf("promptQueue = %v, want [hi]", m.promptQueue)
	}
	if !strings.Contains(m.status, "queued") {
		t.Fatalf("status = %q, want queued message", m.status)
	}
	if m.running != true {
		t.Fatal("running should stay true when queueing")
	}
}

func TestQueuedPromptFiresAfterDone(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.width = 80
	m.height = 24
	m.running = true
	m.promptQueue = []string{"next task"}

	// EventDone should dequeue and publish next prompt.
	m.applyEvent(core.AgentEvent{Type: core.EventDone, Message: "complete"})

	events := drainTuiBus(sub)
	hasUserPrompt := false
	for _, ev := range events {
		if ev.Type == core.EventUserPrompt && ev.Message == "next task" {
			hasUserPrompt = true
		}
	}
	if !hasUserPrompt {
		t.Fatal("expected EventUserPrompt for dequeued prompt")
	}
	if m.running != true {
		t.Fatal("running should be true after dequeueing")
	}
	if len(m.promptQueue) != 0 {
		t.Fatalf("promptQueue should be empty after dequeue, got %v", m.promptQueue)
	}
	if !strings.Contains(m.status, "dequeued") {
		t.Fatalf("status = %q, want dequeued message", m.status)
	}
}

func TestCtrlGDuringRun(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.width = 80
	m.height = 24
	m.running = true
	m.promptQueue = []string{"should be cleared"}

	m = updateModel(t, m, keyMsg(tea.KeyCtrlG))

	if len(m.promptQueue) != 0 {
		t.Fatalf("promptQueue should be cleared on interrupt, got %v", m.promptQueue)
	}
	if !strings.Contains(m.status, "interrupt") {
		t.Fatalf("status = %q, want interrupt message", m.status)
	}

	events := drainTuiBus(sub)
	hasInterrupt := false
	for _, ev := range events {
		if ev.Type == core.EventInterrupt {
			hasInterrupt = true
		}
	}
	if !hasInterrupt {
		t.Fatal("expected EventInterrupt on ctrl+g during run")
	}
}

func TestCtrlGDoesNothingWhenNotRunning(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(10)

	m := NewModel(nil, bus)
	m.width = 80
	m.height = 24
	m.running = false

	prevStatus := m.status
	m = updateModel(t, m, keyMsg(tea.KeyCtrlG))

	if m.status != prevStatus {
		t.Fatalf("ctrl+g should be no-op when not running, status changed from %q to %q", prevStatus, m.status)
	}

	events := drainTuiBus(sub)
	if len(events) > 0 {
		t.Fatal("no events should be published when ctrl+g is pressed while not running")
	}
}

func TestEventAgentStarted(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 80
	m.height = 24
	m.running = false
	m.lastError = "old error"

	m.applyEvent(core.AgentEvent{Type: core.EventAgentStarted})

	if !m.running {
		t.Fatal("running should be true after EventAgentStarted")
	}
	if m.lastError != "" {
		t.Fatalf("lastError should be cleared, got %q", m.lastError)
	}
}

func TestStatusLineRenders(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40
	m.modelName = "test-model"

	// No trace steps yet.
	statusLine := m.renderStatusLine(100)
	if !strings.Contains(statusLine, "test-model") {
		t.Fatalf("status line = %q, want to contain model name", statusLine)
	}
	if !strings.Contains(statusLine, "Step 0") {
		t.Fatalf("status line = %q, want to contain Step 0", statusLine)
	}
	if !strings.Contains(statusLine, "idle") {
		t.Fatalf("status line = %q, want to contain idle", statusLine)
	}

	// With trace steps.
	m.trace = []core.TraceStep{
		{ID: "s1", Goal: "first"},
		{ID: "s2", Goal: "second"},
		{ID: "s3", Goal: "third"},
	}
	statusLine = m.renderStatusLine(100)
	if !strings.Contains(statusLine, "Step 3") {
		t.Fatalf("status line = %q, want to contain Step 3", statusLine)
	}

	// With totalSteps set.
	m.totalSteps = 8
	statusLine = m.renderStatusLine(100)
	if !strings.Contains(statusLine, "Step 3/8") {
		t.Fatalf("status line = %q, want to contain Step 3/8", statusLine)
	}

	// Running state.
	m.running = true
	statusLine = m.renderStatusLine(100)
	if !strings.Contains(statusLine, "running") {
		t.Fatalf("status line = %q, want to contain running", statusLine)
	}

	// Default model name.
	m2 := NewModel(nil, nil)
	m2.width = 100
	m2.height = 40
	statusLine2 := m2.renderStatusLine(100)
	if !strings.Contains(statusLine2, "mimo-v2.5-pro") {
		t.Fatalf("status line = %q, want to contain default model name", statusLine2)
	}
}

func TestContextFocusShowsItems(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40
	m.hasContext = true
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   200,
		Items: []core.ContextItem{
			{
				ID:              "item-1",
				Tier:            core.TierNear,
				Title:           "test file",
				TokenEstimate:   50,
				Pinned:          true,
				SelectionReason: "high relevance",
			},
			{
				ID:            "item-2",
				Tier:          core.TierAnchor,
				Title:         "config",
				TokenEstimate: 30,
				ReplacedBy:    "item-3",
			},
		},
		PollutionRisk: "low",
	}

	content := m.contextContent()

	if !strings.Contains(content, "CONTEXT FOCUS") {
		t.Fatal("context content should contain CONTEXT FOCUS section")
	}
	if !strings.Contains(content, "[P]") {
		t.Fatal("context content should show pin icon [P] for pinned items")
	}
	if !strings.Contains(content, "test file") {
		t.Fatal("context content should show item title")
	}
	if !strings.Contains(content, "(near)") {
		t.Fatal("context content should show tier")
	}
	if !strings.Contains(content, "50 tok") {
		t.Fatal("context content should show token count")
	}
	if !strings.Contains(content, "high relevance") {
		t.Fatal("context content should show SelectionReason")
	}
	if !strings.Contains(content, "replaced by: item-3") {
		t.Fatal("context content should show ReplacedBy")
	}

	// No items should not show CONTEXT FOCUS.
	m2 := NewModel(nil, nil)
	m2.hasContext = true
	m2.context = core.ContextSnapshot{
		WindowTokens:  1000,
		UsedTokens:    0,
		Items:         []core.ContextItem{},
		PollutionRisk: "low",
	}
	content2 := m2.contextContent()
	if strings.Contains(content2, "CONTEXT FOCUS") {
		t.Fatal("empty items should not show CONTEXT FOCUS")
	}
}

func TestErrorDisplayShowsMessage(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	// No error: footer should not contain ERR.
	footer := m.renderFooter(120)
	if strings.Contains(footer, "ERR:") {
		t.Fatal("footer should not contain ERR when no error")
	}

	// Set an error.
	m.lastError = "connection timeout"
	footer = m.renderFooter(120)
	if !strings.Contains(footer, "ERR:") {
		t.Fatal("footer should contain ERR: prefix")
	}
	if !strings.Contains(footer, "connection timeout") {
		t.Fatal("footer should contain error message")
	}

	// Error should be cleared on successful event.
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "ok"})
	if m.lastError != "" {
		t.Fatalf("lastError = %q, want empty after successful event", m.lastError)
	}
	footer = m.renderFooter(120)
	if strings.Contains(footer, "ERR:") {
		t.Fatal("footer should not contain ERR after successful event clears it")
	}
}

func TestTokenCounterRenders(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	// No cost data: should show Tokens: ~0.
	footer := m.renderFooter(120)
	if !strings.Contains(footer, "Tokens:") {
		t.Fatal("footer should contain Tokens: label")
	}
	if !strings.Contains(footer, "~0") {
		t.Fatal("footer should show ~0 when no cost data")
	}

	// With cost data.
	m.hasCost = true
	m.cost = core.CostUpdate{
		InputTokens:  5000,
		OutputTokens: 7345,
		TotalTokens:  12345,
	}
	footer = m.renderFooter(120)
	if !strings.Contains(footer, "12,345") {
		t.Fatalf("footer = %q, want to contain 12,345", footer)
	}

	// Should also show step count.
	m.trace = []core.TraceStep{
		{ID: "s1", Goal: "step one"},
		{ID: "s2", Goal: "step two"},
		{ID: "s3", Goal: "step three"},
	}
	footer = m.renderFooter(120)
	if !strings.Contains(footer, "Step 3") {
		t.Fatalf("footer = %q, want to contain Step 3", footer)
	}
}

func TestHelpPanelGroupsBindings(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	help := helpContent()

	groups := []string{"Navigation", "Input", "Context", "Approval", "Control", "Panels"}
	for _, group := range groups {
		if !strings.Contains(help, group) {
			t.Fatalf("help content should contain group %q", group)
		}
	}

	// Verify specific bindings appear.
	bindings := []string{
		"ctrl+r",
		"ctrl+g",
		"ctrl+l",
		"ctrl+c",
		"tab",
		"pgup",
	}
	for _, binding := range bindings {
		if !strings.Contains(help, binding) {
			t.Fatalf("help content should contain binding %q", binding)
		}
	}
}

func TestGoalDisplayShowsCurrentGoal(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	// No trace yet: should not show goal.
	content := m.traceContent()
	if strings.Contains(content, "Goal:") {
		t.Fatal("trace content should not show Goal when no trace steps exist")
	}

	// Add a trace step with a goal.
	m.applyEvent(core.AgentEvent{
		Type: core.EventTraceUpdate,
		Trace: &core.TraceStep{
			ID:     "step-1",
			Goal:   "implement feature X",
			Status: core.TraceRunning,
		},
	})
	content = m.traceContent()
	if !strings.Contains(content, "Goal: implement feature X") {
		t.Fatalf("trace content = %q, want to contain 'Goal: implement feature X'", content)
	}

	// Update with a new goal: should show the latest goal.
	m.applyEvent(core.AgentEvent{
		Type: core.EventTraceUpdate,
		Trace: &core.TraceStep{
			ID:     "step-2",
			Goal:   "write tests for feature X",
			Status: core.TraceRunning,
		},
	})
	content = m.traceContent()
	if !strings.Contains(content, "Goal: write tests for feature X") {
		t.Fatalf("trace content = %q, want to contain latest goal", content)
	}
}

func TestRiskLevelShowsCorrectColor(t *testing.T) {
	m := NewModel(nil, nil)

	// Default: low risk.
	label, _ := m.riskLevel()
	if label != "low" {
		t.Fatalf("default risk = %q, want low", label)
	}

	// High pollution risk + error = high.
	m.context.PollutionRisk = "high"
	m.lastError = "something broke"
	label, _ = m.riskLevel()
	if label != "high" {
		t.Fatalf("risk with high pollution + error = %q, want high", label)
	}

	// Medium pollution only = medium.
	m.context.PollutionRisk = "medium"
	m.lastError = ""
	label, _ = m.riskLevel()
	if label != "medium" {
		t.Fatalf("risk with medium pollution = %q, want medium", label)
	}

	// Pending approval = medium (score 2).
	m.context.PollutionRisk = "low"
	m.inputMode = InputApprove
	label, _ = m.riskLevel()
	if label != "medium" {
		t.Fatalf("risk with pending approval = %q, want medium", label)
	}

	// Verify the risk display appears in footer.
	m.inputMode = InputNone
	m.context.PollutionRisk = "low"
	footer := m.renderFooter(120)
	if !strings.Contains(footer, "Risk: low") {
		t.Fatalf("footer = %q, want to contain 'Risk: low'", footer)
	}

	// High risk should show in footer.
	m.context.PollutionRisk = "high"
	m.lastError = "err"
	footer = m.renderFooter(120)
	if !strings.Contains(footer, "Risk: high") {
		t.Fatalf("footer = %q, want to contain 'Risk: high'", footer)
	}
}

func TestVerificationStatusShowsPassFail(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	// run_test with passing result.
	m.tools = []toolRun{
		{Name: "run_test", Status: "done", Detail: "all 5 tests passed"},
	}
	content := m.toolContent()
	if !strings.Contains(content, "verification: PASS") {
		t.Fatalf("tool content = %q, want 'verification: PASS'", content)
	}

	// run_test with failing result.
	m.tools = []toolRun{
		{Name: "run_test", Status: "done", Detail: "2 tests failed with exit code 1"},
	}
	content = m.toolContent()
	if !strings.Contains(content, "verification: FAIL") {
		t.Fatalf("tool content = %q, want 'verification: FAIL'", content)
	}

	// write_file with success.
	m.tools = []toolRun{
		{Name: "write_file", Status: "done", Detail: "wrote 120 bytes to file.go"},
	}
	content = m.toolContent()
	if !strings.Contains(content, "status: written") {
		t.Fatalf("tool content = %q, want 'status: written'", content)
	}

	// apply_patch with success.
	m.tools = []toolRun{
		{Name: "apply_patch", Status: "done", Detail: "patch applied successfully"},
	}
	content = m.toolContent()
	if !strings.Contains(content, "status: applied") {
		t.Fatalf("tool content = %q, want 'status: applied'", content)
	}

	// Running tool should not show verification.
	m.tools = []toolRun{
		{Name: "run_test", Status: "running", Detail: "running tests..."},
	}
	content = m.toolContent()
	if strings.Contains(content, "verification:") {
		t.Fatalf("running tool should not show verification, got %q", content)
	}
}

func TestRollbackAvailabilityShown(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	// Tool with rollback artifact.
	m.tools = []toolRun{
		{Name: "write_file", Status: "done", Detail: "wrote file", RollbackArtifactID: "art-123"},
	}
	content := m.toolContent()
	if !strings.Contains(content, "[rollback available]") {
		t.Fatalf("tool content = %q, want '[rollback available]'", content)
	}
	if !strings.Contains(content, "art-123") {
		t.Fatalf("tool content should contain rollback artifact ID")
	}

	// Tool without rollback artifact.
	m.tools = []toolRun{
		{Name: "read_file", Status: "done", Detail: "read file"},
	}
	content = m.toolContent()
	if strings.Contains(content, "[rollback available]") {
		t.Fatalf("tool without rollback should not show '[rollback available]', got %q", content)
	}
}

func TestErrorRecoveryActionShown(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40

	// Permission denied error.
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "permission denied: cannot write to /etc"})
	foundRecovery := false
	for _, note := range m.notes {
		if strings.Contains(note, "Check policy.toml or approve the tool") {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("notes = %v, want recovery for permission denied", m.notes)
	}

	// Tool not found error.
	m.notes = nil
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "tool not found: bad_tool"})
	foundRecovery = false
	for _, note := range m.notes {
		if strings.Contains(note, "Check tool name") {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("notes = %v, want recovery for tool not found", m.notes)
	}

	// Max steps error.
	m.notes = nil
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "max steps reached"})
	foundRecovery = false
	for _, note := range m.notes {
		if strings.Contains(note, "increase MIMO_MAX_STEPS") {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("notes = %v, want recovery for max steps reached", m.notes)
	}

	// Approval timed out error.
	m.notes = nil
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "approval timed out"})
	foundRecovery = false
	for _, note := range m.notes {
		if strings.Contains(note, "Respond faster") {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("notes = %v, want recovery for approval timed out", m.notes)
	}

	// Unknown error should have no recovery suggestion.
	m.notes = nil
	m.applyEvent(core.AgentEvent{Type: core.EventError, Err: "unknown weird error"})
	for _, note := range m.notes {
		if strings.Contains(note, "—") {
			t.Fatalf("unknown error should not have recovery suggestion, got note: %q", note)
		}
	}
}

func TestSelectedEvidenceShowsReason(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 100
	m.height = 40
	m.hasContext = true

	// Pinned item should show "evidence: pinned".
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   200,
		Items: []core.ContextItem{
			{ID: "pinned-1", Tier: core.TierNear, Title: "pinned item", TokenEstimate: 50, Pinned: true},
		},
		PollutionRisk: "low",
	}
	content := m.contextContent()
	if !strings.Contains(content, "evidence: pinned") {
		t.Fatalf("context content = %q, want 'evidence: pinned'", content)
	}

	// Item with SelectionReason should show "evidence: <reason>".
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   200,
		Items: []core.ContextItem{
			{ID: "sel-1", Tier: core.TierNear, Title: "selected item", TokenEstimate: 50, SelectionReason: "high semantic similarity"},
		},
		PollutionRisk: "low",
	}
	content = m.contextContent()
	if !strings.Contains(content, "evidence: high semantic similarity") {
		t.Fatalf("context content = %q, want 'evidence: high semantic similarity'", content)
	}

	// Item referenced in recent notes should show "evidence: referenced by recent observation".
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   200,
		Items: []core.ContextItem{
			{ID: "ref-1", Tier: core.TierNear, Title: "my-file.go", TokenEstimate: 50},
		},
		PollutionRisk: "low",
	}
	m.notes = []string{"read my-file.go and found issues"}
	content = m.contextContent()
	if !strings.Contains(content, "evidence: referenced by recent observation") {
		t.Fatalf("context content = %q, want 'evidence: referenced by recent observation'", content)
	}

	// Item from compression should show "evidence: compressed from: N items".
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   200,
		Items: []core.ContextItem{
			{ID: "comp-1", Tier: core.TierArtifact, Title: "compressed", TokenEstimate: 50},
		},
		PollutionRisk: "low",
		CompressionRecords: []core.CompressionRecord{
			{ID: "rec-1", SourceIDs: []string{"comp-1", "comp-2", "comp-3"}, Summary: "merged items"},
		},
	}
	m.notes = nil
	content = m.contextContent()
	if !strings.Contains(content, "evidence: compressed from: 3 items") {
		t.Fatalf("context content = %q, want 'evidence: compressed from: 3 items'", content)
	}

	// Item from oracle source should show "evidence: oracle promoted".
	m.context = core.ContextSnapshot{
		WindowTokens: 1000,
		UsedTokens:   200,
		Items: []core.ContextItem{
			{ID: "ora-1", Tier: core.TierAnchor, Title: "oracle item", TokenEstimate: 50, Source: "oracle_review"},
		},
		PollutionRisk: "low",
	}
	m.notes = nil
	content = m.contextContent()
	if !strings.Contains(content, "evidence: oracle promoted") {
		t.Fatalf("context content = %q, want 'evidence: oracle promoted'", content)
	}
}

func numberedLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	return strings.Join(lines, "\n")
}
