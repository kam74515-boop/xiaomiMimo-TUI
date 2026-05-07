package main

import (
	"fmt"
	"os"
	"time"

	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
	"mimo-tui/internal/tui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	if err := tui.Run(demoEvents(cfg)); err != nil {
		fmt.Fprintf(os.Stderr, "run tui: %v\n", err)
		os.Exit(1)
	}
}

func demoEvents(cfg config.Config) <-chan core.AgentEvent {
	events := make(chan core.AgentEvent, 16)
	go func() {
		defer close(events)

		publish := func(event core.AgentEvent, delay time.Duration) {
			time.Sleep(delay)
			events <- event
		}

		contextEvent := core.NewEvent(core.EventContextUpdate)
		contextEvent.Context = &core.ContextSnapshot{
			WindowTokens:  cfg.Runtime.ContextWindow,
			UsedTokens:    43600,
			PollutionRisk: "low",
			Items: []core.ContextItem{
				{
					ID:            "repo-contracts",
					Tier:          core.TierAnchor,
					Title:         "internal/core event contracts",
					Source:        "internal/core/types.go",
					TokenEstimate: 2100,
					Pinned:        true,
					Reason:        "AgentEvent drives the shell panels.",
				},
				{
					ID:            "runtime-config",
					Tier:          core.TierNear,
					Title:         "loaded MiMo config",
					Source:        ".mimo/config.toml",
					TokenEstimate: 800,
					Reason:        "Demo source uses the configured model and context window.",
				},
				{
					ID:            "tui-shell",
					Tier:          core.TierArtifact,
					Title:         "Bubble Tea four-panel shell",
					Source:        "internal/tui",
					TokenEstimate: 1200,
					Reason:        "Runnable stand-in until the real agent event source is wired.",
				},
			},
		}
		publish(contextEvent, 150*time.Millisecond)

		traceEvent := core.NewEvent(core.EventTraceUpdate)
		traceEvent.Trace = &core.TraceStep{
			ID:     "bootstrap",
			Goal:   "Boot a MiMo-first TUI shell",
			Plan:   "Load config, attach an event source, and render operational panels.",
			Action: "Start Bubble Tea with demo AgentEvent messages.",
			Risk:   "Demo events are not a live agent integration yet.",
			Status: core.TraceRunning,
		}
		publish(traceEvent, 350*time.Millisecond)

		messageEvent := core.NewEvent(core.EventMessageDelta)
		messageEvent.Message = fmt.Sprintf("Loaded model %s for workspace %s.\n", cfg.Provider.Model, cfg.Runtime.Workspace)
		publish(messageEvent, 300*time.Millisecond)

		toolStart := core.NewEvent(core.EventToolStart)
		toolStart.ToolName = "workspace.scan"
		toolStart.Message = "Checking core/config contracts and TUI startup path."
		publish(toolStart, 300*time.Millisecond)

		toolResult := core.NewEvent(core.EventToolResult)
		toolResult.ToolName = "workspace.scan"
		toolResult.Message = "Found AgentEvent, ContextSnapshot, TraceStep, and config.Load."
		publish(toolResult, 550*time.Millisecond)

		observationEvent := core.NewEvent(core.EventObservation)
		observationEvent.Observation = &core.Observation{
			Summary:          "The TUI can run against any channel of core.AgentEvent values.",
			StateDelta:       "Demo source is active.",
			RiskDelta:        "Real agent wiring remains future work.",
			NextAffordances:  []string{"Connect internal/agent publisher", "Stream tool permissions"},
			ContextPlacement: core.TierAnchor,
		}
		publish(observationEvent, 350*time.Millisecond)

		traceDone := core.NewEvent(core.EventTraceUpdate)
		traceDone.Trace = &core.TraceStep{
			ID:          "bootstrap",
			Goal:        "Boot a MiMo-first TUI shell",
			Plan:        "Load config, attach an event source, and render operational panels.",
			Action:      "Start Bubble Tea with demo AgentEvent messages.",
			Observation: "Four panels are receiving and rendering events.",
			Risk:        "Demo events are not a live agent integration yet.",
			Status:      core.TraceDone,
			EndedAt:     time.Now(),
		}
		publish(traceDone, 250*time.Millisecond)

		costEvent := core.NewEvent(core.EventCostUpdate)
		costEvent.Cost = &core.CostUpdate{
			InputTokens:  18400,
			OutputTokens: 920,
			TotalTokens:  19320,
			EstimatedUSD: 0.0386,
		}
		publish(costEvent, 250*time.Millisecond)

		finalMessage := core.NewEvent(core.EventMessageDelta)
		finalMessage.Message = "Demo stream complete. Press tab to move focus or q to quit.\n"
		publish(finalMessage, 250*time.Millisecond)

		done := core.NewEvent(core.EventDone)
		done.Message = "demo event source complete"
		publish(done, 150*time.Millisecond)
	}()
	return events
}
