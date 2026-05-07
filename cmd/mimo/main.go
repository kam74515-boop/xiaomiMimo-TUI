package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"mimo-tui/internal/agent"
	"mimo-tui/internal/config"
	contextmap "mimo-tui/internal/context"
	"mimo-tui/internal/core"
	"mimo-tui/internal/provider/mimo"
	"mimo-tui/internal/replay"
	"mimo-tui/internal/tools"
	"mimo-tui/internal/tui"
)

func main() {
	smoke := flag.Bool("smoke", false, "run the event pipeline without starting the full-screen TUI")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	events := liveEvents(context.Background(), cfg, promptFromArgs())
	if *smoke {
		if err := runSmoke(os.Stdout, events, 10*time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "smoke: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := tui.Run(events); err != nil {
		fmt.Fprintf(os.Stderr, "run tui: %v\n", err)
		os.Exit(1)
	}
}

func promptFromArgs() string {
	prompt := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if prompt == "" {
		return "Give a concise MiMo Value Amplifier status report and explain the current tool/context architecture."
	}
	return prompt
}

func runSmoke(w io.Writer, events <-chan core.AgentEvent, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	counts := map[core.EventType]int{}
	for {
		select {
		case <-timer.C:
			return errors.New("timed out waiting for event_done")
		case event := <-events:
			counts[event.Type]++
			if event.Type == core.EventDone {
				fmt.Fprintf(w, "smoke ok: events=%d message_delta=%d context_update=%d trace_update=%d tool_result=%d observation=%d\n",
					totalCounts(counts),
					counts[core.EventMessageDelta],
					counts[core.EventContextUpdate],
					counts[core.EventTraceUpdate],
					counts[core.EventToolResult],
					counts[core.EventObservation],
				)
				return nil
			}
		}
	}
}

func totalCounts(counts map[core.EventType]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func liveEvents(ctx context.Context, cfg config.Config, prompt string) <-chan core.AgentEvent {
	bus := core.NewBus()
	uiEvents := bus.Subscribe(256)

	go persistEvents(ctx, cfg.Runtime.Workspace, bus.Subscribe(256))
	go func() {
		publishInitialContext(cfg, bus)
		publishToolSmoke(ctx, cfg, bus)
		client := mimo.New(cfg.Provider)
		if err := agent.RunOnce(ctx, prompt, client, bus); err != nil {
			return
		}
	}()

	return uiEvents
}

func persistEvents(ctx context.Context, workspace string, events <-chan core.AgentEvent) {
	writer, err := replay.NewWriter(workspace, time.Now().Format("20060102T150405"))
	if err != nil {
		return
	}
	defer writer.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			_ = writer.Write(event)
		}
	}
}

func publishInitialContext(cfg config.Config, bus *core.Bus) {
	manager := contextmap.NewSeeded(
		cfg.Runtime.ContextWindow,
		"MiMo Value Amplifier Go workspace: cmd/mimo, internal/core, provider, agent, tools, context, replay, tui.",
		"Build a MiMo-first TUI that makes context, trace, tools, artifacts, and streaming visible.",
	)
	snapshot, _ := manager.Upsert(core.ContextItem{
		ID:            "near:runtime-config",
		Tier:          core.TierNear,
		Title:         "Runtime configuration",
		Source:        "environment + config files",
		TokenEstimate: 320,
		Reason:        fmt.Sprintf("model=%s base_url=%s mock=%t", cfg.Provider.Model, cfg.Provider.BaseURL, cfg.Provider.Mock),
	})

	event := core.NewEvent(core.EventContextUpdate)
	event.Context = &snapshot
	bus.Publish(event)
}

func publishToolSmoke(ctx context.Context, cfg config.Config, bus *core.Bus) {
	registry := tools.NewDefaultRegistry(cfg.Runtime.Workspace)
	tool, ok := registry.Get("git_status")
	if !ok {
		return
	}

	start := core.NewEvent(core.EventToolStart)
	start.ToolName = tool.Name()
	start.Message = "Reading repository status into an artifact-backed observation."
	bus.Publish(start)

	result := tool.Run(ctx, core.ToolInput{})
	done := core.NewEvent(core.EventToolResult)
	done.ToolName = tool.Name()
	done.Message = result.Content
	done.Err = result.Error
	bus.Publish(done)

	observation := tool.Summarize(result)
	obsEvent := core.NewEvent(core.EventObservation)
	obsEvent.Observation = &observation
	bus.Publish(obsEvent)
}
