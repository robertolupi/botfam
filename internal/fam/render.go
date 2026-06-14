package fam

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mcpConfig / mcpServer mirror the `.mcp.json` schema consumed by claude-code.
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// RenderClaudeMCP writes <worktree>/.mcp.json for a claude-code agent: the
// botfam stdio server plus the forge (gitea-mcp-server) pointed at forgeURL with
// the per-fam token file. This is the project-scoped renderer from
// wiki/proposal-unified-fam-config §4.5 — forgeURL/tokenPath come from the one
// resolver, so the config cannot disagree with the health check (#183/#184).
func RenderClaudeMCP(worktree, forgeURL, tokenPath string) error {
	if forgeURL == "" {
		return fmt.Errorf("cannot render .mcp.json: forge_url is empty (set it in fam.toml)")
	}
	if tokenPath == "" {
		return fmt.Errorf("cannot render .mcp.json: token path is empty")
	}
	cfg := mcpConfig{MCPServers: map[string]mcpServer{
		"botfam": {Command: "botfam", Args: []string{"serve"}},
		"forge": {
			Command: "gitea-mcp-server",
			Args:    []string{"-t", "stdio", "-H", forgeURL},
			Env:     map[string]string{"GITEA_ACCESS_TOKEN_FILE": tokenPath},
		},
	}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(worktree, ".mcp.json"), data, 0o644)
}

// RenderGitIdentity sets the worktree's git user.name/user.email from the
// canonical agent entry. When email is empty it defaults to the host git email
// plus-addressed with the agent name (roberto.lupi@x → roberto.lupi+claude@x),
// the existing convention. Replaces the per-worktree self-configuration that let
// identities drift (§4.5).
func RenderGitIdentity(worktree, name, email string) error {
	if name == "" {
		return fmt.Errorf("cannot render git identity: agent name is empty")
	}
	if email == "" {
		host, _ := gitOne(worktree, "config", "user.email")
		email = plusAddress(strings.TrimSpace(host), name)
	}
	// Per-worktree config requires the worktreeConfig extension; without it
	// `git config --worktree` fails on a linked worktree (matches worktree.go).
	if _, err := gitOutput(worktree, "config", "extensions.worktreeConfig", "true"); err != nil {
		return fmt.Errorf("enable worktreeConfig: %w", err)
	}
	if _, err := gitOutput(worktree, "config", "--worktree", "user.name", name); err != nil {
		return fmt.Errorf("set user.name: %w", err)
	}
	if email != "" {
		if _, err := gitOutput(worktree, "config", "--worktree", "user.email", email); err != nil {
			return fmt.Errorf("set user.email: %w", err)
		}
	}
	return nil
}

// plusAddress inserts a +tag local-part suffix into an email (user@host →
// user+tag@host). Returns the input unchanged when it has no '@' or tag is empty.
func plusAddress(email, tag string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 || tag == "" {
		return email
	}
	return email[:at] + "+" + tag + email[at:]
}
