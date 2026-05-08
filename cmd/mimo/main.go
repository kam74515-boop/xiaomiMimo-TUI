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
	sessionlog "mimo-tui/internal/session"
	"mimo-tui/internal/tools"
	"mimo-tui/internal/tui"
)

func main() {
	opts := parseFlags()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	if opts.workspace != "" {
		cfg.Runtime.Workspace = opts.workspace
	}
	if opts.sessionID == "" {
		opts.sessionID = newSessionID()
	}

	events := liveEvents(context.Background(), cfg, promptFromArgs(), opts)
	if opts.smoke {
		if err := runSmoke(os.Stdout, events, opts.smokeTimeout); err != nil {
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

type cliOptions struct {
	smoke        bool
	smokeTimeout time.Duration
	workspace    string
	sessionID    string
	resumeLatest bool
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.BoolVar(&opts.smoke, "smoke", false, "run the event pipeline without starting the full-screen TUI")
	flag.DurationVar(&opts.smokeTimeout, "smoke-timeout", 10*time.Second, "maximum time to wait in smoke mode")
	flag.StringVar(&opts.workspace, "workspace", "", "workspace directory; defaults to config runtime.workspace")
	flag.StringVar(&opts.sessionID, "session", "", "session id for .mimo/sessions event log")
	flag.BoolVar(&opts.resumeLatest, "resume-latest", false, "load a compact summary of the latest usable session into the startup Context Map")
	flag.Parse()
	return opts
}

func newSessionID() string {
	return time.Now().Format("20060102T150405")
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
	var lastErr string
	for {
		select {
		case <-timer.C:
			return errors.New("timed out waiting for event_done")
		case event, ok := <-events:
			if !ok {
				return errors.New("event source closed before event_done")
			}
			counts[event.Type]++
			if event.Type == core.EventError {
				lastErr = firstNonEmpty(event.Err, event.Message)
			}
			if event.Type == core.EventDone {
				if lastErr != "" {
					return fmt.Errorf("agent emitted error: %s", lastErr)
				}
				if err := validateSmokeCounts(counts); err != nil {
					return err
				}
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

func validateSmokeCounts(counts map[core.EventType]int) error {
	required := []core.EventType{
		core.EventMessageDelta,
		core.EventContextUpdate,
		core.EventTraceUpdate,
		core.EventToolResult,
		core.EventObservation,
	}
	var missing []string
	for _, eventType := range required {
		if counts[eventType] == 0 {
			missing = append(missing, string(eventType))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required smoke events: %s", strings.Join(missing, ", "))
	}
	return nil
}

func totalCounts(counts map[core.EventType]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func liveEvents(ctx context.Context, cfg config.Config, prompt string, opts cliOptions) <-chan core.AgentEvent {
	bus := core.NewBus()
	uiEvents := bus.Subscribe(256)

	go persistEvents(ctx, cfg.Runtime.Workspace, opts.sessionID, bus.Subscribe(256))
	go func() {
		manager := newContextManager(cfg)
		publishContextSnapshot(manager.Snapshot(), bus)
		if opts.resumeLatest {
			publishResumeSummary(cfg, manager, bus)
		}

		registry := tools.NewDefaultRegistry(cfg.Runtime.Workspace)
		executor := tools.NewExecutor(registry, bus)
		runBootstrapTools(ctx, executor, manager, bus)

		client := mimo.New(cfg.Provider)
		loopConfig := agent.DefaultLoopConfig()
		if err := agent.Loop(ctx, prompt, client, executor, manager, registry.ToolSpecs(), bus, loopConfig); err != nil {
			return
		}
	}()

	return uiEvents
}

func persistEvents(ctx context.Context, workspace, sessionID string, events <-chan core.AgentEvent) {
	writer, err := replay.NewWriter(workspace, sessionID)
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

func newContextManager(cfg config.Config) *contextmap.Manager {
	manager := contextmap.NewSeeded(
		cfg.Runtime.ContextWindow,
		"MiMo Value Amplifier Go workspace: cmd/mimo, internal/core, provider, agent, tools, context, replay, tui.",
		"Build a MiMo-first TUI that makes context, trace, tools, artifacts, and streaming visible.",
	)
	_, _ = manager.Upsert(core.ContextItem{
		ID:            "near:runtime-config",
		Tier:          core.TierNear,
		Title:         "Runtime configuration",
		Source:        "environment + config files",
		TokenEstimate: 320,
		Reason:        fmt.Sprintf("model=%s base_url=%s mock=%t", cfg.Provider.Model, cfg.Provider.BaseURL, cfg.Provider.Mock),
	})
	return manager
}

func publishContextSnapshot(snapshot core.ContextSnapshot, bus *core.Bus) {
	event := core.NewEvent(core.EventContextUpdate)
	event.Context = &snapshot
	bus.Publish(event)
}

func publishResumeSummary(cfg config.Config, manager *contextmap.Manager, bus *core.Bus) {
	latest, err := replay.LatestSession(cfg.Runtime.Workspace)
	if err != nil {
		return
	}
	events, err := replay.ReadFile(latest.Path)
	if err != nil {
		return
	}
	summary := sessionlog.BuildResumeSummary(events)
	observation := core.Observation{
		Summary:          fmt.Sprintf("Latest session %s has %d events; last status: %s", latest.ID, latest.EventCount, firstNonEmpty(summary.LastStatus, "unknown")),
		StateDelta:       fmt.Sprintf("resume skeleton includes %d recent trace updates and %d artifact references", len(summary.RecentTraceUpdates), len(summary.ArtifactIDs)),
		RiskDelta:        firstNonEmpty(summary.LastError, "no resume error recorded"),
		ContextPlacement: core.TierAnchor,
	}
	publishObservation(observation, bus)
	snapshot, _ := manager.Upsert(contextmap.PromoteObservation("anchor:resume:"+latest.ID, observation))
	publishContextSnapshot(snapshot, bus)
}

func runBootstrapTools(ctx context.Context, executor *tools.Executor, manager *contextmap.Manager, bus *core.Bus) {
	for index, call := range []core.ToolCall{
		{Name: "list_dir", Input: core.ToolInput{"path": ".", "max_entries": 80}},
		{Name: "git_status", Input: core.ToolInput{}},
	} {
		_, observation := executor.Execute(ctx, call)
		snapshot, _ := manager.Upsert(contextmap.PromoteObservation(fmt.Sprintf("artifact:%s:%d", call.Name, index), observation))
		publishContextSnapshot(snapshot, bus)
	}
}

func publishObservation(observation core.Observation, bus *core.Bus) {
	event := core.NewEvent(core.EventObservation)
	event.Observation = &observation
	bus.Publish(event)
}
