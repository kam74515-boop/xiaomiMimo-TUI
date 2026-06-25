package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mimo-tui/internal/core"
)

func collectEvents(ch <-chan core.AgentEvent) []core.AgentEvent {
	var events []core.AgentEvent
	for {
		select {
		case e := <-ch:
			events = append(events, e)
		default:
			return events
		}
	}
}

func firstEventOfType(events []core.AgentEvent, t core.EventType) *core.AgentEvent {
	for i := range events {
		if events[i].Type == t {
			return &events[i]
		}
	}
	return nil
}

func TestSlashGoalSetAndClear(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(16)
	m := NewModel(nil, bus)

	m = m.handleSlashCommand("/goal add tests and make them pass")
	if m.goalCondition != "add tests and make them pass" {
		t.Fatalf("goalCondition = %q", m.goalCondition)
	}
	ev := firstEventOfType(collectEvents(sub), core.EventGoalSet)
	if ev == nil || ev.Message != "add tests and make them pass" {
		t.Fatalf("EventGoalSet not published correctly: %+v", ev)
	}

	m = m.handleSlashCommand("/goal clear")
	if m.goalCondition != "" {
		t.Fatalf("goalCondition after clear = %q, want empty", m.goalCondition)
	}
	cleared := firstEventOfType(collectEvents(sub), core.EventGoalSet)
	if cleared == nil || cleared.Message != "" {
		t.Fatalf("clear should publish empty EventGoalSet: %+v", cleared)
	}
}

func TestSlashMemoryAddPublishes(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(16)
	m := NewModel(nil, bus)

	m = m.handleSlashCommand("/memory add the runtime owns the memory store")
	ev := firstEventOfType(collectEvents(sub), core.EventMemoryWrite)
	if ev == nil || ev.Message != "the runtime owns the memory store" {
		t.Fatalf("EventMemoryWrite not published correctly: %+v", ev)
	}
	if !strings.Contains(m.chat, "recorded") {
		t.Fatalf("transcript should confirm memory record: %q", m.chat)
	}
}

func TestSlashMemoryShowEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := NewModel(nil, nil)
	m.workspace = t.TempDir()
	m = m.handleSlashCommand("/memory")
	if !strings.Contains(m.chat, "empty") {
		t.Fatalf("empty memory show should mention empty: %q", m.chat)
	}
}

func TestSlashHelpAndClear(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat = "some prior text"
	m = m.handleSlashCommand("/help")
	if !m.showHelp {
		t.Fatal("/help should open help")
	}
	m = m.handleSlashCommand("/clear")
	if m.chat != "" {
		t.Fatalf("/clear should empty chat, got %q", m.chat)
	}
}

func TestSlashUnknownCommand(t *testing.T) {
	m := NewModel(nil, nil)
	m = m.handleSlashCommand("/frobnicate now")
	if !strings.Contains(m.status, "unknown") {
		t.Fatalf("status = %q, want unknown-command note", m.status)
	}
	if !strings.Contains(m.chat, "unknown command") {
		t.Fatalf("transcript should note unknown command: %q", m.chat)
	}
}

func TestSlashModelInfo(t *testing.T) {
	m := NewModel(nil, nil)
	m.modelName = "mimo-v2.5-pro"
	m = m.handleSlashCommand("/model")
	if !strings.Contains(m.chat, "mimo-v2.5-pro") {
		t.Fatalf("transcript should show model name: %q", m.chat)
	}
}

func TestIsKnownSlashCommand(t *testing.T) {
	known := []string{"/help", "/goal add tests", "/memory show", "/MODEL", "/sessions", "/export out.md"}
	for _, c := range known {
		if !isKnownSlashCommand(c) {
			t.Errorf("isKnownSlashCommand(%q) = false, want true", c)
		}
	}
	unknown := []string{"/etc/hosts is broken", "/usr/bin/python script", "/frobnicate", "//literal", "not a command"}
	for _, c := range unknown {
		if isKnownSlashCommand(c) {
			t.Errorf("isKnownSlashCommand(%q) = true, want false", c)
		}
	}
}

