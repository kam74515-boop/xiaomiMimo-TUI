// Package summarizers provides budget-aware summary implementations for
// every MiMo built-in tool. Each summarizer honours the BudgetLevel to
// produce progressively terser observations, keeping raw output in the
// artifact store while the observation carries only a bounded digest.
package summarizers

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
	"mimo-tui/internal/tools"
)

// NewRegistry returns a map of tool name → Summarizer wired with the
// optional artifact store so that summarizers can read raw payloads
// when deeper truncation is needed.
func NewRegistry(store *artifact.Store) map[string]tools.Summarizer {
	return map[string]tools.Summarizer{
		"shell":         &ShellSummarizer{store: store},
		"rg":            &RgSummarizer{store: store},
		"read_file":     &ReadFileSummarizer{store: store},
		"list_dir":      &ListDirSummarizer{store: store},
		"git_diff":      &GitDiffSummarizer{store: store},
		"git_log":       &GitLogSummarizer{store: store},
		"git_status":    &GitStatusSummarizer{store: store},
		"run_test":      &RunTestSummarizer{store: store},
		"write_file":    &WriteFileSummarizer{},
		"apply_patch":   &ApplyPatchSummarizer{store: store},
		"artifact_read": &ArtifactReadSummarizer{store: store},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func observation(name string, summary, state, risk string, placement core.ContextTier, artifactID string) core.Observation {
	if summary == "" {
		summary = name + " completed"
	}
	if state == "" {
		state = "raw output stored as artifact " + artifactID
	}
	if risk == "" {
		risk = "no new risk"
	}
	return core.Observation{
		Summary:          summary,
		StateDelta:       state,
		RiskDelta:        risk,
		ContextPlacement: placement,
		ArtifactID:       artifactID,
	}
}

func readStdout(store *artifact.Store, artifactID string) string {
	if store == nil || artifactID == "" {
		return ""
	}
	payloads, err := store.ReadPayloads(artifactID)
	if err != nil {
		return ""
	}
	for _, p := range payloads {
		if p.Metadata.Name == "stdout.txt" {
			return string(p.Data)
		}
	}
	return ""
}

func readContent(store *artifact.Store, artifactID string) string {
	if store == nil || artifactID == "" {
		return ""
	}
	payloads, err := store.ReadPayloads(artifactID)
	if err != nil {
		return ""
	}
	for _, p := range payloads {
		if p.Metadata.Name == "content.txt" {
			return string(p.Data)
		}
	}
	return ""
}

func readPatch(store *artifact.Store, artifactID string) string {
	if store == nil || artifactID == "" {
		return ""
	}
	payloads, err := store.ReadPayloads(artifactID)
	if err != nil {
		return ""
	}
	for _, p := range payloads {
		if p.Metadata.Name == "patch.diff" {
			return string(p.Data)
		}
	}
	return ""
}

func readListing(store *artifact.Store, artifactID string) string {
	if store == nil || artifactID == "" {
		return ""
	}
	payloads, err := store.ReadPayloads(artifactID)
	if err != nil {
		return ""
	}
	for _, p := range payloads {
		if p.Metadata.Name == "listing.txt" {
			return string(p.Data)
		}
	}
	return ""
}

func lastNLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	// Trim trailing empty line from trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// ---------------------------------------------------------------------------
// ShellSummarizer
// ---------------------------------------------------------------------------

type ShellSummarizer struct {
	store *artifact.Store
}

func (s *ShellSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	summary, risk := shellSummary(result, readStdout(s.store, result.ArtifactID), budget)
	return observation("shell", summary, "shell output stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func shellSummary(result core.ToolResult, stdout string, budget tools.BudgetLevel) (string, string) {
	exit := fmt.Sprintf("exit=%d", result.ExitCode)
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
	} else if result.ExitCode != 0 {
		risk = "non-zero exit code " + strconv.Itoa(result.ExitCode)
	}
	if result.Error != "" {
		return "shell " + exit + "; error=" + result.Error, risk
	}

	switch budget {
	case tools.BudgetCritical:
		return "shell " + exit + "; artifact=" + result.ArtifactID, risk
	case tools.BudgetWarning:
		lines := lastNLines(stdout, 10)
		if lines == "" {
			return "shell " + exit + "; artifact=" + result.ArtifactID, risk
		}
		return "shell " + exit + "\n" + lines, risk
	default: // tools.BudgetSafe
		lines := lastNLines(stdout, 20)
		if lines == "" {
			return "shell " + exit + "; artifact=" + result.ArtifactID, risk
		}
		return "shell " + exit + "\n" + lines, risk
	}
}

// ---------------------------------------------------------------------------
// RgSummarizer
// ---------------------------------------------------------------------------

type RgSummarizer struct {
	store *artifact.Store
}

func (s *RgSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	stdout := readStdout(s.store, result.ArtifactID)
	groups := rgGroupByFile(stdout)
	summary, risk := rgSummary(groups, result, budget)
	return observation("rg", summary, "rg results stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

type rgFileGroup struct {
	file    string
	matches []string
}

func rgGroupByFile(stdout string) []rgFileGroup {
	lines := strings.Split(stdout, "\n")
	byFile := map[string][]string{}
	var order []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// rg output: file:line:content
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		file := line[:idx]
		if _, ok := byFile[file]; !ok {
			order = append(order, file)
		}
		byFile[file] = append(byFile[file], line)
	}
	groups := make([]rgFileGroup, 0, len(order))
	for _, f := range order {
		groups = append(groups, rgFileGroup{file: f, matches: byFile[f]})
	}
	return groups
}

func rgSummary(groups []rgFileGroup, result core.ToolResult, budget tools.BudgetLevel) (string, string) {
	exit := fmt.Sprintf("exit=%d", result.ExitCode)
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "rg " + exit + "; error=" + result.Error, risk
	}

	totalFiles := len(groups)
	totalMatches := 0
	for _, g := range groups {
		totalMatches += len(g.matches)
	}

	switch budget {
	case tools.BudgetCritical:
		var parts []string
		for _, g := range groups {
			parts = append(parts, fmt.Sprintf("%s(%d)", filepath.Base(g.file), len(g.matches)))
		}
		return fmt.Sprintf("rg %s; %d files, %d matches [%s]; artifact=%s",
			exit, totalFiles, totalMatches, strings.Join(parts, ", "), result.ArtifactID), risk
	case tools.BudgetWarning:
		perFile := 3
		var b strings.Builder
		fmt.Fprintf(&b, "rg %s; %d files, %d matches", exit, totalFiles, totalMatches)
		for _, g := range groups {
			fmt.Fprintf(&b, "\n  %s: %d matches", filepath.Base(g.file), len(g.matches))
			limit := perFile
			if limit > len(g.matches) {
				limit = len(g.matches)
			}
			for i := 0; i < limit; i++ {
				short := g.matches[i]
				if len(short) > 120 {
					short = short[:120] + "..."
				}
				fmt.Fprintf(&b, "\n    %s", short)
			}
			if len(g.matches) > perFile {
				fmt.Fprintf(&b, "\n    ... +%d more", len(g.matches)-perFile)
			}
		}
		fmt.Fprintf(&b, "\nartifact=%s", result.ArtifactID)
		return b.String(), risk
	default: // BudgetSafe
		perFile := 10
		var b strings.Builder
		fmt.Fprintf(&b, "rg %s; %d files, %d matches", exit, totalFiles, totalMatches)
		for _, g := range groups {
			fmt.Fprintf(&b, "\n  %s: %d matches", filepath.Base(g.file), len(g.matches))
			limit := perFile
			if limit > len(g.matches) {
				limit = len(g.matches)
			}
			for i := 0; i < limit; i++ {
				short := g.matches[i]
				if len(short) > 200 {
					short = short[:200] + "..."
				}
				fmt.Fprintf(&b, "\n    %s", short)
			}
			if len(g.matches) > perFile {
				fmt.Fprintf(&b, "\n    ... +%d more", len(g.matches)-perFile)
			}
		}
		fmt.Fprintf(&b, "\nartifact=%s", result.ArtifactID)
		return b.String(), risk
	}
}

// ---------------------------------------------------------------------------
// ReadFileSummarizer
// ---------------------------------------------------------------------------

type ReadFileSummarizer struct {
	store *artifact.Store
}

func (s *ReadFileSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	content := readContent(s.store, result.ArtifactID)
	summary, risk, placement := readFileSummary(result, content, budget)
	return observation("read_file", summary, "file content stored as artifact "+result.ArtifactID, risk, placement, result.ArtifactID)
}

func readFileSummary(result core.ToolResult, content string, budget tools.BudgetLevel) (string, string, core.ContextTier) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "read_file error=" + result.Error, risk, core.TierArtifact
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	totalLines := len(lines)
	totalBytes := len(content)
	// path info from result.Content not extracted here; _ = result.Content // contains "read N bytes from PATH; artifact=ID"

	headN := 50
	tailN := 50
	switch budget {
	case tools.BudgetCritical:
		return fmt.Sprintf("read_file %d bytes, %d lines; artifact=%s", totalBytes, totalLines, result.ArtifactID), risk, core.TierArtifact
	case tools.BudgetWarning:
		headN = 20
		tailN = 20
	}

	var b strings.Builder
	fmt.Fprintf(&b, "read_file %d bytes, %d lines", totalBytes, totalLines)
	if headN > 0 && len(lines) > 0 {
		end := headN
		if end > len(lines) {
			end = len(lines)
		}
		fmt.Fprintf(&b, "\n--- head (%d lines) ---", end)
		for i := 0; i < end; i++ {
			fmt.Fprintf(&b, "\n  %s", lines[i])
		}
	}
	if tailN > 0 && len(lines) > headN {
		start := len(lines) - tailN
		if start < headN {
			start = headN
		}
		if start < len(lines) {
			fmt.Fprintf(&b, "\n--- tail (%d lines) ---", len(lines)-start)
			for i := start; i < len(lines); i++ {
				fmt.Fprintf(&b, "\n  %s", lines[i])
			}
		}
	}
	fmt.Fprintf(&b, "\nartifact=%s", result.ArtifactID)
	return b.String(), risk, core.TierArtifact
}

// ---------------------------------------------------------------------------
// ListDirSummarizer
// ---------------------------------------------------------------------------

type ListDirSummarizer struct {
	store *artifact.Store
}

func (s *ListDirSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	listing := readListing(s.store, result.ArtifactID)
	summary, risk := listDirSummary(result, listing, budget)
	return observation("list_dir", summary, "directory listing stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func listDirSummary(result core.ToolResult, listing string, budget tools.BudgetLevel) (string, string) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "list_dir error=" + result.Error, risk
	}

	// listing format: kind\t<size>\t<name>
	byExt := map[string]int{}
	entries := strings.Split(strings.TrimSpace(listing), "\n")
	dirs := 0
	files := 0
	for _, e := range entries {
		parts := strings.Split(e, "\t")
		if len(parts) < 3 {
			continue
		}
		kind := parts[0]
		name := parts[2]
		if kind == "dir" {
			dirs++
			byExt["[dir]"]++
		} else {
			files++
			ext := filepath.Ext(name)
			if ext == "" {
				ext = "[no ext]"
			}
			byExt[ext]++
		}
	}

	switch budget {
	case tools.BudgetCritical:
		return fmt.Sprintf("list_dir %d dirs, %d files, %d types; artifact=%s", dirs, files, len(byExt), result.ArtifactID), risk
	case tools.BudgetWarning:
		top := topExtCounts(byExt, 30)
		return fmt.Sprintf("list_dir %d entries (%d dirs, %d files); top types: %s; artifact=%s",
			len(entries), dirs, files, strings.Join(top, ", "), result.ArtifactID), risk
	default: // BudgetSafe
		top := topExtCounts(byExt, 0) // all
		return fmt.Sprintf("list_dir %d entries (%d dirs, %d files); types: %s; artifact=%s",
			len(entries), dirs, files, strings.Join(top, ", "), result.ArtifactID), risk
	}
}

func topExtCounts(byExt map[string]int, limit int) []string {
	type kv struct {
		ext   string
		count int
	}
	var sorted []kv
	for ext, count := range byExt {
		sorted = append(sorted, kv{ext, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].count == sorted[j].count {
			return sorted[i].ext < sorted[j].ext
		}
		return sorted[i].count > sorted[j].count
	})
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	var parts []string
	for _, s := range sorted {
		parts = append(parts, fmt.Sprintf("%s=%d", s.ext, s.count))
	}
	return parts
}

// ---------------------------------------------------------------------------
// GitDiffSummarizer
// ---------------------------------------------------------------------------

type GitDiffSummarizer struct {
	store *artifact.Store
}

func (s *GitDiffSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	stdout := readStdout(s.store, result.ArtifactID)
	summary, risk := gitDiffSummary(result, stdout, budget)
	return observation("git_diff", summary, "diff output stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func gitDiffSummary(result core.ToolResult, diff string, budget tools.BudgetLevel) (string, string) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "git_diff error=" + result.Error, risk
	}

	files := diffFiles(diff)
	hunks := diffHunks(diff)
	numFiles := len(files)
	numHunks := 0
	for _, h := range hunks {
		numHunks += len(h)
	}

	switch budget {
	case tools.BudgetCritical:
		return fmt.Sprintf("git_diff %d files, %d hunks; artifact=%s", numFiles, numHunks, result.ArtifactID), risk
	case tools.BudgetWarning:
		var names []string
		for f := range files {
			names = append(names, filepath.Base(f))
		}
		sort.Strings(names)
		return fmt.Sprintf("git_diff %d files, %d hunks [%s]; artifact=%s",
			numFiles, numHunks, strings.Join(names, ", "), result.ArtifactID), risk
	default: // BudgetSafe
		limit := 5
		var b strings.Builder
		fmt.Fprintf(&b, "git_diff %d files, %d hunks", numFiles, numHunks)
		count := 0
		var fnames []string
		for f := range files {
			fnames = append(fnames, f)
		}
		sort.Strings(fnames)
		for _, f := range fnames {
			if count >= limit {
				fmt.Fprintf(&b, "\n  ... +%d more files", numFiles-count)
				break
			}
			hunkLines := files[f]
			fmt.Fprintf(&b, "\n  %s: %d hunks", filepath.Base(f), len(hunkLines))
			for _, h := range hunkLines {
				fmt.Fprintf(&b, "\n    %s", h)
			}
			count++
		}
		fmt.Fprintf(&b, "\nartifact=%s", result.ArtifactID)
		return b.String(), risk
	}
}

func diffFiles(diff string) map[string][]string {
	lines := strings.Split(diff, "\n")
	files := map[string][]string{}
	var currentFile string
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				currentFile = strings.TrimPrefix(parts[2], "b/")
				files[currentFile] = nil
			}
		}
		if currentFile != "" && strings.HasPrefix(line, "@@ ") {
			files[currentFile] = append(files[currentFile], strings.TrimSpace(line))
		}
	}
	return files
}

