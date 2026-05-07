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

type toolRun struct {
	Name      string
	Status    string
	Detail    string
	StartedAt time.Time
	EndedAt   time.Time
}

type Model struct {
	events <-chan core.AgentEvent

	width  int
	height int
	focus  focusPanel

	context    core.ContextSnapshot
	hasContext bool
	chat       string
	trace      []core.TraceStep
	traceIndex map[string]int
	tools      []toolRun
	notes      []string

	cost    core.CostUpdate
	hasCost bool

	status       string
	sourceClosed bool
}

type agentEventMsg core.AgentEvent

type eventSourceClosedMsg struct{}

func NewModel(events <-chan core.AgentEvent) Model {
	if events == nil {
		ch := make(chan core.AgentEvent)
		close(ch)
		events = ch
	}
	return Model{
		events:     events,
		width:      100,
		height:     32,
		traceIndex: make(map[string]int),
		status:     "waiting for agent events",
	}
}

func Run(events <-chan core.AgentEvent) error {
	_, err := tea.NewProgram(NewModel(events), tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return waitForEvent(m.events)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.focus = (m.focus + 1) % panelCount
			m.status = "focused " + panelNames[m.focus]
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case agentEventMsg:
		m.applyEvent(core.AgentEvent(msg))
		return m, waitForEvent(m.events)
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

	header := renderHeader(width, m.status, m.sourceClosed)
	footer := renderFooter(width)
	bodyHeight := maxInt(height-lipgloss.Height(header)-lipgloss.Height(footer), 12)

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

	return lipgloss.JoinVertical(lipgloss.Left, header, top, bottom, footer)
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
	switch event.Type {
	case core.EventMessageDelta:
		m.chat += event.Message
		m.status = "receiving assistant output"
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
		m.notes = append(m.notes, "error: "+msg)
		m.status = "error: " + msg
	case core.EventDone:
		m.status = fallback(event.Message, "agent run complete")
	default:
		if event.Message != "" {
			m.notes = append(m.notes, event.Message)
		}
		m.status = "received " + string(event.Type)
	}
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

	borderColor := lipgloss.Color("240")
	titleColor := lipgloss.Color("250")
	if m.focus == panel {
		borderColor = lipgloss.Color("39")
		titleColor = lipgloss.Color("15")
	}

	body := lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render(title) + "\n" +
		fitText(m.panelContent(panel), contentWidth, contentHeight-1)

	return lipgloss.NewStyle().
		Width(contentWidth).
		Height(contentHeight).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Foreground(lipgloss.Color("252")).
		Render(body)
}

func (m Model) panelContent(panel focusPanel) string {
	switch panel {
	case contextPanel:
		return m.contextContent()
	case chatPanel:
		if strings.TrimSpace(m.chat) == "" {
			return "waiting for message_delta events"
		}
		return "assistant> " + strings.TrimRight(m.chat, "\n")
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
	var b strings.Builder
	fmt.Fprintf(&b, "tokens: %d / %d (%d%%)\n", used, window, used*100/window)
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
			if item.Reason != "" {
				fmt.Fprintf(&b, "  %s\n", item.Reason)
			}
		}
		if count == 0 {
			b.WriteString("  empty\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) traceContent() string {
	if len(m.trace) == 0 && len(m.notes) == 0 {
		return "waiting for trace_update events"
	}

	var b strings.Builder
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
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHeader(width int, status string, closed bool) string {
	state := "live"
	if closed {
		state = "closed"
	}
	line := fmt.Sprintf("MiMo Value Amplifier TUI | %s | %s", state, fallback(status, "ready"))
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("236")).
		Render(truncate(line, width))
}

func renderFooter(width int) string {
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("248")).
		Render(truncate("tab focus | q/ctrl+c quit", width))
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
