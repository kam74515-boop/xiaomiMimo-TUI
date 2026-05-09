package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

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
	assistantStreaming  bool
	trace               []core.TraceStep
	traceIndex          map[string]int
	tools               []toolRun
	activities          []core.ActivityEvent
	activityIndex       map[string]int
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

	cursorState *terminalCursorState

	inputMode       InputMode
	textInput       string
	cursorPos       int
	pendingApproval core.ApprovalRequest

	approvalCountdown    int
	approvalCountdownMax int

	promptQueue []string

	totalSteps int

	loadingFrame int
}

type agentEventMsg core.AgentEvent

type tickMsg struct{}

type pulseMsg struct{}

type eventSourceClosedMsg struct{}

const defaultApprovalCountdownSeconds = 30

type terminalCursorState struct {
	mu       sync.RWMutex
	sequence string
}

func (s *terminalCursorState) Set(sequence string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sequence = sequence
	s.mu.Unlock()
}

func (s *terminalCursorState) Sequence() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sequence
}

func (s *terminalCursorState) Clear() {
	s.Set("")
}

type cursorTrackingWriter struct {
	io.Writer
	reader io.Reader
	fd     func() uintptr
	state  *terminalCursorState
}

var _ interface {
	io.ReadWriteCloser
	Fd() uintptr
} = cursorTrackingWriter{}

func (w cursorTrackingWriter) Read(p []byte) (int, error) {
	if w.reader == nil {
		return 0, io.EOF
	}
	return w.reader.Read(p)
}

func (w cursorTrackingWriter) Close() error {
	return nil
}

func (w cursorTrackingWriter) Fd() uintptr {
	if w.fd == nil {
		return 0
	}
	return w.fd()
}

func (w cursorTrackingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err != nil || n == 0 || w.state == nil {
		return n, err
	}
	if bytes.Contains(p, []byte(ansi.ResetAltScreenSaveCursorMode)) {
		w.state.Clear()
		return n, nil
	}
	if sequence := w.state.Sequence(); sequence != "" {
		if _, writeErr := w.Writer.Write([]byte(sequence)); writeErr != nil {
			return n, writeErr
		}
	}
	return n, nil
}

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
		focus:               chatPanel,
		selectedContextItem: -1,
		traceIndex:          make(map[string]int),
		activityIndex:       make(map[string]int),
		status:              "waiting for agent events",
		chatAutoScroll:      true,
	}
}

func Run(events <-chan core.AgentEvent, bus *core.Bus, modelName string, mockMode bool) error {
	cursorState := &terminalCursorState{}
	m := NewModel(events, bus)
	m.cursorState = cursorState
	m.modelName = modelName
	m.mockMode = mockMode
	if mockMode {
		m.appendTranscriptBlock("SYSTEM", "MOCK MODE - set MIMO_API_KEY for real MiMo")
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(cursorTrackingWriter{
		Writer: os.Stdout,
		reader: os.Stdout,
		fd:     os.Stdout.Fd,
		state:  cursorState,
	})).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), pulseTick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		if key == "ctrl+c" {
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
						m.appendUserPrompt(m.textInput, true)
						m.promptQueue = append(m.promptQueue, m.textInput)
						m.status = fmt.Sprintf("prompt queued (position %d)", len(m.promptQueue))
					} else {
						m.appendUserPrompt(m.textInput, false)
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
			switch msg.Type {
			case tea.KeyEnter:
				return m.submitApprovalInput()
			case tea.KeyEsc:
				return m.finishApproval(false, "user cancelled", "approval cancelled"), nil
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
				m.status = "approval response typed; press Enter to confirm"
				return m, nil
			}
			m.status = "approval mode: type y/n then Enter, or Esc to deny"
			return m, nil

		case InputNone:
			switch key {
			case "tab":
				m.focus = (m.focus + 1) % panelCount
				m.status = "focused " + panelNames[m.focus]
				return m, nil
			case "shift+tab":
				m.focus = (m.focus + panelCount - 1) % panelCount
				m.status = "focused " + panelNames[m.focus]
				return m, nil
			case "/":
				m.inputMode = InputPrompt
				m.textInput = ""
				m.cursorPos = 0
				m.status = "PROMPT> type your message, Enter to submit, Esc to cancel"
				return m, nil
			case "ctrl+l":
				m.chat = ""
				m.assistantStreaming = false
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

			if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
				m.inputMode = InputPrompt
				m.textInput = string(msg.Runes)
				m.cursorPos = len(msg.Runes)
				m.status = "PROMPT> type your message, Enter to submit, Esc to cancel"
				return m, nil
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
		m.status = fmt.Sprintf("approval required for %s: type y/n then Enter (%ds)", m.pendingApproval.ToolCall.Name, m.approvalCountdown)
		return m, approvalTick()

	case pulseMsg:
		if m.running || m.assistantStreaming {
			m.loadingFrame++
		}
		return m, pulseTick()

	case eventSourceClosedMsg:
		m.sourceClosed = true
		if m.status == "" || m.status == "waiting for agent events" {
			m.status = "event source closed"
		}
		return m, nil
	}
	return m, nil
}