// TestSlashUnknownForwardedToAgent verifies a leading-'/' message that is not a
// known command is sent to the agent as a normal prompt, not dropped.
func TestSlashUnknownForwardedToAgent(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(8)
	m := NewModel(nil, bus)
	m.width, m.height = 80, 24
	m.inputMode = InputPrompt
	m.textInput = "/etc/hosts is broken"
	m.cursorPos = len([]rune(m.textInput))

	m = updateModel(t, m, keyMsg(tea.KeyEnter))

	ev := firstEventOfType(collectEvents(sub), core.EventUserPrompt)
	if ev == nil || ev.Message != "/etc/hosts is broken" {
		t.Fatalf("unknown slash line should be forwarded verbatim as a prompt, got %+v", ev)
	}
	if !m.running {
		t.Fatal("submitting a forwarded prompt should mark running")
	}
}

// TestSlashKnownCommandNotForwarded verifies a known command is handled, not
// sent to the agent.
func TestSlashKnownCommandNotForwarded(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(8)
	m := NewModel(nil, bus)
	m.width, m.height = 80, 24
	m.inputMode = InputPrompt
	m.textInput = "/help"
	m.cursorPos = len([]rune(m.textInput))

	m = updateModel(t, m, keyMsg(tea.KeyEnter))

	if !m.showHelp {
		t.Fatal("/help should open help")
	}
	if firstEventOfType(collectEvents(sub), core.EventUserPrompt) != nil {
		t.Fatal("a known command must not be forwarded to the agent")
	}
}

func TestClearedGoalNotResurrected(t *testing.T) {
	m := NewModel(nil, nil)
	m = m.handleGoalCommand("ship the feature")
	m = m.handleGoalCommand("clear")
	if m.goalCondition != "" {
		t.Fatalf("goal should be cleared, got %q", m.goalCondition)
	}
	// A late terminal verdict from the superseded run must not revive the badge.
	m.applyGoalUpdate(core.GoalUpdate{Condition: "ship the feature", Satisfied: true, Reason: "done"})
	if m.goalCondition != "" || m.goalStatus == "satisfied" {
		t.Fatalf("cleared goal was resurrected: cond=%q status=%q", m.goalCondition, m.goalStatus)
	}
}

func TestSlashSkillList(t *testing.T) {
	m := NewModel(nil, nil)
	m = m.handleSlashCommand("/skill")
	for _, name := range []string{"plan", "tdd", "review", "debug", "verify"} {
		if !strings.Contains(m.chat, name) {
			t.Fatalf("/skill list missing builtin %q: %q", name, m.chat)
		}
	}
}

func TestSlashSkillUseAndClear(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(16)
	m := NewModel(nil, bus)

	m = m.handleSlashCommand("/skill use tdd")
	if !containsString(m.activeSkills, "tdd") {
		t.Fatalf("activeSkills = %v, want tdd", m.activeSkills)
	}
	ev := firstEventOfType(collectEvents(sub), core.EventSkillSet)
	if ev == nil || ev.Message != "tdd" {
		t.Fatalf("EventSkillSet not published correctly: %+v", ev)
	}

	m = m.handleSlashCommand("/skill clear")
	if len(m.activeSkills) != 0 {
		t.Fatalf("activeSkills after clear = %v, want empty", m.activeSkills)
	}
	cleared := firstEventOfType(collectEvents(sub), core.EventSkillSet)
	if cleared == nil || cleared.Message != "" {
		t.Fatalf("clear should publish empty EventSkillSet: %+v", cleared)
	}
}

func TestSlashSkillUnknown(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(16)
	m := NewModel(nil, bus)
	m = m.handleSlashCommand("/skill use does-not-exist")
	if len(m.activeSkills) != 0 {
		t.Fatalf("unknown skill should not activate: %v", m.activeSkills)
	}
	if firstEventOfType(collectEvents(sub), core.EventSkillSet) != nil {
		t.Fatal("unknown skill must not publish EventSkillSet")
	}
	if !strings.Contains(m.chat, "unknown skill") {
		t.Fatalf("transcript should note unknown skill: %q", m.chat)
	}
}

