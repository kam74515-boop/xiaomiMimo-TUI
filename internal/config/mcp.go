package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// MCPConfig holds the configuration for MCP (Model Context Protocol) servers.
type MCPConfig struct {
	Servers []MCPServer `toml:"servers"`
}

// MCPServer describes a single MCP server that can expose tools to the agent.
type MCPServer struct {
	Name    string   `toml:"name"`
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
	Env     []string `toml:"env"`
	Enabled bool     `toml:"enabled"`
}

// DefaultMCPConfig returns an empty MCPConfig with no servers configured.
func DefaultMCPConfig() MCPConfig {
	return MCPConfig{}
}

// LoadMCPConfig reads the MCP configuration from the first candidate path that
// exists. Search order:
//  1. .mimo/mcp.toml (project-local)
//  2. ~/.mimo-tui/mcp.toml (user-global)
//
// If neither file exists, DefaultMCPConfig is returned with no error.
func LoadMCPConfig() (MCPConfig, error) {
	cfg := DefaultMCPConfig()
	paths := mcpCandidatePaths()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("mcp config: %s: %w", path, err)
		}
		return cfg, nil
	}
	return cfg, nil
}

// LoadMCPConfigFromPath reads an MCP config from an explicit file path.
// Returns the default config if the file does not exist.
func LoadMCPConfigFromPath(path string) (MCPConfig, error) {
	cfg := DefaultMCPConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("mcp config: %s: %w", path, err)
	}
	return cfg, nil
}

// EnabledServers returns only the servers that have Enabled=true.
func (c MCPConfig) EnabledServers() []MCPServer {
	var out []MCPServer
	for _, s := range c.Servers {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// Validate checks the MCPConfig for common errors.
func (c MCPConfig) Validate() error {
	seen := make(map[string]bool)
	for _, s := range c.Servers {
		if s.Name == "" {
			return fmt.Errorf("mcp server: name is required")
		}
		if s.Command == "" {
			return fmt.Errorf("mcp server %q: command is required", s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("mcp server %q: duplicate name", s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}

func mcpCandidatePaths() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(".mimo", "mcp.toml"),
		filepath.Join(home, ".mimo-tui", "mcp.toml"),
	}
}
