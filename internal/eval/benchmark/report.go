package benchmark

import (
	"fmt"
	"strings"
	"time"
)

// GenerateReport produces a markdown report from benchmark results.
func GenerateReport(results []TaskResult) string {
	var b strings.Builder

	passed, failed := countResults(results)

	// Header
	b.WriteString("# MiMo Benchmark Report\n\n")
	b.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().Format(time.RFC3339)))

	// Summary table
	b.WriteString("## Summary\n\n")
	b.WriteString(fmt.Sprintf("| Metric | Value |\n"))
	b.WriteString(fmt.Sprintf("|--------|-------|\n"))
	b.WriteString(fmt.Sprintf("| Total Tasks | %d |\n", len(results)))
	b.WriteString(fmt.Sprintf("| Passed | %d |\n", passed))
	b.WriteString(fmt.Sprintf("| Failed | %d |\n", failed))
	b.WriteString(fmt.Sprintf("| Pass Rate | %.0f%% |\n", passRate(passed, len(results))))
	b.WriteString(fmt.Sprintf("| Avg Duration | %s |\n", avgDuration(results)))
	b.WriteString(fmt.Sprintf("| Avg Tool Count | %.1f |\n", avgToolCount(results)))
	b.WriteString(fmt.Sprintf("| Total Tokens | %d |\n", totalTokens(results)))
	b.WriteString("\n")

	// Results table
	b.WriteString("## Results\n\n")
	b.WriteString("| Task | Status | Duration | Tools | Tokens | Errors |\n")
	b.WriteString("|------|--------|----------|-------|--------|--------|\n")
	for _, r := range results {
		status := "PASS"
		if !r.Success {
			status = "FAIL"
		}
		errorCount := len(r.Errors)
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %d |\n",
			r.TaskName, status, r.Duration.Round(time.Millisecond), r.ToolCount, r.TokenEstimate, errorCount))
	}
	b.WriteString("\n")

	// Per-task details
	b.WriteString("## Task Details\n\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("### %s\n\n", r.TaskName))

		status := "PASS"
		if !r.Success {
			status = "FAIL"
		}
		b.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
		b.WriteString(fmt.Sprintf("- **Duration**: %s\n", r.Duration.Round(time.Millisecond)))
		b.WriteString(fmt.Sprintf("- **Tool Calls**: %d\n", r.ToolCount))
		b.WriteString(fmt.Sprintf("- **Token Estimate**: %d\n", r.TokenEstimate))
		b.WriteString(fmt.Sprintf("- **Artifacts Created**: %d\n", len(r.ArtifactsCreated)))
		b.WriteString(fmt.Sprintf("- **Rollbacks**: %d\n", r.RollbackCount))

		if len(r.ToolSequence) > 0 {
			b.WriteString(fmt.Sprintf("- **Tool Sequence**: %s\n", strings.Join(r.ToolSequence, " -> ")))
		}

		if len(r.TraceStages) > 0 {
			b.WriteString(fmt.Sprintf("- **Trace Stages**: %s\n", strings.Join(r.TraceStages, ", ")))
		}

		if !r.Success && r.FailureReason != "" {
			b.WriteString(fmt.Sprintf("- **Failure Reason**: %s\n", r.FailureReason))
		}

		if len(r.Errors) > 0 {
			b.WriteString("- **Errors**:\n")
			for _, e := range r.Errors {
				b.WriteString(fmt.Sprintf("  - %s\n", e))
			}
		}

		if r.FinalSummary != "" {
			summary := r.FinalSummary
			if len(summary) > 500 {
				summary = summary[:500] + "..."
			}
			b.WriteString(fmt.Sprintf("- **Final Summary**: %s\n", summary))
		}

		b.WriteString("\n")
	}

	return b.String()
}

func countResults(results []TaskResult) (passed, failed int) {
	for _, r := range results {
		if r.Success {
			passed++
		} else {
			failed++
		}
	}
	return
}

func passRate(passed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) / float64(total) * 100
}

func avgDuration(results []TaskResult) time.Duration {
	if len(results) == 0 {
		return 0
	}
	var total time.Duration
	for _, r := range results {
		total += r.Duration
	}
	return total / time.Duration(len(results))
}

func avgToolCount(results []TaskResult) float64 {
	if len(results) == 0 {
		return 0
	}
	var total int
	for _, r := range results {
		total += r.ToolCount
	}
	return float64(total) / float64(len(results))
}

func totalTokens(results []TaskResult) int {
	var total int
	for _, r := range results {
		total += r.TokenEstimate
	}
	return total
}