func (m Model) submitApprovalInput() (Model, tea.Cmd) {
	answer := strings.ToLower(strings.TrimSpace(m.textInput))
	switch answer {
	case "", "y", "yes", "approve", "a", "同意", "批准":
		return m.finishApproval(true, "user approved", "tool approved"), nil
	case "n", "no", "deny", "d", "拒绝", "取消":
		return m.finishApproval(false, "user denied", "tool denied"), nil
	default:
		m.status = fmt.Sprintf("invalid approval response %q; type y/n then Enter", answer)
		return m, nil
	}
}

func (m Model) finishApproval(allowed bool, reason, status string) Model {
	if m.pendingApproval.Response != nil {
		m.pendingApproval.Response <- core.ApprovalDecision{Allowed: allowed, Reason: reason}
	}
	m.inputMode = InputNone
	m.pendingApproval = core.ApprovalRequest{}
	m.textInput = ""
	m.cursorPos = 0
	m.status = status
	return m
}

func (m Model) View() string {
	width := maxInt(m.width, 60)
	height := maxInt(m.height, 20)

	header := renderHeader(width, m.status, m.sourceClosed, panelNames[m.focus], "")
	statusLine := m.renderStatusLine(width)
	footer := m.renderFooter(width)
	inputBar := m.renderInputBar(width)
	inputBarHeight := lipgloss.Height(inputBar)
	statusLineHeight := lipgloss.Height(statusLine)
	bodyHeight := maxInt(height-lipgloss.Height(header)-statusLineHeight-lipgloss.Height(footer)-inputBarHeight, 12)

	var view string
	if m.showHelp {
		view = lipgloss.JoinVertical(lipgloss.Left, header, statusLine, renderHelp(width, bodyHeight), footer)
	} else if m.inputMode == InputApprove {
		approvalPanel := m.renderApprovalPanel(width, bodyHeight)
		if m.inputMode != InputNone {
			view = lipgloss.JoinVertical(lipgloss.Left, header, statusLine, approvalPanel, inputBar, footer)
		} else {
			view = lipgloss.JoinVertical(lipgloss.Left, header, statusLine, approvalPanel, footer)
		}
	} else {
		var body string
		if mainWidth, sideWidth := dashboardSplit(width); sideWidth > 0 {
			body = lipgloss.JoinHorizontal(
				lipgloss.Top,
				m.renderTranscript(mainWidth, bodyHeight),
				m.renderDashboardRail(sideWidth, bodyHeight),
			)
		} else if m.focus == chatPanel {
			body = m.renderTranscript(width, bodyHeight)
		} else {
			body = m.renderPanel(m.focus, width, bodyHeight)
		}
		view = lipgloss.JoinVertical(lipgloss.Left, header, statusLine, body, inputBar, footer)
	}

	m.updateTerminalCursor(width, lipgloss.Height(header), statusLineHeight, bodyHeight)
	return view
}

