package agent

import (
	"fmt"
	"strings"
	"time"

	"mimo-tui/internal/core"
)

const (
	subAgentActivitySummaryLimit = 220
	subAgentStepSummaryLimit     = 160
)

// SubAgentStatus represents the lifecycle state of a sub-agent task.
type SubAgentStatus string

const (
	SubAgentPending SubAgentStatus = "pending"
	SubAgentRunning SubAgentStatus = "running"
	SubAgentDone    SubAgentStatus = "done"
	SubAgentFailed  SubAgentStatus = "failed"
)

// SubAgentStepStatus represents the state of a single step within a task.
type SubAgentStepStatus string

const (
	StepPending SubAgentStepStatus = "pending"
	StepRunning SubAgentStepStatus = "running"
	StepDone    SubAgentStepStatus = "done"
	StepFailed  SubAgentStepStatus = "failed"
	StepSkipped SubAgentStepStatus = "skipped"
)

// SubAgentTask represents a decomposed unit of work that can be delegated to
// a sub-agent. Tasks form a tree: a parent task can spawn child tasks, and
// each task tracks its own sequence of steps.
//
// This is a data structure only. Execution logic will be added in a future
// phase when the agent runtime supports parallel task dispatch.
type SubAgentTask struct {
	// ID uniquely identifies this task within a session.
	ID string `json:"id"`

	// Name is a short display label for the sub-agent task.
	Name string `json:"name,omitempty"`

	// Title is a human-readable heading for dashboards and activity streams.
	Title string `json:"title,omitempty"`

	// Goal is a natural-language description of what the task should achieve.
	Goal string `json:"goal"`

	// Role describes the delegated responsibility of this sub-agent.
	Role string `json:"role,omitempty"`

	// ModelName records the model used by the sub-agent, when known.
	ModelName string `json:"model_name,omitempty"`

	// Status tracks the task lifecycle.
	Status SubAgentStatus `json:"status"`

	// ParentID references the task that spawned this one. Empty for top-level tasks.
	ParentID string `json:"parent_id,omitempty"`

	// Steps is the ordered sequence of actions taken to accomplish the goal.
	Steps []SubAgentStep `json:"steps"`

	// Result is a summary produced when the task completes (success or failure).
	Result string `json:"result,omitempty"`

	// Error contains the failure reason when Status is SubAgentFailed.
	Error string `json:"error,omitempty"`

	// CreatedAt records when the task was created.
	CreatedAt time.Time `json:"created_at"`

	// StartedAt records when execution began.
	StartedAt time.Time `json:"started_at,omitempty"`

	// CompletedAt records when the task finished (success or failure).
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// SubAgentStep represents a single action within a sub-agent task. Each step
// corresponds to one tool call or reasoning action.
type SubAgentStep struct {
	// Number is the 1-based ordinal of this step within the task.
	Number int `json:"number"`

	// Action describes what was attempted (e.g. tool name, reasoning summary).
	Action string `json:"action"`

	// Observation captures the outcome of the action.
	Observation string `json:"observation,omitempty"`

	// Status tracks the step lifecycle.
	Status SubAgentStepStatus `json:"status"`

	// Error contains the failure reason when Status is StepFailed.
	Error string `json:"error,omitempty"`

	// StartedAt records when the step began.
	StartedAt time.Time `json:"started_at,omitempty"`

	// CompletedAt records when the step finished.
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// NewSubAgentTask creates a new task in Pending status with the current time.
func NewSubAgentTask(id, goal, parentID string) SubAgentTask {
	return SubAgentTask{
		ID:        id,
		Goal:      goal,
		Status:    SubAgentPending,
		ParentID:  parentID,
		Steps:     []SubAgentStep{},
		CreatedAt: time.Now(),
	}
}

// AddStep appends a new step to the task and returns its index.
func (t *SubAgentTask) AddStep(action string) int {
	step := SubAgentStep{
		Number:    len(t.Steps) + 1,
		Action:    action,
		Status:    StepPending,
		StartedAt: time.Now(),
	}
	t.Steps = append(t.Steps, step)
	return len(t.Steps) - 1
}

// Activity converts the task into a visible high-level activity event.
func (t SubAgentTask) Activity() core.ActivityEvent {
	return t.ActivityEvent()
}

// ActivityEvent converts the task into a visible high-level activity event.
// Pass an override status when the runtime needs to surface states such as
// blocked or skipped that are not part of SubAgentStatus.
func (t SubAgentTask) ActivityEvent(statusOverride ...core.ActivityStatus) core.ActivityEvent {
	status := subAgentActivityStatus(t.Status)
	if len(statusOverride) > 0 && statusOverride[0] != "" {
		status = statusOverride[0]
	}

	return core.ActivityEvent{
		ID:        t.ID,
		ParentID:  t.ParentID,
		Kind:      core.ActivitySubAgent,
		Name:      t.activityName(),
		Title:     t.activityTitle(),
		Status:    status,
		Summary:   t.activitySummary(),
		Detail:    compactSubAgentText(t.Goal, subAgentActivitySummaryLimit),
		Role:      strings.TrimSpace(t.Role),
		ModelName: strings.TrimSpace(t.ModelName),
		StartedAt: t.activityStartedAt(),
		EndedAt:   t.CompletedAt,
	}
}

func (t SubAgentTask) activityName() string {
	switch {
	case strings.TrimSpace(t.Name) != "":
		return strings.TrimSpace(t.Name)
	case strings.TrimSpace(t.Title) != "":
		return strings.TrimSpace(t.Title)
	case strings.TrimSpace(t.Goal) != "":
		return compactSubAgentText(t.Goal, 80)
	case strings.TrimSpace(t.ID) != "":
		return strings.TrimSpace(t.ID)
	default:
		return "subagent"
	}
}

func (t SubAgentTask) activityTitle() string {
	switch {
	case strings.TrimSpace(t.Title) != "":
		return strings.TrimSpace(t.Title)
	case strings.TrimSpace(t.Goal) != "":
		return compactSubAgentText(t.Goal, 120)
	default:
		return t.activityName()
	}
}

func (t SubAgentTask) activitySummary() string {
	switch {
	case strings.TrimSpace(t.Error) != "":
		return compactSubAgentText("failed: "+t.Error, subAgentActivitySummaryLimit)
	case strings.TrimSpace(t.Result) != "":
		return compactSubAgentText(t.Result, subAgentActivitySummaryLimit)
	case len(t.Steps) > 0:
		latest := t.Steps[len(t.Steps)-1].CompactSummary()
		return compactSubAgentText(fmt.Sprintf("%d step(s); latest %s", len(t.Steps), latest), subAgentActivitySummaryLimit)
	case strings.TrimSpace(t.Goal) != "":
		return compactSubAgentText(t.Goal, subAgentActivitySummaryLimit)
	default:
		return ""
	}
}

func (t SubAgentTask) activityStartedAt() time.Time {
	if !t.StartedAt.IsZero() {
		return t.StartedAt
	}
	if t.Status == SubAgentPending {
		return t.CreatedAt
	}
	return time.Time{}
}

// CompactSummary returns a short, UI-safe summary of a step without embedding
// raw tool output or long observations.
func (s SubAgentStep) CompactSummary() string {
	parts := []string{}
	if s.Number > 0 {
		parts = append(parts, fmt.Sprintf("#%d", s.Number))
	}
	if s.Status != "" {
		parts = append(parts, string(s.Status))
	}

	summary := strings.Join(parts, " ")
	action := strings.TrimSpace(s.Action)
	switch {
	case strings.TrimSpace(s.Error) != "":
		summary = joinSubAgentSummary(summary, action, "error: "+s.Error)
	case strings.TrimSpace(s.Observation) != "":
		summary = joinSubAgentSummary(summary, action, "observed: "+s.Observation)
	default:
		summary = joinSubAgentSummary(summary, action)
	}
	return compactSubAgentText(summary, subAgentStepSummaryLimit)
}

func joinSubAgentSummary(parts ...string) string {
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			trimmed = append(trimmed, part)
		}
	}
	return strings.Join(trimmed, " - ")
}

