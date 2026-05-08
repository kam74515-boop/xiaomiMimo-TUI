package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMCPConfigDefault(t *testing.T) {
	cfg := DefaultMCPConfig()
	if len(cfg.Servers) != 0 {
		t.Fatalf("default config should have 0 servers, got %d", len(cfg.Servers))
	}
	if enabled := cfg.EnabledServers(); len(enabled) != 0 {
		t.Fatalf("default config should have 0 enabled servers, got %d", len(enabled))
	}
}

func TestMCPConfigLoad(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "mcp.toml")

	content := `
[[servers]]
name = "github"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
enabled = true

[[servers]]
name = "filesystem"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
enabled = false
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadMCPConfigFromPath(configPath)
	if err != nil {
		t.Fatalf("LoadMCPConfigFromPath: %v", err)
	}

	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}

	s0 := cfg.Servers[0]
	if s0.Name != "github" {
		t.Fatalf("server[0].Name = %q, want github", s0.Name)
	}
	if s0.Command != "npx" {
		t.Fatalf("server[0].Command = %q, want npx", s0.Command)
	}
	if len(s0.Args) != 2 {
		t.Fatalf("server[0].Args length = %d, want 2", len(s0.Args))
	}
	if !s0.Enabled {
		t.Fatal("server[0].Enabled should be true")
	}

	s1 := cfg.Servers[1]
	if s1.Name != "filesystem" {
		t.Fatalf("server[1].Name = %q, want filesystem", s1.Name)
	}
	if s1.Enabled {
		t.Fatal("server[1].Enabled should be false")
	}

	enabled := cfg.EnabledServers()
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled server, got %d", len(enabled))
	}
	if enabled[0].Name != "github" {
		t.Fatalf("enabled server name = %q, want github", enabled[0].Name)
	}
}

func TestMCPConfigLoadFileNotFound(t *testing.T) {
	cfg, err := LoadMCPConfigFromPath("/nonexistent/mcp.toml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("expected 0 servers for missing file, got %d", len(cfg.Servers))
	}
}

func TestMCPConfigLoadInvalidToml(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "mcp.toml")
	if err := os.WriteFile(configPath, []byte("{{{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadMCPConfigFromPath(configPath)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestMCPConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPConfig
		wantErr bool
	}{
		{
			name:    "empty config is valid",
			cfg:     MCPConfig{},
			wantErr: false,
		},
		{
			name: "valid server",
			cfg: MCPConfig{
				Servers: []MCPServer{
					{Name: "test", Command: "npx", Enabled: true},
				},
			},
			wantErr: false,
		},
		{
			name: "missing name",
			cfg: MCPConfig{
				Servers: []MCPServer{
					{Command: "npx"},
				},
			},
			wantErr: true,
		},
		{
			name: "missing command",
			cfg: MCPConfig{
				Servers: []MCPServer{
					{Name: "test"},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate name",
			cfg: MCPConfig{
				Servers: []MCPServer{
					{Name: "test", Command: "npx"},
					{Name: "test", Command: "node"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
