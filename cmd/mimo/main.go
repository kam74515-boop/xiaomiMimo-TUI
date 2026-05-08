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
	"mimo-tui/internal/artifact"
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

	// ----- model registry (persisted) -----
	registry, err := config.LoadModelsConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load models config: %v\n", err)
		os.Exit(1)
	}

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

	// ----- rollback commands -----
	if opts.rollbackList {
		if err := runRollbackList(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "rollback list: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if opts.rollbackShow != "" {
		if err := runRollbackShow(cfg, opts.rollbackShow); err != nil {
			fmt.Fprintf(os.Stderr, "rollback show: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if opts.rollbackApply != "" {
		if err := runRollbackApply(cfg, opts.rollbackApply, opts.rollbackConfirm); err != nil {
			fmt.Fprintf(os.Stderr, "rollback apply: %v\n", err)
			os.Exit(1)
		}
		return
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
	rollbackList     bool
	rollbackShow     string
	rollbackApply    string
	rollbackConfirm  bool
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
	flag.BoolVar(&opts.rollbackList, "rollback-list", false, "list all rollback artifacts")
	flag.StringVar(&opts.rollbackShow, "rollback-show", "", "show what a rollback artifact will restore")
	flag.StringVar(&opts.rollbackApply, "rollback-apply", "", "apply a rollback artifact (dry-run by default, use -rollback-confirm to commit)")
	flag.BoolVar(&opts.rollbackConfirm, "rollback-confirm", false, "confirm actual rollback apply (required with -rollback-apply)")
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
			return fmt.Errorf("timed out waiting for event_done; received: message_delta=%d context_update=%d trace_update=%d tool_result=%d observation=%d error=%s",
				counts[core.EventMessageDelta],
				counts[core.EventContextUpdate],
				counts[core.EventTraceUpdate],
				counts[core.EventToolResult],
				counts[core.EventObservation],
				lastErr)
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
		var resumeHistory []core.Message
		if opts.resumeLatest {
			_, _, resumeHistory = publishResumeSummary(cfg, manager, bus)
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
		policyCfg, _ := config.LoadPolicy()
		executor := tools.NewExecutor(
			toolRegistry,
			bus,
			tools.WithApprovalChannel(approvalCh),
			tools.WithBudgetProvider(func() tools.BudgetLevel {
				snapshot := manager.Snapshot()
				return tools.BudgetFromContext(snapshot.WindowTokens, snapshot.UsedTokens)
			}),
			tools.WithPolicyConfig(policyCfg),
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
		oracleEvents := bus.Subscribe(8)
		interruptEvents := bus.Subscribe(4)

		// Track the most recent prompt and observations for oracle review.
		lastPrompt := prompt
		var recentObservations []core.Observation

		// Collect observation events for oracle context.
		obsSub := bus.Subscribe(32)
		go func() {
			for event := range obsSub {
				if event.Type == core.EventObservation && event.Observation != nil {
					recentObservations = append(recentObservations, *event.Observation)
					if len(recentObservations) > 5 {
						recentObservations = recentObservations[len(recentObservations)-5:]
					}
				}
			}
		}()

		// Shared cancel for in-progress agent runs.
		var agentCancel context.CancelFunc

		// Initial agent run with the startup prompt.
		runCtx, cancel := context.WithCancel(ctx)
		agentCancel = cancel
		history, err := agent.Loop(runCtx, prompt, client, executor, manager, toolRegistry.ToolSpecs(), bus, loopConfig, resumeHistory)
		if err != nil {
			// Log error but continue to multi-turn listening so the user can retry.
			bus.Publish(core.AgentEvent{Type: core.EventObservation, Observation: &core.Observation{Summary: "startup agent error: " + err.Error()}})
		}

		// Multi-turn: listen for user prompts, oracle review, and interrupt.
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-userPrompts:
				if !ok {
					return
				}
				if event.Type == core.EventUserPrompt && event.Message != "" {
					lastPrompt = event.Message
					// Create a fresh cancel context if previous was used.
					if agentCancel == nil {
						runCtx, cancel = context.WithCancel(ctx)
						agentCancel = cancel
					} else {
						select {
						case <-runCtx.Done():
							runCtx, cancel = context.WithCancel(ctx)
							agentCancel = cancel
						default:
						}
					}
					bus.Publish(core.NewEvent(core.EventAgentStarted))
					nextHistory, loopErr := agent.Loop(runCtx, event.Message, client, executor, manager, toolRegistry.ToolSpecs(), bus, loopConfig, history)
					if loopErr != nil {
						bus.Publish(core.AgentEvent{Type: core.EventObservation, Observation: &core.Observation{Summary: "agent error: " + loopErr.Error()}})
						if len(nextHistory) > 0 {
							history = nextHistory
						}
						continue
					}
					history = nextHistory
				}
			case event, ok := <-interruptEvents:
				if !ok {
					return
				}
				if event.Type == core.EventInterrupt {
					if agentCancel != nil {
						agentCancel()
					}
				}
			case event, ok := <-oracleEvents:
				if !ok {
					return
				}
				if event.Type == core.EventOracleReview {
					goal := lastPrompt
					if goal == "" {
						goal = prompt
					}
					contextmap.RunOracleStep(manager, goal, recentObservations, bus)
					bus.Publish(core.AgentEvent{
						Type: core.EventObservation,
						Observation: &core.Observation{
							Summary: "oracle review triggered manually via ctrl+r — context placement re-evaluated",
						},
					})
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

func publishResumeSummary(cfg config.Config, manager *contextmap.Manager, bus *core.Bus) (*sessionlog.ResumeSummary, string, []core.Message) {
	latest, err := replay.LatestSession(cfg.Runtime.Workspace)
	if err != nil {
		return nil, "", nil
	}
	events, err := replay.ReadFile(latest.Path)
	if err != nil {
		return nil, "", nil
	}
	summary := sessionlog.BuildResumeSummary(events)

	// --- Publish resumed session notification for TUI display. ---
	// The EventAgentStarted clears any stale error state and signals a new run.
	// The observation appears in the notes section of the Trace panel.
	resumeMsg := fmt.Sprintf("resumed session %s (%d events, stage=%s)",
		latest.ID, latest.EventCount, firstNonEmpty(string(summary.LastStage), "unknown"))
	bus.Publish(core.AgentEvent{
		Type:    core.EventAgentStarted,
		Message: resumeMsg,
		Time:    time.Now(),
	})

	// --- Restore context items from last session snapshot. ---
	// Each item from the previous session's context map is re-inserted so the
	// resumed context map has real content, not just a skeleton.
	if summary.LatestContext != nil {
		for _, item := range summary.LatestContext.Items {
			manager.Add(core.ContextItem{
				ID:            item.ID,
				Tier:          item.Tier,
				Title:         item.Title,
				Source:        item.Source,
				TokenEstimate: item.TokenEstimate,
				Pinned:        item.Pinned,
				Reason:        fmt.Sprintf("resumed from session %s: %s", latest.ID, item.Reason),
			})
		}
	}

	// --- Publish recent trace updates so the TUI Trace panel shows the latest stage. ---
	for _, ts := range summary.RecentTraceUpdates {
		step := core.TraceStep{
			ID:     ts.ID,
			Goal:   ts.Goal,
			Status: ts.Status,
			Stage:  ts.Stage,
		}
		event := core.NewEvent(core.EventTraceUpdate)
		event.Trace = &step
		event.Time = ts.Time
		bus.Publish(event)
	}

	// --- Publish resume summary as a rich observation. ---
	// This serves as the "chat summary" and "context snapshot" of the previous
	// session, using only observation metadata (no raw artifact content).
	observation := core.Observation{
		Summary: fmt.Sprintf("resumed session %s: %d events, last stage=%s, status=%s",
			latest.ID, latest.EventCount, string(summary.LastStage), firstNonEmpty(summary.LastStatus, "unknown")),
		StateDelta: fmt.Sprintf("restored %d context items, %d trace steps, %d artifact refs, %d history messages; last tool results: %s",
			len(summary.LatestContext.Items), len(summary.RecentTraceUpdates), len(summary.ArtifactIDs), len(summary.RecentMessages),
			strings.Join(summary.LastToolResults, "; ")),
		RiskDelta:        firstNonEmpty(summary.LastError, "no pending risk from previous session"),
		ContextPlacement: core.TierAnchor,
	}
	publishObservation(observation, bus)
	snapshot, _ := manager.Upsert(contextmap.PromoteObservation("anchor:resume:"+latest.ID, observation))
	publishContextSnapshot(snapshot, bus)

	return &summary, latest.ID, summary.RecentMessages
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

// runRollbackList lists all rollback artifacts.
func runRollbackList(cfg config.Config) error {
	rollbacks, err := artifact.ListRollbacks(cfg.Runtime.Workspace)
	if err != nil {
		return err
	}
	if len(rollbacks) == 0 {
		fmt.Println("No rollback artifacts found.")
		return nil
	}
	fmt.Printf("Rollback artifacts (%d):\n", len(rollbacks))
	for _, rb := range rollbacks {
		status := "dirty"
		if rb.ExitCode == 0 {
			status = "clean"
		}
		fmt.Printf("  %s  tool=%-12s  size=%-6d  state=%-5s  created=%s\n",
			rb.ID, rb.Tool, rb.DiffSize, status, rb.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

// runRollbackShow displays what a rollback artifact will restore.
func runRollbackShow(cfg config.Config, id string) error {
	output, err := artifact.ShowRollback(cfg.Runtime.Workspace, id)
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}

// runRollbackApply applies a rollback artifact.
func runRollbackApply(cfg config.Config, id string, confirm bool) error {
	if !confirm {
		output, err := artifact.ApplyRollback(cfg.Runtime.Workspace, id, true)
		if err != nil {
			return err
		}
		fmt.Println(output)
		fmt.Println("\nUse -rollback-confirm to actually apply this rollback.")
		return nil
	}

	output, err := artifact.ApplyRollback(cfg.Runtime.Workspace, id, false)
	if err != nil {
		return err
	}
	fmt.Println(output)

	// Record the rollback operation as an artifact event.
	recordID, err := artifact.RecordRollbackApply(cfg.Runtime.Workspace, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to record rollback apply: %v\n", err)
	} else {
		fmt.Printf("rollback apply recorded as artifact %s\n", recordID)
	}
	return nil
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

	// Persist the updated registry to project-level .mimo/models.toml.
	if err := config.SaveModelsConfig(registry); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to persist model registry: %v\n", err)
	} else {
		fmt.Println("Model registry saved to .mimo/models.toml")
	}
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
