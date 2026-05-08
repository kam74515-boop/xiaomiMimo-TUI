package tools

import (
	"context"
	"fmt"

	"mimo-tui/internal/core"
)

const permissionDeniedExitCode = 126

type Executor struct {
	registry *Registry
	bus      *core.Bus
	policy   ExecutorPolicy
}

type ExecutorPolicy struct {
	AllowedAskTools map[string]bool
}

type ExecutorOption func(*Executor)

func NewExecutor(registry *Registry, bus *core.Bus, options ...ExecutorOption) *Executor {
	executor := &Executor{
		registry: registry,
		bus:      bus,
		policy:   ExecutorPolicy{AllowedAskTools: map[string]bool{}},
	}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func WithAllowedAskTools(names ...string) ExecutorOption {
	return func(executor *Executor) {
		if executor.policy.AllowedAskTools == nil {
			executor.policy.AllowedAskTools = map[string]bool{}
		}
		for _, name := range names {
			if name != "" {
				executor.policy.AllowedAskTools[name] = true
			}
		}
	}
}

func (e *Executor) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, core.Observation) {
	if ctx == nil {
		ctx = context.Background()
	}

	e.publishStart(call)

	if e == nil || e.registry == nil {
		result := core.ToolResult{ExitCode: 127, Error: "tool registry is nil"}
		observation := executorObservation(call.Name, result)
		e.publishResult(call, result)
		e.publishObservation(call, observation)
		return result, observation
	}

	tool, ok := e.registry.Get(call.Name)
	if !ok {
		result := core.ToolResult{ExitCode: 127, Error: fmt.Sprintf("unknown tool %q", call.Name)}
		observation := executorObservation(call.Name, result)
		e.publishResult(call, result)
		e.publishObservation(call, observation)
		return result, observation
	}

	permission := tool.Permission(call.Input)
	if allowed, reason := e.policy.allows(tool.Name(), permission); !allowed {
		result := core.ToolResult{
			Content:  fmt.Sprintf("tool %s was not executed: %s", tool.Name(), reason),
			ExitCode: permissionDeniedExitCode,
			Error:    reason,
		}
		observation := executorObservation(tool.Name(), result)
		e.publishResult(call, result)
		e.publishObservation(call, observation)
		return result, observation
	}

	result := tool.Run(ctx, call.Input)
	e.publishResult(call, result)

	observation := tool.Summarize(result)
	normalizeObservation(&observation, result)
	e.publishObservation(call, observation)
	return result, observation
}

func (p ExecutorPolicy) allows(name string, permission core.PermissionRequest) (bool, string) {
	switch permission.Behavior {
	case core.PermissionAllow:
		return true, permission.Reason
	case core.PermissionAsk:
		if p.AllowedAskTools != nil && p.AllowedAskTools[name] {
			return true, permission.Reason
		}
		if permission.Reason == "" {
			return false, "tool requires explicit permission"
		}
		return false, "tool requires explicit permission: " + permission.Reason
	case core.PermissionDeny:
		if permission.Reason == "" {
			return false, "tool denied execution"
		}
		return false, "tool denied execution: " + permission.Reason
	default:
		return false, fmt.Sprintf("tool returned unsupported permission behavior %q", permission.Behavior)
	}
}

func (e *Executor) publishStart(call core.ToolCall) {
	if e == nil || e.bus == nil {
		return
	}
	event := core.NewEvent(core.EventToolStart)
	event.ToolName = call.Name
	event.ToolCall = &call
	event.Message = "Starting tool " + call.Name
	e.bus.Publish(event)
}

func (e *Executor) publishResult(call core.ToolCall, result core.ToolResult) {
	if e == nil || e.bus == nil {
		return
	}
	event := core.NewEvent(core.EventToolResult)
	event.ToolName = call.Name
	event.ToolCall = &call
	event.Message = result.Content
	event.Err = result.Error
	e.bus.Publish(event)
}

func (e *Executor) publishObservation(call core.ToolCall, observation core.Observation) {
	if e == nil || e.bus == nil {
		return
	}
	event := core.NewEvent(core.EventObservation)
	event.ToolName = call.Name
	event.ToolCall = &call
	event.Observation = &observation
	e.bus.Publish(event)
}

func executorObservation(name string, result core.ToolResult) core.Observation {
	observation := summarizeResult(name, result, core.TierNear)
	if result.ArtifactID == "" {
		observation.StateDelta = "tool did not run"
	}
	return observation
}

func normalizeObservation(observation *core.Observation, result core.ToolResult) {
	if observation.ArtifactID == "" {
		observation.ArtifactID = result.ArtifactID
	}
	if observation.ContextPlacement == "" {
		if result.ArtifactID != "" {
			observation.ContextPlacement = core.TierArtifact
		} else {
			observation.ContextPlacement = core.TierNear
		}
	}
}
