package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	contextmap "mimo-tui/internal/context"
	"mimo-tui/internal/core"
)

var (
	ErrNilBus    = errors.New("agent: nil event bus")
	ErrNilClient = errors.New("agent: nil model client")
)

// ToolExecutor is the interface the agent loop uses to execute tool calls.
// Implemented by tools.Executor.
type ToolExecutor interface {
	Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, core.Observation)
}

// ContextManager is the interface the agent loop uses to track context.
// Implemented by context.Manager.
type ContextManager interface {
	Add(item core.ContextItem) (core.ContextSnapshot, error)
	Update(item core.ContextItem) (core.ContextSnapshot, error)
	Upsert(item core.ContextItem) (core.ContextSnapshot, error)
	Remove(id string) (core.ContextSnapshot, error)
	Pin(id string) (core.ContextSnapshot, error)
	Unpin(id string) (core.ContextSnapshot, error)
	Admit(item core.ContextItem) (core.ContextSnapshot, error)
	Snapshot() core.ContextSnapshot
	AutoBudget() contextmap.AutoBudgetResult
}

type CriticalThinkingPolicy struct {
	Name       string
	Principles []string
}

func (p CriticalThinkingPolicy) String() string {
	var b strings.Builder
	if p.Name != "" {
		b.WriteString(p.Name)
		b.WriteString(":\n")
	}
	for _, principle := range p.Principles {
		if strings.TrimSpace(principle) == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(principle)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

var DefaultCriticalThinkingPolicy = CriticalThinkingPolicy{
	Name: "Critical thinking policy",
	Principles: []string{
		"State uncertainty and assumptions plainly.",
		"Prefer small, reversible steps backed by observable evidence.",
		"Keep raw tool output out of the prompt unless it has been summarized into an observation.",
		"Use the context window deliberately; include only information that changes the answer.",
		"Do not claim tool execution or hidden model internals.",
	},
}

type LoopConfig struct {
	MaxSteps     int
	StepTimeout  time.Duration
	TotalTimeout time.Duration

	// Memory, when non-empty, is injected into the system prompt every turn so
	// persistent cross-session facts influence the model. It is supplied by the
	// runtime, which owns the memory store.
	Memory string

	// Skills, when non-empty, is the combined body of active skill playbooks,
	// injected into the system prompt so the model follows them. Supplied by the
	// runtime, which owns the active-skill set.
	Skills string

	// GoalCondition, when non-empty together with Judge, enables the goal gate:
	// at the point the model would produce a final answer, the judge verifies
	// the condition is met before the loop is allowed to stop.
	GoalCondition string
	// Judge independently evaluates GoalCondition. Optional; the gate is a no-op
	// when nil.
	Judge Judge
	// MaxGoalReact bounds goal-gate re-entries per prompt. Zero means
	// DefaultMaxGoalReact.
	MaxGoalReact int
}

func DefaultLoopConfig() LoopConfig {
	maxSteps := 16
	if v := os.Getenv("MIMO_MAX_STEPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSteps = n
		}
	}
	return LoopConfig{
		MaxSteps:     maxSteps,
		StepTimeout:  120 * time.Second,
		TotalTimeout: 600 * time.Second,
		MaxGoalReact: DefaultMaxGoalReact,
	}
}

func BuildSystemPrompt() string {
	return strings.Join([]string{
		"You are MiMo inside a developer TUI coding agent.",
		"Act as a careful coding collaborator: explain assumptions, stream useful progress, and keep the trace honest.",
		"MiMo capability profile: MiMo supports agentic coding, long-context reasoning, native web/deep search, and native multimodal understanding for text, image, video, and audio in multimodal-capable models. Do not say that MiMo lacks web search or image/audio/video understanding.",
		"Runtime capability contract: this TUI wires MiMo web_search by default and converts user-provided media paths or URLs into MiMo multimodal content parts. If a specific media file or URL is missing, ask for that input; do not deny the underlying MiMo capability.",
		"For web requests: use the native web_search capability when current or external information is needed. Do not fabricate search results.",
		"For image/video/audio requests: inspect the attached multimodal content when it is present. If the user only says an image/audio/video exists but provides no path, URL, or attachment, ask for the concrete media reference.",
		"Tools are available and will be executed. When you call a tool, its result will be fed back to you so you can continue reasoning.",
		"Keep raw tool output out of context; summarize results into observations before using them.",
		"When you are finished, provide a final answer without calling any more tools.",
		DefaultCriticalThinkingPolicy.String(),
	}, "\n\n")
}

// Loop runs the agent in a tool-execution loop.
//
// Each iteration:
//  1. Injects a context-map summary into the system message.
//  2. Streams the model response with available tool specs.
//  3. Collects any tool calls the model emits.
//  4. If there are no tool calls, the loop ends (the model produced a final answer).
//  5. Executes each tool call, appends results to the message history, and promotes
//     observations into the context map.
//
// The loop stops when the model emits no tool calls, max steps are reached, or a timeout fires.
//
// If history is non-empty, it is used as the message prefix (system + prior turns).
// A new user message with the prompt is appended. The returned messages slice
// contains the full conversation including this turn, suitable for multi-turn use.
func Loop(
	ctx context.Context,
	prompt string,
	client core.ModelClient,
	executor ToolExecutor,
	ctxMgr ContextManager,
	toolSpecs []core.ToolSpec,
	bus *core.Bus,
	config LoopConfig,
	history []core.Message,
) ([]core.Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if bus == nil {
		return nil, ErrNilBus
	}
	if client == nil {
		publishError(bus, ErrNilClient)
		publishDone(bus)
		return nil, ErrNilClient
	}

	totalCtx, totalCancel := context.WithTimeout(ctx, config.TotalTimeout)
	defer totalCancel()

	var messages []core.Message
	if len(history) > 0 {
		messages = make([]core.Message, len(history))
		copy(messages, history)
	} else {
		messages = []core.Message{
			{Role: "system", Content: BuildSystemPrompt()},
		}
	}
	messages = append(messages, BuildUserMessage(prompt))

	maxGoalReact := config.MaxGoalReact
	if maxGoalReact <= 0 {
		maxGoalReact = DefaultMaxGoalReact
	}
	goalActive := strings.TrimSpace(config.GoalCondition) != "" && config.Judge != nil
	reactCount := 0

	for step := 0; step < config.MaxSteps; step++ {
		select {
		case <-totalCtx.Done():
			publishError(bus, totalCtx.Err())
			publishDone(bus)
			return messages, totalCtx.Err()
		default:
		}

		stepTrace := core.TraceStep{
			ID:        fmt.Sprintf("agent-step-%d-%d", step, time.Now().UnixNano()),
			Goal:      fmt.Sprintf("Step %d: reason about the prompt and decide whether to call tools.", step+1),
			Plan:      "Stream the model, collect tool calls if any, execute them, and loop.",
			Action:    "chat.completions.stream",
			Status:    core.TraceRunning,
			StartedAt: time.Now(),
		}
		publishTrace(bus, stepTrace)

		// Inject memory, active skills, and the context-map summary so the model
		// knows the persistent facts, the playbooks to follow, and what evidence
		// is loaded.
		messages[0].Content = buildSystemContext(config.Memory, config.Skills) + "\n\n" + buildContextSummary(ctxMgr.Snapshot())

		stepCtx, stepCancel := context.WithTimeout(totalCtx, config.StepTimeout)

		events, err := client.ChatStream(stepCtx, core.ChatRequest{
			Messages: messages,
			Stream:   true,
			Tools:    augmentToolSpecs(toolSpecs),
		})
		if err != nil {
			stepCancel()
			stepTrace.Status = core.TraceFailed
			stepTrace.EndedAt = time.Now()
			stepTrace.Observation = "Model stream could not be started."
			stepTrace.Revision = err.Error()
			publishTrace(bus, stepTrace)
			publishError(bus, err)
			publishDone(bus)
			return messages, err
		}

		var assistantContent string
		var toolCalls []core.ToolCall

	streamLoop:
		for {
			select {
			case <-totalCtx.Done():
				stepCancel()
				stepTrace.Status = core.TraceFailed
				stepTrace.EndedAt = time.Now()
				stepTrace.Observation = "Total timeout reached during streaming."
				publishTrace(bus, stepTrace)
				publishError(bus, totalCtx.Err())
				publishDone(bus)
				return messages, totalCtx.Err()
			case event, ok := <-events:
				if !ok {
					break streamLoop
				}
				if event.Err != nil {
					stepCancel()
					stepTrace.Status = core.TraceFailed
					stepTrace.EndedAt = time.Now()
					stepTrace.Observation = "Model stream returned an error."
					stepTrace.Revision = event.Err.Error()
					publishTrace(bus, stepTrace)
					publishError(bus, event.Err)
					publishDone(bus)
					return messages, event.Err
				}
				if event.Delta != "" {
					assistantContent += event.Delta
					publishMessageDelta(bus, event.Delta)
				}
				for _, toolCall := range event.ToolCalls {
					toolCalls = append(toolCalls, toolCall)
					publishToolStart(bus, toolCall)
				}
				if event.Usage != nil {
					publishCost(bus, event.Usage)
				}
				if event.Done {
					break streamLoop
				}
			}
		}
		stepCancel()

		// If the model produced no tool calls it gave a final answer.
		if len(toolCalls) == 0 {
			// Append the assistant message to history so callers can
			// continue the conversation across turns.
			messages = append(messages, core.Message{
				Role:    "assistant",
				Content: assistantContent,
			})

			// Goal gate: before allowing the loop to stop, let an independent
			// judge verify the active goal is genuinely satisfied. If not, inject
			// a synthetic user turn with the judge's diagnosis and re-enter.
			if goalActive {
				verdict, jerr := config.Judge.Evaluate(totalCtx, config.GoalCondition, messages)
				// If the run was cancelled or timed out during the judge call,
				// report it as such rather than masking it as a fail-open stop.
				if cerr := totalCtx.Err(); cerr != nil {
					stepTrace.Status = core.TraceFailed
					stepTrace.EndedAt = time.Now()
					stepTrace.Observation = "Run ended during goal evaluation."
					publishTrace(bus, stepTrace)
					publishError(bus, cerr)
					publishDone(bus)
					return messages, cerr
				}
				if jerr == nil && !verdict.OK && !verdict.Impossible {
					if reactCount < maxGoalReact {
						reactCount++
						publishGoalUpdate(bus, core.GoalUpdate{
							Condition:  config.GoalCondition,
							Active:     true,
							Reason:     verdict.Reason,
							ReactCount: reactCount,
						})
						publishNote(bus, fmt.Sprintf("goal not yet satisfied (re-entry %d/%d): %s", reactCount, maxGoalReact, verdict.Reason))
						messages = append(messages, core.Message{
							Role:    "user",
							Content: goalReminderMessage(config.GoalCondition, verdict.Reason),
						})
						stepTrace.Status = core.TraceDone
						stepTrace.EndedAt = time.Now()
						stepTrace.Observation = "Goal not yet satisfied; judge requested another attempt."
						stepTrace.Revision = verdict.Reason
						publishTrace(bus, stepTrace)
						continue
					}
					// Re-entry cap reached: stop and report that we gave up.
					publishGoalUpdate(bus, core.GoalUpdate{
						Condition:  config.GoalCondition,
						Active:     false,
						Reason:     fmt.Sprintf("stopped after %d re-entries without meeting the goal", reactCount),
						ReactCount: reactCount,
					})
					publishNote(bus, fmt.Sprintf("goal gate gave up after %d re-entries", reactCount))
				} else {
					// Satisfied, impossible, or a judge error (fail-open): clear
					// the goal so it does not re-gate the next prompt.
					update := core.GoalUpdate{
						Condition:  config.GoalCondition,
						Active:     false,
						Satisfied:  jerr == nil && verdict.OK,
						Impossible: jerr == nil && verdict.Impossible,
						Reason:     verdict.Reason,
						ReactCount: reactCount,
					}
					if jerr != nil {
						update.Reason = "judge unavailable; allowing stop (fail-open): " + jerr.Error()
					}
					publishGoalUpdate(bus, update)
				}
			}

			stepTrace.Status = core.TraceDone
			stepTrace.EndedAt = time.Now()
			stepTrace.Observation = "Model produced final answer without tool calls."
			publishTrace(bus, stepTrace)
			publishDone(bus)
			return messages, nil
		}

		// Append the assistant message with tool calls to the history.
		messages = append(messages, core.Message{
			Role:      "assistant",
			Content:   assistantContent,
			ToolCalls: toolCalls,
		})

		stepTrace.Action = "tool_call.execute"
		stepTrace.Observation = fmt.Sprintf("Executing %d tool call(s).", len(toolCalls))

		// Execute each tool call.
		for _, call := range toolCalls {
			toolTrace := core.TraceStep{
				ID:        fmt.Sprintf("tool-%s-%d", call.Name, time.Now().UnixNano()),
				Goal:      fmt.Sprintf("Execute tool %s", toolCallLabel(call)),
				Action:    "tool.execute",
				Status:    core.TraceRunning,
				StartedAt: time.Now(),
			}
			publishTrace(bus, toolTrace)
			publishToolActivity(bus, call, core.ActivityRunning, "tool execution started", "")

			result, observation := executor.Execute(totalCtx, call)

			// Publish tool result and observation events.
			publishToolResult(bus, call, result)
			publishObservation(bus, call.Name, &observation)
			activityStatus := core.ActivityDone
			activitySummary := observation.Summary
			if result.Error != "" {
				activityStatus = core.ActivityFailed
				activitySummary = result.Error
			}
			publishToolActivity(bus, call, activityStatus, activitySummary, result.ArtifactID)

			// Append tool result to message history.
			messages = append(messages, core.Message{
				Role:       "tool",
				Content:    result.Content,
				ToolCallID: call.ID,
			})

			// Promote the observation into the context map.
			promoted := contextmap.PromoteObservation(fmt.Sprintf("obs:%s:%d", call.Name, time.Now().UnixNano()), observation)
			snapshot, err := ctxMgr.Admit(promoted)
			if err != nil {
				publishNote(bus, fmt.Sprintf("context admission rejected for %s: %v", promoted.ID, err))
			}
			publishContext(bus, snapshot)

			toolTrace.Status = core.TraceDone
			toolTrace.EndedAt = time.Now()
			if result.Error != "" {
				toolTrace.Status = core.TraceFailed
				toolTrace.Observation = result.Error
				toolTrace.Revision = "Tool execution reported an error; it has been fed back to the model."
			} else {
				toolTrace.Observation = observation.Summary
			}
			publishTrace(bus, toolTrace)
		}

		// Enforce context budget after tool execution.
		budgetResult := ctxMgr.AutoBudget()
		if len(budgetResult.Evicted) > 0 || budgetResult.Warning != "" {
			note := fmt.Sprintf("context auto-budget: evicted %d item(s)", len(budgetResult.Evicted))
			if budgetResult.Warning != "" {
				note += "; " + budgetResult.Warning
			}
			publishNote(bus, note)
			// Publish refreshed context snapshot after evictions.
			publishContext(bus, ctxMgr.Snapshot())
		}

		stepTrace.Status = core.TraceDone
		stepTrace.EndedAt = time.Now()
		stepTrace.Revision = fmt.Sprintf("Step %d complete; %d tool call(s) executed.", step+1, len(toolCalls))
		publishTrace(bus, stepTrace)
	}

	// Max steps reached. If a goal was active, close it out so it does not
	// silently re-gate the next prompt and so the truncation is visible.
	err := fmt.Errorf("agent loop reached max steps limit (%d)", config.MaxSteps)
	if goalActive {
		publishGoalUpdate(bus, core.GoalUpdate{
			Condition:  config.GoalCondition,
			Active:     false,
			Reason:     fmt.Sprintf("stopped: reached max steps (%d) before the goal was satisfied", config.MaxSteps),
			ReactCount: reactCount,
		})
	}
	publishError(bus, err)
	publishDone(bus)
	return messages, err
}

// RunOnce is the original single-pass agent call, retained for backward compatibility
// with smoke tests and simple queries.
func RunOnce(ctx context.Context, prompt string, client core.ModelClient, bus *core.Bus) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if bus == nil {
		return ErrNilBus
	}
	if client == nil {
		publishError(bus, ErrNilClient)
		publishDone(bus)
		return ErrNilClient
	}

	step := core.TraceStep{
		ID:        fmt.Sprintf("agent-%d", time.Now().UnixNano()),
		Goal:      "Respond to the user prompt with a MiMo-first coding-agent answer.",
		Plan:      "Build a system prompt, stream the model response, and publish bus events.",
		Action:    "chat.completions.stream",
		Risk:      "RunOnce does not execute tool calls.",
		Status:    core.TraceRunning,
		StartedAt: time.Now(),
	}
	publishTrace(bus, step)

	events, err := client.ChatStream(ctx, core.ChatRequest{
		Messages: []core.Message{
			{Role: "system", Content: BuildSystemPrompt()},
			BuildUserMessage(prompt),
		},
		Stream: true,
		Tools:  augmentToolSpecs(nil),
	})
	if err != nil {
		step.Status = core.TraceFailed
		step.EndedAt = time.Now()
		step.Observation = "Model stream could not be started."
		step.Revision = err.Error()
		publishTrace(bus, step)
		publishError(bus, err)
		publishDone(bus)
		return err
	}

	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			step.Status = core.TraceFailed
			step.EndedAt = time.Now()
			step.Observation = "Run was canceled before the model stream completed."
			step.Revision = err.Error()
			publishTrace(bus, step)
			publishError(bus, err)
			publishDone(bus)
			return err
		case event, ok := <-events:
			if !ok {
				step.Status = core.TraceDone
				step.EndedAt = time.Now()
				step.Observation = "Model stream closed."
				publishTrace(bus, step)
				publishDone(bus)
				return nil
			}
			if event.Err != nil {
				step.Status = core.TraceFailed
				step.EndedAt = time.Now()
				step.Observation = "Model stream returned an error."
				step.Revision = event.Err.Error()
				publishTrace(bus, step)
				publishError(bus, event.Err)
				publishDone(bus)
				return event.Err
			}
			if event.Delta != "" {
				publishMessageDelta(bus, event.Delta)
			}
			for _, toolCall := range event.ToolCalls {
				publishToolStart(bus, toolCall)
				step.Action = "tool_call.requested"
				step.Observation = fmt.Sprintf("Model requested tool call %s; execution is not enabled in this loop.", toolCallLabel(toolCall))
				step.Risk = "Tool call was surfaced to the trace; no tool execution occurred."
				publishTrace(bus, step)
			}
			if event.Usage != nil {
				publishCost(bus, event.Usage)
			}
			if event.Done {
				step.Status = core.TraceDone
				step.EndedAt = time.Now()
				step.Observation = "Model stream completed."
				publishTrace(bus, step)
				publishDone(bus)
				return nil
			}
		}
	}
}

