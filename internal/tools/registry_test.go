package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

func TestDefaultRegistryExecutesReadOnlyTools(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)
	writeFile(t, filepath.Join(workspace, "main.go"), "package main\n\nfunc main() {}\n")
	run(t, workspace, "git", "add", ".")
	run(t, workspace, "git", "commit", "-m", "initial")
	writeFile(t, filepath.Join(workspace, "main.go"), "package main\n\nfunc main() { println(\"changed\") }\n")

	registry := NewDefaultRegistry(workspace)
	readFile, ok := registry.Get("read_file")
	if !ok {
		t.Fatal("missing read_file")
	}
	readResult := readFile.Run(context.Background(), core.ToolInput{"path": "main.go"})
	if readResult.ArtifactID == "" {
		t.Fatalf("read_file did not write an artifact: %+v", readResult)
	}

	cases := []struct {
		name  string
		input core.ToolInput
	}{
		{name: "read_file", input: core.ToolInput{"path": "main.go"}},
		{name: "list_dir", input: core.ToolInput{"path": "."}},
		{name: "git_diff", input: core.ToolInput{"path": "main.go"}},
		{name: "git_log", input: core.ToolInput{"limit": 1}},
		{name: "artifact_read", input: core.ToolInput{"artifact_id": readResult.ArtifactID}},
	}

	for _, tc := range cases {
		tool, ok := registry.Get(tc.name)
		if !ok {
			t.Fatalf("missing tool %s", tc.name)
		}
		result := tool.Run(context.Background(), tc.input)
		if result.ArtifactID == "" {
			t.Fatalf("%s did not write an artifact: %+v", tc.name, result)
		}
		if obs := tool.Summarize(result); obs.ArtifactID == "" || obs.Summary == "" {
			t.Fatalf("%s produced weak observation: %+v", tc.name, obs)
		}
	}
}

func TestExecutorPublishesEventsAndPlacesArtifacts(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "alpha.txt"), "alpha\n")

	registry := NewDefaultRegistry(workspace)
	bus := core.NewBus()
	events := bus.Subscribe(10)
	executor := NewExecutor(registry, bus)

	result, observation := executor.Execute(context.Background(), core.ToolCall{
		ID:    "call-list",
		Name:  "list_dir",
		Input: core.ToolInput{"path": "."},
	})
	if result.ExitCode != 0 {
		t.Fatalf("list_dir failed: %+v", result)
	}
	if observation.ArtifactID == "" || observation.ContextPlacement != core.TierArtifact {
		t.Fatalf("list_dir observation was not artifact-backed: %+v", observation)
	}

	got := drainEvents(events)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(got), got)
	}
	if got[0].Type != core.EventToolStart || got[1].Type != core.EventToolResult || got[2].Type != core.EventObservation {
		t.Fatalf("unexpected event sequence: %+v", got)
	}
	if got[2].Observation == nil || got[2].Observation.ArtifactID != result.ArtifactID {
		t.Fatalf("observation event missing artifact id: %+v", got[2])
	}
}

func TestExecutorUsesRuntimeBudgetForSummarizers(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "alpha.txt"), strings.Repeat("alpha\n", 40))

	recorder := &recordingSummarizer{}
	registry := NewDefaultRegistry(workspace, func(store *artifact.Store) map[string]Summarizer {
		return map[string]Summarizer{"read_file": recorder}
	})
	executor := NewExecutor(registry, nil, WithBudgetProvider(func() BudgetLevel {
		return BudgetCritical
	}))

	_, observation := executor.Execute(context.Background(), core.ToolCall{
		Name:  "read_file",
		Input: core.ToolInput{"path": "alpha.txt"},
	})

	if recorder.budget != BudgetCritical {
		t.Fatalf("summarizer budget = %v, want %v", recorder.budget, BudgetCritical)
	}
	if !strings.Contains(observation.Summary, "budget=2") {
		t.Fatalf("observation summary = %q, want critical budget marker", observation.Summary)
	}
}

func TestExecutorConservativePermissionPolicy(t *testing.T) {
	workspace := t.TempDir()
	registry := NewDefaultRegistry(workspace)

	deniedResult, deniedObservation := NewExecutor(registry, nil).Execute(context.Background(), core.ToolCall{
		Name:  "write_file",
		Input: core.ToolInput{"path": "denied.txt", "content": "nope"},
	})
	if deniedResult.ExitCode != permissionDeniedExitCode {
		t.Fatalf("expected permission denied exit, got %+v", deniedResult)
	}
	if deniedObservation.ContextPlacement != core.TierNear || deniedObservation.ArtifactID != "" {
		t.Fatalf("denied observation should stay near without artifact: %+v", deniedObservation)
	}
	if _, err := os.Stat(filepath.Join(workspace, "denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("write_file ran despite missing explicit permission")
	}

	allowedResult, allowedObservation := NewExecutor(registry, nil, WithAllowedAskTools("write_file")).Execute(context.Background(), core.ToolCall{
		Name:  "write_file",
		Input: core.ToolInput{"path": "allowed.txt", "content": "ok"},
	})
	if allowedResult.ExitCode != 0 {
		t.Fatalf("explicitly allowed write_file failed: %+v", allowedResult)
	}
	if allowedObservation.ArtifactID == "" || allowedObservation.ContextPlacement != core.TierArtifact {
		t.Fatalf("allowed write_file observation should be artifact-backed: %+v", allowedObservation)
	}
	if got := mustReadFile(t, filepath.Join(workspace, "allowed.txt")); got != "ok" {
		t.Fatalf("allowed write_file content = %q", got)
	}
}

type recordingSummarizer struct {
	budget BudgetLevel
}

func (r *recordingSummarizer) Summarize(result core.ToolResult, budget BudgetLevel) core.Observation {
	r.budget = budget
	return core.Observation{
		Summary:          fmt.Sprintf("budget=%d artifact=%s", budget, result.ArtifactID),
		ContextPlacement: core.TierArtifact,
		ArtifactID:       result.ArtifactID,
	}
}

func TestArtifactReadSummarizesSmallPayloadsAndKeepsLargeRawOutputOut(t *testing.T) {
	workspace := t.TempDir()
	store := artifact.NewStore(workspace)
	largeRaw := strings.Repeat("SECRET-LARGE-DATA\n", 400)
	record, err := store.Write(artifact.WriteRequest{
		Tool:     "fixture",
		Kind:     "command",
		ExitCode: 0,
		Payloads: []artifact.Payload{
			{Name: "small.txt", Data: []byte("first line\nsecond line\n")},
			{Name: "large.txt", Data: []byte(largeRaw)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tool := NewArtifactReadTool(workspace, store, nil)
	result := tool.Run(context.Background(), core.ToolInput{"artifact_id": record.ID})
	if result.ExitCode != 0 {
		t.Fatalf("artifact_read failed: %+v", result)
	}
	if !strings.Contains(result.Content, "preview=\"first line | second line\"") {
		t.Fatalf("small payload was not summarized: %s", result.Content)
	}
	if strings.Contains(result.Content, "SECRET-LARGE-DATA") {
		t.Fatalf("large raw payload leaked into result content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "large_files=1") {
		t.Fatalf("large payload was not marked artifact-backed: %s", result.Content)
	}
	if observation := tool.Summarize(result); observation.ContextPlacement != core.TierArtifact {
		t.Fatalf("large artifact_read observation should stay artifact-backed: %+v", observation)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "Test User")
}

func drainEvents(events <-chan core.AgentEvent) []core.AgentEvent {
	var got []core.AgentEvent
	for {
		select {
		case event := <-events:
			got = append(got, event)
		default:
			return got
		}
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
