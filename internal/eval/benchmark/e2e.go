package benchmark

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mimo-tui/internal/agent"
	"mimo-tui/internal/artifact"
	"mimo-tui/internal/config"
	contextmap "mimo-tui/internal/context"
	"mimo-tui/internal/core"
	mimoclient "mimo-tui/internal/provider/mimo"
	"mimo-tui/internal/tools"
)

// E2EConfig holds configuration for the end-to-end runner.
type E2EConfig struct {
	// BaseURL is the MiMo API base URL. If empty, the default is used.
	BaseURL string
	// APIKey is the MiMo API key. If empty, the test is skipped.
	APIKey string
	// Model is the model identifier. If empty, the default is used.
	Model string
	// Tasks is the list of E2E tasks to run.
	Tasks []E2ETask
	// Timeout is the global timeout for the entire run. Defaults to 10 minutes.
	Timeout time.Duration
	// Workspace is the working directory for tool execution. Defaults to ".".
	Workspace string
}

// E2EResult captures the outcome of a single E2E task.
type E2EResult struct {
	TaskName     string
	Success      bool
	ErrorClass   string
	Steps        int
	ToolCalls    int
	Duration     time.Duration
	ErrorMessage string
}

// RunE2E executes the E2E task suite against the MiMo API.
//
// If MIMO_API_KEY is not set and cfg.APIKey is empty, it returns a nil
// results slice with no error (graceful skip).
//
// The function never prints the full API key; at most the first 8 characters
// plus "..." are included in any log output.
func RunE2E(cfg E2EConfig) ([]E2EResult, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("MIMO_API_KEY"))
	}
	if apiKey == "" {
		return nil, nil // graceful skip
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("MIMO_BASE_URL"))
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "mimo-v2.5-pro"
	}

	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "."
	}
	if absWS, err := filepath.Abs(workspace); err == nil {
		workspace = absWS
	}

	tasks := cfg.Tasks
	if len(tasks) == 0 {
		tasks = DefaultE2ETasks()
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}

	fmt.Printf("e2e: starting %d tasks (model=%s, base_url=%s, key=%s)\n",
		len(tasks), model, maskAPIKey(baseURL), maskAPIKey(apiKey))

	results := make([]E2EResult, 0, len(tasks))
	for _, task := range tasks {
		result := runE2ETask(apiKey, baseURL, model, workspace, task)
		results = append(results, result)
		printE2EResult(result)
	}

	return results, nil
}

// maskAPIKey returns the first 8 characters of s plus "..." if s is longer
// than 8 characters. Otherwise it returns "***".
func maskAPIKey(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:8] + "..."
}

// classifyFailure categorizes an error and event stream into a failure class.
//
// Categories:
//   - api_error: authentication, rate-limit, server, or connection failures
//   - stream_error: SSE stream decode or parse errors
//   - tool_parse_error: tool call JSON parsing failures
//   - timeout: context deadline exceeded or canceled
//   - no_tool_calls: the model never invoked a tool when it should have
//   - max_steps: agent loop hit the step limit
//   - unknown: anything else
func classifyFailure(err error, events []core.AgentEvent) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return "timeout"
	case strings.Contains(errMsg, "stream error") || strings.Contains(errMsg, "decode stream chunk"):
		return "stream_error"
	case strings.Contains(errMsg, "authentication failed") || strings.Contains(errMsg, "status 4"):
		return "api_error"
	case strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "status 429"):
		return "api_error"
	case strings.Contains(errMsg, "temporarily unavailable") || strings.Contains(errMsg, "status 5"):
		return "api_error"
	case strings.Contains(errMsg, "Cannot reach") || strings.Contains(errMsg, "connection"):
		return "api_error"
	case strings.Contains(errMsg, "max steps"):
		return "max_steps"
	}

	// Check events for tool parse errors.
	for _, event := range events {
		if event.Type == core.EventError {
			if strings.Contains(event.Err, "decode") || strings.Contains(event.Err, "parse") || strings.Contains(event.Err, "unmarshal") {
				return "tool_parse_error"
			}
		}
	}

	// Check whether the model made any tool calls.
	hasToolCalls := false
	for _, event := range events {
		if event.Type == core.EventToolStart {
			hasToolCalls = true
			break
		}
	}
	if !hasToolCalls && err != nil {
		return "no_tool_calls"
	}

	return "unknown"
}

