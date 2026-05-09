package agent

import (
	"strings"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

func TestSubAgentTaskActivityLifecycleMapping(t *testing.T) {
	created := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	started := created.Add(2 * time.Minute)
	ended := started.Add(5 * time.Minute)

	base := SubAgentTask{
		ID:        "subagent-visibility",
		Name:      "visibility-worker",
		Title:     "Subagent visibility",
		Goal:      "Make sub-agent lifecycle observable",
		Role:      "visibility worker",
		ModelName: "mimo-coder",
		ParentID:  "phase-41",
		CreatedAt: created,
	}

	pending := base
	pending.Status = SubAgentPending
	assertActivity(t, pending.Activity(), core.ActivityPlanned, base, created, time.Time{})

	running := base
	running.Status = SubAgentRunning
	running.StartedAt = started
	assertActivity(t, running.Activity(), core.ActivityRunning, base, started, time.Time{})

	done := running
	done.Status = SubAgentDone
	done.Result = "published visible lifecycle event"
	done.CompletedAt = ended
	doneActivity := done.Activity()
	assertActivity(t, doneActivity, core.ActivityDone, base, started, ended)
	if doneActivity.Summary != done.Result {
		t.Fatalf("done summary = %q, want result %q", doneActivity.Summary, done.Result)
	}

	failed := running
	failed.Status = SubAgentFailed
	failed.Error = "model timed out"
	failed.CompletedAt = ended
	failedActivity := failed.Activity()
	assertActivity(t, failedActivity, core.ActivityFailed, base, started, ended)
	if !strings.Contains(failedActivity.Summary, failed.Error) {
		t.Fatalf("failed summary = %q, want error %q", failedActivity.Summary, failed.Error)
	}
}

func TestSubAgentTaskActivityStatusOverride(t *testing.T) {
	task := NewSubAgentTask("subagent-blocked", "Wait for dependency", "")

	activity := task.ActivityEvent(core.ActivityBlocked)
	if activity.Status != core.ActivityBlocked {
		t.Fatalf("override status = %q, want %q", activity.Status, core.ActivityBlocked)
	}

	activity = task.ActivityEvent(core.ActivitySkipped)
	if activity.Status != core.ActivitySkipped {
		t.Fatalf("override status = %q, want %q", activity.Status, core.ActivitySkipped)
	}
}

func TestSubAgentStepCompactSummaryVisibleAndBounded(t *testing.T) {
	step := SubAgentStep{
		Number:      3,
		Action:      "inspect subagent lifecycle records",
		Observation: "found parent id and status transitions " + strings.Repeat("with verbose diagnostic detail ", 20),
		Status:      StepDone,
	}

	summary := step.CompactSummary()
	for _, want := range []string{"#3", "done", "inspect subagent lifecycle records", "found parent id"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not contain visible detail %q", summary, want)
		}
	}
	if got := len([]rune(summary)); got > subAgentStepSummaryLimit {
		t.Fatalf("summary length = %d, want <= %d: %q", got, subAgentStepSummaryLimit, summary)
	}
	if strings.Contains(summary, "\n") {
		t.Fatalf("summary should be single-line, got %q", summary)
	}
}

func TestSubAgentTaskActivityPreservesParentChildRelationship(t *testing.T) {
	parent := NewSubAgentTask("parent-agent", "Coordinate Phase 41", "")
	child := NewSubAgentTask("child-agent", "Expose subagent lifecycle", parent.ID)

	parentActivity := parent.Activity()
	childActivity := child.Activity()

	if parentActivity.ParentID != "" {
		t.Fatalf("parent activity ParentID = %q, want empty", parentActivity.ParentID)
	}
	if childActivity.ParentID != parent.ID {
		t.Fatalf("child activity ParentID = %q, want %q", childActivity.ParentID, parent.ID)
	}
	if childActivity.ID != child.ID {
		t.Fatalf("child activity ID = %q, want %q", childActivity.ID, child.ID)
	}
}

func assertActivity(t *testing.T, activity core.ActivityEvent, status core.ActivityStatus, task SubAgentTask, started, ended time.Time) {
	t.Helper()

	if activity.ID != task.ID {
		t.Fatalf("ID = %q, want %q", activity.ID, task.ID)
	}
	if activity.ParentID != task.ParentID {
		t.Fatalf("ParentID = %q, want %q", activity.ParentID, task.ParentID)
	}
	if activity.Kind != core.ActivitySubAgent {
		t.Fatalf("Kind = %q, want %q", activity.Kind, core.ActivitySubAgent)
	}
	if activity.Name != task.Name {
		t.Fatalf("Name = %q, want %q", activity.Name, task.Name)
	}
	if activity.Title != task.Title {
		t.Fatalf("Title = %q, want %q", activity.Title, task.Title)
	}
	if activity.Detail != task.Goal {
		t.Fatalf("Detail = %q, want goal %q", activity.Detail, task.Goal)
	}
	if activity.Status != status {
		t.Fatalf("Status = %q, want %q", activity.Status, status)
	}
	if activity.Role != task.Role {
		t.Fatalf("Role = %q, want %q", activity.Role, task.Role)
	}
	if activity.ModelName != task.ModelName {
		t.Fatalf("ModelName = %q, want %q", activity.ModelName, task.ModelName)
	}
	if !activity.StartedAt.Equal(started) {
		t.Fatalf("StartedAt = %v, want %v", activity.StartedAt, started)
	}
	if !activity.EndedAt.Equal(ended) {
		t.Fatalf("EndedAt = %v, want %v", activity.EndedAt, ended)
	}
}
