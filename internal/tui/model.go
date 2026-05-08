package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mimo-tui/internal/core"
)

type focusPanel int

const (
	contextPanel focusPanel = iota
	chatPanel
	tracePanel
	toolPanel
	panelCount
)

var panelNames = []string{
	"Context Map",
	"Chat Stream",
	"Agent Trace",
	"Tool Cockpit",
}

type InputMode int

const (
	InputNone InputMode = iota
	InputPrompt
	InputApprove
)

type toolRun struct {
	Name               string
	Status             string
	Detail             string
	StartedAt          time.Time
	EndedAt            time.Time
	RollbackArtifactID string
}

type Model struct {
	events <-chan core.AgentEvent

	width  int
	height int
	focus  focusPanel
	scroll [panelCount]int

	context             core.ContextSnapshot
	hasContext          bool
	selectedContextItem int
	chat                string
	trace               []core.TraceStep
	traceIndex          map[string]int
	tools               []toolRun
	notes               []string

	cost    core.CostUpdate
	hasCost bool

	status       string
	sourceClosed bool
	showHelp     bool

	running   bool
	lastError string

	chatAutoScroll bool

	modelName string
	mockMode  bool

	bus *core.Bus

	inputMode       InputMode
	textInput       string
	cursorPos       int
	pendingApproval core.ApprovalRequest

	approvalCountdown    int
	approvalCountdownMax int

	promptQueue []string

	totalSteps int
}

type agentEventMsg core.AgentEvent

type tickMsg struct{}

type eventSourceClosedMsg struct{}

const approvalCountdownSeconds = 10

func NewModel(events <-chan core.AgentEvent, bus *core.Bus) Model {
	if events == nil {
		ch := make(chan core.AgentEvent)
		close(ch)
		events = ch
	}
	return Model{
		events:              events,
		bus:                 bus,
		width:               100,
		height:              32,
		selectedContextItem: -1,
		traceIndex:          make(map[string]int),
		status:              "waiting for agent events",
		chatAutoScroll:      true,
	}
}