func diffHunks(diff string) map[string][]string {
	return diffFiles(diff) // same structure: file -> hunk headers
}

// ---------------------------------------------------------------------------
// GitLogSummarizer
// ---------------------------------------------------------------------------

type GitLogSummarizer struct {
	store *artifact.Store
}

func (s *GitLogSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	stdout := readStdout(s.store, result.ArtifactID)
	summary, risk := gitLogSummary(result, stdout, budget)
	return observation("git_log", summary, "log output stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func gitLogSummary(result core.ToolResult, stdout string, budget tools.BudgetLevel) (string, string) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "git_log error=" + result.Error, risk
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	total := len(lines)

	maxCommits := total
	switch budget {
	case tools.BudgetCritical:
		maxCommits = 3
	case tools.BudgetWarning:
		maxCommits = 5
	default:
		maxCommits = 10
	}
	if maxCommits > total {
		maxCommits = total
	}

	var b strings.Builder
	fmt.Fprintf(&b, "git_log %d commits", total)
	for i := 0; i < maxCommits; i++ {
		line := lines[i]
		if budget == tools.BudgetCritical {
			// hash + msg only: format is hash\tdate\tauthor\tmsg
			parts := strings.SplitN(line, "\t", 4)
			if len(parts) >= 4 {
				fmt.Fprintf(&b, "\n  %s %s", parts[0], parts[3])
			} else {
				fmt.Fprintf(&b, "\n  %s", line)
			}
		} else {
			fmt.Fprintf(&b, "\n  %s", line)
		}
	}
	if maxCommits < total {
		fmt.Fprintf(&b, "\n  ... +%d more", total-maxCommits)
	}
	fmt.Fprintf(&b, "\nartifact=%s", result.ArtifactID)
	return b.String(), risk
}

// ---------------------------------------------------------------------------
// GitStatusSummarizer
// ---------------------------------------------------------------------------

type GitStatusSummarizer struct {
	store *artifact.Store
}

func (s *GitStatusSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	stdout := readStdout(s.store, result.ArtifactID)
	summary, risk := gitStatusSummary(result, stdout, budget)
	return observation("git_status", summary, "status output stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func gitStatusSummary(result core.ToolResult, stdout string, budget tools.BudgetLevel) (string, string) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "git_status error=" + result.Error, risk
	}

	staged, unstaged, untracked := groupStatus(stdout)
	stagedCount := len(staged)
	unstagedCount := len(unstaged)
	untrackedCount := len(untracked)
	_ = stagedCount + unstagedCount + untrackedCount // totalEntries not used

	switch budget {
	case tools.BudgetCritical:
		return fmt.Sprintf("git_status staged=%d unstaged=%d untracked=%d; artifact=%s",
			stagedCount, unstagedCount, untrackedCount, result.ArtifactID), risk
	case tools.BudgetWarning:
		return formatStatusGroups(staged, unstaged, untracked, 5, result.ArtifactID), risk
	default: // BudgetSafe
		return formatStatusGroups(staged, unstaged, untracked, 0, result.ArtifactID), risk
	}
}

func groupStatus(output string) (staged, unstaged, untracked []string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, " \t\r\n")
		if line == "" || strings.HasPrefix(line, "##") {
			continue
		}
		if len(line) < 2 {
			continue
		}
		status := line[:2]
		file := strings.TrimSpace(line[2:])
		if strings.ContainsRune(status, '?') {
			untracked = append(untracked, file)
		} else if isStaged(status) {
			staged = append(staged, file)
		} else {
			unstaged = append(unstaged, file)
		}
	}
	return
}

