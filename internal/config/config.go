package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Provider ProviderConfig `toml:"provider"`
	Runtime  RuntimeConfig  `toml:"runtime"`
}

type ProviderConfig struct {
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
	Mock    bool   `toml:"mock"`
}

type RuntimeConfig struct {
	Workspace     string `toml:"workspace"`
	ContextWindow int    `toml:"context_window"`
}

func Default() Config {
	return Config{
		Provider: ProviderConfig{
			BaseURL: "https://api.xiaomimimo.com/v1",
			APIKey:  os.Getenv("MIMO_API_KEY"),
			Model:   "mimo-v2.5-pro[1m]",
			Mock:    os.Getenv("MIMO_API_KEY") == "",
		},
		Runtime: RuntimeConfig{
			Workspace:     ".",
			ContextWindow: 1_000_000,
		},
	}
}

func Load() (Config, error) {
	cfg := Default()
	for _, path := range candidatePaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	}
	if cfg.Provider.APIKey == "" {
		cfg.Provider.APIKey = os.Getenv("MIMO_API_KEY")
	}
	if cfg.Provider.APIKey == "" {
		cfg.Provider.Mock = true
	}
	return cfg, nil
}

func candidatePaths() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".mimo-tui", "config.toml"),
		filepath.Join(".mimo", "config.toml"),
	}
}
