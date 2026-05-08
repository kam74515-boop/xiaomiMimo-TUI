package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.toml")

	original := DefaultRegistry()
	if err := original.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadFromFile returned nil registry for existing file")
	}

	if loaded.DefaultID() != original.DefaultID() {
		t.Errorf("default ID: got %q, want %q", loaded.DefaultID(), original.DefaultID())
	}
	if loaded.Len() != original.Len() {
		t.Errorf("model count: got %d, want %d", loaded.Len(), original.Len())
	}

	for _, orig := range original.ListAll() {
		got, ok := loaded.Get(orig.ID)
		if !ok {
			t.Errorf("missing model %q after round-trip", orig.ID)
			continue
		}
		if got.BaseURL != orig.BaseURL {
			t.Errorf("model %q BaseURL: got %q, want %q", orig.ID, got.BaseURL, orig.BaseURL)
		}
		if got.Channel != orig.Channel {
			t.Errorf("model %q Channel: got %q, want %q", orig.ID, got.Channel, orig.Channel)
		}
		if got.ContextLimit != orig.ContextLimit {
			t.Errorf("model %q ContextLimit: got %d, want %d", orig.ID, got.ContextLimit, orig.ContextLimit)
		}
		if got.Accepted != orig.Accepted {
			t.Errorf("model %q Accepted: got %v, want %v", orig.ID, got.Accepted, orig.Accepted)
		}
		if got.Description != orig.Description {
			t.Errorf("model %q Description: got %q, want %q", orig.ID, got.Description, orig.Description)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	loaded, err := LoadFromFile("/nonexistent/path/models.toml")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if loaded != nil {
		t.Fatal("expected nil registry for missing file")
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("this is not valid toml {{{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
	if loaded != nil {
		t.Fatal("expected nil registry for invalid TOML")
	}
}

func TestConfigPrecedence(t *testing.T) {
	dir := t.TempDir()

	// Simulate global config overriding the default base URL.
	globalDir := filepath.Join(dir, "global")
	globalPath := filepath.Join(globalDir, "models.toml")
	global := NewRegistry()
	global.Register(Info{
		ID:           "mimo-v2.5-pro",
		BaseURL:      "https://global.example.com/v1",
		Channel:      ChannelDefault,
		ContextLimit: 500_000,
		Description:  "overridden by global",
		Accepted:     true,
	})
	global.Register(Info{
		ID:           "custom-global-model",
		BaseURL:      "https://global.example.com/v1",
		Channel:      ChannelCandidate,
		ContextLimit: 100_000,
		Description:  "only in global",
		Accepted:     false,
	})
	_ = global.SetDefault("mimo-v2.5-pro")
	if err := global.SaveToFile(globalPath); err != nil {
		t.Fatalf("save global: %v", err)
	}

	// Simulate project config overriding the default again.
	projectDir := filepath.Join(dir, "project", ".mimo")
	projectPath := filepath.Join(projectDir, "models.toml")
	project := NewRegistry()
	project.Register(Info{
		ID:           "mimo-v2.5-pro",
		BaseURL:      "https://project.example.com/v1",
		Channel:      ChannelDefault,
		ContextLimit: 200_000,
		Description:  "overridden by project",
		Accepted:     true,
	})
	_ = project.SetDefault("mimo-v2.5-pro")
	if err := project.SaveToFile(projectPath); err != nil {
		t.Fatalf("save project: %v", err)
	}

	// Merge: start with defaults, layer global, then project.
	r := DefaultRegistry()

	gOverride, err := LoadFromFile(globalPath)
	if err != nil {
		t.Fatalf("load global: %v", err)
	}
	mergeTestRegistry(r, gOverride)

	pOverride, err := LoadFromFile(projectPath)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	mergeTestRegistry(r, pOverride)

	// Project should win on mimo-v2.5-pro.
	info, ok := r.Get("mimo-v2.5-pro")
	if !ok {
		t.Fatal("mimo-v2.5-pro not found after merge")
	}
	if info.BaseURL != "https://project.example.com/v1" {
		t.Errorf("expected project base URL, got %q", info.BaseURL)
	}
	if info.ContextLimit != 200_000 {
		t.Errorf("expected project context limit 200000, got %d", info.ContextLimit)
	}
	if info.Description != "overridden by project" {
		t.Errorf("expected project description, got %q", info.Description)
	}

	// Global-only model should still be present.
	if _, ok := r.Get("custom-global-model"); !ok {
		t.Error("custom-global-model should survive merge")
	}

	// Default models from DefaultRegistry should still be present.
	if _, ok := r.Get("mimo-v2.5-flash"); !ok {
		t.Error("mimo-v2.5-flash should survive merge")
	}
}

func TestAcceptCandidatePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.toml")

	r := DefaultRegistry()
	if err := r.AcceptCandidate("mimo-v2.5-flash"); err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	flash, ok := loaded.Get("mimo-v2.5-flash")
	if !ok {
		t.Fatal("mimo-v2.5-flash not found after reload")
	}
	if !flash.Accepted {
		t.Error("expected Accepted=true after AcceptCandidate + reload")
	}
	if flash.Channel != ChannelDefault {
		t.Errorf("expected channel=%q after accept, got %q", ChannelDefault, flash.Channel)
	}
	if loaded.DefaultID() != "mimo-v2.5-flash" {
		t.Errorf("expected default=mimo-v2.5-flash after accept, got %q", loaded.DefaultID())
	}
}

// mergeTestRegistry is a test helper that merges src into dst.
func mergeTestRegistry(dst, src *Registry) {
	for _, info := range src.ListAll() {
		dst.Register(info)
	}
	if def := src.Default(); def.ID != "" {
		_ = dst.SetDefault(def.ID)
	}
}