func isStaged(status string) bool {
	// Index status position (first char) is non-space and non-?
	return status[0] != ' ' && status[0] != '?'
}

func formatStatusGroups(staged, unstaged, untracked []string, perGroup int, artifactID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "git_status staged=%d unstaged=%d untracked=%d",
		len(staged), len(unstaged), len(untracked))

	writeGroup := func(label string, files []string) {
		if len(files) == 0 {
			return
		}
		limit := perGroup
		if limit <= 0 || limit > len(files) {
			limit = len(files)
		}
		fmt.Fprintf(&b, "\n  %s:", label)
		for i := 0; i < limit; i++ {
			fmt.Fprintf(&b, "\n    %s", files[i])
		}
		if perGroup > 0 && len(files) > perGroup {
			fmt.Fprintf(&b, "\n    ... +%d more", len(files)-perGroup)
		}
	}
	writeGroup("staged", staged)
	writeGroup("unstaged", unstaged)
	writeGroup("untracked", untracked)
	fmt.Fprintf(&b, "\nartifact=%s", artifactID)
	return b.String()
}

// ---------------------------------------------------------------------------
// RunTestSummarizer
// ---------------------------------------------------------------------------

type RunTestSummarizer struct {
	store *artifact.Store
}

func (s *RunTestSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	stdout := readStdout(s.store, result.ArtifactID)
	summary, risk := runTestSummary(result, stdout, budget)
	return observation("run_test", summary, "test output stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func runTestSummary(result core.ToolResult, stdout string, budget tools.BudgetLevel) (string, string) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
	}
	if result.ExitCode != 0 {
		if risk == "no new risk" {
			risk = "non-zero exit code " + strconv.Itoa(result.ExitCode)
		}
	}

	passes, fails, skips := parseTestResults(stdout)

	switch budget {
	case tools.BudgetCritical:
		return fmt.Sprintf("run_test pass=%d fail=%d skip=%d exit=%d; artifact=%s",
			len(passes), len(fails), len(skips), result.ExitCode, result.ArtifactID), risk
	case tools.BudgetWarning:
		return formatTestResults(passes, fails, skips, 10, result), risk
	default: // BudgetSafe
		return formatTestResults(passes, fails, skips, 0, result), risk
	}
}

