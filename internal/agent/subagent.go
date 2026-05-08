package agent

import (
	"fmt"
	"time"
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

	// Goal is a natural-language description of what the task should achieve.
	Goal string `json:"goal"`

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
