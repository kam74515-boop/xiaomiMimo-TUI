package benchmark

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mimo-tui/internal/agent"
	contextmap "mimo-tui/internal/context"
	"mimo-tui/internal/core"
	"mimo-tui/internal/tools"
)

// Task defines a benchmark coding task.
type Task struct {
	Name        string
	Prompt      string
	MaxSteps    int
	Timeout     time.Duration
	ExpectTools []string // expected tool names in sequence (prefix match)
	Validate    func(result TaskResult) (bool, string)
}

// TaskResult captures what happened during a task.
type TaskResult struct {
	TaskName         string
	Success          bool
	FailureReason    string
	Duration         time.Duration
	ToolCount        int
	ToolSequence     []string
	TokenEstimate    int
	ArtifactsCreated []string
	RollbackCount    int
	TraceStages      []string
	Errors           []string
	FinalSummary     string
}

// RunConfig holds benchmark configuration.
type RunConfig struct {
	Client    core.ModelClient
	Workspace string
	Bus       *core.Bus
	Tasks     []Task
}

// RunAll executes all tasks and returns results.
func RunAll(cfg RunConfig) ([]TaskResult, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("benchmark: client is required")
	}
	if len(cfg.Tasks) == 0 {
		return nil, fmt.Errorf("benchmark: no tasks provided")
	}

	// Ensure workspace exists.
	if cfg.Workspace == "" {
		cfg.Workspace = "."
	}
	if absWS, err := filepath.Abs(cfg.Workspace); err == nil {
		cfg.Workspace = absWS
	}

	results := make([]TaskResult, 0, len(cfg.Tasks))
	for _, task := range cfg.Tasks {
		result := runSingle(cfg, task)
		results = append(results, result)
	}
	return results, nil
}

// runSingle executes a single benchmark task.
func runSingle(cfg RunConfig, task Task) TaskResult {
	start := time.Now()

	result := TaskResult{
		TaskName: task.Name,
	}

	// Create a fresh bus for this task if none provided, or create a child bus.
	taskBus := core.NewBus()

	// Subscribe to capture events.
	eventCh := taskBus.Subscribe(1024)

	// Collect events in background since Bus never closes channels.
	var events []core.AgentEvent
	doneCollecting := make(chan struct{})
	go func() {
		defer close(doneCollecting)
		// Read until we see EventDone or timeout.
		timeout := task.Timeout
		if timeout <= 0 {
			timeout = 120 * time.Second
		}
		timer := time.NewTimer(timeout + 5*time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-eventCh:
				events = append(events, event)
				if event.Type == core.EventDone {
					return
				}
			case <-timer.C:
				return
			}
		}
	}()

	// Create fresh context manager.
	ctxMgr := contextmap.New(contextmap.DefaultWindowTokens)

	// Create fresh tool registry and executor.
	registry := tools.NewDefaultRegistry(cfg.Workspace)

	// Set up auto-approve approval channel.
	approvalCh := make(chan core.ApprovalRequest, 16)
	// Pass nil bus to the executor; agent.Loop already publishes all events
	// to taskBus. Passing the same bus would cause duplicate EventToolStart,
	// EventToolResult, and EventObservation events.
	executor := tools.NewExecutor(
		registry,
		nil,
		tools.WithApprovalChannel(approvalCh),
		tools.WithAllowedAskTools(), // no pre-approved; use channel
	)

	// Drain approval channel: auto-approve everything.
	go func() {
		for req := range approvalCh {
			req.Response <- core.ApprovalDecision{Allowed: true, Reason: "benchmark auto-approve"}
		}
	}()

	// Build tool specs from registry.
	toolSpecs := registry.ToolSpecs()

	// Configure the loop.
	config := agent.LoopConfig{
		MaxSteps:    task.MaxSteps,
		StepTimeout: 30 * time.Second,
	}
	if task.Timeout > 0 {
		config.TotalTimeout = task.Timeout
	} else {
		config.TotalTimeout = 120 * time.Second
	}

	// Run the agent loop.
	ctx := context.Background()
	if task.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, task.Timeout)
		defer cancel()
	}

	_, loopErr := agent.Loop(
		ctx,
		task.Prompt,
		cfg.Client,
		executor,
		ctxMgr,
		toolSpecs,
		taskBus,
		config,
		nil, // no history
	)

	// Close the approval channel to stop the drain goroutine.
	close(approvalCh)

	// Wait for event collector to finish.
	<-doneCollecting

	// Process collected events to build result.
	result.Duration = time.Since(start)
	result = processEvents(events, result)

	if loopErr != nil {
		result.Errors = append(result.Errors, loopErr.Error())
	}

	// Run validation.
	if task.Validate != nil {
		ok, reason := task.Validate(result)
		result.Success = ok
		if !ok {
			result.FailureReason = reason
		}
	}

	return result
}

// processEvents processes a collected slice of events into TaskResult fields.
func processEvents(events []core.AgentEvent, result TaskResult) TaskResult {
	seenArtifacts := map[string]bool{}
	traceStages := []string{}

	for _, event := range events {
		switch event.Type {
		case core.EventToolStart:
			if event.ToolCall != nil {
				result.ToolSequence = append(result.ToolSequence, event.ToolName)
				result.ToolCount = len(result.ToolSequence)
			}
		case core.EventToolResult:
			if event.ToolCall != nil {
				// Tool result events may carry artifact info in the message.
				// We track tool-level errors here.
			}
			if event.Err != "" {
				result.Errors = append(result.Errors, event.Err)
			}
		case core.EventObservation:
			if event.Observation != nil {
				if event.Observation.ArtifactID != "" {
					if !seenArtifacts[event.Observation.ArtifactID] {
						seenArtifacts[event.Observation.ArtifactID] = true
						result.ArtifactsCreated = append(result.ArtifactsCreated, event.Observation.ArtifactID)
					}
				}
				if event.Observation.RollbackArtifactID != "" {
					result.RollbackCount++
				}
			}
		case core.EventTraceUpdate:
			if event.Trace != nil {
				stage := string(event.Trace.Stage)
				if stage != "" {
					traceStages = append(traceStages, stage)
				}
			}
		case core.EventCostUpdate:
			if event.Cost != nil {
				result.TokenEstimate += event.Cost.TotalTokens
			}
		case core.EventMessageDelta:
			result.FinalSummary += event.Message
		case core.EventError:
			if event.Err != "" {
				result.Errors = append(result.Errors, event.Err)
			}
		}
	}

	result.TraceStages = traceStages
	return result
}

// MustRunAll is a convenience wrapper that calls RunAll and panics on error.
func MustRunAll(cfg RunConfig) []TaskResult {
	results, err := RunAll(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark: %v\n", err)
		os.Exit(1)
	}
	return results
}
