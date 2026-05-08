package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
)

const permissionDeniedExitCode = 126

type Executor struct {
	registry       *Registry
	bus            *core.Bus
	policy         ExecutorPolicy
	policyConfig   *config.PolicyConfig
	budgetProvider func() BudgetLevel
	ApprovalCh     chan<- core.ApprovalRequest
	store          *artifact.Store
	workspace      string
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

func WithApprovalChannel(ch chan<- core.ApprovalRequest) ExecutorOption {
	return func(executor *Executor) {
		executor.ApprovalCh = ch
	}
}

func WithBudgetProvider(provider func() BudgetLevel) ExecutorOption {
	return func(executor *Executor) {
		executor.budgetProvider = provider
	}
}

func WithArtifactStore(store *artifact.Store, workspace string) ExecutorOption {
	return func(executor *Executor) {
		executor.store = store
		executor.workspace = workspace
	}
}

func WithPolicyConfig(cfg config.PolicyConfig) ExecutorOption {
	return func(executor *Executor) {
		executor.policyConfig = &cfg
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

	// Evaluate permission: policy.toml overrides tool's default if configured.
	permission := tool.Permission(call.Input)
	if e.policyConfig != nil {
		grade := tool.Safety(call.Input)
		permission = config.EvaluatePolicy(*e.policyConfig, call.Name, call.Input, grade)
	}

	// If the tool asks for permission, there is an approval channel, and the tool
	// is not already whitelisted, request user approval via the channel.
	if permission.Behavior == core.PermissionAsk && e.ApprovalCh != nil && !e.policy.isAllowedAsk(tool.Name()) {
		req := core.ApprovalRequest{
			ToolCall:   call,
			Permission: permission,
			Response:   make(chan core.ApprovalDecision, 1),
		}
		select {
		case e.ApprovalCh <- req:
		default:
			result := core.ToolResult{
				Content:  fmt.Sprintf("tool %s approval channel full", tool.Name()),
				ExitCode: permissionDeniedExitCode,
				Error:    "approval channel is full; cannot request approval",
			}
			observation := executorObservation(tool.Name(), result)
			e.publishResult(call, result)
			e.publishObservation(call, observation)
			return result, observation
		}

		select {
		case decision := <-req.Response:
			if !decision.Allowed {
				reason := decision.Reason
				if reason == "" {
					reason = "user denied tool execution"
				}
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
			// Approved: fall through to normal execution.
		case <-time.After(e.approvalTimeout()):
			result := core.ToolResult{
				Content:  fmt.Sprintf("tool %s approval timed out", tool.Name()),
				ExitCode: permissionDeniedExitCode,
				Error:    "approval timed out after 30s",
			}
			observation := executorObservation(tool.Name(), result)
			e.publishResult(call, result)
			e.publishObservation(call, observation)
			return result, observation
		}
	} else {
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
	}

	// Capture a rollback snapshot before mutating tools.
	var rollbackArtifactID string
	if tool.Safety(call.Input) != core.SafetyReadOnly {
		if id, err := e.captureRollbackSnapshot(call.Name); err == nil {
			rollbackArtifactID = id
		}
	}

	result := tool.Run(ctx, call.Input)
	e.publishResult(call, result)

	observation := SummarizeTool(tool, result, e.currentBudget())
	normalizeObservation(&observation, result)
	observation.RollbackArtifactID = rollbackArtifactID
	e.publishObservation(call, observation)
	return result, observation
}

func (e *Executor) currentBudget() BudgetLevel {
	if e == nil || e.budgetProvider == nil {
		return BudgetSafe
	}
	return e.budgetProvider()
}

func (e *Executor) approvalTimeout() time.Duration {
	if e != nil && e.policyConfig != nil && e.policyConfig.ApprovalTimeout > 0 {
		return time.Duration(e.policyConfig.ApprovalTimeout) * time.Second
	}
	return 30 * time.Second
}

func (p ExecutorPolicy) isAllowedAsk(name string) bool {
	return p.AllowedAskTools != nil && p.AllowedAskTools[name]
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

func (e *Executor) publishApprovalNeeded(call core.ToolCall, req core.ApprovalRequest) {
	if e == nil || e.bus == nil {
		return
	}
	event := core.NewEvent(core.EventApprovalNeeded)
	event.ToolName = call.Name
	event.ToolCall = &call
	event.Approval = &req
	event.Message = "Approval needed for tool " + call.Name
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

func (e *Executor) captureRollbackSnapshot(toolName string) (string, error) {
	if e.store == nil {
		return "", fmt.Errorf("artifact store not available for rollback")
	}

	cmd := exec.Command("git", "diff")
	if e.workspace != "" {
		cmd.Dir = e.workspace
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// git diff exits 1 when there are changes (expected), 0 when clean, other on error.
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", err
		}
	}

	// Accept exit code 0 (clean) or 1 (dirty); anything else is a failure.
	if exitCode > 1 {
		return "", fmt.Errorf("git diff exited %d: %s", exitCode, stderr.String())
	}

	record, err := e.store.Write(artifact.WriteRequest{
		Tool:     toolName,
		Kind:     "rollback",
		ExitCode: exitCode,
		Inputs:   map[string]any{"tool": toolName},
		Payloads: []artifact.Payload{
			{Name: "pre_state.diff", Data: stdout.Bytes()},
			{Name: "stderr.txt", Data: stderr.Bytes()},
		},
	})
	if err != nil {
		return "", err
	}
	return record.ID, nil
}