func (m Model) renderInputBar(width int) string {
	prefix := m.inputPrefix()
	content := m.textInput
	switch m.inputMode {
	case InputNone:
		content = ""
	}

	runes := []rune(content)
	var display string
	if m.inputMode == InputPrompt || m.inputMode == InputApprove {
		display = prefix + string(runes)
	} else if m.inputMode == InputNone {
		placeholder := "type a message, / command, tab dashboard"
		display = prefix + lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(placeholder)
	} else {
		display = prefix
	}

	return lipgloss.NewStyle().
		Width(maxInt(width-2, 20)).
		Height(1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(0, 1).
		Foreground(lipgloss.Color("15")).
		Render(truncate(display, maxInt(width-4, 16)))
}

func (m Model) inputPrefix() string {
	switch m.inputMode {
	case InputApprove:
		countdown := m.approvalCountdown
		if countdown <= 0 {
			countdown = m.approvalCountdownMax
		}
		if countdown <= 0 {
			countdown = defaultApprovalCountdownSeconds
		}
		toolName := fallback(m.pendingApproval.ToolCall.Name, "tool")
		return fmt.Sprintf("approval %s (%ds) y/n› ", toolName, countdown)
	default:
		return "› "
	}
}

func (m Model) updateTerminalCursor(width, headerHeight, statusLineHeight, bodyHeight int) {
	if m.cursorState == nil {
		return
	}
	if m.showHelp {
		m.cursorState.Set(ansi.HideCursor)
		return
	}
	inputContentRow := headerHeight + statusLineHeight + bodyHeight + 2
	inputContentCol := m.inputCursorColumn(width)
	m.cursorState.Set(ansi.ShowCursor + ansi.CursorPosition(inputContentCol, inputContentRow))
}

func (m Model) inputCursorColumn(width int) int {
	prefix := m.inputPrefix()
	runes := []rune(m.textInput)
	cursorPos := clampInt(m.cursorPos, 0, len(runes))
	before := string(runes[:cursorPos])
	col := 3 + runewidth.StringWidth(prefix+before)
	return clampInt(col, 3, maxInt(width-2, 3))
}

func approvalTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func pulseTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return pulseMsg{}
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
		countdown = m.approvalCountdownMax
	}
	if countdown <= 0 {
		countdown = defaultApprovalCountdownSeconds
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
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("  type y + Enter to approve    "))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("type n + Enter to deny    "))
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
		m.appendAssistantDelta(event.Message)
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
		toolName := fallback(event.ToolName, "tool")
		if m.hasRunningTool(toolName) {
			m.status = "tool running: " + toolName
			return
		}
		m.tools = append(m.tools, toolRun{
			Name:      toolName,
			Status:    "running",
			Detail:    event.Message,
			StartedAt: eventTime(event),
		})
		m.status = "tool started: " + toolName
	case core.EventToolResult:
		m.finishTool(event)
		m.status = "tool finished: " + fallback(event.ToolName, "tool")
	case core.EventActivityUpdate:
		if event.Activity != nil {
			previous := m.upsertActivity(*event.Activity, event.Time)
			activity := m.activities[m.activityIndexFor(*event.Activity)]
			if activity.Status == core.ActivityFailed && (previous == nil || previous.Status != core.ActivityFailed) {
				m.appendActivityFailureTranscript(activity)
			}
			m.status = fmt.Sprintf("activity %s: %s", activityStatusLabel(activity), activityDisplayName(activity))
		} else {
			m.status = fallback(event.Message, "activity updated")
		}
	case core.EventObservation:
		if event.Observation != nil {
			m.notes = append(m.notes, event.Observation.Summary)
			m.appendObservationTranscriptIfUseful(event.Observation.Summary)
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
		m.assistantStreaming = false
		recovery := errorRecoveryAction(msg)
		note := "error: " + msg
		if recovery != "" {
			note += " — " + recovery
		}
		m.notes = append(m.notes, note)
		m.appendTranscriptBlock("ERROR", note)
		m.running = false
		m.status = "error: " + msg
	case core.EventAgentStarted:
		m.running = true
		m.lastError = ""
		m.assistantStreaming = false
	case core.EventDone:
		m.running = false
		m.assistantStreaming = false
		m.status = fallback(event.Message, "agent complete")
		if len(m.promptQueue) > 0 {
			next := m.promptQueue[0]
			m.promptQueue = m.promptQueue[1:]
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
			timeoutSeconds := event.Approval.TimeoutSeconds
			if timeoutSeconds <= 0 {
				timeoutSeconds = defaultApprovalCountdownSeconds
			}
			m.inputMode = InputApprove
			m.pendingApproval = *event.Approval
			m.textInput = ""
			m.cursorPos = 0
			m.approvalCountdown = timeoutSeconds
			m.approvalCountdownMax = timeoutSeconds
			m.status = fmt.Sprintf("approval required for %s: type y/n then Enter (%ds)", event.Approval.ToolCall.Name, timeoutSeconds)
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
	status := "done"
	if event.Err != "" {
		status = "failed"
	}
	for i := len(m.tools) - 1; i >= 0; i-- {
		if m.tools[i].Name == name && m.tools[i].Status == "running" {
			m.tools[i].Status = status
			m.tools[i].Detail = fallback(event.Message, event.Err)
			m.tools[i].EndedAt = eventTime(event)
			return
		}
	}
	m.tools = append(m.tools, toolRun{
		Name:    name,
		Status:  status,
		Detail:  fallback(event.Message, event.Err),
		EndedAt: eventTime(event),
	})
}

func (m *Model) upsertActivity(activity core.ActivityEvent, eventTime time.Time) *core.ActivityEvent {
	if !eventTime.IsZero() {
		switch activity.Status {
		case core.ActivityPlanned, core.ActivityRunning, core.ActivityBlocked:
			if activity.StartedAt.IsZero() {
				activity.StartedAt = eventTime
			}
		case core.ActivityDone, core.ActivityFailed, core.ActivitySkipped:
			if activity.EndedAt.IsZero() {
				activity.EndedAt = eventTime
			}
		}
	}
	if m.activityIndex == nil {
		m.activityIndex = make(map[string]int)
		for i, existing := range m.activities {
			if existing.ID != "" {
				m.activityIndex[existing.ID] = i
			}
		}
	}
	if activity.ID != "" {
		if index, ok := m.activityIndex[activity.ID]; ok && index >= 0 && index < len(m.activities) {
			previous := m.activities[index]
			merged := mergeActivityEvent(previous, activity)
			m.activities[index] = merged
			return &previous
		}
		m.activityIndex[activity.ID] = len(m.activities)
	}
	m.activities = append(m.activities, activity)
	return nil
}

func mergeActivityEvent(previous, next core.ActivityEvent) core.ActivityEvent {
	if next.ID == "" {
		next.ID = previous.ID
	}
	if next.ParentID == "" {
		next.ParentID = previous.ParentID
	}
	if next.Kind == "" {
		next.Kind = previous.Kind
	}
	if next.Name == "" {
		next.Name = previous.Name
	}
	if next.Title == "" {
		next.Title = previous.Title
	}
	if next.Status == "" {
		next.Status = previous.Status
	}
	if next.Summary == "" {
		next.Summary = previous.Summary
	}
	if next.Detail == "" {
		next.Detail = previous.Detail
	}
	if next.Reason == "" {
		next.Reason = previous.Reason
	}
	if next.ToolName == "" {
		next.ToolName = previous.ToolName
	}
	if next.ServerName == "" {
		next.ServerName = previous.ServerName
	}
	if next.ModelName == "" {
		next.ModelName = previous.ModelName
	}
	if next.Role == "" {
		next.Role = previous.Role
	}
	if len(next.Files) == 0 {
		next.Files = previous.Files
	}
	if len(next.Artifacts) == 0 {
		next.Artifacts = previous.Artifacts
	}
	if next.StartedAt.IsZero() {
		next.StartedAt = previous.StartedAt
	}
	if next.EndedAt.IsZero() {
		next.EndedAt = previous.EndedAt
	}
	return next
}

func (m Model) activityIndexFor(activity core.ActivityEvent) int {
	if activity.ID != "" && m.activityIndex != nil {
		if index, ok := m.activityIndex[activity.ID]; ok && index >= 0 && index < len(m.activities) {
			return index
		}
	}
	return maxInt(len(m.activities)-1, 0)
}

func (m Model) hasRunningTool(name string) bool {
	if len(m.tools) == 0 {
		return false
	}
	last := m.tools[len(m.tools)-1]
	return last.Name == name && last.Status == "running"
}

func (m *Model) appendUserPrompt(prompt string, queued bool) {
	m.assistantStreaming = false
	m.ensureTranscriptGap()
	m.chat += userPromptBlock(prompt, queued)
	m.scrollTranscriptToBottom()
}

func userPromptBlock(prompt string, queued bool) string {
	prompt = strings.TrimRight(prompt, "\n")
	if prompt == "" {
		prompt = "(empty)"
	}
	prefix := "■ "
	if queued {
		prefix = "■ queued "
	}
	lines := strings.Split(prompt, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = prefix + line
			continue
		}
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func (m *Model) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	if !m.assistantStreaming {
		m.ensureTranscriptGap()
		m.chat += "MIMO\n  "
		m.assistantStreaming = true
	}
	m.chat += delta
	m.scrollTranscriptToBottom()
}

func (m *Model) appendToolTranscript(status, name, detail string) {
	m.assistantStreaming = false
	body := compactTranscriptDetail(detail)
	if body == "" {
		body = "running"
	}
	m.appendTranscriptBlock(fmt.Sprintf("TOOL %s [%s]", name, status), body)
	m.scrollTranscriptToBottom()
}

func (m *Model) appendActivityFailureTranscript(activity core.ActivityEvent) {
	m.assistantStreaming = false
	label := fmt.Sprintf("ACTIVITY %s %s [failed]", activityKindLabel(activity.Kind), activityDisplayName(activity))
	m.appendTranscriptBlock(label, activityFailureSummary(activity))
	m.scrollTranscriptToBottom()
}

func activityFailureSummary(activity core.ActivityEvent) string {
	body := compactTranscriptDetail(firstNonEmpty(activity.Summary, activity.Detail, activity.Reason))
	if body == "" {
		body = "background activity failed"
	}
	if activity.Reason != "" && !strings.Contains(body, activity.Reason) {
		body += "\nreason: " + compactTranscriptDetail(activity.Reason)
	}
	body += "\nnext: " + activityRecoveryAction(activity)
	return body
}

func activityRecoveryAction(activity core.ActivityEvent) string {
	lower := strings.ToLower(strings.Join([]string{activity.Summary, activity.Detail, activity.Reason}, " "))
	switch {
	case strings.Contains(lower, "permission") || strings.Contains(lower, "denied") || strings.Contains(lower, "policy") || strings.Contains(lower, "approval"):
		return "check permissions or approve the required action"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return "retry or increase the timeout"
	case strings.Contains(lower, "not found") || strings.Contains(lower, "missing"):
		return "check the configured name or path"
	case strings.Contains(lower, "blocked"):
		return "resolve the blocker, then retry"
	default:
		return "review Activity Timeline details, then retry or skip it"
	}
}

func (m *Model) appendObservationTranscript(summary string) {
	summary = compactTranscriptDetail(summary)
	if summary == "" {
		return
	}
	m.assistantStreaming = false
	m.appendTranscriptBlock("OBSERVATION", summary)
	m.scrollTranscriptToBottom()
}

func (m *Model) appendObservationTranscriptIfUseful(summary string) {
	if !shouldShowObservationInTranscript(summary) {
		return
	}
	m.appendObservationTranscript(summary)
}

func compactTranscriptDetail(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "starting tool ") {
		return "running"
	}
	if strings.Contains(lower, "large payload kept in artifact") || strings.Contains(lower, "remain artifact-backed") {
		return "large output stored as artifact; raw payload kept out of model context"
	}
	if strings.HasPrefix(lower, "artifact ") {
		if idx := strings.Index(text, ":"); idx > 0 {
			return text[:idx] + ": artifact stored"
		}
	}
	return truncate(text, 180)
}

