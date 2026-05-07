package tools

import (
	"fmt"
	"sort"

	"mimo-tui/internal/artifact"
	"mimo-tui/internal/core"
)

type Registry struct {
	tools map[string]core.Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]core.Tool{}}
}

func NewDefaultRegistry(workspace string) *Registry {
	store := artifact.NewStore(workspace)
	registry := NewRegistry()
	for _, tool := range Builtins(workspace, store) {
		_ = registry.Register(tool)
	}
	return registry
}

func Builtins(workspace string, store *artifact.Store) []core.Tool {
	return []core.Tool{
		NewShellTool(workspace, store),
		NewRGTool(workspace, store),
		NewReadFileTool(workspace, store),
		NewWriteFileTool(workspace, store),
		NewApplyPatchTool(workspace, store),
		NewGitStatusTool(workspace, store),
		NewRunTestTool(workspace, store),
	}
}

func (r *Registry) Register(tool core.Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	r.tools[name] = tool
	return nil
}

func (r *Registry) Get(name string) (core.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Tools() []core.Tool {
	names := r.Names()
	out := make([]core.Tool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}
