package eval

import (
	"fmt"

	"mimo-tui/internal/core"
)

// KeyToolNames lists the tools whose name and order must match exactly
// between golden and candidate sessions for the gate to pass.
var KeyToolNames = []string{"shell", "write_file", "apply_patch"}

// GateResult summarizes the outcome of a replay gate evaluation.
type GateResult struct {
	Passed               bool     `json:"passed"`
	Score                float64  `json:"score"`
	ToolMatchRate        float64  `json:"tool_match_rate"`
	TrajectorySimilarity float64  `json:"trajectory_similarity"`
	Failures             []string `json:"failures,omitempty"`
}

// EvaluateCandidate compares goldenEvents against candidateEvents and
// returns a GateResult indicating whether the candidate session passes the
// replay gate.
//
// Gate rules:
//   - Candidate must have no EventError events and must contain EventDone.
//   - Key tools (shell, write_file, apply_patch) must appear in the same
//     name and order in both golden and candidate.
//   - Overall tool call name-sequence similarity must exceed 70%.
func EvaluateCandidate(goldenEvents, candidateEvents []core.AgentEvent) GateResult {
	var res GateResult
	var failures []string

	hasDone := hasEventType(candidateEvents, core.EventDone)
	hasError := hasEventType(candidateEvents, core.EventError)

	if hasError {
		failures = append(failures, "candidate session contains error events")
	}
	if !hasDone {
		failures = append(failures, "candidate session did not complete (no event_done)")
	}

	goldenToolNames := extractToolCallNames(goldenEvents)
	candidateToolNames := extractToolCallNames(candidateEvents)

	// Key-tool name+order check.
	goldenKey := filterKeyTools(goldenToolNames)
	candidateKey := filterKeyTools(candidateToolNames)
	if !sequencesEqual(goldenKey, candidateKey) {
		failures = append(failures, fmt.Sprintf(
			"key tool sequence mismatch: golden=%v candidate=%v",
			goldenKey, candidateKey,
		))
	}

	// Overall tool sequence similarity.
	similarity := toolSequenceSimilarity(goldenToolNames, candidateToolNames)
	res.TrajectorySimilarity = similarity
	if similarity <= 0.7 {
		failures = append(failures, fmt.Sprintf(
			"tool sequence similarity too low: %.2f (threshold 0.70)",
			similarity,
		))
	}

	// Tool match rate: fraction of candidate tools that appear in golden at
	// the same position.
	if len(candidateToolNames) > 0 && len(goldenToolNames) > 0 {
		match := 0
		minLen := len(goldenToolNames)
		if len(candidateToolNames) < minLen {
			minLen = len(candidateToolNames)
		}
		for i := 0; i < minLen; i++ {
			if goldenToolNames[i] == candidateToolNames[i] {
				match++
			}
		}
		maxLen := len(goldenToolNames)
		if len(candidateToolNames) > maxLen {
			maxLen = len(candidateToolNames)
		}
		res.ToolMatchRate = float64(match) / float64(maxLen)
	} else if len(goldenToolNames) == 0 && len(candidateToolNames) == 0 {
		res.ToolMatchRate = 1.0
	}

	// Composite score: 40% key-tool match + 60% trajectory similarity.
	keyScore := 1.0
	if !sequencesEqual(goldenKey, candidateKey) {
		keyScore = 0.0
	}
	res.Score = 0.4*keyScore + 0.6*similarity

	res.Failures = failures
	res.Passed = len(failures) == 0
	return res
}

// extractToolCallNames returns the ordered list of tool names from
// EventToolStart events.
func extractToolCallNames(events []core.AgentEvent) []string {
	var names []string
	for _, e := range events {
		if e.Type == core.EventToolStart && e.ToolCall != nil {
			names = append(names, e.ToolCall.Name)
		}
	}
	return names
}

// filterKeyTools returns only the tool names that belong to the key set.
func filterKeyTools(names []string) []string {
	key := make(map[string]bool, len(KeyToolNames))
	for _, k := range KeyToolNames {
		key[k] = true
	}
	var out []string
	for _, n := range names {
		if key[n] {
			out = append(out, n)
		}
	}
	return out
}

// sequencesEqual reports whether two string slices are identical.
func sequencesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hasEventType reports whether events contains at least one event of the
// given type.
func hasEventType(events []core.AgentEvent, t core.EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// toolSequenceSimilarity computes the longest-common-subsequence ratio
// between two tool-name sequences. Returns 1.0 when both are empty.
func toolSequenceSimilarity(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	lcsLen := lcsLength(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 0
	}
	return float64(lcsLen) / float64(maxLen)
}

// lcsLength returns the length of the longest common subsequence of a and b.
func lcsLength(a, b []string) int {
	n, m := len(a), len(b)
	// Use a 1D DP array to keep memory small.
	prev := make([]int, m+1)
	curr := make([]int, m+1)
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				curr[j] = prev[j]
				if curr[j-1] > curr[j] {
					curr[j] = curr[j-1]
				}
			}
		}
		prev, curr = curr, prev
	}
	return prev[m]
}
