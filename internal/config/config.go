package config

import (
	"os"
	"path/filepath"
	"strings"

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
	baseURL := envOrDefault("MIMO_BASE_URL", "https://api.xiaomimimo.com/v1")
	model := envOrDefault("MIMO_MODEL", "mimo-v2.5-pro[1m]")
	return Config{
		Provider: ProviderConfig{
			BaseURL: baseURL,
			APIKey:  os.Getenv("MIMO_API_KEY"),
			Model:   model,
			Mock:    envBool("MIMO_MOCK") || os.Getenv("MIMO_API_KEY") == "",
		},
		Runtime: RuntimeConfig{
			Workspace:     ".",
			ContextWindow: 1_000_000,
		},
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
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
	applyEnvOverrides(&cfg)
	if cfg.Provider.APIKey == "" {
		cfg.Provider.APIKey = os.Getenv("MIMO_API_KEY")
	}
	if cfg.Provider.APIKey == "" {
		cfg.Provider.Mock = true
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if value := os.Getenv("MIMO_BASE_URL"); value != "" {
		cfg.Provider.BaseURL = value
	}
	if value := os.Getenv("MIMO_MODEL"); value != "" {
		cfg.Provider.Model = value
	}
	if value := os.Getenv("MIMO_API_KEY"); value != "" {
		cfg.Provider.APIKey = value
	}
	if envBool("MIMO_MOCK") {
		cfg.Provider.Mock = true
	}
}

func candidatePaths() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".mimo-tui", "config.toml"),
		filepath.Join(".mimo", "config.toml"),
	}
}
