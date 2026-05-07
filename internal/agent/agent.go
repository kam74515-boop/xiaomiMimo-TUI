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

type ToolHook func(ctx context.Context, name string, input core.ToolInput) (core.ToolResult, error)

func BuildSystemPrompt() string {
	return strings.Join([]string{
		"You are MiMo inside a developer TUI coding agent.",
		"Act as a careful coding collaborator: explain assumptions, stream useful progress, and keep the trace honest.",
		"Tool hooks are extensible, but this loop does not execute tools yet.",
		DefaultCriticalThinkingPolicy.String(),
	}, "\n\n")
}

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
		Risk:      "No tools are executed in this first loop.",
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
