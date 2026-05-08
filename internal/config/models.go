package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"mimo-tui/internal/model"
)

// ModelsConfig mirrors the models.toml structure persisted to disk.
type ModelsConfig struct {
	Version int          `toml:"version"`
	Default string       `toml:"default"`
	Models  []ModelEntry `toml:"models"`
}

// ModelEntry is the TOML-friendly representation of a single model.
type ModelEntry struct {
	ID           string `toml:"id"`
	BaseURL      string `toml:"base_url"`
	Channel      string `toml:"channel"`
	Description  string `toml:"description"`
	ContextLimit int    `toml:"context_limit"`
	Accepted     bool   `toml:"accepted"`
}

const modelsConfigVersion = 1

// modelsCandidatePaths returns the ordered list of models.toml paths to check.
// Global (~/.mimo-tui/models.toml) comes first; project (.mimo/models.toml) second
// and wins on conflict.
func modelsCandidatePaths() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".mimo-tui", "models.toml"),
		filepath.Join(".mimo", "models.toml"),
	}
}

// LoadModelsConfig builds a Registry by layering persisted models.toml files
// on top of the built-in defaults.
//
// Precedence (highest last):
//  1. DefaultRegistry (built-in defaults)
//  2. ~/.mimo-tui/models.toml (global overrides)
//  3. .mimo/models.toml (project overrides, wins on conflict)
//
// Returns the merged registry, or an error if a file exists but is invalid.
func LoadModelsConfig() (*model.Registry, error) {
	r := model.DefaultRegistry()

	for _, path := range modelsCandidatePaths() {
		override, err := model.LoadFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("models config: %s: %w", path, err)
		}
		if override == nil {
			continue // file does not exist; skip
		}
		mergeRegistry(r, override)
	}

	return r, nil
}

// SaveModelsConfig persists the given registry to .mimo/models.toml in the
// current working directory (project-level).
func SaveModelsConfig(r *model.Registry) error {
	return r.SaveToFile(filepath.Join(".mimo", "models.toml"))
}

// mergeRegistry applies all entries from src into dst. For each model in src:
//   - If dst already has that ID, overwrite it entirely.
//   - If dst does not have it, register it.
//   - If src specifies a default, promote it in dst.
func mergeRegistry(dst, src *model.Registry) {
	for _, info := range src.ListAll() {
		dst.Register(info)
	}
	if def := src.Default(); def.ID != "" {
		_ = dst.SetDefault(def.ID)
	}
}

// RegistryToConfig converts a Registry into a ModelsConfig for serialization.
func RegistryToConfig(r *model.Registry) ModelsConfig {
	cfg := ModelsConfig{Version: modelsConfigVersion}
	cfg.Default = r.Default().ID
	for _, info := range r.ListAll() {
		cfg.Models = append(cfg.Models, ModelEntry{
			ID:           info.ID,
			BaseURL:      info.BaseURL,
			Channel:      string(info.Channel),
			Description:  info.Description,
			ContextLimit: info.ContextLimit,
			Accepted:     info.Accepted,
		})
	}
	return cfg
}

// ConfigToRegistry converts a ModelsConfig back into a Registry.
func ConfigToRegistry(cfg ModelsConfig) *model.Registry {
	r := model.NewRegistry()
	for _, entry := range cfg.Models {
		r.Register(model.Info{
			ID:           entry.ID,
			BaseURL:      entry.BaseURL,
			Channel:      model.Channel(entry.Channel),
			Description:  entry.Description,
			ContextLimit: entry.ContextLimit,
			Accepted:     entry.Accepted,
		})
	}
	if cfg.Default != "" {
		_ = r.SetDefault(cfg.Default)
	}
	return r
}

// ReadModelsFile reads and parses a models.toml file, returning the config.
// Returns nil config if the file does not exist.
func ReadModelsFile(path string) (*ModelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg ModelsConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = modelsConfigVersion
	}
	return &cfg, nil
}

// WriteModelsFile serializes a ModelsConfig to a TOML file, creating
// parent directories as needed.
func WriteModelsFile(path string, cfg ModelsConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