func Run(events <-chan core.AgentEvent, bus *core.Bus, modelName string, mockMode bool) error {
	m := NewModel(events, bus)
	m.modelName = modelName
	m.mockMode = mockMode
	if mockMode {
		m.chat = "MOCK MODE — set MIMO_API_KEY for real MiMo\n"
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return waitForEvent(m.events)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		if key == "q" || key == "ctrl+c" {
			return m, tea.Quit
		}

		if m.inputMode == InputNone && key == "?" {
			m.showHelp = !m.showHelp
			if m.showHelp {
				m.status = "help opened"
			} else {
				m.status = "help closed"
			}
			return m, nil
		}

		if key == "esc" {
			if m.showHelp {
				m.showHelp = false
				m.status = "help closed"
				return m, nil
			}
		}

		if m.showHelp {
			return m, nil
		}

		switch m.inputMode {
		case InputPrompt:
			switch msg.Type {
			case tea.KeyEnter:
				if strings.TrimSpace(m.textInput) != "" {
					if m.running {
						m.promptQueue = append(m.promptQueue, m.textInput)
						m.status = fmt.Sprintf("prompt queued (position %d)", len(m.promptQueue))
					} else {
						m.chat += "\nuser> " + m.textInput
						if m.bus != nil {
							event := core.NewEvent(core.EventUserPrompt)
							event.Message = m.textInput
							m.bus.Publish(event)
						}
						m.running = true
						m.lastError = ""
						m.status = "agent processing..."
					}
				} else {
					m.status = "prompt submitted (empty)"
				}
				m.textInput = ""
				m.cursorPos = 0
				m.inputMode = InputNone
				return m, nil
			case tea.KeyEsc:
				m.textInput = ""
				m.cursorPos = 0
				m.inputMode = InputNone
				m.status = "prompt cancelled"
				return m, nil
			case tea.KeyBackspace, tea.KeyDelete:
				if m.cursorPos > 0 {
					runes := []rune(m.textInput)
					m.textInput = string(runes[:m.cursorPos-1]) + string(runes[m.cursorPos:])
					m.cursorPos--
				}
				return m, nil
			case tea.KeyLeft:
				if m.cursorPos > 0 {
					m.cursorPos--
				}
				return m, nil
			case tea.KeyRight:
				if m.cursorPos < len([]rune(m.textInput)) {
					m.cursorPos++
				}
				return m, nil
			case tea.KeyHome:
				m.cursorPos = 0
				return m, nil
			case tea.KeyEnd:
				m.cursorPos = len([]rune(m.textInput))
				return m, nil
			case tea.KeyRunes:
				runes := []rune(m.textInput)
				insert := string(msg.Runes)
				m.textInput = string(runes[:m.cursorPos]) + insert + string(runes[m.cursorPos:])
				m.cursorPos += len(msg.Runes)
				return m, nil
			}
			return m, nil

		case InputApprove:
			switch key {
			case "y", "Y":
				if m.pendingApproval.Response != nil {
					m.pendingApproval.Response <- core.ApprovalDecision{Allowed: true, Reason: "user approved"}
				}
				m.inputMode = InputNone
				m.pendingApproval = core.ApprovalRequest{}
				m.status = "tool approved"
				return m, nil
			case "n", "N":
				if m.pendingApproval.Response != nil {
					m.pendingApproval.Response <- core.ApprovalDecision{Allowed: false, Reason: "user denied"}
				}
				m.inputMode = InputNone
				m.pendingApproval = core.ApprovalRequest{}
				m.status = "tool denied"
				return m, nil
			case "esc":
				if m.pendingApproval.Response != nil {
					m.pendingApproval.Response <- core.ApprovalDecision{Allowed: false, Reason: "user cancelled"}
				}
				m.inputMode = InputNone
				m.pendingApproval = core.ApprovalRequest{}
				m.status = "approval cancelled"
				return m, nil
			}
			return m, nil

		case InputNone:
			switch key {
			case "tab":
				m.focus = (m.focus + 1) % panelCount
				m.status = "focused " + panelNames[m.focus]
				return m, nil
			case "i", "/":
				m.inputMode = InputPrompt
				m.textInput = ""
				m.cursorPos = 0
				m.status = "PROMPT> type your message, Enter to submit, Esc to cancel"
				return m, nil
			case "ctrl+l":
				m.chat = ""
				m.status = "chat cleared"
				return m, nil
			case "ctrl+r":
				if m.bus != nil {
					event := core.NewEvent(core.EventOracleReview)
					m.bus.Publish(event)
					m.status = "oracle review requested"
				}
				return m, nil
			case "ctrl+g":
				if m.running {
					m.promptQueue = nil
					if m.bus != nil {
						m.bus.Publish(core.NewEvent(core.EventInterrupt))
					}
					m.status = "interrupt sent — agent run cancelled"
				}
				return m, nil
			}

			if m.focus == contextPanel {
				switch key {
				case "j", "down":
					m.moveContextSelection(1)
					return m, nil
				case "k", "up":
					m.moveContextSelection(-1)
					return m, nil
				case "p":
					m.toggleContextPin()
					return m, nil
				case "d":
					m.removeContextItem()
					return m, nil
				case "pgup", "pgdown", "home", "end":
					m.scrollFocused(key)
					return m, nil
				}
			} else {
				switch key {
				case "up", "down", "pgup", "pgdown", "home", "end":
					m.scrollFocused(key)
					return m, nil
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampAllScrolls()
		return m, nil

	case agentEventMsg:
		m.applyEvent(core.AgentEvent(msg))
		m.clampAllScrolls()
		cmds := []tea.Cmd{waitForEvent(m.events)}
		if m.approvalCountdown > 0 {
			cmds = append(cmds, approvalTick())
		}
		return m, tea.Batch(cmds...)

	case tickMsg:
		if m.inputMode != InputApprove || m.approvalCountdown <= 0 {
			return m, nil
		}
		m.approvalCountdown--
		if m.approvalCountdown <= 0 {
			if m.pendingApproval.Response != nil {
				m.pendingApproval.Response <- core.ApprovalDecision{
					Allowed: false,
					Reason:  "approval timed out after countdown",
				}
			}
			m.inputMode = InputNone
			m.pendingApproval = core.ApprovalRequest{}
			m.status = "approval timed out (auto-denied)"
			return m, nil
		}
		m.status = fmt.Sprintf("APPROVE? tool '%s' requires approval [y/n] (%ds)", m.pendingApproval.ToolCall.Name, m.approvalCountdown)
		return m, approvalTick()

	case eventSourceClosedMsg:
		m.sourceClosed = true
		if m.status == "" || m.status == "waiting for agent events" {
			m.status = "event source closed"
		}
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	width := maxInt(m.width, 60)
	height := maxInt(m.height, 20)

	header := renderHeader(width, m.status, m.sourceClosed, panelNames[m.focus], "")
	statusLine := m.renderStatusLine(width)
	footer := m.renderFooter(width)
	inputBar := m.renderInputBar(width)
	inputBarHeight := 0
	if m.inputMode != InputNone {
		inputBarHeight = lipgloss.Height(inputBar)
	}
	statusLineHeight := lipgloss.Height(statusLine)
	bodyHeight := maxInt(height-lipgloss.Height(header)-statusLineHeight-lipgloss.Height(footer)-inputBarHeight, 12)

	if m.showHelp {
		return lipgloss.JoinVertical(lipgloss.Left, header, statusLine, renderHelp(width, bodyHeight), footer)
	}

	if m.inputMode == InputApprove {
		approvalPanel := m.renderApprovalPanel(width, bodyHeight)
		if m.inputMode != InputNone {
			return lipgloss.JoinVertical(lipgloss.Left, header, statusLine, approvalPanel, inputBar, footer)
		}
		return lipgloss.JoinVertical(lipgloss.Left, header, statusLine, approvalPanel, footer)
	}

	leftWidth := width / 2
	rightWidth := width - leftWidth
	topHeight := bodyHeight / 2
	bottomHeight := bodyHeight - topHeight

	top := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderPanel(contextPanel, leftWidth, topHeight),
		m.renderPanel(chatPanel, rightWidth, topHeight),
	)
	bottom := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderPanel(tracePanel, leftWidth, bottomHeight),
		m.renderPanel(toolPanel, rightWidth, bottomHeight),
	)

	if m.inputMode != InputNone {
		return lipgloss.JoinVertical(lipgloss.Left, header, statusLine, top, bottom, inputBar, footer)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, statusLine, top, bottom, footer)
}

func (m Model) renderInputBar(width int) string {
	if m.inputMode == InputNone {
		return ""
	}

	var prefix string
	content := m.textInput
	switch m.inputMode {
	case InputPrompt:
		prefix = "PROMPT> "
	case InputApprove:
		countdown := m.approvalCountdown
		if countdown <= 0 {
			countdown = approvalCountdownSeconds
		}
		prefix = fmt.Sprintf("APPROVE? (%ds) ", countdown)
		if m.pendingApproval.ToolCall.Name != "" {
			prefix += m.pendingApproval.ToolCall.Name
			if m.pendingApproval.Permission.Reason != "" {
				prefix += ": " + m.pendingApproval.Permission.Reason
			}
			prefix += " [y/n] "
		} else {
			prefix += "Allow? [y/n] "
		}
		content = ""
	}

	runes := []rune(content)
	cursorPos := clampInt(m.cursorPos, 0, len(runes))
	var display string
	if m.inputMode == InputPrompt {
		before := string(runes[:cursorPos])
		after := string(runes[cursorPos:])
		cursorChar := " "
		if cursorPos < len(runes) {
			cursorChar = string(runes[cursorPos])
		}
		display = prefix + before + lipgloss.NewStyle().
			Background(lipgloss.Color("15")).
			Foreground(lipgloss.Color("0")).
			Render(cursorChar) + after
	} else {
		display = prefix
	}

	return lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color("237")).
		Foreground(lipgloss.Color("15")).
		Render(truncate(display, width))
}

func approvalTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m Model) renderApprovalPanel(width, height int) string {
	w := maxInt(width-4, 40)
	h := maxInt(height-4, 8)

	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220")).Width(w)
	b.WriteString(titleStyle.Render("TOOL APPROVAL REQUIRED"))
	b.WriteString("\n\n")

	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Tool: "))
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render(m.pendingApproval.ToolCall.Name))
	b.WriteString("\n")

	gradeDisplay, gradeColor := inferApprovalSafety(m.pendingApproval)
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Safety: "))
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(gradeColor).Render(gradeDisplay))
	b.WriteString("\n")

	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Reason: "))
	reason := m.pendingApproval.Permission.Reason
	if reason == "" {
		reason = "tool requires explicit permission"
	}
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(reason))
	b.WriteString("\n")

	inputSummary := approvalInputSummary(m.pendingApproval.ToolCall.Input)
	if inputSummary != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Input:  "))
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("248")).Render(truncate(inputSummary, w-8)))
		b.WriteString("\n")
	}

	paths := extractPaths(m.pendingApproval.ToolCall.Input, m.pendingApproval.ToolCall.Name)
	if len(paths) > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Paths:  "))
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("248")).Render(strings.Join(paths, ", ")))
		b.WriteString("\n")
	}

	rollbackInfo := m.findRollbackArtifact(m.pendingApproval.ToolCall.Name)
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Rollback: "))
	if rollbackInfo != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(rollbackInfo))
	} else {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("will be captured pre-execution"))
	}
	b.WriteString("\n")

	countdown := m.approvalCountdown
	if countdown <= 0 {
		countdown = approvalCountdownSeconds
	}
	countdownStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	if countdown <= 3 {
		countdownStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Render("Timeout: "))
	b.WriteString(countdownStyle.Render(fmt.Sprintf("%ds (default: deny)", countdown)))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("  [y] Approve    "))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("[n] Deny    "))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("[esc] Cancel"))
	b.WriteString("\n")

	content := b.String()
	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("220")).
		Foreground(lipgloss.Color("252")).
		Render(fitText(content, maxInt(w-4, 20), maxInt(h-2, 6)))
}

