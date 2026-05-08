package benchmark

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"mimo-tui/internal/agent"
	contextmap "mimo-tui/internal/context"
	"mimo-tui/internal/core"
	"mimo-tui/internal/session"
	"mimo-tui/internal/tools"
)

// EnduranceConfig configures a long-run test.
type EnduranceConfig struct {
	Prompts      []string      // sequence of prompts to run
	MaxStepsEach int           // max steps per prompt
	Timeout      time.Duration // total timeout for the entire run
	InterruptAt  int           // interrupt after this many prompts (0 = no interrupt)
	ResumeAfter  bool          // resume session after interrupt
}

// EnduranceResult captures long-run behavior.
type EnduranceResult struct {
	PromptsCompleted int
	TotalSteps       int
	TotalDuration    time.Duration
	Interrupted      bool
	Resumed          bool
	EventCount       int
	GoroutineCount   int
	Errors           []string
	SessionIDs       []string
}

// RunEndurance executes a multi-prompt endurance test.
//
// Each prompt is run through agent.Loop sequentially. Between prompts the
// runner builds a resume summary and extracts conversation history so that
// the next prompt benefits from prior context. If InterruptAt is set the
// context is cancelled after that many prompts; if ResumeAfter is also set
// the runner creates a fresh context and continues with reconstructed history.
func RunEndurance(client core.ModelClient, workspace string, cfg EnduranceConfig) EnduranceResult {
	start := time.Now()

	if workspace == "" {
		workspace = "."
	}
	if absWS, err := filepath.Abs(workspace); err == nil {
		workspace = absWS
	}

	goroutinesStart := runtime.NumGoroutine()
	result := EnduranceResult{}

	if cfg.MaxStepsEach <= 0 {
		cfg.MaxStepsEach = 4
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300 * time.Second
	}

	totalCtx, totalCancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer totalCancel()

	// Shared objects that persist across prompts in a session.
	bus := core.NewBus()
	ctxMgr := contextmap.New(contextmap.DefaultWindowTokens)
	registry := tools.NewDefaultRegistry(workspace)
	toolSpecs := registry.ToolSpecs()

	approvalCh := make(chan core.ApprovalRequest, 16)
	executor := tools.NewExecutor(
		registry,
		nil,
		tools.WithApprovalChannel(approvalCh),
		tools.WithAllowedAskTools(),
	)

	// Auto-approve everything in the endurance harness.
	go func() {
		for req := range approvalCh {
			req.Response <- core.ApprovalDecision{Allowed: true, Reason: "endurance auto-approve"}
		}
	}()

	var history []core.Message
	var allEvents []core.AgentEvent

	for i, prompt := range cfg.Prompts {
		// Check for interruption.
		if cfg.InterruptAt > 0 && i == cfg.InterruptAt {
			result.Interrupted = true
			if !cfg.ResumeAfter {
				break
			}
			// Build resume summary from collected events so far.
			summary := session.BuildResumeSummary(allEvents)
			history = summary.RecentMessages
			result.Resumed = true
			result.SessionIDs = append(result.SessionIDs, fmt.Sprintf("resumed-after-%d", i))
		}

		// Subscribe to capture events for this prompt.
		eventCh := bus.Subscribe(2048)
		promptEvents := collectPromptEvents(bus, eventCh, cfg.MaxStepsEach, 30*time.Second)

		config := agent.LoopConfig{
			MaxSteps:     cfg.MaxStepsEach,
			StepTimeout:  30 * time.Second,
			TotalTimeout: 60 * time.Second,
		}

		_, loopErr := agent.Loop(
			totalCtx,
			prompt,
			client,
			executor,
			ctxMgr,
			toolSpecs,
			bus,
			config,
			history,
		)

		// Wait for events from this prompt.
		events := <-promptEvents
		allEvents = append(allEvents, events...)
		result.EventCount += len(events)

		if loopErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("prompt %d: %v", i, loopErr))
		}

		// Build history for next prompt.
		summary := session.BuildResumeSummary(events)
		history = summary.RecentMessages

		result.PromptsCompleted++
		result.SessionIDs = append(result.SessionIDs, fmt.Sprintf("prompt-%d", i))

		// Check if total context was cancelled.
		if totalCtx.Err() != nil {
			break
		}
	}

	close(approvalCh)

	result.TotalDuration = time.Since(start)
	result.TotalSteps = result.PromptsCompleted * cfg.MaxStepsEach
	result.GoroutineCount = runtime.NumGoroutine() - goroutinesStart

	return result
}

// collectPromptEvents starts a goroutine that drains the event channel
// until EventDone is seen or the collector times out. Returns a channel
// that delivers the collected events when ready.
func collectPromptEvents(bus *core.Bus, eventCh <-chan core.AgentEvent, maxSteps int, timeout time.Duration) <-chan []core.AgentEvent {
	done := make(chan []core.AgentEvent, 1)
	go func() {
		var events []core.AgentEvent
		timer := time.NewTimer(timeout + 5*time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-eventCh:
				events = append(events, event)
				if event.Type == core.EventDone {
					done <- events
					return
				}
			case <-timer.C:
				done <- events
				return
			}
		}
	}()
	return done
}