func parseTestResults(stdout string) (passes, fails, skips []string) {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "--- PASS:"):
			passes = append(passes, line)
		case strings.HasPrefix(line, "--- FAIL:"):
			fails = append(fails, line)
		case strings.HasPrefix(line, "--- SKIP:"):
			skips = append(skips, line)
		}
	}
	return
}

func formatTestResults(passes, fails, skips []string, failLimit int, result core.ToolResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run_test pass=%d fail=%d skip=%d exit=%d",
		len(passes), len(fails), len(skips), result.ExitCode)

	if len(fails) > 0 {
		limit := failLimit
		if limit <= 0 || limit > len(fails) {
			limit = len(fails)
		}
		fmt.Fprintf(&b, "\n  failures:")
		for i := 0; i < limit; i++ {
			fmt.Fprintf(&b, "\n    %s", fails[i])
		}
		if failLimit > 0 && len(fails) > failLimit {
			fmt.Fprintf(&b, "\n    ... +%d more failures", len(fails)-failLimit)
		}
	}

	// Always show pass/skip counts but not individual items (they're not interesting).
	if len(skips) > 0 {
		fmt.Fprintf(&b, "\n  skipped=%d", len(skips))
	}
	fmt.Fprintf(&b, "\nartifact=%s", result.ArtifactID)
	return b.String()
}