func inferApprovalSafety(req core.ApprovalRequest) (string, lipgloss.Color) {
	reason := strings.ToLower(req.Permission.Reason)
	toolName := req.ToolCall.Name

	if strings.Contains(reason, "destructive") || strings.Contains(reason, "rm ") || strings.Contains(reason, "sudo") {
		return string(core.SafetyDestructive), lipgloss.Color("196")
	}
	if toolName == "shell" || toolName == "run_test" {
		return string(core.SafetyShellMutation), lipgloss.Color("220")
	}
	if toolName == "write_file" || toolName == "apply_patch" {
		return string(core.SafetyWorkspaceMutation), lipgloss.Color("220")
	}
	if toolName == "rg" || toolName == "read_file" || toolName == "git_status" {
		return string(core.SafetyReadOnly), lipgloss.Color("42")
	}
	switch req.Permission.Behavior {
	case core.PermissionAllow:
		return string(core.SafetyReadOnly), lipgloss.Color("42")
	case core.PermissionDeny:
		return string(core.SafetyDestructive), lipgloss.Color("196")
	default:
		return string(core.SafetyShellMutation), lipgloss.Color("220")
	}
}

func approvalInputSummary(input core.ToolInput) string {
	if input == nil || len(input) == 0 {
		return ""
	}
	var parts []string
	if cmd, ok := input["command"]; ok {
		parts = append(parts, fmt.Sprintf("command=%q", truncateAny(cmd, 60)))
	}
	if path, ok := input["path"]; ok {
		parts = append(parts, fmt.Sprintf("path=%q", truncateAny(path, 40)))
	}
	if patch, ok := input["patch"]; ok {
		parts = append(parts, fmt.Sprintf("patch=%d bytes", len(fmt.Sprint(patch))))
	}
	if content, ok := input["content"]; ok {
		parts = append(parts, fmt.Sprintf("content=%d bytes", len(fmt.Sprint(content))))
	}
	if pattern, ok := input["pattern"]; ok {
		parts = append(parts, fmt.Sprintf("pattern=%q", truncateAny(pattern, 40)))
	}
	if len(parts) == 0 {
		count := 0
		for k, v := range input {
			if count >= 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s=%q", k, truncateAny(v, 30)))
			count++
		}
	}
	return strings.Join(parts, ", ")
}