func TestTranscriptWrapCacheMatchesFresh(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat = "MIMO\n  hello world this is a deliberately long assistant line that must wrap\n\n■ a user prompt here\n\nTOOL shell [done]\n  ran something\n"

	// For any (width, streaming, frame), the cached result must equal a fresh
	// compute — the cache must never return stale/wrong output.
	for _, w := range []int{20, 40, 80} {
		for _, streaming := range []bool{false, true} {
			for _, frame := range []int{0, 1, 5} {
				m.assistantStreaming = streaming
				m.loadingFrame = frame
				fresh := m.transcriptLinesUncached(w)
				got := m.transcriptLines(w) // may hit or miss; must match fresh
				if strings.Join(got, "\n") != strings.Join(fresh, "\n") {
					t.Fatalf("cache mismatch at width=%d streaming=%v frame=%d", w, streaming, frame)
				}
			}
		}
	}

	// Content change must invalidate.
	m.assistantStreaming = false
	before := m.transcriptLines(40)
	m.chat += "\nMIMO\n  a brand new answer block\n"
	after := m.transcriptLines(40)
	if strings.Join(before, "\n") == strings.Join(after, "\n") {
		t.Fatal("cache not invalidated after content change")
	}
	if strings.Join(after, "\n") != strings.Join(m.transcriptLinesUncached(40), "\n") {
		t.Fatal("post-change cache does not match fresh compute")
	}
}

func TestTranscriptWrapCacheStableWhenIdle(t *testing.T) {
	// When NOT streaming, the animation frame must not affect the wrap, so idle
	// pulses hit the cache (the key omits loadingFrame).
	m := NewModel(nil, nil)
	m.chat = "MIMO\n  some completed answer\n"
	m.assistantStreaming = false
	a := m.transcriptLines(40)
	m.loadingFrame += 7
	b := m.transcriptLines(40)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Fatal("idle frame change should not change the wrapped transcript")
	}
}

func TestApprovalCountdownSingleTickLoop(t *testing.T) {
	m := NewModel(nil, nil)
	m.width, m.height = 80, 24

	req := core.ApprovalRequest{ToolCall: core.ToolCall{Name: "shell"}, TimeoutSeconds: 8, Response: make(chan core.ApprovalDecision, 1)}
	m = updateModel(t, m, agentEventMsg(core.AgentEvent{Type: core.EventApprovalNeeded, Approval: &req}))
	if m.inputMode != InputApprove || m.approvalCountdown != 8 {
		t.Fatalf("approval not entered: mode=%d countdown=%d", m.inputMode, m.approvalCountdown)
	}
	if !m.countdownTicking {
		t.Fatal("entering approval should arm exactly one tick loop")
	}

	// Other events during the wait must NOT re-arm or drain the countdown.
	for i := 0; i < 3; i++ {
		m = updateModel(t, m, agentEventMsg(core.AgentEvent{
			Type:     core.EventActivityUpdate,
			Activity: &core.ActivityEvent{ID: "a1", Kind: core.ActivityTool, Name: "x", Status: core.ActivityRunning},
		}))
	}
	if m.approvalCountdown != 8 {
		t.Fatalf("countdown drained by non-tick events: %d (concurrent tick loops)", m.approvalCountdown)
	}
	if !m.countdownTicking {
		t.Fatal("should still have its single tick loop")
	}

	// A single tick decrements by exactly one.
	m = updateModel(t, m, tickMsg{})
	if m.approvalCountdown != 7 {
		t.Fatalf("one tick should decrement by 1, got %d", m.approvalCountdown)
	}

	// Answering clears the countdown state so no stray ticks re-arm.
	m = m.finishApproval(true, "ok", "approved")
	if m.countdownTicking || m.approvalCountdown != 0 {
		t.Fatalf("finishApproval should clear countdown state: ticking=%v countdown=%d", m.countdownTicking, m.approvalCountdown)
	}
}

