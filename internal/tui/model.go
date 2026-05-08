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
	scroll [panelCount]int

	context             core.ContextSnapshot
	hasContext          bool
	selectedContextItem int // index into context.Items; -1 means none selected
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

	bus *core.Bus // optional, for publishing context commands back to the event bus

	inputMode       InputMode
	textInput       string
	cursorPos       int
	pendingApproval core.ApprovalRequest
}

type agentEventMsg core.AgentEvent

type eventSourceClosedMsg struct{}

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
	}
}

func Run(events <-chan core.AgentEvent, bus *core.Bus) error {
	_, err := tea.NewProgram(NewModel(events, bus), tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return waitForEvent(m.events)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
			if m.showHelp {
				m.status = "help opened"
			} else {
				m.status = "help closed"
			}
			return m, nil
		case "esc":
			if m.showHelp {
				m.showHelp = false
				m.status = "help closed"
				return m, nil
			}
		}
		if m.showHelp {
			return m, nil
		}
		switch key {
		case "tab":
			m.focus = (m.focus + 1) % panelCount
			m.status = "focused " + panelNames[m.focus]
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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampAllScrolls()
		return m, nil
	case agentEventMsg:
		m.applyEvent(core.AgentEvent(msg))
		m.clampAllScrolls()
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

	header := renderHeader(width, m.status, m.sourceClosed, panelNames[m.focus], m.scrollPosition(m.focus))
	footer := renderFooter(width, m.showHelp)
	bodyHeight := maxInt(height-lipgloss.Height(header)-lipgloss.Height(footer), 12)

	if m.showHelp {
		return lipgloss.JoinVertical(lipgloss.Left, header, renderHelp(width, bodyHeight), footer)
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
	bodyHeight := maxInt(height-2, 12)

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

	// Color-code pollution risk.
	riskColor := lipgloss.Color("252")
	switch m.context.PollutionRisk {
	case "low":
		riskColor = lipgloss.Color("42") // green
	case "warning":
		riskColor = lipgloss.Color("220") // yellow
	case "over_window":
		riskColor = lipgloss.Color("196") // red
	}
	riskLine := fmt.Sprintf("pollution risk: %s", fallback(m.context.PollutionRisk, "unknown"))
	fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(riskColor).Render(riskLine))

	// Bar chart for token usage across tiers.
	nearT, anchorT, artifactT := 0, 0, 0
	for _, item := range m.context.Items {
		switch item.Tier {
		case core.TierNear:
			nearT += item.TokenEstimate
		case core.TierAnchor:
			anchorT += item.TokenEstimate
		case core.TierArtifact:
			artifactT += item.TokenEstimate
		}
	}
	renderTierBar(&b, "NEAR:", nearT, window)
	renderTierBar(&b, "ANCHOR:", anchorT, window)
	renderTierBar(&b, "ARTIFACT:", artifactT, window)

	// Item listing with selection highlighting.
	itemIdx := 0
	for _, tier := range []core.ContextTier{core.TierNear, core.TierAnchor, core.TierArtifact} {
		b.WriteByte('\n')
		b.WriteString(strings.ToUpper(string(tier)))
		b.WriteByte('\n')
		count := 0
		for _, item := range m.context.Items {
			if item.Tier != tier {
				continue
			}
			count++
			isSelected := itemIdx == m.selectedContextItem

			pin := " "
			if item.Pinned {
				pin = "*"
			}
			cursor := " "
			if isSelected {
				cursor = ">"
			}
			line := fmt.Sprintf("%s%s %5d %s", cursor, pin, item.TokenEstimate, fallback(item.Title, item.ID))

			if isSelected {
				line = lipgloss.NewStyle().Reverse(true).Render(line)
			}
			b.WriteString(line)
			b.WriteByte('\n')

			if item.Reason != "" {
				reasonLine := fmt.Sprintf("  %s", item.Reason)
				if isSelected {
					reasonLine = lipgloss.NewStyle().Reverse(true).Render(reasonLine)
				}
				b.WriteString(reasonLine)
				b.WriteByte('\n')
			}
			itemIdx++
		}
		if count == 0 {
			b.WriteString("  empty\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderTierBar(b *strings.Builder, label string, tierTokens, windowTokens int) {
	pct := 0
	if windowTokens > 0 {
		pct = tierTokens * 100 / windowTokens
	}
	filled := pct / 10
	if filled > 10 {
		filled = 10
	}
	bar := strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", 10-filled)
	fmt.Fprintf(b, "\n%-9s %s %d%%", label, bar, pct)
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

func renderFooter(width int, showHelp bool) string {
	line := "tab focus | ? help | j/k select | p pin | d delete | pgup/pgdn scroll | q quit"
	if showHelp {
		line = "? close help | esc close | q quit"
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("248")).
		Render(truncate(line, width))
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
Controls
  tab: focus next panel
  up/down: scroll focused panel (list panels)
  j/k: move cursor in context panel
  p: pin/unpin selected context item
  d: delete selected context item (not pinned)
  pgup/pgdn: scroll focused panel by page
  home/end: jump focused panel
  ?: toggle help
  q or ctrl+c: quit

Panels
  Context Map: evidence and context budget by tier
  Chat Stream: assistant output deltas
  Agent Trace: plan, action, risk, revision, and evidence notes
  Tool Cockpit: tool runs, timing, and cost
`)
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