func shouldShowObservationInTranscript(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	lower := strings.ToLower(summary)
	noisyMarkers := []string{
		"artifact ",
		"large payload kept in artifact",
		"remain artifact-backed",
		"tool=",
		" exit=",
		"rg exit=",
		"read_file",
	}
	for _, marker := range noisyMarkers {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	usefulMarkers := []string{
		"agent run cancelled",
		"interrupt requested",
		"oracle review",
		"approval timed out",
		"prompt queued",
		"rollback",
	}
	for _, marker := range usefulMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (m *Model) appendTranscriptBlock(label, body string) {
	m.ensureTranscriptGap()
	label = strings.TrimSpace(label)
	if label == "" {
		label = "EVENT"
	}
	m.chat += label
	body = strings.TrimRight(body, "\n")
	if body != "" {
		m.chat += "\n" + indentTranscriptBody(body)
	}
}

func (m *Model) ensureTranscriptGap() {
	if strings.TrimSpace(m.chat) == "" {
		m.chat = ""
		return
	}
	if strings.HasSuffix(m.chat, "\n\n") {
		return
	}
	if !strings.HasSuffix(m.chat, "\n") {
		m.chat += "\n"
	}
	m.chat += "\n"
}

func (m *Model) scrollTranscriptToBottom() {
	if m.chatAutoScroll {
		m.scroll[chatPanel] = m.maxPanelScroll(chatPanel)
	}
}

func indentTranscriptBody(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderTranscript(width, height int) string {
	w := maxInt(width, 30)
	h := maxInt(height, 6)
	viewportHeight := maxInt(h-1, 1)
	lines := m.panelLines(chatPanel, w)
	offset := clampInt(m.scroll[chatPanel], 0, maxScrollForLines(len(lines), viewportHeight))

	title := m.transcriptTitle(w)
	body := scrollText(lines, offset, viewportHeight)
	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Foreground(lipgloss.Color("252")).
		Render(title + "\n" + body)
}

func (m Model) transcriptTitle(width int) string {
	title := "Transcript"
	if m.assistantStreaming {
		title += " " + m.loadingGlyph() + " streaming"
	} else if m.running {
		title += " " + m.loadingGlyph() + " working"
	}
	title += " | " + m.scrollPosition(chatPanel)
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Render(truncate(title, width))
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
	if panel == chatPanel {
		return maxInt(panelWidth, 18), maxInt(panelHeight-1, 1)
	}
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
	inputBarHeight := lipgloss.Height(m.renderInputBar(width))
	bodyHeight := maxInt(height-lipgloss.Height(header)-lipgloss.Height(statusLine)-lipgloss.Height(footer)-inputBarHeight, 12)

	if m.focus == chatPanel {
		if panel == chatPanel {
			return width, bodyHeight
		}
		side := width / 2
		return side, bodyHeight
	}

	mainWidth, sideWidth := dashboardSplit(width)
	if sideWidth == 0 {
		return width, bodyHeight
	}

	if panel == chatPanel {
		return mainWidth, bodyHeight
	}

	switch panel {
	case contextPanel:
		return sideWidth, bodyHeight
	case chatPanel:
		return mainWidth, bodyHeight
	case tracePanel:
		return sideWidth, bodyHeight
	case toolPanel:
		return sideWidth, bodyHeight
	default:
		return sideWidth, bodyHeight
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
	if panel == chatPanel {
		lines := m.transcriptLines(width)
		if len(lines) == 0 {
			return []string{""}
		}
		return lines
	}
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

func (m Model) transcriptLines(width int) []string {
	content := strings.TrimRight(m.chat, "\n")
	if content == "" {
		return wrapText("waiting for message_delta events", width)
	}
	rawLines := strings.Split(content, "\n")
	lastAssistantHeader := -1
	for i, line := range rawLines {
		if ansi.Strip(line) == "MIMO" {
			lastAssistantHeader = i
		}
	}

	inAssistant := false
	inUser := false
	lines := make([]string, 0, len(rawLines))
	for i, line := range rawLines {
		plain := strings.TrimSpace(ansi.Strip(line))
		switch {
		case plain == "MIMO":
			live := m.assistantStreaming && i == lastAssistantHeader
			lines = append(lines, m.assistantHeader(live))
			inAssistant = true
			inUser = false
		case strings.HasPrefix(plain, "■ "):
			lines = append(lines, styleWrappedLines(line, width, userPromptStyle())...)
			inAssistant = false
			inUser = true
		case isTranscriptControlHeader(plain):
			lines = append(lines, wrapText(line, width)...)
			inAssistant = false
			inUser = false
		case inAssistant && plain != "":
			live := m.assistantStreaming && i > lastAssistantHeader
			lines = append(lines, styleWrappedLines(line, width, m.assistantBodyStyle(live))...)
		case inUser && plain != "":
			lines = append(lines, styleWrappedLines(line, width, userPromptStyle())...)
		default:
			lines = append(lines, wrapText(line, width)...)
		}
	}
	return lines
}

func (m Model) assistantHeader(live bool) string {
	label := "✦ MiMo"
	if live {
		label = fmt.Sprintf("✦ MiMo %s streaming", m.loadingGlyph())
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("75")).
		Bold(true).
		Render(label)
}

func (m Model) assistantBodyStyle(live bool) lipgloss.Style {
	color := lipgloss.Color("252")
	if live {
		color = m.streamAccentColor()
	}
	return lipgloss.NewStyle().Foreground(color)
}

func (m Model) streamAccentColor() lipgloss.Color {
	colors := []lipgloss.Color{"159", "123", "87", "81", "117", "153"}
	return colors[m.loadingFrame%len(colors)]
}

func isTranscriptControlHeader(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "■ ") {
		return true
	}
	for _, prefix := range []string{"SYSTEM", "ERROR", "OBSERVATION", "ACTIVITY ", "TOOL "} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func userPromptStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true)
}

func styleWrappedLines(line string, width int, style lipgloss.Style) []string {
	lines := wrapText(line, width)
	for i, wrapped := range lines {
		if wrapped == "" {
			continue
		}
		lines[i] = style.Render(wrapped)
	}
	return lines
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
		if len(m.activities) == 0 {
			b.WriteString("waiting for tool_start events")
			return b.String()
		}
	} else {
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
	}

	if len(m.activities) > 0 {
		if strings.TrimSpace(b.String()) != "" {
			b.WriteString("\n\n")
		}
		b.WriteString(m.activityTimelineContent())
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) activityTimelineContent() string {
	var b strings.Builder
	b.WriteString("Activity Timeline\n")
	for i, activity := range m.activities {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s] %s %s\n", activityStatusLabel(activity), activityKindLabel(activity.Kind), activityDisplayName(activity))
		if !activity.StartedAt.IsZero() || !activity.EndedAt.IsZero() {
			if !activity.StartedAt.IsZero() {
				fmt.Fprintf(&b, "  start: %s\n", activity.StartedAt.Format("15:04:05"))
			}
			if !activity.EndedAt.IsZero() {
				fmt.Fprintf(&b, "  end: %s\n", activity.EndedAt.Format("15:04:05"))
			}
		}
		switch activity.Kind {
		case core.ActivitySubAgent:
			writeDetail(&b, "goal", firstNonEmpty(activity.Title, activity.Name))
			writeDetail(&b, "step", firstNonEmpty(activity.Summary, activity.Detail))
			writeDetail(&b, "role", activity.Role)
			writeDetail(&b, "model", activity.ModelName)
		case core.ActivityMCP:
			writeDetail(&b, "mcp", firstNonEmpty(activity.ServerName, activity.Name))
			writeDetail(&b, "tool", activity.ToolName)
			writeDetail(&b, "summary", activity.Summary)
			writeDetail(&b, "detail", activity.Detail)
		case core.ActivitySkill:
			writeDetail(&b, "skill", firstNonEmpty(activity.Name, activity.Title))
			writeDetail(&b, "summary", activity.Summary)
			writeDetail(&b, "detail", activity.Detail)
		default:
			writeDetail(&b, "summary", activity.Summary)
			writeDetail(&b, "detail", activity.Detail)
		}
		writeDetail(&b, "reason", activity.Reason)
		if len(activity.Files) > 0 {
			writeDetail(&b, "files", strings.Join(activity.Files, ", "))
		}
		if len(activity.Artifacts) > 0 {
			writeDetail(&b, "artifacts", strings.Join(activity.Artifacts, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) renderDashboardRail(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	gap := 1
	topHeight := maxInt(height/2, 8)
	bottomHeight := maxInt(height-topHeight-gap, 8)
	if topHeight+bottomHeight+gap > height {
		topHeight = height / 2
		bottomHeight = maxInt(height-topHeight-gap, 1)
	}
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderContextViz(width, topHeight),
		strings.Repeat(" ", maxInt(width, 1)),
		m.renderActivityViz(width, bottomHeight),
	)
}

func (m Model) renderContextViz(width, height int) string {
	innerWidth := maxInt(width-4, 24)
	used := m.context.UsedTokens
	window := maxInt(m.context.WindowTokens, 1)
	pct := clampInt(used*100/window, 0, 100)

	nearCount, anchorCount, artifactCount := m.contextTierCounts()
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Render("Context Map"),
		progressBar(pct, innerWidth),
		fmt.Sprintf("%s / %s tokens  %d%%", formatNumber(used), formatNumber(window), pct),
		fmt.Sprintf("Near     %s %2d", smallBar(nearCount, len(m.context.Items), 10), nearCount),
		fmt.Sprintf("Anchor   %s %2d", smallBar(anchorCount, len(m.context.Items), 10), anchorCount),
		fmt.Sprintf("Artifact %s %2d", smallBar(artifactCount, len(m.context.Items), 10), artifactCount),
		"Risk     " + m.visualRiskLabel(),
	}

	for _, item := range m.topContextItems(2) {
		lines = append(lines, truncate("• "+fallback(item.Title, item.ID), innerWidth))
	}

	return dashboardBox("MiMo / 1M", lines, width, height, m.focus == contextPanel)
}

func (m Model) renderActivityViz(width, height int) string {
	innerWidth := maxInt(width-4, 24)
	state := "idle"
	if m.running {
		state = m.loadingGlyph() + " working"
	}
	activityTotal, activityRunning, activityFailed, toolCount, skillCount, mcpCount, subagentCount := m.activityCounts()

	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Render("Agent Cockpit"),
		"State  " + state,
		fmt.Sprintf("Activity %d total / %d running / %d failed", activityTotal, activityRunning, activityFailed),
		fmt.Sprintf("Counts tool %d skill %d mcp %d sub %d", toolCount, skillCount, mcpCount, subagentCount),
		fmt.Sprintf("Trace  %s", smallBar(len(m.trace), maxInt(len(m.trace)+len(m.promptQueue), 1), 14)),
		fmt.Sprintf("Tools  %d total", len(m.tools)),
		"Last   " + truncate(m.lastToolSummary(), maxInt(innerWidth-7, 10)),
	}
	for _, activity := range m.runningSubagents(2) {
		lines = append(lines, truncate("Subagent "+activityDisplayName(activity)+" running", innerWidth))
		if activity.Summary != "" {
			lines = append(lines, truncate("Step: "+activity.Summary, innerWidth))
		}
	}
	if names := m.activityShortNamesForKinds([]core.ActivityKind{core.ActivityMCP, core.ActivitySkill}, 3); len(names) > 0 {
		lines = append(lines, truncate("MCP/Skill "+strings.Join(names, ", "), innerWidth))
	}
	if recent := m.recentActivities(3); len(recent) > 0 {
		lines = append(lines, "Recent activity")
		for _, activity := range recent {
			lines = append(lines, truncate(fmt.Sprintf("- %s %s %s", activityKindTitle(activity.Kind), activityDisplayName(activity), activityStatusLabel(activity)), innerWidth))
		}
	}
	if len(m.trace) > 0 {
		latest := m.trace[len(m.trace)-1]
		lines = append(lines, "Stage  "+fallback(string(latest.Stage), string(latest.Status)))
		if latest.Goal != "" {
			lines = append(lines, truncate("Goal   "+latest.Goal, innerWidth))
		}
	}
	if m.hasCost {
		lines = append(lines, fmt.Sprintf("Tokens %s", formatNumber(m.cost.TotalTokens)))
	}
	if len(m.promptQueue) > 0 {
		lines = append(lines, fmt.Sprintf("Queue  %d", len(m.promptQueue)))
	}

	return dashboardBox("Trace / Tools", lines, width, height, m.focus == tracePanel || m.focus == toolPanel)
}

func dashboardBox(title string, lines []string, width, height int, focused bool) string {
	border := lipgloss.Color("238")
	titleColor := lipgloss.Color("75")
	if focused {
		border = lipgloss.Color("39")
		titleColor = lipgloss.Color("15")
	}
	innerWidth := maxInt(width-4, 20)
	innerHeight := maxInt(height-2, 4)
	content := append([]string{
		lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render(title),
	}, lines...)
	body := fitText(strings.Join(content, "\n"), innerWidth, innerHeight)
	return lipgloss.NewStyle().
		Width(maxInt(width-2, 24)).
		Height(maxInt(height-2, 4)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Render(body)
}

func (m Model) contextTierCounts() (near, anchor, artifact int) {
	for _, item := range m.context.Items {
		switch item.Tier {
		case core.TierNear:
			near++
		case core.TierAnchor:
			anchor++
		case core.TierArtifact:
			artifact++
		}
	}
	return near, anchor, artifact
}

func (m Model) topContextItems(limit int) []core.ContextItem {
	if limit <= 0 || len(m.context.Items) == 0 {
		return nil
	}
	items := make([]core.ContextItem, 0, limit)
	for _, item := range m.context.Items {
		if item.Pinned || item.Tier == core.TierNear {
			items = append(items, item)
			if len(items) >= limit {
				return items
			}
		}
	}
	for _, item := range m.context.Items {
		items = append(items, item)
		if len(items) >= limit {
			return items
		}
	}
	return items
}

func (m Model) visualRiskLabel() string {
	label, color := m.riskLevel()
	return lipgloss.NewStyle().Foreground(color).Bold(true).Render(label)
}

func (m Model) lastToolSummary() string {
	if len(m.tools) == 0 {
		return "none"
	}
	last := m.tools[len(m.tools)-1]
	if last.Detail != "" {
		return fmt.Sprintf("%s %s", last.Name, last.Status)
	}
	return last.Name + " " + last.Status
}

func activityKindLabel(kind core.ActivityKind) string {
	if kind == "" {
		return "activity"
	}
	return string(kind)
}

func activityKindTitle(kind core.ActivityKind) string {
	switch kind {
	case core.ActivityMCP:
		return "MCP"
	case core.ActivitySkill:
		return "Skill"
	case core.ActivitySubAgent:
		return "Subagent"
	case core.ActivityTool:
		return "Tool"
	default:
		return "Activity"
	}
}

func activityStatusLabel(activity core.ActivityEvent) string {
	if activity.Status == "" {
		return "unknown"
	}
	return string(activity.Status)
}

func activityDisplayName(activity core.ActivityEvent) string {
	switch activity.Kind {
	case core.ActivityMCP:
		if activity.ServerName != "" && activity.ToolName != "" {
			return simplifyActivityName(activity.ServerName) + "/" + simplifyActivityName(activity.ToolName)
		}
		return simplifyActivityName(firstNonEmpty(activity.ServerName, activity.ToolName, activity.Name, activity.Title, activity.ID))
	case core.ActivitySkill:
		return simplifyActivityName(firstNonEmpty(activity.Name, activity.Title, activity.ID))
	case core.ActivitySubAgent:
		return simplifyActivityName(firstNonEmpty(activity.Role, activity.Name, activity.Title, activity.ID))
	case core.ActivityTool:
		return simplifyActivityName(firstNonEmpty(activity.ToolName, activity.Name, activity.Title, activity.ID))
	default:
		return simplifyActivityName(firstNonEmpty(activity.Name, activity.Title, activity.ToolName, activity.ServerName, activity.ID, "activity"))
	}
}

func simplifyActivityName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "@")
	if name == "" {
		return ""
	}
	if idx := strings.LastIndex(name, "/"); idx >= 0 && idx < len(name)-1 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndex(name, ":"); idx >= 0 && idx < len(name)-1 {
		name = name[idx+1:]
	}
	if idx := strings.Index(name, "@"); idx > 0 {
		name = name[:idx]
	}
	return name
}

func (m Model) activityCounts() (total, running, failed, tools, skills, mcps, subagents int) {
	total = len(m.activities)
	for _, activity := range m.activities {
		switch activity.Status {
		case core.ActivityRunning:
			running++
		case core.ActivityFailed:
			failed++
		}
		switch activity.Kind {
		case core.ActivityTool:
			tools++
		case core.ActivitySkill:
			skills++
		case core.ActivityMCP:
			mcps++
		case core.ActivitySubAgent:
			subagents++
		}
	}
	return total, running, failed, tools, skills, mcps, subagents
}

func (m Model) runningSubagents(limit int) []core.ActivityEvent {
	if limit <= 0 {
		return nil
	}
	var out []core.ActivityEvent
	for i := len(m.activities) - 1; i >= 0; i-- {
		activity := m.activities[i]
		if activity.Kind != core.ActivitySubAgent || activity.Status != core.ActivityRunning {
			continue
		}
		out = append(out, activity)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

func (m Model) recentActivities(limit int) []core.ActivityEvent {
	if limit <= 0 {
		return nil
	}
	var out []core.ActivityEvent
	for i := len(m.activities) - 1; i >= 0; i-- {
		out = append(out, m.activities[i])
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (m Model) activityShortNamesForKinds(kinds []core.ActivityKind, limit int) []string {
	if limit <= 0 {
		return nil
	}
	allowed := make(map[core.ActivityKind]bool, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = true
	}
	var names []string
	seen := make(map[string]bool)
	for i := len(m.activities) - 1; i >= 0; i-- {
		activity := m.activities[i]
		if !allowed[activity.Kind] {
			continue
		}
		name := activityDisplayName(activity)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) >= limit {
			break
		}
	}
	return names
}

func progressBar(pct, width int) string {
	barWidth := clampInt(width-8, 8, 32)
	filled := clampInt(pct*barWidth/100, 0, barWidth)
	color := lipgloss.Color("42")
	if pct >= 90 {
		color = lipgloss.Color("196")
	} else if pct >= 70 {
		color = lipgloss.Color("220")
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("░", barWidth-filled))
}

func smallBar(value, total, width int) string {
	total = maxInt(total, 1)
	width = maxInt(width, 1)
	filled := clampInt(value*width/total, 0, width)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render(strings.Repeat("▰", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("▱", width-filled))
}

func dashboardSplit(width int) (int, int) {
	if width < 96 {
		return width, 0
	}
	sideWidth := clampInt(width/4, 34, 48)
	mainWidth := width - sideWidth
	if mainWidth < 48 {
		return width, 0
	}
	return mainWidth, sideWidth
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
		controls = "? close help | esc close | ctrl+c quit"
	} else {
		controls = "type to prompt | / command | tab dashboard | ctrl+g stop | ctrl+r oracle | up/down scroll | ctrl+c quit"
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
		state = m.loadingGlyph() + " working"
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

func (m Model) loadingGlyph() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[m.loadingFrame%len(frames)]
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
  tab / shift+tab: switch transcript and dashboard views
  j/k: move context selection (context panel)
  up/down: scroll current view
  pgup/pgdn: scroll current view by page
  home/end: jump current view

Input
  type normally: start prompt input
  /: enter prompt input mode
  Enter: submit prompt
  Esc: cancel input / close help

Context
  p: toggle pin on selected context item
  d: delete selected context item
  ctrl+r: request context oracle review

Approval
  type y then Enter: approve tool execution
  type n then Enter: deny tool execution
  empty Enter: approve tool execution
  esc: cancel approval

Control
  ctrl+c: quit
  ctrl+g: interrupt running agent
  ctrl+l: clear chat display
  ?: toggle help

Panels
  Transcript: continuous user, assistant, tool, approval, and observation timeline
  Context Map: evidence and context budget by tier
  Agent Trace: plan, action, risk, revision, and evidence notes
  Tool Cockpit: tool runs, activity timeline, timing, and cost
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
