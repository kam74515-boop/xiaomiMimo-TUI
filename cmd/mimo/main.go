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
	"mimo-tui/internal/eval"
	"mimo-tui/internal/model"
	"mimo-tui/internal/provider/mimo"
	"mimo-tui/internal/replay"
	sessionlog "mimo-tui/internal/session"
	"mimo-tui/internal/tools"
	"mimo-tui/internal/tools/summarizers"
	"mimo-tui/internal/tui"
)

func main() {
	opts := parseFlags()

	// ----- model registry -----
	registry := model.DefaultRegistry()

	if opts.listModels {
		fmt.Print(registry.ListModels())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// Resolve the model ID through the registry, applying channel gating.
	cfg.Provider.Model, cfg.Provider.BaseURL = resolveModel(registry, cfg.Provider.Model, cfg.Provider.BaseURL)

	if opts.workspace != "" {
		cfg.Runtime.Workspace = opts.workspace
	}

	// ----- golden session marking -----
	if opts.goldenSession != "" && opts.modelAccept == "" {
		if err := runGoldenMark(cfg, opts); err != nil {
			fmt.Fprintf(os.Stderr, "golden: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("golden: session %q marked as golden\n", opts.goldenSession)
		return
	}

	// ----- model acceptance via replay gate -----
	if opts.modelAccept != "" {
		if err := runGateAccept(cfg, opts, registry); err != nil {
			fmt.Fprintf(os.Stderr, "gate: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if opts.sessionID == "" {
		opts.sessionID = newSessionID()
	}

	events, ctxBus := liveEvents(context.Background(), cfg, promptFromArgs(), opts, registry)
	if opts.smoke {
		if err := runSmoke(os.Stdout, events, opts.smokeTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "smoke: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if opts.eval {
		if err := runEval(cfg, opts); err != nil {
			fmt.Fprintf(os.Stderr, "eval: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := tui.Run(events, ctxBus, cfg.Provider.Model, cfg.Provider.Mock); err != nil {
		fmt.Fprintf(os.Stderr, "run tui: %v\n", err)
		os.Exit(1)
	}
}

type cliOptions struct {
	smoke            bool
	smokeTimeout     time.Duration
	workspace        string
	sessionID        string
	resumeLatest     bool
	eval             bool
	evalSession      string
	listModels       bool
	goldenSession    string
	modelAccept      string
	candidateSession string
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.BoolVar(&opts.smoke, "smoke", false, "run the event pipeline without starting the full-screen TUI")
	flag.DurationVar(&opts.smokeTimeout, "smoke-timeout", 10*time.Second, "maximum time to wait in smoke mode")
	flag.StringVar(&opts.workspace, "workspace", "", "workspace directory; defaults to config runtime.workspace")
	flag.StringVar(&opts.sessionID, "session", "", "session id for .mimo/sessions event log")
	flag.BoolVar(&opts.resumeLatest, "resume-latest", false, "load a compact summary of the latest usable session into the startup Context Map")
	flag.BoolVar(&opts.eval, "eval", false, "extract and compare trajectories from session logs")
	flag.StringVar(&opts.evalSession, "eval-session", "", "session ID to evaluate (default: latest)")
	flag.BoolVar(&opts.listModels, "list-models", false, "print all registered models and exit")
	flag.StringVar(&opts.goldenSession, "golden-session", "", "mark a session as golden")
	flag.StringVar(&opts.modelAccept, "model-accept", "", "accept a candidate model if the replay gate passes")
	flag.StringVar(&opts.candidateSession, "candidate-session", "", "candidate session to evaluate against the golden session")
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

// isLabsUnlocked returns true when the MIMO_LABS environment variable is
// set to "1", "true", "yes", or "on".
func isLabsUnlocked() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MIMO_LABS"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// resolveModel applies the model registry to the configured model ID,
// enforcing channel gating rules:
//   - If the model is not registered, warn and use values as-is (forward compat).
//   - If the model is in the labs channel and labs is not unlocked, fall back to
//     the registry default.
//   - If the model is a candidate, log a note and allow it.
//   - If the model is the default, use the registered base URL.
//
// Returns (model, baseURL).
func resolveModel(registry *model.Registry, configuredModel, configuredBaseURL string) (string, string) {
	info, ok := registry.Get(configuredModel)
	if !ok {
		fmt.Fprintf(os.Stderr, "mimo: model %q not in registry; using provider config as-is for forward compatibility\n", configuredModel)
		return configuredModel, configuredBaseURL
	}

	if info.Channel == model.ChannelLabs && !isLabsUnlocked() {
		def := registry.Default()
		fmt.Fprintf(os.Stderr, "mimo: model %q is labs-only; set MIMO_LABS=1 to unlock. Falling back to default %q\n", configuredModel, def.ID)
		return def.ID, def.BaseURL
	}

	if info.Channel == model.ChannelCandidate {
		fmt.Fprintf(os.Stderr, "mimo: model %q is a candidate — use with awareness of potential instability\n", configuredModel)
	}

	if info.BaseURL != "" {
		return info.ID, info.BaseURL
	}
	return info.ID, configuredBaseURL
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

func liveEvents(ctx context.Context, cfg config.Config, prompt string, opts cliOptions, registry *model.Registry) (<-chan core.AgentEvent, *core.Bus) {
	bus := core.NewBus()
	uiEvents := bus.Subscribe(256)

	go persistEvents(ctx, cfg.Runtime.Workspace, opts.sessionID, bus.Subscribe(256))
	go func() {
		manager := newContextManager(cfg)
		publishContextSnapshot(manager.Snapshot(), bus)
		if opts.resumeLatest {
			publishResumeSummary(cfg, manager, bus)
		}

		// Subscribe to TUI context commands (pin / unpin / remove).
		cmdSub := bus.Subscribe(64)
		go func() {
			for event := range cmdSub {
				switch event.Type {
				case core.EventContextPin:
					snapshot, err := manager.Pin(event.Message)
					if err == nil {
						publishContextSnapshot(snapshot, bus)
					}
				case core.EventContextUnpin:
					snapshot, err := manager.Unpin(event.Message)
					if err == nil {
						publishContextSnapshot(snapshot, bus)
					}
				case core.EventContextRemove:
					snapshot, err := manager.Remove(event.Message)
					if err == nil {
						publishContextSnapshot(snapshot, bus)
					}
				}
			}
		}()

		toolRegistry := tools.NewDefaultRegistry(cfg.Runtime.Workspace, summarizers.NewRegistry)
		approvalCh := make(chan core.ApprovalRequest, 8)
		defer close(approvalCh)
		executor := tools.NewExecutor(
			toolRegistry,
			bus,
			tools.WithApprovalChannel(approvalCh),
			tools.WithBudgetProvider(func() tools.BudgetLevel {
				snapshot := manager.Snapshot()
				return tools.BudgetFromContext(snapshot.WindowTokens, snapshot.UsedTokens)
			}),
		)

		// Bridge approval requests from the executor to the event bus.
		go func() {
			for req := range approvalCh {
				event := core.NewEvent(core.EventApprovalNeeded)
				event.ToolName = req.ToolCall.Name
				event.ToolCall = &req.ToolCall
				event.Approval = &req
				event.Message = "Approval needed for tool " + req.ToolCall.Name
				bus.Publish(event)
			}
		}()

		bootstrapObservations := runBootstrapTools(ctx, executor, manager, bus)

		// Run oracle review after bootstrap tools to re-evaluate context placement.
		contextmap.RunOracleStep(manager, prompt, bootstrapObservations, bus)

		client := mimo.New(cfg.Provider)
		if info, ok := registry.Get(cfg.Provider.Model); ok {
			client.SetModelInfo(info)
		}
		loopConfig := agent.DefaultLoopConfig()
		userPrompts := bus.Subscribe(8)

		// Initial agent run with the startup prompt.
		history, err := agent.Loop(ctx, prompt, client, executor, manager, toolRegistry.ToolSpecs(), bus, loopConfig, nil)
		if err != nil {
			// Log error but continue to multi-turn listening so the user can retry.
			bus.Publish(core.AgentEvent{Type: core.EventObservation, Observation: &core.Observation{Summary: "startup agent error: " + err.Error()}})
		}

		// Multi-turn: listen for user prompts from the TUI.
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-userPrompts:
				if !ok {
					return
				}
				if event.Type == core.EventUserPrompt && event.Message != "" {
					nextHistory, loopErr := agent.Loop(ctx, event.Message, client, executor, manager, toolRegistry.ToolSpecs(), bus, loopConfig, history)
					if loopErr != nil {
						bus.Publish(core.AgentEvent{Type: core.EventObservation, Observation: &core.Observation{Summary: "agent error: " + loopErr.Error()}})
						if len(nextHistory) > 0 {
							history = nextHistory
						}
						continue
					}
					history = nextHistory
				}
			}
		}
	}()

	return uiEvents, bus
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

func runBootstrapTools(ctx context.Context, executor *tools.Executor, manager *contextmap.Manager, bus *core.Bus) []core.Observation {
	var observations []core.Observation
	for index, call := range []core.ToolCall{
		{Name: "list_dir", Input: core.ToolInput{"path": ".", "max_entries": 80}},
		{Name: "git_status", Input: core.ToolInput{}},
	} {
		_, observation := executor.Execute(ctx, call)
		observations = append(observations, observation)
		snapshot, _ := manager.Upsert(contextmap.PromoteObservation(fmt.Sprintf("artifact:%s:%d", call.Name, index), observation))
		publishContextSnapshot(snapshot, bus)
	}
	return observations
}

func publishObservation(observation core.Observation, bus *core.Bus) {
	event := core.NewEvent(core.EventObservation)
	event.Observation = &observation
	bus.Publish(event)
}

// runGoldenMark marks the session specified by -golden-session as a golden
// session by copying it into .mimo/golden/.
func runGoldenMark(cfg config.Config, opts cliOptions) error {
	workspace := cfg.Runtime.Workspace
	desc := fmt.Sprintf("golden session %s", opts.goldenSession)
	return eval.MarkGolden(workspace, opts.goldenSession, desc)
}

// runGateAccept evaluates a candidate session against a golden session and
// promotes the candidate model if the replay gate passes.
func runGateAccept(cfg config.Config, opts cliOptions, registry *model.Registry) error {
	workspace := cfg.Runtime.Workspace

	// Validate flags.
	goldenID := strings.TrimSpace(opts.goldenSession)
	candidateID := strings.TrimSpace(opts.candidateSession)
	modelID := strings.TrimSpace(opts.modelAccept)

	if goldenID == "" {
		return fmt.Errorf("-golden-session is required for gate evaluation")
	}
	if candidateID == "" {
		return fmt.Errorf("-candidate-session is required for gate evaluation")
	}
	if modelID == "" {
		return fmt.Errorf("-model-accept is required")
	}

	// Load golden events.
	goldenEvents, err := eval.LoadGolden(workspace, goldenID)
	if err != nil {
		return fmt.Errorf("load golden session %q: %w", goldenID, err)
	}
	if len(goldenEvents) == 0 {
		return fmt.Errorf("golden session %q is empty", goldenID)
	}

	// Load candidate events.
	candidateEvents, err := replay.Read(workspace, candidateID)
	if err != nil {
		return fmt.Errorf("load candidate session %q: %w", candidateID, err)
	}
	if len(candidateEvents) == 0 {
		return fmt.Errorf("candidate session %q is empty", candidateID)
	}

	// Evaluate.
	result := eval.EvaluateCandidate(goldenEvents, candidateEvents)

	fmt.Printf("Gate Result for model %q:\n", modelID)
	fmt.Printf("  Passed: %t\n", result.Passed)
	fmt.Printf("  Score: %.2f\n", result.Score)
	fmt.Printf("  ToolMatchRate: %.2f\n", result.ToolMatchRate)
	fmt.Printf("  TrajectorySimilarity: %.2f\n", result.TrajectorySimilarity)
	if len(result.Failures) > 0 {
		fmt.Println("  Failures:")
		for _, f := range result.Failures {
			fmt.Printf("    - %s\n", f)
		}
	}

	if !result.Passed {
		return fmt.Errorf("replay gate NOT passed for model %q — candidate not promoted", modelID)
	}

	// Promote the candidate model.
	if err := registry.AcceptCandidate(modelID); err != nil {
		return fmt.Errorf("accept candidate model %q: %w", modelID, err)
	}
	fmt.Printf("Model %q promoted to default channel.\n", modelID)
	return nil
}

func runEval(cfg config.Config, opts cliOptions) error {
	// 1. Find the target session.
	var target replay.SessionInfo
	if opts.evalSession != "" {
		sessions, err := replay.ListSessions(cfg.Runtime.Workspace)
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		found := false
		for _, s := range sessions {
			if s.ID == opts.evalSession {
				target = s
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("session %q not found", opts.evalSession)
		}
	} else {
		var err error
		target, err = replay.LatestSession(cfg.Runtime.Workspace)
		if err != nil {
			return fmt.Errorf("find latest session: %w", err)
		}
	}

	// 2. Read events and extract trajectory.
	events, err := replay.ReadFile(target.Path)
	if err != nil {
		return fmt.Errorf("read session %s: %w", target.ID, err)
	}

	traj := eval.ExtractTrajectory(events)
	traj.SessionID = target.ID
	fmt.Print(eval.FormatTrajectory(traj))

	// 3. If a specific session was requested, compare with the latest.
	if opts.evalSession != "" {
		latest, err := replay.LatestSession(cfg.Runtime.Workspace)
		if err != nil {
			return fmt.Errorf("find latest session for comparison: %w", err)
		}
		if latest.ID != target.ID {
			latestEvents, err := replay.ReadFile(latest.Path)
			if err != nil {
				return fmt.Errorf("read latest session %s: %w", latest.ID, err)
			}
			latestTraj := eval.ExtractTrajectory(latestEvents)
			latestTraj.SessionID = latest.ID

			fmt.Println("\n--- Comparison ---")
			fmt.Print(eval.CompareTrajectories(traj, latestTraj))
		}
	}

	return nil
}
