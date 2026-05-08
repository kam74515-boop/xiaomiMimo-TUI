package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	Upsert(item core.ContextItem) (core.ContextSnapshot, error)
	Snapshot() core.ContextSnapshot
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
}

func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		MaxSteps:     8,
		StepTimeout:  120 * time.Second,
		TotalTimeout: 600 * time.Second,
	}
}

func BuildSystemPrompt() string {
	return strings.Join([]string{
		"You are MiMo inside a developer TUI coding agent.",
		"Act as a careful coding collaborator: explain assumptions, stream useful progress, and keep the trace honest.",
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
func Loop(
	ctx context.Context,
	prompt string,
	client core.ModelClient,
	executor ToolExecutor,
	ctxMgr ContextManager,
	toolSpecs []core.ToolSpec,
	bus *core.Bus,
	config LoopConfig,
) error {
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

	totalCtx, totalCancel := context.WithTimeout(ctx, config.TotalTimeout)
	defer totalCancel()

	messages := []core.Message{
		{Role: "system", Content: BuildSystemPrompt()},
		{Role: "user", Content: prompt},
	}

	for step := 0; step < config.MaxSteps; step++ {
		select {
		case <-totalCtx.Done():
			publishError(bus, totalCtx.Err())
			publishDone(bus)
			return totalCtx.Err()
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

		// Inject context-map summary so the model knows what evidence is loaded.
		messages[0].Content = BuildSystemPrompt() + "\n\n" + buildContextSummary(ctxMgr.Snapshot())

		stepCtx, stepCancel := context.WithTimeout(totalCtx, config.StepTimeout)

		events, err := client.ChatStream(stepCtx, core.ChatRequest{
			Messages: messages,
			Stream:   true,
			Tools:    toolSpecs,
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
			return err
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
				return totalCtx.Err()
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
					return event.Err
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
			stepTrace.Status = core.TraceDone
			stepTrace.EndedAt = time.Now()
			stepTrace.Observation = "Model produced final answer without tool calls."
			publishTrace(bus, stepTrace)
			publishDone(bus)
			return nil
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

			result, observation := executor.Execute(totalCtx, call)

			// Publish tool result and observation events.
			publishToolResult(bus, call, result)
			publishObservation(bus, call.Name, &observation)

			// Append tool result to message history.
			messages = append(messages, core.Message{
				Role:       "tool",
				Content:    result.Content,
				ToolCallID: call.ID,
			})

			// Promote the observation into the context map.
			promoted := promoteObservationToContext(fmt.Sprintf("obs:%s:%d", call.Name, time.Now().UnixNano()), observation)
			snapshot, _ := ctxMgr.Upsert(promoted)
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

		stepTrace.Status = core.TraceDone
		stepTrace.EndedAt = time.Now()
		stepTrace.Revision = fmt.Sprintf("Step %d complete; %d tool call(s) executed.", step+1, len(toolCalls))
		publishTrace(bus, stepTrace)
	}

	// Max steps reached.
	err := fmt.Errorf("agent loop reached max steps limit (%d)", config.MaxSteps)
	publishError(bus, err)
	publishDone(bus)
	return err
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
			{Role: "user", Content: prompt},
		},
		Stream: true,
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

func promoteObservationToContext(id string, obs core.Observation) core.ContextItem {
	item := core.ContextItem{
		ID:     id,
		Tier:   obs.ContextPlacement,
		Source: "observation",
		Pinned: false,
	}
	if obs.ArtifactID != "" {
		item.Source = "artifact:" + obs.ArtifactID
		item.Tier = core.TierArtifact
	}
	if obs.Summary != "" {
		item.Title = obs.Summary
	} else if obs.StateDelta != "" {
		item.Title = obs.StateDelta
	} else if obs.RiskDelta != "" {
		item.Title = obs.RiskDelta
	}
	item.TokenEstimate = core.EstimateTokens(item.Title + item.Source + obs.StateDelta + obs.RiskDelta)
	item.Reason = fmt.Sprintf("Tool observation: %s", obs.Summary)
	if item.Tier == "" {
		item.Tier = core.TierNear
	}
	return item
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

func publishContext(bus *core.Bus, snapshot core.ContextSnapshot) {
	event := core.NewEvent(core.EventContextUpdate)
	event.Context = &snapshot
	bus.Publish(event)
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
