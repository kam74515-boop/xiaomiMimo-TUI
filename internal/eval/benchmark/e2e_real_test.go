package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// maskAPIKeyTest returns the first 8 characters of s plus "..." for safe logging.
func maskAPIKeyTest(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:8] + "..."
}

// repoRoot returns the absolute path to the worktree root directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	// internal/eval/benchmark/e2e_real_test.go -> repo root
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("cannot resolve repo root: %v", err)
	}
	return abs
}

// realE2ETasks returns the E2E task set tuned for live API execution.
// Differences from DefaultE2ETasks:
//   - Higher MaxSteps to accommodate the model's tendency to retry tool calls.
//   - run_test uses "go version" instead of "go test ./..." to avoid recursively
//     triggering the E2E test itself.
//   - Longer timeouts per task.
func realE2ETasks() []E2ETask {
	return []E2ETask{
		{
			Name:              "simple_question",
			Prompt:            "What is the Go programming language? Answer in one sentence.",
			ExpectedToolCalls: 0,
			MaxSteps:          4,
			Timeout:           60 * time.Second,
		},
		{
			Name:              "read_file",
			Prompt:            "Read the file go.mod in the current directory and tell me the module name.",
			ExpectedToolCalls: 1,
			MaxSteps:          16,
			Timeout:           120 * time.Second,
		},
		{
			Name:              "search_code",
			Prompt:            "Search the codebase for all Go files that contain the string 'func New' and list the file paths.",
			ExpectedToolCalls: 1,
			MaxSteps:          20,
			Timeout:           180 * time.Second,
		},
		{
			Name:              "generate_patch",
			Prompt:            "Create a file called e2e_marker.txt in the current directory with the content 'e2e test marker'. Use the write_file tool.",
			ExpectedToolCalls: 1,
			MaxSteps:          12,
			Timeout:           120 * time.Second,
		},
		{
			Name:              "run_test",
			Prompt:            "Run the command 'go version' using the shell tool and tell me the output.",
			ExpectedToolCalls: 1,
			MaxSteps:          8,
			Timeout:           120 * time.Second,
		},
	}
}

// TestRealMiMoE2E runs the full E2E benchmark against the live MiMo API.
//
// It is skipped when MIMO_API_KEY is not set in the environment.
// The base URL defaults to https://token-plan-cn.xiaomimimo.com/v1 and
// the model defaults to mimo-v2.5-pro unless overridden by MIMO_BASE_URL
// and MIMO_MODEL respectively.
func TestRealMiMoE2E(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("MIMO_API_KEY"))
	if apiKey == "" {
		t.Skip("MIMO_API_KEY not set; skipping real E2E test")
	}

	baseURL := strings.TrimSpace(os.Getenv("MIMO_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://token-plan-cn.xiaomimimo.com/v1"
	}

	model := strings.TrimSpace(os.Getenv("MIMO_MODEL"))
	if model == "" {
		model = "mimo-v2.5-pro"
	}

	ws := repoRoot(t)
	t.Logf("Real E2E: base_url=%s model=%s key=%s workspace=%s", baseURL, model, maskAPIKeyTest(apiKey), ws)

	cfg := E2EConfig{
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		Tasks:     realE2ETasks(),
		Timeout:   10 * time.Minute,
		Workspace: ws,
	}

	results, err := RunE2E(cfg)
	if err != nil {
		t.Fatalf("RunE2E returned error: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("RunE2E returned no results")
	}

	// Print detailed results table.
	t.Log("\n=== Real MiMo E2E Results ===")
	t.Log(fmt.Sprintf("%-20s %-8s %-6s %-6s %-10s %-16s %s",
		"TASK", "STATUS", "STEPS", "TOOLS", "DURATION", "ERROR_CLASS", "ERROR_MSG"))
	t.Log(strings.Repeat("-", 110))

	passCount := 0
	for _, r := range results {
		status := "PASS"
		if !r.Success {
			status = "FAIL"
		}
		if r.Success {
			passCount++
		}
		errMsg := r.ErrorMessage
		if len(errMsg) > 80 {
			errMsg = errMsg[:80] + "..."
		}
		t.Log(fmt.Sprintf("%-20s %-8s %-6d %-6d %-10s %-16s %s",
			r.TaskName, status, r.Steps, r.ToolCalls,
			r.Duration.Round(time.Millisecond), r.ErrorClass, errMsg))
	}

	t.Log(strings.Repeat("-", 110))
	t.Logf("Summary: %d/%d tasks passed", passCount, len(results))

	// Require at least 4/5 tasks to pass (model may occasionally hit step limits).
	minPass := 4
	if len(results) < minPass {
		minPass = len(results)
	}
	if passCount < minPass {
		t.Errorf("only %d/%d tasks passed (need at least %d)", passCount, len(results), minPass)
		for _, r := range results {
			if !r.Success {
				t.Logf("  failed: %q (class=%s): %s", r.TaskName, r.ErrorClass, r.ErrorMessage)
			}
		}
	}
}

// TestRealMiMoE2E_StreamingOnly tests just the streaming path (single API call)
// without the full agent loop. Useful for verifying the SSE parser works with the
// live API even if tool execution has issues.
func TestRealMiMoE2E_StreamingOnly(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("MIMO_API_KEY"))
	if apiKey == "" {
		t.Skip("MIMO_API_KEY not set; skipping streaming-only test")
	}

	baseURL := strings.TrimSpace(os.Getenv("MIMO_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://token-plan-cn.xiaomimimo.com/v1"
	}

	model := strings.TrimSpace(os.Getenv("MIMO_MODEL"))
	if model == "" {
		model = "mimo-v2.5-pro"
	}

	ws := repoRoot(t)
	t.Logf("Streaming test: base_url=%s model=%s key=%s", baseURL, model, maskAPIKeyTest(apiKey))

	// Run only the simple_question task which doesn't need tools.
	cfg := E2EConfig{
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		Tasks:     []E2ETask{realE2ETasks()[0]}, // simple_question only
		Timeout:   90 * time.Second,
		Workspace: ws,
	}

	results, err := RunE2E(cfg)
	if err != nil {
		t.Fatalf("RunE2E returned error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	t.Logf("Result: success=%v steps=%d tool_calls=%d duration=%s class=%s",
		r.Success, r.Steps, r.ToolCalls, r.Duration.Round(time.Millisecond), r.ErrorClass)

	if !r.Success {
		t.Errorf("simple_question failed (class=%s): %s", r.ErrorClass, r.ErrorMessage)
	}
}
