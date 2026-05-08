package artifact

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// RollbackInfo holds lightweight metadata for listing rollback artifacts.
type RollbackInfo struct {
	ID        string
	Tool      string
	CreatedAt time.Time
	ExitCode  int
	DiffSize  int
}

// ListRollbacks returns all rollback artifacts sorted by creation time (newest first).
func ListRollbacks(workspace string) ([]RollbackInfo, error) {
	store := NewStore(workspace)
	entries, err := os.ReadDir(store.Root())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read artifact dir: %w", err)
	}

	var rollbacks []RollbackInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		meta, err := store.ReadMetadata(id)
		if err != nil {
			continue // skip unreadable artifacts
		}
		if meta.Kind != "rollback" {
			continue
		}
		info := RollbackInfo{
			ID:        meta.ID,
			Tool:      meta.Tool,
			CreatedAt: meta.CreatedAt,
			ExitCode:  meta.ExitCode,
		}
		for _, f := range meta.Files {
			if f.Name == "pre_state" {
				info.DiffSize = f.Bytes
				break
			}
		}
		rollbacks = append(rollbacks, info)
	}

	sort.Slice(rollbacks, func(i, j int) bool {
		return rollbacks[i].CreatedAt.After(rollbacks[j].CreatedAt)
	})
	return rollbacks, nil
}

// ShowRollback returns a human-readable summary of what a rollback artifact
// will restore, using "git apply --reverse --stat" on the stored diff.
func ShowRollback(workspace, id string) (string, error) {
	diffData, err := readRollbackDiff(workspace, id)
	if err != nil {
		return "", err
	}
	if len(diffData) == 0 {
		return "(empty diff — nothing to rollback)", nil
	}
	return gitApplyStat(workspace, diffData)
}

// ApplyRollback applies a rollback artifact by reverse-applying the stored git diff.
// When dryRun is true it only prints what would change via "git apply --reverse --stat".
func ApplyRollback(workspace, id string, dryRun bool) (string, error) {
	diffData, err := readRollbackDiff(workspace, id)
	if err != nil {
		return "", err
	}
	if len(diffData) == 0 {
		return "(empty diff — nothing to rollback)", nil
	}

	if dryRun {
		stat, err := gitApplyStat(workspace, diffData)
		if err != nil {
			return "", err
		}
		return "(dry-run, not applied) " + stat, nil
	}

	// Apply the reverse diff.
	cmd := exec.Command("git", "apply", "--reverse")
	cmd.Dir = workspace
	cmd.Stdin = bytes.NewReader(diffData)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git apply --reverse failed: %s", strings.TrimSpace(stderr.String()))
	}
	return "rollback applied successfully", nil
}

// readRollbackDiff returns the pre_state.diff payload from a rollback artifact.
func readRollbackDiff(workspace, id string) ([]byte, error) {
	store := NewStore(workspace)
	meta, err := store.ReadMetadata(id)
	if err != nil {
		return nil, fmt.Errorf("read rollback %q metadata: %w", id, err)
	}
	if meta.Kind != "rollback" {
		return nil, fmt.Errorf("artifact %q is kind %q, not rollback", id, meta.Kind)
	}

	payloads, err := store.ReadPayloads(id)
	if err != nil {
		return nil, fmt.Errorf("read rollback %q payloads: %w", id, err)
	}
	for _, p := range payloads {
		if p.Metadata.Name == "pre_state" {
			return p.Data, nil
		}
	}
	return nil, fmt.Errorf("rollback %q has no pre_state diff", id)
}

// gitApplyStat runs "git apply --reverse --stat" on the given diff data.
func gitApplyStat(workspace string, diffData []byte) (string, error) {
	cmd := exec.Command("git", "apply", "--reverse", "--stat")
	cmd.Dir = workspace
	cmd.Stdin = bytes.NewReader(diffData)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}
	// --stat may exit non-zero but still produce useful output for edge cases.
	if err != nil && output == "" {
		return "", fmt.Errorf("git apply --stat: %s", stderr.String())
	}
	return output, nil
}

// RecordRollbackApply writes an artifact entry documenting that a rollback was applied.
func RecordRollbackApply(workspace, rollbackID string) (string, error) {
	store := NewStore(workspace)
	now := time.Now().UTC().Format(time.RFC3339)
	record, err := store.Write(WriteRequest{
		Tool:     "rollback_apply",
		Kind:     "rollback_apply",
		ExitCode: 0,
		Inputs:   map[string]any{"rollback_id": rollbackID},
		Payloads: []Payload{
			{Name: "applied_at.txt", Data: []byte(now + "\n")},
		},
	})
	if err != nil {
		return "", err
	}
	return record.ID, nil
}
