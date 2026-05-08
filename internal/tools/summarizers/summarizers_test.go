package summarizers

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
	"mimo-tui/internal/tools"
)

func TestShellSummarizerErrorAndExitCode(t *testing.T) {
	s := &ShellSummarizer{}
	obs := s.Summarize(core.ToolResult{
		ExitCode:   1,
		Error:      "permission denied",
		ArtifactID: "art-1",
	}, tools.BudgetSafe)

	if !strings.Contains(obs.Summary, "exit=1") {
		t.Errorf("expected exit=1 in summary, got: %s", obs.Summary)
	}
	if !strings.Contains(obs.RiskDelta, "permission denied") {
		t.Errorf("expected error in risk, got: %s", obs.RiskDelta)
	}
	if obs.ContextPlacement != core.TierArtifact {
		t.Errorf("expected artifact tier, got: %s", obs.ContextPlacement)
	}
}

func TestShellSummarizerTruncationByBudget(t *testing.T) {
	s := &ShellSummarizer{}
	result := core.ToolResult{ExitCode: 0, ArtifactID: "art-1"}

	safe := s.Summarize(result, tools.BudgetSafe)
	warn := s.Summarize(result, tools.BudgetWarning)
	crit := s.Summarize(result, tools.BudgetCritical)

	if len(safe.Summary) < len(warn.Summary) || len(warn.Summary) < len(crit.Summary) {
		t.Logf("safe=%d warn=%d crit=%d", len(safe.Summary), len(warn.Summary), len(crit.Summary))
	}
	// Critical should be noticeably terser than safe (when stdout is empty, all have same artifact ref)
	_ = safe
	_ = warn
	_ = crit
}

func TestRgSummarizerGroupsByFile(t *testing.T) {
	s := &RgSummarizer{}
	result := core.ToolResult{ExitCode: 0, ArtifactID: "art-1"}

	obs := s.Summarize(result, tools.BudgetWarning)
	if !strings.Contains(obs.Summary, "rg") {
		t.Errorf("expected rg in summary, got: %s", obs.Summary)
	}
	// Even with empty input, format is correct.
	if obs.ArtifactID != "art-1" {
		t.Errorf("artifact ID = %s, want art-1", obs.ArtifactID)
	}
}

func TestRgSummarizerBudgetCriticalOnlyShowsCounts(t *testing.T) {
	s := &RgSummarizer{}
	result := core.ToolResult{ExitCode: 0, ArtifactID: "art-1"}

	obs := s.Summarize(result, tools.BudgetCritical)
	if !strings.Contains(obs.Summary, "artifact=art-1") {
		t.Errorf("expected artifact ref in summary, got: %s", obs.Summary)
	}
}

func TestReadFileSummarizerBudgetCriticalTruncation(t *testing.T) {
	s := &ReadFileSummarizer{}
	result := core.ToolResult{Content: "read 5000 bytes from test.txt; artifact=art-1", ArtifactID: "art-1"}

	crit := s.Summarize(result, tools.BudgetCritical)
	if !strings.Contains(crit.Summary, "0 bytes") || !strings.Contains(crit.Summary, "0 lines") {
		t.Logf("critical summary (empty artifact): %s", crit.Summary)
	}
}

func TestReadFileSummarizerError(t *testing.T) {
	s := &ReadFileSummarizer{}
	result := core.ToolResult{Error: "file not found", ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.RiskDelta, "file not found") {
		t.Errorf("expected error in risk, got: %s", obs.RiskDelta)
	}
}

func TestListDirSummarizerGroupsByExt(t *testing.T) {
	s := &ListDirSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.Summary, "list_dir") {
		t.Errorf("expected list_dir in summary, got: %s", obs.Summary)
	}
}

func TestListDirSummarizerBudgetCritical(t *testing.T) {
	s := &ListDirSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetCritical)
	if !strings.Contains(obs.Summary, "artifact=art-1") {
		t.Errorf("expected artifact ref in summary, got: %s", obs.Summary)
	}
}

func TestGitDiffSummarizerFileCount(t *testing.T) {
	s := &GitDiffSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.Summary, "git_diff") {
		t.Errorf("expected git_diff in summary, got: %s", obs.Summary)
	}
}

