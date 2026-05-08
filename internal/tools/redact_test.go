package tools

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

// TestRedactSecrets verifies the core redaction function catches common secret patterns.
func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "api_key with equals",
			input: `api_key=sk_live_abc123xyz`,
			want:  `api_key=***REDACTED***`,
		},
		{
			name:  "API-SECRET with colon",
			input: `API-SECRET: supersecretvalue123`,
			want:  `API-SECRET:***REDACTED***`,
		},
		{
			name:  "access_token with equals",
			input: `access_token=ghp_abcdef123456`,
			want:  `access_token=***REDACTED***`,
		},
		{
			name:  "Bearer token",
			input: `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature`,
			want:  `Authorization: Bearer ***REDACTED***`,
		},
		{
			name:  "private_key with colon",
			input: `private_key: -----BEGIN RSA PRIVATE KEY-----`,
			want:  `private_key:***REDACTED*** RSA PRIVATE KEY-----`,
		},
		{
			name:  "no secrets",
			input: `just a normal line of text`,
			want:  `just a normal line of text`,
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "secret_key in multiline",
			input: "line1\nsecret_key = mysecretvalue\nline3",
			want:  "line1\nsecret_key =***REDACTED***\nline3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(redactSecrets([]byte(tc.input)))
			if got != tc.want {
				t.Fatalf("redactSecrets(%q)\n  got:  %q\n  want: %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestReadFileRedactsSecrets verifies that read_file redacts secrets from file content
// before storing it in an artifact.
func TestReadFileRedactsSecrets(t *testing.T) {
	workspace := t.TempDir()
	secretContent := "config:\n  api_key=sk_live_abc123xyz\n  normal: value\n"
	writeFile(t, filepath.Join(workspace, "config.yaml"), secretContent)

	store := newTestStore(t, workspace)
	tool := NewReadFileTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{"path": "config.yaml"})
	if result.ExitCode != 0 {
		t.Fatalf("read_file failed: %+v", result)
	}
	if result.ArtifactID == "" {
		t.Fatal("read_file did not produce an artifact")
	}

	payloads := readTestPayloads(t, store, result.ArtifactID)
	found := false
	for _, p := range payloads {
		text := string(p.Data)
		if strings.Contains(text, "sk_live_abc123xyz") {
			t.Fatalf("read_file artifact contains unredacted secret: %s", text)
		}
		if strings.Contains(text, "***REDACTED***") {
			found = true
		}
	}
	if !found {
		t.Fatal("read_file artifact should contain ***REDACTED*** marker")
	}
}

// TestRgRedactsSecrets verifies that rg redacts secrets from search results
// before storing them in an artifact.
func TestRgRedactsSecrets(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available, skipping")
	}

	workspace := t.TempDir()
	secretContent := "api_token=ghp_abcdef123456\nnormal line\n"
	writeFile(t, filepath.Join(workspace, "secrets.txt"), secretContent)

	store := newTestStore(t, workspace)
	tool := NewRGTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{"pattern": "ghp_abcdef"})
	if result.ExitCode != 0 {
		if result.ExitCode > 1 {
			t.Fatalf("rg failed: %+v", result)
		}
	}
	if result.ArtifactID == "" {
		t.Fatal("rg did not produce an artifact")
	}

	payloads := readTestPayloads(t, store, result.ArtifactID)
	found := false
	for _, p := range payloads {
		text := string(p.Data)
		if strings.Contains(text, "ghp_abcdef123456") {
			t.Fatalf("rg artifact contains unredacted secret in %s: %s", p.Metadata.Name, text)
		}
		if strings.Contains(text, "***REDACTED***") {
			found = true
		}
	}
	if !found {
		t.Fatal("rg artifact should contain ***REDACTED*** marker")
	}
}

// TestGitDiffRedactsSecrets verifies that git_diff redacts secrets from diff output
// before storing them in an artifact.
func TestGitDiffRedactsSecrets(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)

	writeFile(t, filepath.Join(workspace, "config.go"), "package main\n\nconst apiKey = \"safe\"\n")
	run(t, workspace, "git", "add", ".")
	run(t, workspace, "git", "commit", "-m", "initial")

	// Add a line with a secret token pattern to generate a diff.
	writeFile(t, filepath.Join(workspace, "config.go"), "package main\n\nconst apiKey = \"safe\"\nconst token=secrettoken123\n")

	store := newTestStore(t, workspace)
	tool := NewGitDiffTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{})
	if result.ExitCode != 0 {
		t.Fatalf("git_diff failed: %+v", result)
	}
	if result.ArtifactID == "" {
		t.Fatal("git_diff did not produce an artifact")
	}

	payloads := readTestPayloads(t, store, result.ArtifactID)
	foundRedacted := false
	for _, p := range payloads {
		text := string(p.Data)
		if strings.Contains(text, "secrettoken123") && !strings.Contains(text, "***REDACTED***") {
			t.Fatalf("git_diff artifact contains unredacted secret in %s: %s", p.Metadata.Name, text)
		}
		if strings.Contains(text, "***REDACTED***") {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Fatal("git_diff artifact should contain ***REDACTED*** marker")
	}
}

// TestGitLogRedactsSecrets verifies that git_log redacts secrets from commit messages
// before storing them in an artifact.
func TestGitLogRedactsSecrets(t *testing.T) {
	workspace := t.TempDir()
	initGitRepo(t, workspace)

	writeFile(t, filepath.Join(workspace, "file.txt"), "hello\n")
	run(t, workspace, "git", "add", ".")
	run(t, workspace, "git", "commit", "-m", "fix: rotate api_key=sk_live_leaked123")

	store := newTestStore(t, workspace)
	tool := NewGitLogTool(workspace, store, nil)

	result := tool.Run(context.Background(), core.ToolInput{"limit": 5})
	if result.ExitCode != 0 {
		t.Fatalf("git_log failed: %+v", result)
	}
	if result.ArtifactID == "" {
		t.Fatal("git_log did not produce an artifact")
	}

	payloads := readTestPayloads(t, store, result.ArtifactID)
	for _, p := range payloads {
		text := string(p.Data)
		if strings.Contains(text, "sk_live_leaked123") {
			t.Fatalf("git_log artifact contains unredacted secret: %s", text)
		}
		if strings.Contains(text, "***REDACTED***") {
			return // success
		}
	}
	t.Fatal("git_log artifact should contain ***REDACTED*** marker")
}

// readTestPayloads reads all payloads from an artifact for test verification.
func readTestPayloads(t *testing.T, store *artifact.Store, id string) []artifact.PayloadRecord {
	t.Helper()
	payloads, err := store.ReadPayloads(id)
	if err != nil {
		t.Fatalf("failed to read artifact payloads: %v", err)
	}
	return payloads
}
