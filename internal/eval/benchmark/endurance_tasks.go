package benchmark

import "time"

// EnduranceSessionTasks returns a 10-prompt coding session that exercises
// the agent across a variety of common developer tasks.
func EnduranceSessionTasks() []string {
	return []string{
		"Read the README.md",
		"Find all Go test files",
		"Count the total lines of code in internal/",
		"List all exported functions in internal/core/types.go",
		"Find any TODO comments in the codebase",
		"Check if there are any unused imports",
		"Verify all tests pass",
		"Summarize the project structure",
		"Find the largest file by line count",
		"Generate a brief architecture overview",
	}
}

// DefaultEnduranceConfig returns a sensible default endurance configuration
// for a full 10-prompt session.
func DefaultEnduranceConfig() EnduranceConfig {
	return EnduranceConfig{
		Prompts:      EnduranceSessionTasks(),
		MaxStepsEach: 4,
		Timeout:      300 * time.Second,
	}
}

// InterruptResumeEnduranceConfig returns an endurance configuration that
// interrupts after 3 prompts and then resumes.
func InterruptResumeEnduranceConfig() EnduranceConfig {
	return EnduranceConfig{
		Prompts:      EnduranceSessionTasks(),
		MaxStepsEach: 4,
		Timeout:      300 * time.Second,
		InterruptAt:  3,
		ResumeAfter:  true,
	}
}