// runE2ETask executes a single E2E task using the agent loop.
func runE2ETask(apiKey, baseURL, model, workspace string, task E2ETask) E2EResult {
	start := time.Now()
	result := E2EResult{TaskName: task.Name}

	// Build a client that talks to the real API.
	client := mimoclient.New(config.ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	})

	// Create a fresh bus and subscribe to capture events.
	bus := core.NewBus()
	eventCh := bus.Subscribe(2048)

	var events []core.AgentEvent
	doneCollecting := make(chan struct{})
	go func() {
		defer close(doneCollecting)
		timer := time.NewTimer(task.Timeout + 5*time.Second)
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

	// Set up context manager and tool executor.
	ctxMgr := contextmap.New(contextmap.DefaultWindowTokens)
	store := artifact.NewStore(workspace)
	registry := tools.NewDefaultRegistryWithStore(workspace, store)

	approvalCh := make(chan core.ApprovalRequest, 16)
	executor := tools.NewExecutor(
		registry,
		nil,
		tools.WithApprovalChannel(approvalCh),
		tools.WithAllowedAskTools(),
		tools.WithArtifactStore(store, workspace),
	)

	// Auto-approve non-destructive requests only. E2E should validate the loop,
	// not normalize unsafe commands into passing tests.
	go func() {
		for req := range approvalCh {
			if req.Permission.Behavior == core.PermissionDeny || strings.Contains(strings.ToLower(req.Permission.Reason), "destructive") {
				req.Response <- core.ApprovalDecision{Allowed: false, Reason: "e2e denied unsafe tool call"}
				continue
			}
			req.Response <- core.ApprovalDecision{Allowed: true, Reason: "e2e auto-approve non-destructive tool call"}
		}
	}()

	toolSpecs := registry.ToolSpecs()

	loopCfg := agent.LoopConfig{
		MaxSteps:     task.MaxSteps,
		StepTimeout:  60 * time.Second,
		TotalTimeout: task.Timeout,
	}

	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout)
	defer cancel()

	_, loopErr := agent.Loop(
		ctx,
		task.Prompt,
		client,
		executor,
		ctxMgr,
		toolSpecs,
		bus,
		loopCfg,
		nil,
	)

	close(approvalCh)
	<-doneCollecting

	result.Duration = time.Since(start)
	result.Steps = countSteps(events)
	result.ToolCalls = countToolCalls(events)

	if loopErr != nil {
		result.ErrorMessage = loopErr.Error()
		result.ErrorClass = classifyFailure(loopErr, events)
		result.Success = false
	} else {
		result.Success = result.ToolCalls >= task.ExpectedToolCalls
		if !result.Success {
			result.ErrorClass = "no_tool_calls"
			result.ErrorMessage = fmt.Sprintf("expected at least %d tool calls, got %d", task.ExpectedToolCalls, result.ToolCalls)
		}
	}

	return result
}

// countSteps counts the number of completed agent steps from events.
func countSteps(events []core.AgentEvent) int {
	steps := 0
	for _, event := range events {
		if event.Type == core.EventTraceUpdate && event.Trace != nil && event.Trace.Status == core.TraceDone {
			steps++
		}
	}
	return steps
}

// countToolCalls counts the number of tool_start events.
func countToolCalls(events []core.AgentEvent) int {
	count := 0
	for _, event := range events {
		if event.Type == core.EventToolStart {
			count++
		}
	}
	return count
}

func printE2EResult(r E2EResult) {
	status := "PASS"
	if !r.Success {
		status = "FAIL"
	}
	fmt.Printf("e2e: [%s] %s (steps=%d tools=%d duration=%s",
		status, r.TaskName, r.Steps, r.ToolCalls, r.Duration.Round(time.Millisecond))
	if r.ErrorClass != "" {
		fmt.Printf(" class=%s", r.ErrorClass)
	}
	if r.ErrorMessage != "" {
		msg := r.ErrorMessage
		if len(msg) > 120 {
			msg = msg[:120] + "..."
		}
		fmt.Printf(" err=%q", msg)
	}
	fmt.Println(")")
}