// ---------------------------------------------------------------------------
// WriteFileSummarizer
// ---------------------------------------------------------------------------

type WriteFileSummarizer struct{}

func (s *WriteFileSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	// NEVER include file content, regardless of budget.
	return observation("write_file", result.Content, "file content stored as artifact "+result.ArtifactID, "no new risk", core.TierArtifact, result.ArtifactID)
}

// ---------------------------------------------------------------------------
// ApplyPatchSummarizer
// ---------------------------------------------------------------------------

type ApplyPatchSummarizer struct {
	store *artifact.Store
}

func (s *ApplyPatchSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	patch := readPatch(s.store, result.ArtifactID)
	summary, risk := applyPatchSummary(result, patch, budget)
	return observation("apply_patch", summary, "patch applied; output stored as artifact "+result.ArtifactID, risk, core.TierArtifact, result.ArtifactID)
}

func applyPatchSummary(result core.ToolResult, patch string, budget tools.BudgetLevel) (string, string) {
	risk := "no new risk"
	if result.Error != "" {
		risk = result.Error
		return "apply_patch error=" + result.Error, risk
	}

	patchFiles := patchFileNames(patch)
	patchHunkCount := patchHunkCount(patch)

	switch budget {
	case tools.BudgetCritical:
		return fmt.Sprintf("apply_patch %d files, %d hunks; artifact=%s",
			len(patchFiles), patchHunkCount, result.ArtifactID), risk
	case tools.BudgetWarning:
		var names []string
		for _, f := range patchFiles {
			names = append(names, filepath.Base(f))
		}
		return fmt.Sprintf("apply_patch %d files: %s; artifact=%s",
			len(patchFiles), strings.Join(names, ", "), result.ArtifactID), risk
	default: // BudgetSafe
		var names []string
		for _, f := range patchFiles {
			names = append(names, filepath.Base(f))
		}
		return fmt.Sprintf("apply_patch %d files (%d hunks): %s; artifact=%s",
			len(patchFiles), patchHunkCount, strings.Join(names, ", "), result.ArtifactID), risk
	}
}

func patchFileNames(patch string) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				name := strings.TrimPrefix(parts[2], "b/")
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func patchHunkCount(patch string) int {
	count := 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// ArtifactReadSummarizer
// ---------------------------------------------------------------------------

type ArtifactReadSummarizer struct {
	store *artifact.Store
}

func (s *ArtifactReadSummarizer) Summarize(result core.ToolResult, budget tools.BudgetLevel) core.Observation {
	summary := artifactReadSummary(result, budget)

	placement := core.TierNear
	state := "small artifact payloads summarized"
	if strings.Contains(result.Content, "large_files=") {
		placement = core.TierArtifact
		state = "large artifact payloads left in artifact storage"
	}

	return observation("artifact_read", summary, state+" for artifact "+result.ArtifactID, "no new risk", placement, result.ArtifactID)
}

func artifactReadSummary(result core.ToolResult, budget tools.BudgetLevel) string {
	if result.Error != "" {
		return "artifact_read error=" + result.Error
	}

	content := result.Content
	maxChars := 2000
	switch budget {
	case tools.BudgetCritical:
		maxChars = 200
	case tools.BudgetWarning:
		maxChars = 500
	default:
		maxChars = 1000
	}

	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "..."
}