func extractPaths(input core.ToolInput, toolName string) []string {
	if input == nil {
		return nil
	}
	var paths []string
	if p, ok := input["path"]; ok {
		paths = append(paths, fmt.Sprint(p))
	}
	if toolName == "shell" {
		if cmd, ok := input["command"]; ok {
			for _, part := range strings.Fields(fmt.Sprint(cmd)) {
				if strings.Contains(part, "/") && !strings.HasPrefix(part, "-") {
					paths = append(paths, part)
				}
			}
		}
	}
	return paths
}

func (m Model) findRollbackArtifact(toolName string) string {
	for i := len(m.tools) - 1; i >= 0; i-- {
		if m.tools[i].Name == toolName && m.tools[i].RollbackArtifactID != "" {
			return m.tools[i].RollbackArtifactID
		}
	}
	return ""
}

func truncateAny(v any, max int) string {
	s := fmt.Sprint(v)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func waitForEvent(events <-chan core.AgentEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return eventSourceClosedMsg{}
		}
		return agentEventMsg(event)
	}
}

func (m *Model) applyEvent(event core.AgentEvent) {
	// Clear last error on any successful (non-error) event.
	if event.Type != core.EventError {
		m.lastError = ""
	}
	switch event.Type {
	case core.EventMessageDelta:
		m.chat += event.Message
		m.status = "receiving assistant output"
		if m.chatAutoScroll {
			m.scroll[chatPanel] = m.maxPanelScroll(chatPanel)
		}
	case core.EventContextUpdate:
		if event.Context != nil {
			m.context = *event.Context
			m.hasContext = true
		}
		m.status = "context map updated"
	case core.EventTraceUpdate:
		if event.Trace != nil {
			m.upsertTrace(*event.Trace)
			m.status = "agent trace updated"
		}
	case core.EventToolStart:
		m.tools = append(m.tools, toolRun{
			Name:      fallback(event.ToolName, "tool"),
			Status:    "running",
			Detail:    event.Message,
			StartedAt: eventTime(event),
		})
		m.status = "tool started: " + fallback(event.ToolName, "tool")
	case core.EventToolResult:
		m.finishTool(event)
		m.status = "tool finished: " + fallback(event.ToolName, "tool")
	case core.EventObservation:
		if event.Observation != nil {
			m.notes = append(m.notes, event.Observation.Summary)
			m.status = "observation recorded"
			if event.Observation.RollbackArtifactID != "" {
				toolName := fallback(event.ToolName, "tool")
				for i := len(m.tools) - 1; i >= 0; i-- {
					if m.tools[i].Name == toolName {
						m.tools[i].RollbackArtifactID = event.Observation.RollbackArtifactID
						break
					}
				}
			}
		}
	case core.EventCostUpdate:
		if event.Cost != nil {
			m.cost = *event.Cost
			m.hasCost = true
			m.status = "cost updated"
		}
	case core.EventRiskUpdate:
		m.status = fallback(event.Message, "risk updated")
	case core.EventError:
		msg := fallback(event.Err, event.Message)
		m.lastError = msg
		recovery := errorRecoveryAction(msg)
		note := "error: " + msg
		if recovery != "" {
			note += " — " + recovery
		}
		m.notes = append(m.notes, note)
		m.running = false
		m.status = "error: " + msg
	case core.EventAgentStarted:
		m.running = true
		m.lastError = ""
	case core.EventDone:
		m.running = false
		m.status = fallback(event.Message, "agent complete")
		if len(m.promptQueue) > 0 {
			next := m.promptQueue[0]
			m.promptQueue = m.promptQueue[1:]
			m.chat += "\nuser> " + next
			if m.bus != nil {
				ev := core.NewEvent(core.EventUserPrompt)
				ev.Message = next
				m.bus.Publish(ev)
			}
			m.running = true
			m.status = fmt.Sprintf("dequeued prompt (%d remaining)", len(m.promptQueue))
		}
	case core.EventApprovalNeeded:
		if event.Approval != nil {
			m.inputMode = InputApprove
			m.pendingApproval = *event.Approval
			m.textInput = ""
			m.cursorPos = 0
			m.approvalCountdown = approvalCountdownSeconds
			m.approvalCountdownMax = approvalCountdownSeconds
			m.status = fmt.Sprintf("APPROVE? tool '%s' requires approval [y/n] (%ds)", event.Approval.ToolCall.Name, approvalCountdownSeconds)
		}
	default:
		if event.Message != "" {
			m.notes = append(m.notes, event.Message)
		}
		m.status = "received " + string(event.Type)
	}
}

func (m *Model) moveContextSelection(delta int) {
	if !m.hasContext || len(m.context.Items) == 0 {
		return
	}
	m.selectedContextItem += delta
	if m.selectedContextItem < 0 {
		m.selectedContextItem = len(m.context.Items) - 1
	} else if m.selectedContextItem >= len(m.context.Items) {
		m.selectedContextItem = 0
	}
}

func (m *Model) toggleContextPin() {
	if m.bus == nil || !m.hasContext {
		return
	}
	idx := m.selectedContextItem
	if idx < 0 || idx >= len(m.context.Items) {
		return
	}
	item := m.context.Items[idx]
	eventType := core.EventContextUnpin
	if !item.Pinned {
		eventType = core.EventContextPin
	}
	ev := core.NewEvent(eventType)
	ev.Message = item.ID
	m.bus.Publish(ev)
	m.status = fmt.Sprintf("toggle pin for %s", fallback(item.Title, item.ID))
}

