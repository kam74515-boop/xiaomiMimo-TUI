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

// NewDefaultRegistry creates a Registry pre-populated with all built-in tools.
// An optional factory function may be passed to create budget-aware summarizers.
// The factory receives the artifact store so summarizers can read raw payloads.
// If no factory is provided, each tool falls back to a basic summarizer that
// echoes the raw result content.
func NewDefaultRegistry(workspace string, factory ...func(*artifact.Store) map[string]Summarizer) *Registry {
	store := artifact.NewStore(workspace)
	return NewDefaultRegistryWithStore(workspace, store, factory...)
}

// NewDefaultRegistryWithStore creates a Registry using a caller-provided
// artifact store. Use this when other runtime components, such as the tool
// executor, must write artifacts into the same store.
func NewDefaultRegistryWithStore(workspace string, store *artifact.Store, factory ...func(*artifact.Store) map[string]Summarizer) *Registry {
	if store == nil {
		store = artifact.NewStore(workspace)
	}
	var summarizerMap map[string]Summarizer
	if len(factory) > 0 && factory[0] != nil {
		summarizerMap = factory[0](store)
	}
	registry := NewRegistry()
	for _, tool := range Builtins(workspace, store, summarizerMap) {
		_ = registry.Register(tool)
	}
	return registry
}

// Builtins returns a slice of all built-in tools. An optional summarizer map
// wires each tool to a budget-aware summarizer.
func Builtins(workspace string, store *artifact.Store, summarizers map[string]Summarizer) []core.Tool {
	s := func(name string) Summarizer {
		if summarizers == nil {
			return nil
		}
		return summarizers[name]
	}
	return []core.Tool{
		NewShellTool(workspace, store, s("shell")),
		NewRGTool(workspace, store, s("rg")),
		NewReadFileTool(workspace, store, s("read_file")),
		NewListDirTool(workspace, store, s("list_dir")),
		NewGitDiffTool(workspace, store, s("git_diff")),
		NewGitLogTool(workspace, store, s("git_log")),
		NewArtifactReadTool(workspace, store, s("artifact_read")),
		NewWriteFileTool(workspace, store, s("write_file")),
		NewApplyPatchTool(workspace, store, s("apply_patch")),
		NewGitStatusTool(workspace, store, s("git_status")),
		NewRunTestTool(workspace, store, s("run_test")),
		NewDiagTool(workspace, store, s("diagnostics")),
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

func (r *Registry) ToolSpecs() []core.ToolSpec {
	names := r.Names()
	specs := make([]core.ToolSpec, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		schema := tool.Schema()
		description, _ := schema["description"].(string)
		specs = append(specs, core.ToolSpec{
			Type: "function",
			Function: core.ToolFunctionSpec{
				Name:        tool.Name(),
				Description: description,
				Parameters:  schema,
			},
		})
	}
	return specs
}
