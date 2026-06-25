package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
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
		e.publishActivityResult(call, nil, result, "")
		e.publishObservation(call, observation)
		return result, observation
	}

	tool, ok := e.registry.Get(call.Name)
	if !ok {
		result := core.ToolResult{ExitCode: 127, Error: fmt.Sprintf("unknown tool %q", call.Name)}
		observation := executorObservation(call.Name, result)
		e.publishResult(call, result)
		e.publishActivityResult(call, nil, result, "")
		e.publishObservation(call, observation)
		return result, observation
	}

	e.publishActivity(call, tool, core.ActivityRunning, "Tool started", "", "", nil)

	// Evaluate permission: policy.toml overrides tool's default if configured.
	grade := tool.Safety(call.Input)
	permission := tool.Permission(call.Input)
	if e.policyConfig != nil {
		permission = config.EvaluatePolicy(*e.policyConfig, call.Name, call.Input, grade)
	}
	approvalTimeout := e.approvalTimeout(grade) // per-safety-grade, then global, then default

	// If the tool asks for permission, there is an approval channel, and the tool
	// is not already whitelisted, request user approval via the channel.
	if permission.Behavior == core.PermissionAsk && e.ApprovalCh != nil && !e.policy.isAllowedAsk(tool.Name()) {
		req := core.ApprovalRequest{
			ToolCall:       call,
			Permission:     permission,
			TimeoutSeconds: int(approvalTimeout / time.Second),
			Response:       make(chan core.ApprovalDecision, 1),
		}
		select {
		case e.ApprovalCh <- req:
			e.publishApprovalNeeded(call, req)
			e.publishActivity(call, tool, core.ActivityBlocked, "Waiting for approval", permission.Reason, permission.Reason, nil)
		default:
			result := core.ToolResult{
				Content:  fmt.Sprintf("tool %s approval channel full", tool.Name()),
				ExitCode: permissionDeniedExitCode,
				Error:    "approval channel is full; cannot request approval",
			}
			observation := executorObservation(tool.Name(), result)
			e.publishResult(call, result)
			e.publishActivity(call, tool, core.ActivityFailed, "Approval request failed", result.Error, result.Error, nil)
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
				e.publishActivity(call, tool, core.ActivitySkipped, "Approval denied", reason, reason, nil)
				e.publishObservation(call, observation)
				return result, observation
			}
		// Approved: fall through to normal execution.
		case <-time.After(approvalTimeout):
			timeout := approvalTimeout
			result := core.ToolResult{
				Content:  fmt.Sprintf("tool %s approval timed out", tool.Name()),
				ExitCode: permissionDeniedExitCode,
				Error:    fmt.Sprintf("approval timed out after %s", timeout),
			}
			observation := executorObservation(tool.Name(), result)
			e.publishResult(call, result)
			e.publishActivity(call, tool, core.ActivityFailed, "Approval timed out", result.Error, result.Error, nil)
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
			e.publishActivity(call, tool, core.ActivitySkipped, "Tool skipped", reason, reason, nil)
			e.publishObservation(call, observation)
			return result, observation
		}
	}

	// Capture pre-tool state for mutating tools: the tracked-file diff and the
	// set of untracked files. The rollback artifact is written AFTER the tool so
	// we can record exactly the files this tool created (precise, not a broad
	// "anything untracked now" set that could delete unrelated files/artifacts).
	mutating := tool.Safety(call.Input) != core.SafetyReadOnly
	var preDiff []byte
	var preUntracked map[string]bool
	var preErr error
	if mutating {
		preDiff, preUntracked, preErr = e.capturePreRollback()
	}

	result := tool.Run(ctx, call.Input)
	e.publishResult(call, result)

	// Now that the tool has run, write the rollback artifact recording the
	// tracked-file diff and the exact files the tool created.
	var rollbackArtifactID string
	if mutating {
		if id, err := e.writeRollback(call.Name, preDiff, preUntracked, preErr); err == nil {
			rollbackArtifactID = id
		} else if e.bus != nil {
			e.bus.Publish(core.AgentEvent{
				Type:        core.EventObservation,
				Time:        time.Now(),
				Observation: &core.Observation{Summary: fmt.Sprintf("rollback snapshot unavailable for %s (%v); proceeding without rollback", call.Name, err)},
			})
		}
	}

	e.publishActivityResult(call, tool, result, rollbackArtifactID)

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