func (m *Model) removeContextItem() {
	if m.bus == nil || !m.hasContext {
		return
	}
	idx := m.selectedContextItem
	if idx < 0 || idx >= len(m.context.Items) {
		return
	}
	item := m.context.Items[idx]
	if item.Pinned {
		m.status = fmt.Sprintf("cannot remove pinned item %s", fallback(item.Title, item.ID))
		return
	}
	ev := core.NewEvent(core.EventContextRemove)
	ev.Message = item.ID
	m.bus.Publish(ev)
	m.status = fmt.Sprintf("remove item %s", fallback(item.Title, item.ID))
}

func (m *Model) upsertTrace(step core.TraceStep) {
	if step.ID != "" {
		if index, ok := m.traceIndex[step.ID]; ok {
			m.trace[index] = step
			return
		}
		m.traceIndex[step.ID] = len(m.trace)
	}
	m.trace = append(m.trace, step)
}

func (m *Model) finishTool(event core.AgentEvent) {
	name := fallback(event.ToolName, "tool")
	for i := len(m.tools) - 1; i >= 0; i-- {
		if m.tools[i].Name == name && m.tools[i].Status == "running" {
			m.tools[i].Status = "done"
			m.tools[i].Detail = fallback(event.Message, event.Err)
			m.tools[i].EndedAt = eventTime(event)
			return
		}
	}
	m.tools = append(m.tools, toolRun{
		Name:    name,
		Status:  "done",
		Detail:  fallback(event.Message, event.Err),
		EndedAt: eventTime(event),
	})
}