func TestSlashTimelineEmptyAndPopulated(t *testing.T) {
	m := NewModel(nil, nil)
	m = m.handleSlashCommand("/timeline")
	if !strings.Contains(m.chat, "no context snapshots") {
		t.Fatalf("empty timeline should say so: %q", m.chat)
	}
	// Feed two context snapshots through applyEvent.
	s1 := core.ContextSnapshot{WindowTokens: 1000, UsedTokens: 100, PollutionRisk: "low"}
	s2 := core.ContextSnapshot{WindowTokens: 1000, UsedTokens: 200, PollutionRisk: "low"}
	m.applyEvent(core.AgentEvent{Type: core.EventContextUpdate, Context: &s1})
	m.applyEvent(core.AgentEvent{Type: core.EventContextUpdate, Context: &s2})
	if len(m.contextHistory) != 2 {
		t.Fatalf("contextHistory = %d, want 2", len(m.contextHistory))
	}
	m = m.handleSlashCommand("/timeline")
	if !m.showTimeline {
		t.Fatal("/timeline with snapshots should open the time-travel overlay")
	}
	if m.timelineIndex != len(m.contextHistory)-1 {
		t.Fatalf("timelineIndex = %d, want last (%d)", m.timelineIndex, len(m.contextHistory)-1)
	}
	overlay := m.renderTimelineOverlay(100, 30)
	if !strings.Contains(overlay, "tokens") {
		t.Fatalf("overlay should list snapshots with token counts: %q", overlay)
	}
}

func TestTimelineScrubAndClose(t *testing.T) {
	m := NewModel(nil, nil)
	for i := 0; i < 4; i++ {
		s := core.ContextSnapshot{WindowTokens: 1000, UsedTokens: 100 * (i + 1), PollutionRisk: "low"}
		m.applyEvent(core.AgentEvent{Type: core.EventContextUpdate, Context: &s})
	}
	m = m.handleSlashCommand("/timeline")
	// Scrub up twice, then to home, then end; index must stay in range.
	m = updateModel(t, m, runeKey('k'))
	m = updateModel(t, m, runeKey('k'))
	if m.timelineIndex != 1 {
		t.Fatalf("after two 'k' from last(3), index = %d, want 1", m.timelineIndex)
	}
	m = updateModel(t, m, keyMsg(tea.KeyHome))
	if m.timelineIndex != 0 {
		t.Fatalf("home should jump to 0, got %d", m.timelineIndex)
	}
	m = updateModel(t, m, keyMsg(tea.KeyEnd))
	if m.timelineIndex != 3 {
		t.Fatalf("end should jump to last, got %d", m.timelineIndex)
	}
	// k below 0 must clamp.
	m.timelineIndex = 0
	m = updateModel(t, m, runeKey('k'))
	if m.timelineIndex != 0 {
		t.Fatalf("k at 0 should clamp, got %d", m.timelineIndex)
	}
	// esc closes the overlay.
	m = updateModel(t, m, keyMsg(tea.KeyEsc))
	if m.showTimeline {
		t.Fatal("esc should close the time-travel overlay")
	}
}

func TestSlashWhyShowsEvidenceAndDelta(t *testing.T) {
	m := NewModel(nil, nil)
	s1 := core.ContextSnapshot{WindowTokens: 1000, UsedTokens: 100, PollutionRisk: "low",
		Items: []core.ContextItem{{ID: "a", Tier: core.TierAnchor, Title: "project map", Pinned: true}}}
	s2 := core.ContextSnapshot{WindowTokens: 1000, UsedTokens: 180, PollutionRisk: "low",
		Items: []core.ContextItem{
			{ID: "a", Tier: core.TierAnchor, Title: "project map", Pinned: true},
			{ID: "obs1", Tier: core.TierNear, Title: "read README"},
		}}
	m.applyEvent(core.AgentEvent{Type: core.EventContextUpdate, Context: &s1})
	m.applyEvent(core.AgentEvent{Type: core.EventObservation, Observation: &core.Observation{Summary: "read README.md (artifact art-1)"}})
	m.applyEvent(core.AgentEvent{Type: core.EventContextUpdate, Context: &s2})

	m = m.handleWhyCommand()
	if !strings.Contains(m.chat, "project map") {
		t.Fatalf("why should list anchored evidence: %q", m.chat)
	}
	if !strings.Contains(m.chat, "read README.md") {
		t.Fatalf("why should show recent observations: %q", m.chat)
	}
	if !strings.Contains(m.chat, "read README") || !strings.Contains(m.chat, "delta") {
		t.Fatalf("why should show context delta with the added near item: %q", m.chat)
	}
}

