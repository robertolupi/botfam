package fam

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Scope selects which config file an MCPConfigurator operates on.
type Scope int

const (
	// Project scope is the per-worktree config (claude-code: <worktree>/.mcp.json).
	Project Scope = iota
	// Global scope is the user-level config (claude-code: ~/.claude.json).
	Global
)

func (s Scope) String() string {
	switch s {
	case Project:
		return "project"
	case Global:
		return "global"
	default:
		return fmt.Sprintf("Scope(%d)", int(s))
	}
}

// MCPServerSpec is a harness-agnostic description of a single MCP server entry.
type MCPServerSpec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Scope   Scope
}

// MCPConfigurator edits a harness's MCP server configuration in place.
//
// Every implementation MUST satisfy this contract:
//   - merge-not-overwrite: Set/Remove rewrite only the named server and leave
//     every other server (and every unknown top-level key) byte-preserved;
//   - idempotent: re-Set of an identical spec yields a byte-identical file;
//   - scoped: an operation touches only the file for the requested Scope and
//     only the named server within it;
//   - non-destructive: never delete an unrelated entry, and never clobber the
//     whole file on missing/partial input (the #227 bug).
type MCPConfigurator interface {
	// Harness returns the harness identifier (e.g. "claude-code").
	Harness() string
	// Set adds or updates spec.Name, preserving all other entries.
	Set(spec MCPServerSpec) error
	// Remove deletes name from scope s; it is a no-op if name is absent.
	Remove(name string, s Scope) error
	// Get returns the spec for name in scope s, and whether it was present.
	Get(name string, s Scope) (MCPServerSpec, bool, error)
	// List returns the server names defined in scope s, sorted.
	List(s Scope) ([]string, error)
}

// ClaudeMCPConfigurator is the claude-code MCPConfigurator. It edits the
// per-worktree .mcp.json (Project scope). Global scope (~/.claude.json) is not
// yet implemented and returns an error so callers fail loudly rather than
// silently writing to the wrong place.
type ClaudeMCPConfigurator struct {
	// Worktree is the worktree root whose .mcp.json is edited for Project scope.
	Worktree string
}

// NewClaudeMCPConfigurator returns a claude-code configurator for worktree.
func NewClaudeMCPConfigurator(worktree string) *ClaudeMCPConfigurator {
	return &ClaudeMCPConfigurator{Worktree: worktree}
}

// Harness implements MCPConfigurator.
func (c *ClaudeMCPConfigurator) Harness() string { return "claude-code" }

// projectPath is the .mcp.json path for Project scope.
func (c *ClaudeMCPConfigurator) projectPath() string {
	return filepath.Join(c.Worktree, ".mcp.json")
}

// pathFor resolves the config file for scope s. Only Project is supported.
func (c *ClaudeMCPConfigurator) pathFor(s Scope) (string, error) {
	switch s {
	case Project:
		return c.projectPath(), nil
	case Global:
		return "", fmt.Errorf("claude-code global scope (~/.claude.json) not yet implemented")
	default:
		return "", fmt.Errorf("unknown scope %v", s)
	}
}

// loadRaw reads the config file into a generic map so unknown top-level keys and
// unknown servers are preserved verbatim. A missing file yields an empty map.
func loadRaw(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

// mcpServersOf returns the (possibly newly created) mcpServers sub-map of root.
// It coerces an existing value into map[string]any so we can edit individual
// servers without disturbing siblings.
func mcpServersOf(root map[string]any) map[string]any {
	if existing, ok := root["mcpServers"].(map[string]any); ok {
		return existing
	}
	servers := map[string]any{}
	root["mcpServers"] = servers
	return servers
}

// writeRaw marshals root with claude-code's 2-space indent and trailing newline.
func writeRaw(path string, root map[string]any) error {
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// serverEntry renders a spec as the JSON object claude-code expects, omitting
// empty args/env so re-Set of a spec is byte-stable.
func serverEntry(spec MCPServerSpec) map[string]any {
	entry := map[string]any{"command": spec.Command}
	if len(spec.Args) > 0 {
		args := make([]any, len(spec.Args))
		for i, a := range spec.Args {
			args[i] = a
		}
		entry["args"] = args
	}
	if len(spec.Env) > 0 {
		env := make(map[string]any, len(spec.Env))
		for k, v := range spec.Env {
			env[k] = v
		}
		entry["env"] = env
	}
	return entry
}

// Set implements MCPConfigurator: add or update spec.Name under mcpServers,
// preserving all other servers and unknown top-level keys.
func (c *ClaudeMCPConfigurator) Set(spec MCPServerSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("MCPServerSpec.Name is empty")
	}
	if spec.Command == "" {
		return fmt.Errorf("MCPServerSpec.Command is empty for %q", spec.Name)
	}
	path, err := c.pathFor(spec.Scope)
	if err != nil {
		return err
	}
	root, err := loadRaw(path)
	if err != nil {
		return err
	}
	servers := mcpServersOf(root)
	servers[spec.Name] = serverEntry(spec)
	return writeRaw(path, root)
}

// Remove implements MCPConfigurator: drop name from scope s, no-op if absent.
func (c *ClaudeMCPConfigurator) Remove(name string, s Scope) error {
	path, err := c.pathFor(s)
	if err != nil {
		return err
	}
	root, err := loadRaw(path)
	if err != nil {
		return err
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return nil // nothing to remove
	}
	if _, present := servers[name]; !present {
		return nil
	}
	delete(servers, name)
	return writeRaw(path, root)
}

// Get implements MCPConfigurator: return the spec for name in scope s.
func (c *ClaudeMCPConfigurator) Get(name string, s Scope) (MCPServerSpec, bool, error) {
	path, err := c.pathFor(s)
	if err != nil {
		return MCPServerSpec{}, false, err
	}
	root, err := loadRaw(path)
	if err != nil {
		return MCPServerSpec{}, false, err
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return MCPServerSpec{}, false, nil
	}
	raw, ok := servers[name].(map[string]any)
	if !ok {
		return MCPServerSpec{}, false, nil
	}
	spec := MCPServerSpec{Name: name, Scope: s}
	if cmd, ok := raw["command"].(string); ok {
		spec.Command = cmd
	}
	if args, ok := raw["args"].([]any); ok {
		for _, a := range args {
			if str, ok := a.(string); ok {
				spec.Args = append(spec.Args, str)
			}
		}
	}
	if env, ok := raw["env"].(map[string]any); ok {
		spec.Env = make(map[string]string, len(env))
		for k, v := range env {
			if str, ok := v.(string); ok {
				spec.Env[k] = str
			}
		}
	}
	return spec, true, nil
}

// List implements MCPConfigurator: sorted server names in scope s.
func (c *ClaudeMCPConfigurator) List(s Scope) ([]string, error) {
	path, err := c.pathFor(s)
	if err != nil {
		return nil, err
	}
	root, err := loadRaw(path)
	if err != nil {
		return nil, err
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return nil, nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
