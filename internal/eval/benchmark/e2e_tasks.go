package benchmark

import "time"

// E2ETask defines a single end-to-end test scenario for the MiMo API.
type E2ETask struct {
	// Name is a short identifier for the task (used in result reporting).
	Name string
	// Prompt is the user message sent to the agent loop.
	Prompt string
	// ExpectedToolCalls is the minimum number of tool calls the model should produce.
	ExpectedToolCalls int
	// MaxSteps is the maximum agent loop iterations before forced stop.
	MaxSteps int
	// Timeout is the per-task deadline.
	Timeout time.Duration
}

// DefaultE2ETasks returns the standard set of five E2E tasks covering
// simple question, read_file, search_code, generate_patch, and run_test.
func DefaultE2ETasks() []E2ETask {
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
			MaxSteps:          6,
			Timeout:           90 * time.Second,
		},
		{
			Name:              "search_code",
			Prompt:            "Search the codebase for all Go files that contain the string 'func New' and list the file paths.",
			ExpectedToolCalls: 1,
			MaxSteps:          6,
			Timeout:           90 * time.Second,
		},
		{
			Name:              "generate_patch",
			Prompt:            "Create a file called e2e_marker.txt in the current directory with the content 'e2e test marker'. Use the write_file tool.",
			ExpectedToolCalls: 1,
			MaxSteps:          6,
			Timeout:           90 * time.Second,
		},
		{
			Name:              "run_test",
			Prompt:            "Run the Go tests in the current directory with 'go test ./...' and tell me if they passed.",
			ExpectedToolCalls: 1,
			MaxSteps:          8,
			Timeout:           120 * time.Second,
		},
	}
}