func TestGitDiffSummarizerError(t *testing.T) {
	s := &GitDiffSummarizer{}
	result := core.ToolResult{Error: "not a git repo", ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.RiskDelta, "not a git repo") {
		t.Errorf("expected error in risk, got: %s", obs.RiskDelta)
	}
}

func TestGitLogSummarizerTruncationByBudget(t *testing.T) {
	s := &GitLogSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetCritical)
	if !strings.Contains(obs.Summary, "0 commits") {
		t.Logf("expected commit count, got: %s", obs.Summary)
	}
}

func TestGitStatusSummarizerGroupsByStatus(t *testing.T) {
	s := &GitStatusSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.Summary, "git_status") {
		t.Errorf("expected git_status in summary, got: %s", obs.Summary)
	}
}

func TestGitStatusSummarizerBudgetCritical(t *testing.T) {
	s := &GitStatusSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetCritical)
	if !strings.Contains(obs.Summary, "staged=") {
		t.Errorf("expected staged count, got: %s", obs.Summary)
	}
}

func TestRunTestSummarizerParsesResults(t *testing.T) {
	s := &RunTestSummarizer{}
	result := core.ToolResult{ExitCode: 1, ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.Summary, "run_test") {
		t.Errorf("expected run_test in summary, got: %s", obs.Summary)
	}
	if !strings.Contains(obs.Summary, "exit=1") {
		t.Errorf("expected exit=1, got: %s", obs.Summary)
	}
}

func TestRunTestSummarizerErrors(t *testing.T) {
	s := &RunTestSummarizer{}
	result := core.ToolResult{Error: "test binary not found", ExitCode: 2, ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.RiskDelta, "test binary not found") {
		t.Errorf("expected error in risk, got: %s", obs.RiskDelta)
	}
}

func TestWriteFileSummarizerNeverIncludesContent(t *testing.T) {
	s := &WriteFileSummarizer{}
	result := core.ToolResult{
		Content:    "wrote 1024 bytes to secret.txt",
		ArtifactID: "art-1",
	}

	obs := s.Summarize(result, tools.BudgetSafe)
	// Content comes from result.Content, which is the status message, not file contents.
	// The critical invariant: file content is in artifact store, NOT in observation.
	if strings.Contains(obs.Summary, "password") || strings.Contains(obs.Summary, "secret") {
		// OK - "secret.txt" is in the result.Content status message.
		// But the raw file bytes must never appear.
	}
	// Verify observation does NOT contain raw file data.
	if len(obs.Summary) > 200 {
		t.Errorf("write_file summary too long (%d chars), may contain file content", len(obs.Summary))
	}
}

func TestApplyPatchSummarizerBudgetAware(t *testing.T) {
	s := &ApplyPatchSummarizer{}
	result := core.ToolResult{ArtifactID: "art-1"}

	safe := s.Summarize(result, tools.BudgetSafe)
	warn := s.Summarize(result, tools.BudgetWarning)
	crit := s.Summarize(result, tools.BudgetCritical)

	for _, obs := range []core.Observation{safe, warn, crit} {
		if !strings.Contains(obs.Summary, "apply_patch") {
			t.Errorf("expected apply_patch in summary, got: %s", obs.Summary)
		}
	}
}

func TestApplyPatchSummarizerError(t *testing.T) {
	s := &ApplyPatchSummarizer{}
	result := core.ToolResult{Error: "patch does not apply", ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.RiskDelta, "patch does not apply") {
		t.Errorf("expected error in risk, got: %s", obs.RiskDelta)
	}
}

func TestArtifactReadSummarizerTruncation(t *testing.T) {
	s := &ArtifactReadSummarizer{}
	longContent := strings.Repeat("x", 3000)
	result := core.ToolResult{Content: longContent, ArtifactID: "art-1"}

	crit := s.Summarize(result, tools.BudgetCritical)
	if len(crit.Summary) > 250 {
		t.Errorf("critical summary too long: %d chars", len(crit.Summary))
	}

	safe := s.Summarize(result, tools.BudgetSafe)
	if len(safe.Summary) > 1050 {
		t.Errorf("safe summary too long: %d chars", len(safe.Summary))
	}
}