func subAgentActivityStatus(status SubAgentStatus) core.ActivityStatus {
	switch status {
	case SubAgentPending:
		return core.ActivityPlanned
	case SubAgentRunning:
		return core.ActivityRunning
	case SubAgentDone:
		return core.ActivityDone
	case SubAgentFailed:
		return core.ActivityFailed
	default:
		return core.ActivityBlocked
	}
}

func compactSubAgentText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

// CompleteStep marks a step as done with an observation.
func (t *SubAgentTask) CompleteStep(index int, observation string) error {
	if index < 0 || index >= len(t.Steps) {
		return fmt.Errorf("step index %d out of range [0, %d)", index, len(t.Steps))
	}
	t.Steps[index].Observation = observation
	t.Steps[index].Status = StepDone
	t.Steps[index].CompletedAt = time.Now()
	return nil
}

// FailStep marks a step as failed with an error message.
func (t *SubAgentTask) FailStep(index int, errMsg string) error {
	if index < 0 || index >= len(t.Steps) {
		return fmt.Errorf("step index %d out of range [0, %d)", index, len(t.Steps))
	}
	t.Steps[index].Error = errMsg
	t.Steps[index].Status = StepFailed
	t.Steps[index].CompletedAt = time.Now()
	return nil
}

// Start transitions the task from Pending to Running.
func (t *SubAgentTask) Start() error {
	if t.Status != SubAgentPending {
		return fmt.Errorf("cannot start task in %q status", t.Status)
	}
	t.Status = SubAgentRunning
	t.StartedAt = time.Now()
	return nil
}

// Complete transitions the task to Done with a result summary.
func (t *SubAgentTask) Complete(result string) error {
	if t.Status != SubAgentRunning {
		return fmt.Errorf("cannot complete task in %q status", t.Status)
	}
	t.Status = SubAgentDone
	t.Result = result
	t.CompletedAt = time.Now()
	return nil
}

// Fail transitions the task to Failed with an error message.
func (t *SubAgentTask) Fail(errMsg string) error {
	if t.Status != SubAgentRunning {
		return fmt.Errorf("cannot fail task in %q status", t.Status)
	}
	t.Status = SubAgentFailed
	t.Error = errMsg
	t.CompletedAt = time.Now()
	return nil
}

// IsTerminal returns true if the task is in a final state (done or failed).
func (t *SubAgentTask) IsTerminal() bool {
	return t.Status == SubAgentDone || t.Status == SubAgentFailed
}