func (e *Executor) approvalTimeout(grade core.SafetyGrade) time.Duration {
	if e != nil && e.policyConfig != nil {
		if s := e.policyConfig.TimeoutForGrade(grade); s > 0 {
			return time.Duration(s) * time.Second
		}
		if e.policyConfig.ApprovalTimeout > 0 {
			return time.Duration(e.policyConfig.ApprovalTimeout) * time.Second
		}
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

func (e *Executor) publishActivityResult(call core.ToolCall, tool core.Tool, result core.ToolResult, rollbackArtifactID string) {
	status, summary, detail := activityResult(tool, result)
	e.publishActivity(call, tool, status, summary, detail, result.Error, activityArtifacts(result.ArtifactID, rollbackArtifactID))
}

func (e *Executor) publishActivity(call core.ToolCall, tool core.Tool, status core.ActivityStatus, summary, detail, reason string, artifacts []string) {
	if e == nil || e.bus == nil {
		return
	}
	now := time.Now()
	activity := core.ActivityEvent{
		ID:        activityID(call),
		Kind:      activityKind(tool),
		Name:      call.Name,
		Title:     activityTitle(call, tool),
		Status:    status,
		Summary:   shortActivityText(summary),
		Detail:    shortActivityText(detail),
		Reason:    shortActivityText(reason),
		ToolName:  activityToolName(call, tool),
		Artifacts: artifacts,
	}
	if mcp, ok := tool.(mcpMetadata); ok {
		activity.Kind = core.ActivityMCP
		activity.ToolName = mcp.ToolName()
		activity.ServerName = mcp.ServerName()
	}
	if status == core.ActivityRunning {
		activity.StartedAt = now
	} else if status == core.ActivityDone || status == core.ActivityFailed || status == core.ActivitySkipped {
		activity.EndedAt = now
	}

	event := core.NewEvent(core.EventActivityUpdate)
	event.ToolName = call.Name
	event.ToolCall = &call
	event.Activity = &activity
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

type mcpMetadata interface {
	ServerName() string
	ToolName() string
}

type stubbedTool interface {
	Stubbed() bool
}

func activityResult(tool core.Tool, result core.ToolResult) (core.ActivityStatus, string, string) {
	if isStubbedMCPTool(tool) && result.ExitCode == 0 && result.Error == "" {
		return core.ActivityBlocked, "MCP stub not connected", mcpStubDetail(tool)
	}
	if result.ExitCode != 0 || result.Error != "" {
		detail := result.Error
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return core.ActivityFailed, "Tool failed", detail
	}
	if _, ok := tool.(mcpMetadata); ok {
		return core.ActivityDone, "MCP tool completed", mcpToolDetail(tool)
	}
	return core.ActivityDone, "Tool completed", ""
}

func activityID(call core.ToolCall) string {
	if call.ID != "" {
		return call.ID
	}
	if call.Name != "" {
		return "tool:" + call.Name
	}
	return "tool:unknown"
}

func activityKind(tool core.Tool) core.ActivityKind {
	if _, ok := tool.(mcpMetadata); ok {
		return core.ActivityMCP
	}
	return core.ActivityTool
}

func activityTitle(call core.ToolCall, tool core.Tool) string {
	if mcp, ok := tool.(mcpMetadata); ok {
		return fmt.Sprintf("MCP %s/%s", mcp.ServerName(), mcp.ToolName())
	}
	if call.Name != "" {
		return call.Name
	}
	return "tool"
}

func activityToolName(call core.ToolCall, tool core.Tool) string {
	if mcp, ok := tool.(mcpMetadata); ok {
		return mcp.ToolName()
	}
	if tool != nil {
		return tool.Name()
	}
	return call.Name
}

func activityArtifacts(ids ...string) []string {
	seen := map[string]bool{}
	var artifacts []string
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		artifacts = append(artifacts, id)
	}
	return artifacts
}

func isStubbedMCPTool(tool core.Tool) bool {
	if _, ok := tool.(mcpMetadata); !ok {
		return false
	}
	stub, ok := tool.(stubbedTool)
	return ok && stub.Stubbed()
}

func mcpStubDetail(tool core.Tool) string {
	if mcp, ok := tool.(mcpMetadata); ok {
		return fmt.Sprintf("server %q tool %q is configured but no MCP connection is active", mcp.ServerName(), mcp.ToolName())
	}
	return "MCP tool is configured but no MCP connection is active"
}

func mcpToolDetail(tool core.Tool) string {
	if mcp, ok := tool.(mcpMetadata); ok {
		return fmt.Sprintf("server %q tool %q", mcp.ServerName(), mcp.ToolName())
	}
	return ""
}

func shortActivityText(text string) string {
	const limit = 240
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-3]) + "..."
}