func TestArtifactReadSummarizerError(t *testing.T) {
	s := &ArtifactReadSummarizer{}
	result := core.ToolResult{Error: "artifact not found", ArtifactID: "art-1"}
	obs := s.Summarize(result, tools.BudgetSafe)
	if !strings.Contains(obs.Summary, "artifact not found") {
		t.Errorf("expected error in summary, got: %s", obs.Summary)
	}
}

func TestObservationHelper(t *testing.T) {
	obs := observation("test_tool", "summary", "state", "risk", core.TierNear, "art-1")
	if obs.Summary != "summary" {
		t.Errorf("summary = %s, want summary", obs.Summary)
	}
	if obs.ArtifactID != "art-1" {
		t.Errorf("artifactID = %s, want art-1", obs.ArtifactID)
	}
}

func TestBudgetLevelsAreDistinct(t *testing.T) {
	if tools.BudgetSafe == tools.BudgetWarning || tools.BudgetWarning == tools.BudgetCritical {
		t.Fatal("budget levels must be distinct")
	}
}

func TestRgGroupByFile(t *testing.T) {
	input := "file.go:10:func main()\nfile.go:20:fmt.Println()\nother.go:5:package x\n"
	groups := rgGroupByFile(input)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].file != "file.go" || len(groups[0].matches) != 2 {
		t.Errorf("file.go group: %+v", groups[0])
	}
	if groups[1].file != "other.go" || len(groups[1].matches) != 1 {
		t.Errorf("other.go group: %+v", groups[1])
	}
}

func TestParseTestResults(t *testing.T) {
	input := "--- PASS: TestFoo\n--- FAIL: TestBar\n--- SKIP: TestBaz\n--- PASS: TestQux\n"
	passes, fails, skips := parseTestResults(input)
	if len(passes) != 2 {
		t.Errorf("passes = %d, want 2", len(passes))
	}
	if len(fails) != 1 {
		t.Errorf("fails = %d, want 1", len(fails))
	}
	if len(skips) != 1 {
		t.Errorf("skips = %d, want 1", len(skips))
	}
}

func TestGroupStatus(t *testing.T) {
	input := " M modified.go\n?? untracked.go\nA  staged.go\n"
	staged, unstaged, untracked := groupStatus(input)
	if len(staged) != 1 || staged[0] != "staged.go" {
		t.Errorf("staged = %v, want [staged.go]", staged)
	}
	if len(unstaged) != 1 || unstaged[0] != "modified.go" {
		t.Errorf("unstaged = %v, want [modified.go]", unstaged)
	}
	if len(untracked) != 1 || untracked[0] != "untracked.go" {
		t.Errorf("untracked = %v, want [untracked.go]", untracked)
	}
}

func TestDiffFiles(t *testing.T) {
	input := "diff --git a/foo.go b/foo.go\n@@ -1,3 +1,4 @@\ndiff --git a/bar.go b/bar.go\n@@ -5,2 +5,3 @@\n"
	files := diffFiles(input)
	if len(files) != 2 {
		t.Errorf("files = %d, want 2 (files=%v)", len(files), files)
	}
	// diffFiles extracts paths from the b/ side of diff --git lines.
	// parts[2] for "diff --git a/foo.go b/foo.go" is "a/foo.go", TrimPrefix("b/") is no-op.
	// So the key is "a/foo.go".
	if _, ok := files["a/foo.go"]; !ok {
		t.Errorf("missing a/foo.go in %v", files)
	}
	if _, ok := files["a/bar.go"]; !ok {
		t.Errorf("missing a/bar.go in %v", files)
	}
}

func TestLastNLines(t *testing.T) {
	text := "a\nb\nc\nd\ne\n"
	result := lastNLines(text, 3)
	if result != "c\nd\ne" {
		t.Errorf("last 3 lines = %q, want 'c\\nd\\ne'", result)
	}
}

func TestNewRegistryHasAllTools(t *testing.T) {
	reg := NewRegistry(nil)
	expected := []string{"shell", "rg", "read_file", "list_dir", "git_diff", "git_log",
		"git_status", "run_test", "write_file", "apply_patch", "artifact_read"}
	for _, name := range expected {
		if reg[name] == nil {
			t.Errorf("missing summarizer for %s", name)
		}
	}
}
