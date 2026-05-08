package model

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// persistedConfig is the on-disk TOML structure for a model registry.
type persistedConfig struct {
	Version int              `toml:"version"`
	Default string           `toml:"default"`
	Models  []persistedModel `toml:"models"`
}

// persistedModel is the TOML-friendly representation of a single model.
type persistedModel struct {
	ID           string `toml:"id"`
	BaseURL      string `toml:"base_url"`
	Channel      string `toml:"channel"`
	Description  string `toml:"description"`
	ContextLimit int    `toml:"context_limit"`
	Accepted     bool   `toml:"accepted"`
}

const currentVersion = 1

// SaveToFile serializes the registry to a TOML file at path.
// Parent directories are created if they do not exist.
func (r *Registry) SaveToFile(path string) error {
	cfg := persistedConfig{
		Version: currentVersion,
		Default: r.DefaultID(),
	}
	for _, info := range r.ListAll() {
		cfg.Models = append(cfg.Models, persistedModel{
			ID:           info.ID,
			BaseURL:      info.BaseURL,
			Channel:      string(info.Channel),
			Description:  info.Description,
			ContextLimit: info.ContextLimit,
			Accepted:     info.Accepted,
		})
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal TOML: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// LoadFromFile reads a TOML file at path and returns a populated Registry.
// If the file does not exist, returns (nil, nil).
// If the file exists but contains invalid TOML, returns an error.
func LoadFromFile(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read file: %w", err)
	}

	var cfg persistedConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}

	r := NewRegistry()
	for _, m := range cfg.Models {
		if m.ID == "" {
			continue // skip entries with no ID
		}
		r.Register(Info{
			ID:           m.ID,
			BaseURL:      m.BaseURL,
			Channel:      Channel(m.Channel),
			Description:  m.Description,
			ContextLimit: m.ContextLimit,
			Accepted:     m.Accepted,
		})
	}

	if cfg.Default != "" {
		if err := r.SetDefault(cfg.Default); err != nil {
			return nil, fmt.Errorf("default model %q: %w", cfg.Default, err)
		}
	}

	return r, nil
}