// buildSystemContext appends persistent memory and active skill playbooks to
// the base system prompt so the model reads recorded cross-session facts and
// follows activated procedures every turn.
func buildSystemContext(memory, skills string) string {
	b := strings.Builder{}
	b.WriteString(BuildSystemPrompt())
	if memory = strings.TrimSpace(memory); memory != "" {
		b.WriteString("\n\n[Persistent Memory — durable facts recorded across sessions; trust and apply these]\n")
		b.WriteString(memory)
	}
	if skills = strings.TrimSpace(skills); skills != "" {
		b.WriteString("\n\n[Active Skills — follow these playbooks for this session]\n")
		b.WriteString(skills)
	}
	return b.String()
}

func buildContextSummary(snapshot core.ContextSnapshot) string {
	var b strings.Builder
	b.WriteString("[Context Map Summary]\n")
	b.WriteString(fmt.Sprintf("Window: %d / %d tokens (%s risk)\n",
		snapshot.UsedTokens, snapshot.WindowTokens, snapshot.PollutionRisk))

	nearItems := []string{}
	anchorItems := []string{}
	artifactItems := []string{}
	for _, item := range snapshot.Items {
		label := item.Title
		if label == "" {
			label = item.ID
		}
		switch item.Tier {
		case core.TierNear:
			nearItems = append(nearItems, label)
		case core.TierAnchor:
			if item.Pinned {
				label += " (pinned)"
			}
			anchorItems = append(anchorItems, label)
		case core.TierArtifact:
			artifactItems = append(artifactItems, label)
		}
	}

	if len(nearItems) > 0 {
		b.WriteString("Near: " + strings.Join(nearItems, ", ") + "\n")
	}
	if len(anchorItems) > 0 {
		b.WriteString("Anchor: " + strings.Join(anchorItems, ", ") + "\n")
	}
	if len(artifactItems) > 0 {
		b.WriteString("Artifacts: " + strings.Join(artifactItems, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func publishTrace(bus *core.Bus, step core.TraceStep) {
	event := core.NewEvent(core.EventTraceUpdate)
	trace := step
	event.Trace = &trace
	bus.Publish(event)
}

func publishMessageDelta(bus *core.Bus, delta string) {
	event := core.NewEvent(core.EventMessageDelta)
	event.Message = delta
	bus.Publish(event)
}

func publishToolStart(bus *core.Bus, call core.ToolCall) {
	event := core.NewEvent(core.EventToolStart)
	toolCall := call
	event.ToolCall = &toolCall
	event.ToolName = call.Name
	bus.Publish(event)
}

func publishToolResult(bus *core.Bus, call core.ToolCall, result core.ToolResult) {
	event := core.NewEvent(core.EventToolResult)
	event.ToolName = call.Name
	event.ToolCall = &call
	event.Message = result.Content
	event.Err = result.Error
	bus.Publish(event)
}

func publishObservation(bus *core.Bus, name string, obs *core.Observation) {
	event := core.NewEvent(core.EventObservation)
	event.ToolName = name
	event.Observation = obs
	bus.Publish(event)
}

func publishCost(bus *core.Bus, cost *core.CostUpdate) {
	event := core.NewEvent(core.EventCostUpdate)
	event.Cost = cost
	bus.Publish(event)
}

func publishError(bus *core.Bus, err error) {
	event := core.NewEvent(core.EventError)
	if err != nil {
		event.Err = err.Error()
	}
	bus.Publish(event)
}

func publishDone(bus *core.Bus) {
	bus.Publish(core.NewEvent(core.EventDone))
}

func publishGoalUpdate(bus *core.Bus, update core.GoalUpdate) {
	if bus == nil {
		return
	}
	event := core.NewEvent(core.EventGoalUpdate)
	g := update
	event.Goal = &g
	switch {
	case g.Satisfied:
		event.Message = "goal satisfied: " + g.Condition
	case g.Impossible:
		event.Message = "goal judged impossible: " + g.Condition
	case g.Active:
		event.Message = "goal pending: " + g.Condition
	default:
		event.Message = "goal closed: " + g.Condition
	}
	bus.Publish(event)
}

func publishContext(bus *core.Bus, snapshot core.ContextSnapshot) {
	event := core.NewEvent(core.EventContextUpdate)
	event.Context = &snapshot
	bus.Publish(event)
}

func publishNote(bus *core.Bus, note string) {
	event := core.NewEvent(core.EventObservation)
	event.Observation = &core.Observation{Summary: note}
	bus.Publish(event)
}

func publishToolActivity(bus *core.Bus, call core.ToolCall, status core.ActivityStatus, summary, artifactID string) {
	if bus == nil {
		return
	}
	event := core.NewEvent(core.EventActivityUpdate)
	activity := core.ActivityEvent{
		ID:       activityIDForTool(call),
		Kind:     activityKindForTool(call.Name),
		Name:     toolCallLabel(call),
		Title:    toolCallLabel(call),
		Status:   status,
		Summary:  truncateActivitySummary(summary, 220),
		ToolName: call.Name,
	}
	if strings.HasPrefix(call.Name, "mcp__") {
		parts := strings.SplitN(call.Name, "__", 3)
		if len(parts) == 3 {
			activity.ServerName = parts[1]
			activity.Name = parts[2]
			activity.Title = parts[1] + "/" + parts[2]
		}
	}
	if status == core.ActivityRunning {
		activity.StartedAt = event.Time
	} else {
		activity.EndedAt = event.Time
	}
	if artifactID != "" {
		activity.Artifacts = []string{artifactID}
	}
	event.Activity = &activity
	bus.Publish(event)
}

func activityIDForTool(call core.ToolCall) string {
	if strings.TrimSpace(call.ID) != "" {
		return "tool:" + call.ID
	}
	return "tool:" + toolCallLabel(call)
}

func activityKindForTool(name string) core.ActivityKind {
	switch {
	case strings.HasPrefix(name, "mcp__"):
		return core.ActivityMCP
	case strings.HasPrefix(name, "skill__"), strings.HasPrefix(name, "skill:"):
		return core.ActivitySkill
	default:
		return core.ActivityTool
	}
}

func truncateActivitySummary(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}

func toolCallLabel(call core.ToolCall) string {
	if strings.TrimSpace(call.Name) != "" {
		return call.Name
	}
	if strings.TrimSpace(call.ID) != "" {
		return call.ID
	}
	return "unknown"
}