func (m Model) renderPanel(panel focusPanel, width, height int) string {
	title := panelNames[panel]
	contentWidth := maxInt(width-2, 18)
	contentHeight := maxInt(height-2, 4)
	viewportHeight := maxInt(contentHeight-1, 1)

	borderColor := lipgloss.Color("240")
	titleColor := lipgloss.Color("250")
	if m.focus == panel {
		borderColor = lipgloss.Color("39")
		titleColor = lipgloss.Color("15")
	}

	lines := m.panelLines(panel, contentWidth)
	offset := clampInt(m.scroll[panel], 0, maxScrollForLines(len(lines), viewportHeight))
	body := lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render(title) + "\n" +
		scrollText(lines, offset, viewportHeight)

	return lipgloss.NewStyle().
		Width(contentWidth).
		Height(contentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Foreground(lipgloss.Color("252")).
		Render(body)
}

func (m *Model) scrollFocused(key string) {
	panel := m.focus
	_, viewportHeight := m.panelViewport(panel)
	page := maxInt(viewportHeight-1, 1)
	maxScroll := m.maxPanelScroll(panel)

	if panel == chatPanel {
		switch key {
		case "up", "pgup", "home":
			m.chatAutoScroll = false
		case "end":
			m.chatAutoScroll = true
		}
	}

	switch key {
	case "up":
		m.scroll[panel]--
	case "down":
		m.scroll[panel]++
	case "pgup":
		m.scroll[panel] -= page
	case "pgdown":
		m.scroll[panel] += page
	case "home":
		m.scroll[panel] = 0
	case "end":
		m.scroll[panel] = maxScroll
	}
	m.scroll[panel] = clampInt(m.scroll[panel], 0, maxScroll)
}

func (m *Model) clampAllScrolls() {
	for panel := focusPanel(0); panel < panelCount; panel++ {
		m.scroll[panel] = clampInt(m.scroll[panel], 0, m.maxPanelScroll(panel))
	}
}

func (m Model) maxPanelScroll(panel focusPanel) int {
	width, height := m.panelViewport(panel)
	return maxScrollForLines(len(m.panelLines(panel, width)), height)
}

func (m Model) panelViewport(panel focusPanel) (int, int) {
	panelWidth, panelHeight := m.panelSize(panel)
	contentWidth := maxInt(panelWidth-2, 18)
	contentHeight := maxInt(panelHeight-2, 4)
	return contentWidth, maxInt(contentHeight-1, 1)
}

func (m Model) panelSize(panel focusPanel) (int, int) {
	width := maxInt(m.width, 60)
	height := maxInt(m.height, 20)

	header := renderHeader(width, m.status, m.sourceClosed, panelNames[m.focus], "")
	statusLine := m.renderStatusLine(width)
	footer := m.renderFooter(width)
	inputBarHeight := 0
	if m.inputMode != InputNone {
		inputBarHeight = 1
	}
	bodyHeight := maxInt(height-lipgloss.Height(header)-lipgloss.Height(statusLine)-lipgloss.Height(footer)-inputBarHeight, 12)

	leftWidth := width / 2
	rightWidth := width - leftWidth
	topHeight := bodyHeight / 2
	bottomHeight := bodyHeight - topHeight

	switch panel {
	case contextPanel:
		return leftWidth, topHeight
	case chatPanel:
		return rightWidth, topHeight
	case tracePanel:
		return leftWidth, bottomHeight
	case toolPanel:
		return rightWidth, bottomHeight
	default:
		return leftWidth, topHeight
	}
}

func (m Model) scrollPosition(panel focusPanel) string {
	width, height := m.panelViewport(panel)
	total := maxInt(len(m.panelLines(panel, width)), 1)
	offset := clampInt(m.scroll[panel], 0, maxScrollForLines(total, height))
	bottom := minInt(offset+height, total)
	return fmt.Sprintf("%d-%d/%d", offset+1, bottom, total)
}

func (m Model) panelLines(panel focusPanel, width int) []string {
	lines := wrapText(m.panelContent(panel), width)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m Model) panelContent(panel focusPanel) string {
	switch panel {
	case contextPanel:
		return m.contextContent()
	case chatPanel:
		if strings.TrimSpace(m.chat) == "" {
			return "waiting for message_delta events"
		}
		return strings.TrimRight(m.chat, "\n")
	case tracePanel:
		return m.traceContent()
	case toolPanel:
		return m.toolContent()
	default:
		return ""
	}
}

func (m Model) contextContent() string {
	if !m.hasContext {
		return "waiting for context_update events"
	}

	used := m.context.UsedTokens
	window := maxInt(m.context.WindowTokens, 1)
	pct := used * 100 / window
	var b strings.Builder

	tokenStyle := tokenBudgetStyle(pct)
	fmt.Fprintf(&b, "%s\n", tokenStyle.Render(fmt.Sprintf("tokens: %d / %d (%d%%)", used, window, pct)))
	fmt.Fprintf(&b, "pollution risk: %s\n", fallback(m.context.PollutionRisk, "unknown"))

	for _, tier := range []core.ContextTier{core.TierNear, core.TierAnchor, core.TierArtifact} {
		fmt.Fprintf(&b, "\n%s\n", strings.ToUpper(string(tier)))
		count := 0
		for _, item := range m.context.Items {
			if item.Tier != tier {
				continue
			}
			count++
			pin := " "
			if item.Pinned {
				pin = "*"
			}
			fmt.Fprintf(&b, "%s %5d %s\n", pin, item.TokenEstimate, fallback(item.Title, item.ID))
			if item.ReplacedBy != "" {
				fmt.Fprintf(&b, "  (replaced by: %s)\n", item.ReplacedBy)
			}
			if item.Reason != "" {
				fmt.Fprintf(&b, "  %s\n", item.Reason)
			}
			if item.SelectionReason != "" {
				fmt.Fprintf(&b, "  (selected: %s)\n", item.SelectionReason)
			}
		}
		if count == 0 {
			b.WriteString("  empty\n")
		}
	}

	// Compression lineage.
	if len(m.context.CompressionRecords) > 0 {
		fmt.Fprintf(&b, "\n%s\n", "COMPRESSION LINEAGE")
		for _, rec := range m.context.CompressionRecords {
			saved := rec.TokensBefore - rec.TokensAfter
			if saved < 0 {
				saved = 0
			}
			fmt.Fprintf(&b, "[%s]\n", rec.ID)
			fmt.Fprintf(&b, "  compressed from: %s\n", strings.Join(rec.SourceIDs, ", "))
			fmt.Fprintf(&b, "  summary: %s\n", rec.Summary)
			fmt.Fprintf(&b, "  tokens saved: %d (%d -> %d)\n", saved, rec.TokensBefore, rec.TokensAfter)
			if rec.Reason != "" {
				fmt.Fprintf(&b, "  reason: %s\n", rec.Reason)
			}
		}
	}

	// Context focus summary with selection evidence.
	if len(m.context.Items) > 0 {
		fmt.Fprintf(&b, "\n%s\n", "CONTEXT FOCUS")
		for _, item := range m.context.Items {
			pin := " "
			if item.Pinned {
				pin = "[P]"
			}
			fmt.Fprintf(&b, "%s %s (%s) %d tok", pin, fallback(item.Title, item.ID), item.Tier, item.TokenEstimate)
			if item.SelectionReason != "" {
				fmt.Fprintf(&b, " — %s", item.SelectionReason)
			}
			b.WriteString("\n")
			if item.ReplacedBy != "" {
				fmt.Fprintf(&b, "    replaced by: %s\n", item.ReplacedBy)
			}
			// Show evidence reason for why this item was selected.
			evidenceReason := m.contextItemEvidence(item)
			if evidenceReason != "" {
				fmt.Fprintf(&b, "    %s\n", evidenceReason)
			}
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m Model) traceContent() string {
	if len(m.trace) == 0 && len(m.notes) == 0 {
		return "waiting for trace_update events"
	}

	var b strings.Builder

	// Current goal display: show the latest trace step's Goal as cyan text.
	if len(m.trace) > 0 {
		latest := m.trace[len(m.trace)-1]
		if latest.Goal != "" {
			goalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
			fmt.Fprintf(&b, "%s\n", goalStyle.Render("Goal: "+latest.Goal))
			b.WriteString("\n")
		}
	}

	// Current plan display: extract plan steps with Stage="plan" and show as numbered list.
	var planSteps []string
	for _, step := range m.trace {
		if step.Stage == core.StagePlan && step.Plan != "" {
			planSteps = append(planSteps, step.Plan)
		}
	}
	if len(planSteps) > 0 {
		planStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
		fmt.Fprintf(&b, "%s\n", planStyle.Render("Plan:"))
		for i, ps := range planSteps {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, ps)
		}
		b.WriteString("\n")
	}

	for i, step := range m.trace {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s] %s\n", fallback(string(step.Status), "pending"), fallback(step.Goal, step.ID))
		writeDetail(&b, "plan", step.Plan)
		writeDetail(&b, "action", step.Action)
		writeDetail(&b, "observe", step.Observation)
		writeDetail(&b, "risk", step.Risk)
		writeDetail(&b, "revise", step.Revision)
	}
	for _, note := range m.notes {
		if note == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) toolContent() string {
	var b strings.Builder
	if m.hasCost {
		fmt.Fprintf(&b, "tokens: in %d / out %d / total %d\n", m.cost.InputTokens, m.cost.OutputTokens, m.cost.TotalTokens)
		fmt.Fprintf(&b, "estimate: $%.4f\n\n", m.cost.EstimatedUSD)
	}

	if len(m.tools) == 0 {
		b.WriteString("waiting for tool_start events")
		return b.String()
	}

	for i, tool := range m.tools {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s] %s", tool.Status, tool.Name)
		if !tool.StartedAt.IsZero() {
			fmt.Fprintf(&b, " %s", tool.StartedAt.Format("15:04:05"))
		}
		if !tool.EndedAt.IsZero() {
			fmt.Fprintf(&b, "-%s", tool.EndedAt.Format("15:04:05"))
		}
		b.WriteByte('\n')
		if tool.Detail != "" {
			fmt.Fprintf(&b, "%s\n", tool.Detail)
		}

		// Verification status: show pass/fail for run_test, written/applied for mutation tools.
		if tool.Status == "done" {
			verificationLine := verificationStatus(tool.Name, tool.Detail)
			if verificationLine != "" {
				fmt.Fprintf(&b, "%s\n", verificationLine)
			}
		}

		// Rollback availability: show "[rollback available]" if RollbackArtifactID is set.
		if tool.RollbackArtifactID != "" {
			rollbackStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
			fmt.Fprintf(&b, "%s\n", rollbackStyle.Render("[rollback available]"))
			fmt.Fprintf(&b, "rollback: %s\n", tool.RollbackArtifactID)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// verificationStatus returns a colored verification line for a completed tool run.
func verificationStatus(toolName, detail string) string {
	lower := strings.ToLower(detail)
	switch toolName {
	case "run_test":
		if strings.Contains(lower, "fail") || strings.Contains(lower, "error") || strings.Contains(lower, "exit code") {
			failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
			return failStyle.Render("verification: FAIL")
		}
		passStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
		return passStyle.Render("verification: PASS")
	case "write_file":
		if !strings.Contains(lower, "error") {
			statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
			return statusStyle.Render("status: written")
		}
		failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		return failStyle.Render("status: write failed")
	case "apply_patch":
		if !strings.Contains(lower, "error") && !strings.Contains(lower, "fail") {
			statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
			return statusStyle.Render("status: applied")
		}
		failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		return failStyle.Render("status: patch failed")
	}
	return ""
}

// errorRecoveryAction returns a recovery suggestion for a given error message.
func errorRecoveryAction(errMsg string) string {
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "permission denied"):
		return "Check policy.toml or approve the tool"
	case strings.Contains(lower, "tool not found"):
		return "Check tool name"
	case strings.Contains(lower, "max steps reached"):
		return "Try: increase MIMO_MAX_STEPS or simplify the task"
	case strings.Contains(lower, "approval timed out"):
		return "Respond faster or increase timeout"
	}
	return ""
}

// contextItemEvidence returns a reason string explaining why a context item
// is in the current context (e.g., referenced by a tool, pinned, oracle promoted, or compressed).
func (m Model) contextItemEvidence(item core.ContextItem) string {
	if item.Pinned {
		return "evidence: pinned"
	}
	if item.SelectionReason != "" {
		return "evidence: " + item.SelectionReason
	}
	// Check if item is referenced by a recent observation (tool name in notes).
	for i := len(m.notes) - 1; i >= 0 && i >= len(m.notes)-5; i-- {
		if strings.Contains(m.notes[i], item.ID) || strings.Contains(m.notes[i], fallback(item.Title, "")) {
			return "evidence: referenced by recent observation"
		}
	}
	// Check if item came from compression.
	for _, rec := range m.context.CompressionRecords {
		for _, srcID := range rec.SourceIDs {
			if srcID == item.ID {
				return fmt.Sprintf("evidence: compressed from: %d items", len(rec.SourceIDs))
			}
		}
	}
	// Check if item source is oracle.
	if strings.Contains(strings.ToLower(item.Source), "oracle") {
		return "evidence: oracle promoted"
	}
	return ""
}

func renderHeader(width int, status string, closed bool, focusName, scroll string) string {
	state := "live"
	if closed {
		state = "closed"
	}
	line := fmt.Sprintf("MiMo Value Amplifier TUI | source %s | focus %s | scroll %s | %s", state, focusName, scroll, fallback(status, "ready"))
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("236")).
		Render(truncate(line, width))
}

func (m Model) renderFooter(width int) string {
	mode := "real"
	if m.mockMode {
		mode = "mock"
	}
	modelInfo := fmt.Sprintf("model: %s (%s)", m.modelName, mode)

	totalTokens := 0
	if m.hasCost {
		totalTokens = m.cost.TotalTokens
	}
	stepInfo := fmt.Sprintf("Step %d", len(m.trace))
	if m.totalSteps > 0 {
		stepInfo = fmt.Sprintf("Step %d/%d", len(m.trace), m.totalSteps)
	}
	tokenInfo := fmt.Sprintf("Tokens: ~%s | %s", formatNumber(totalTokens), stepInfo)

	// Risk level display.
	riskLabel, riskColor := m.riskLevel()
	riskDisplay := lipgloss.NewStyle().Foreground(riskColor).Render("Risk: " + riskLabel)

	var controls string
	if m.showHelp {
		controls = "? close help | esc close | q quit"
	} else {
		controls = "tab focus | i/ prompt | ctrl+l clear | ctrl+r oracle | ? help | up/down scroll | pgup/pgdn | home/end | q quit"
	}

	left := modelInfo + " | " + tokenInfo + " | "
	right := " | " + riskDisplay
	if m.lastError != "" {
		errText := "ERR: " + m.lastError
		right += " | " + lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(errText)
	}

	avail := width - lipgloss.Width(left) - lipgloss.Width(right)
	if avail < 10 {
		avail = 10
	}
	line := left + truncate(controls, avail) + right

	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("248")).
		Render(truncate(line, width))
}