// capturePreRollback records pre-tool state: the tracked-file diff and the set
// of untracked files. Called before a mutating tool runs.
func (e *Executor) capturePreRollback() (diff []byte, untracked map[string]bool, err error) {
	if e.store == nil {
		return nil, nil, fmt.Errorf("artifact store not available for rollback")
	}
	cmd := exec.Command("git", "diff")
	if e.workspace != "" {
		cmd.Dir = e.workspace
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			if exitErr.ExitCode() > 1 { // 0 clean, 1 dirty, >1 real error
				return nil, nil, fmt.Errorf("git diff exited %d: %s", exitErr.ExitCode(), stderr.String())
			}
		} else {
			return nil, nil, runErr
		}
	}
	return stdout.Bytes(), e.untrackedSet(), nil
}

// writeRollback writes the rollback artifact after the tool has run, recording
// the pre-tool diff and exactly the files this tool created (untracked files that
// appeared since the snapshot, excluding the agent's own .mimo bookkeeping dir).
func (e *Executor) writeRollback(toolName string, preDiff []byte, preUntracked map[string]bool, preErr error) (string, error) {
	if preErr != nil {
		return "", preErr
	}
	if e.store == nil {
		return "", fmt.Errorf("artifact store not available for rollback")
	}
	var created []string
	for f := range e.untrackedSet() {
		if preUntracked[f] {
			continue
		}
		if f == ".mimo" || strings.HasPrefix(f, ".mimo/") {
			continue // never roll back the agent's own artifact/session dir
		}
		created = append(created, f)
	}
	sort.Strings(created)

	exitCode := 0
	if len(preDiff) > 0 {
		exitCode = 1
	}
	record, err := e.store.Write(artifact.WriteRequest{
		Tool:     toolName,
		Kind:     "rollback",
		ExitCode: exitCode,
		Inputs:   map[string]any{"tool": toolName},
		Payloads: []artifact.Payload{
			{Name: "pre_state.diff", Data: preDiff},
			{Name: "created_files.txt", Data: []byte(strings.Join(created, "\n"))},
		},
	})
	if err != nil {
		return "", err
	}
	return record.ID, nil
}

// untrackedSet returns the workspace's current untracked files as a set.
func (e *Executor) untrackedSet() map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.Split(gitUntrackedFiles(e.workspace), "\n") {
		if s := strings.TrimSpace(f); s != "" {
			set[s] = true
		}
	}
	return set
}

// gitUntrackedFiles returns the workspace's untracked files (one per line) at
// call time, or "" on error. Used to snapshot pre-tool state for rollback.
func gitUntrackedFiles(workspace string) string {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	if workspace != "" {
		cmd.Dir = workspace
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
