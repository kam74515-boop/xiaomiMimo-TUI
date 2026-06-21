// Package skill implements MiMo-TUI's governed skill playbooks (a clean-room
// take on MiMo-Code's Compose/skills).
//
// A skill is a named Markdown playbook with simple frontmatter:
//
//	---
//	name: tdd
//	description: Test-driven development loop
//	triggers: test, tdd, coverage
//	---
//	<playbook body>
//
// Skills are deliberately plain Markdown so they are reviewable and
// version-controllable. Built-in skills ship with the binary; project skills
// live in <workspace>/.mimo/skills/*.md and override built-ins by name.
//
// Activation is explicit and visible: the runtime injects an active skill's
// body into the system prompt and surfaces it in the activity dashboard, so
// delegation never becomes a black box.
package skill

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed builtins/*.md
var builtinFS embed.FS

// Source identifies where a skill came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceProject Source = "project"
)

// Skill is a named playbook.
type Skill struct {
	Name        string
	Description string
	Triggers    []string
	Body        string
	Source      Source
	Path        string // set for project skills
}

// Parse reads a skill from raw Markdown. When the content has no frontmatter,
// the name falls back to fallbackName and the whole content becomes the body.
func Parse(data []byte, source Source, path, fallbackName string) (Skill, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	sk := Skill{Name: fallbackName, Source: source, Path: path}

	trimmed := strings.TrimLeft(text, "\n")
	if trimmed == "---" || strings.HasPrefix(trimmed, "---\n") {
		lines := strings.Split(trimmed, "\n") // lines[0] is the opening fence
		closeIdx := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				closeIdx = i
				break
			}
		}
		if closeIdx >= 0 {
			parseFrontmatter(strings.Join(lines[1:closeIdx], "\n"), &sk)
			sk.Body = strings.TrimSpace(strings.Join(lines[closeIdx+1:], "\n"))
		} else {
			// Malformed (opening fence, no proper closing fence): drop the fence
			// line so it is not injected, and treat the remainder as the body.
			sk.Body = strings.TrimSpace(strings.Join(lines[1:], "\n"))
		}
	} else {
		sk.Body = strings.TrimSpace(text)
	}

	if strings.TrimSpace(sk.Name) == "" {
		sk.Name = fallbackName
	}
	if strings.TrimSpace(sk.Name) == "" {
		return sk, fmt.Errorf("skill: missing name")
	}
	return sk, nil
}

func parseFrontmatter(front string, sk *Skill) {
	for _, line := range strings.Split(front, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "name":
			sk.Name = val
		case "description":
			sk.Description = val
		case "triggers":
			for _, t := range strings.Split(val, ",") {
				if t = strings.TrimSpace(t); t != "" {
					sk.Triggers = append(sk.Triggers, t)
				}
			}
		}
	}
}

// Store discovers and reads skills for a workspace.
type Store struct {
	workspace string
}

// NewStore returns a skill store rooted at workspace ("" defaults to ".").
func NewStore(workspace string) *Store {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return &Store{workspace: workspace}
}

// ProjectDir is where project skills live.
func (s *Store) ProjectDir() string {
	return filepath.Join(s.workspace, ".mimo", "skills")
}

// List returns all skills sorted by name. Project skills override built-ins
// with the same name.
func (s *Store) List() []Skill {
	// Key case-insensitively (consistent with Get) so a project skill overrides
	// a builtin even when the names differ only by case, rather than duplicating.
	byName := map[string]Skill{}
	for _, sk := range builtinSkills() {
		byName[strings.ToLower(strings.TrimSpace(sk.Name))] = sk
	}
	for _, sk := range s.projectSkills() {
		byName[strings.ToLower(strings.TrimSpace(sk.Name))] = sk // project overrides builtin
	}
	out := make([]Skill, 0, len(byName))
	for _, sk := range byName {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a skill by name (case-insensitive).
func (s *Store) Get(name string) (Skill, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, sk := range s.List() {
		if strings.ToLower(sk.Name) == name {
			return sk, true
		}
	}
	return Skill{}, false
}

func (s *Store) projectSkills() []Skill {
	entries, err := os.ReadDir(s.ProjectDir())
	if err != nil {
		return nil
	}
	var skills []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(s.ProjectDir(), e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fallback := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if sk, err := Parse(data, SourceProject, path, fallback); err == nil {
			skills = append(skills, sk)
		}
	}
	return skills
}

func builtinSkills() []Skill {
	entries, err := builtinFS.ReadDir("builtins")
	if err != nil {
		return nil
	}
	var skills []Skill
	for _, e := range entries {
		data, err := builtinFS.ReadFile("builtins/" + e.Name())
		if err != nil {
			continue
		}
		fallback := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if sk, err := Parse(data, SourceBuiltin, "", fallback); err == nil {
			skills = append(skills, sk)
		}
	}
	return skills
}

// Combine renders the active skills into a single block for system-prompt
// injection. Returns "" when there are no skills.
func Combine(skills []Skill) string {
	var blocks []string
	for _, sk := range skills {
		header := "## Skill: " + sk.Name
		if sk.Description != "" {
			header += " — " + sk.Description
		}
		blocks = append(blocks, header+"\n"+strings.TrimSpace(sk.Body))
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}