// riskLevel calculates the current risk level from context pollution,
// pending approvals, and recent errors. Returns label and color.
func (m Model) riskLevel() (string, lipgloss.Color) {
	score := 0

	// Context pollution level contributes to risk.
	switch strings.ToLower(m.context.PollutionRisk) {
	case "high":
		score += 3
	case "medium":
		score += 2
	case "low", "unknown":
		score += 0
	}

	// Pending approval contributes to risk.
	if m.inputMode == InputApprove {
		score += 2
	}

	// Recent error contributes to risk.
	if m.lastError != "" {
		score += 2
	}

	// Recent failed tools contribute to risk.
	for i := len(m.tools) - 1; i >= 0 && i >= len(m.tools)-3; i-- {
		if m.tools[i].Status == "failed" {
			score += 1
		}
	}

	switch {
	case score >= 4:
		return "high", lipgloss.Color("196")
	case score >= 2:
		return "medium", lipgloss.Color("220")
	default:
		return "low", lipgloss.Color("42")
	}
}

func (m Model) renderStatusLine(width int) string {
	modelName := fallback(m.modelName, "mimo-v2.5-pro")

	stepInfo := fmt.Sprintf("Step %d", len(m.trace))
	if m.totalSteps > 0 {
		stepInfo = fmt.Sprintf("Step %d/%d", len(m.trace), m.totalSteps)
	}

	var state string
	switch {
	case m.running:
		state = "running"
	case m.sourceClosed:
		state = "closed"
	default:
		state = "idle"
	}

	totalTokens := 0
	if m.hasCost {
		totalTokens = m.cost.TotalTokens
	}

	line := fmt.Sprintf("%s | %s | %s | Tokens: ~%s", modelName, stepInfo, state, formatNumber(totalTokens))

	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("238")).
		Render(truncate(line, width))
}