func TestTrustNudgeOnDoneForUnverifiedClaim(t *testing.T) {
	m := NewModel(nil, nil)
	// Assistant claims tests pass, but no run_test ran.
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "All tests pass now."})
	m.applyEvent(core.AgentEvent{Type: core.EventDone})
	if !strings.Contains(m.chat, "TRUST") || !strings.Contains(m.chat, "unverified") {
		t.Fatalf("done with an unverified claim should append a TRUST warning: %q", m.chat)
	}
}

func TestTrustNoNudgeWhenVerified(t *testing.T) {
	m := NewModel(nil, nil)
	// A successful run_test backs the claim.
	m.tools = append(m.tools, toolRun{Name: "run_test", Status: "done"})
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "All tests pass now."})
	m.applyEvent(core.AgentEvent{Type: core.EventDone})
	if strings.Contains(m.chat, "TRUST") {
		t.Fatalf("verified claim should not produce a TRUST warning: %q", m.chat)
	}
}

// TestTrustScopedToCurrentTurn verifies a claim is only verified by a tool that
// ran in the SAME turn, not an earlier turn's test run.
func TestTrustScopedToCurrentTurn(t *testing.T) {
	m := NewModel(nil, nil)

	// Turn 1: run tests successfully, claim pass → verified, no warning.
	m.applyEvent(core.AgentEvent{Type: core.EventAgentStarted})
	m.applyEvent(core.AgentEvent{Type: core.EventToolStart, ToolName: "run_test"})
	m.applyEvent(core.AgentEvent{Type: core.EventToolResult, ToolName: "run_test"})
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "All tests pass."})
	m.applyEvent(core.AgentEvent{Type: core.EventDone})
	turn1 := strings.Count(m.chat, "TRUST")
	if turn1 != 0 {
		t.Fatalf("turn 1 (tests actually ran) should not warn, got %d TRUST blocks", turn1)
	}

	// Turn 2: claim pass again but no test run THIS turn → should warn.
	m.applyEvent(core.AgentEvent{Type: core.EventAgentStarted})
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "Tests still pass."})
	m.applyEvent(core.AgentEvent{Type: core.EventDone})
	if strings.Count(m.chat, "TRUST") < 1 {
		t.Fatalf("turn 2 (no test run) should warn that the claim is unverified: %q", m.chat)
	}
}

func TestSlashTrustCommand(t *testing.T) {
	m := NewModel(nil, nil)
	m.applyEvent(core.AgentEvent{Type: core.EventMessageDelta, Message: "it compiles and tests pass"})
	m = m.handleSlashCommand("/trust")
	if !strings.Contains(m.chat, "unverified") {
		t.Fatalf("/trust should show the ledger with unverified claims: %q", m.chat)
	}
}

func TestSlashGoalUpdateAppliesBadge(t *testing.T) {
	m := NewModel(nil, nil)
	m.applyGoalUpdate(core.GoalUpdate{Condition: "ship", Active: true, ReactCount: 1, Reason: "not done"})
	if m.goalCondition != "ship" || !strings.Contains(m.goalStatus, "retry") {
		t.Fatalf("active update not applied: cond=%q status=%q", m.goalCondition, m.goalStatus)
	}
	m.applyGoalUpdate(core.GoalUpdate{Condition: "ship", Satisfied: true, Reason: "done"})
	if m.goalStatus != "satisfied" {
		t.Fatalf("satisfied update not applied: status=%q", m.goalStatus)
	}
}