func formatNumber(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	var result []rune
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, c)
	}
	return string(result)
}

func renderHelp(width, height int) string {
	content := lipgloss.NewStyle().
		Width(maxInt(width-4, 20)).
		Height(maxInt(height-2, 8)).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("39")).
		Foreground(lipgloss.Color("252")).
		Render(fitText(helpContent(), maxInt(width-6, 18), maxInt(height-4, 6)))

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

func helpContent() string {
	return strings.TrimSpace(`
Navigation
  tab: focus next panel
  j/k: move context selection (context panel)
  up/down: scroll focused panel
  pgup/pgdn: scroll focused panel by page
  home/end: jump focused panel

Input
  i or /: enter prompt input mode
  Enter: submit prompt
  Esc: cancel input / close help

Context
  p: toggle pin on selected context item
  d: delete selected context item
  ctrl+r: request context oracle review

Approval
  y: approve tool execution
  n: deny tool execution
  esc: cancel approval

Control
  ctrl+c or q: quit
  ctrl+g: interrupt running agent
  ctrl+l: clear chat display
  ?: toggle help

Panels
  Context Map: evidence and context budget by tier
  Chat Stream: assistant output deltas
  Agent Trace: plan, action, risk, revision, and evidence notes
  Tool Cockpit: tool runs, timing, and cost
`)
}

func tokenBudgetStyle(pct int) lipgloss.Style {
	switch {
	case pct < 70:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case pct < 90:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	}
}

func fitText(text string, width, height int) string {
	lines := wrapText(text, width)
	if len(lines) > height {
		lines = append(lines[:maxInt(height-1, 0)], "...")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func scrollText(lines []string, offset, height int) string {
	if height <= 0 {
		return ""
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	offset = clampInt(offset, 0, maxScrollForLines(len(lines), height))
	end := minInt(offset+height, len(lines))
	visible := append([]string{}, lines[offset:end]...)
	for len(visible) < height {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n")
}

func wrapText(text string, width int) []string {
	width = maxInt(width, 1)
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, wrapLine(line, width)...)
	}
	return lines
}

func wrapLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}

	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	current := words[0]
	if lipgloss.Width(current) > width {
		chunks := splitLongWord(current, width)
		lines = append(lines, chunks[:len(chunks)-1]...)
		current = chunks[len(chunks)-1]
	}

	for _, word := range words[1:] {
		if lipgloss.Width(word) > width {
			if current != "" {
				lines = append(lines, current)
			}
			chunks := splitLongWord(word, width)
			lines = append(lines, chunks[:len(chunks)-1]...)
			current = chunks[len(chunks)-1]
			continue
		}
		next := current + " " + word
		if lipgloss.Width(next) <= width {
			current = next
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func splitLongWord(word string, width int) []string {
	width = maxInt(width, 1)
	var chunks []string
	var b strings.Builder
	for _, r := range word {
		next := b.String() + string(r)
		if lipgloss.Width(next) > width && b.Len() > 0 {
			chunks = append(chunks, b.String())
			b.Reset()
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		chunks = append(chunks, b.String())
	}
	return chunks
}

func truncate(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 3 {
		return strings.Repeat(".", maxInt(width, 0))
	}
	var b strings.Builder
	for _, r := range text {
		next := b.String() + string(r)
		if lipgloss.Width(next) > width-3 {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "..."
}

func writeDetail(b *strings.Builder, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "  %s: %s\n", label, value)
}

func eventTime(event core.AgentEvent) time.Time {
	if event.Time.IsZero() {
		return time.Now()
	}
	return event.Time
}

func fallback(value, fallbackValue string) string {
	if value != "" {
		return value
	}
	return fallbackValue
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampInt(value, minValue, maxValue int) int {
	if maxValue < minValue {
		return minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func maxScrollForLines(lineCount, viewportHeight int) int {
	if lineCount <= viewportHeight {
		return 0
	}
	return lineCount - viewportHeight
}
