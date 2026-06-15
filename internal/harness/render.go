package harness

import (
	"fmt"
	"github.com/robertolupi/botfam/internal/gitexec"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// mcpConfig / mcpServer mirror the `.mcp.json` schema consumed by claude-code.
// They are retained for the render_test.go assertions, which decode the file
// back into these typed structs.
type mcpConfig struct {
	MCPServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// RenderClaudeMCP writes <worktree>/.mcp.json for a claude-code agent: the
// botfam stdio server plus the forge MCP pointed at forgeURL with the per-fam
// token file. This is the project-scoped renderer from
// wiki/proposal-unified-fam-config §4.5 — forgeURL/tokenPath come from the one
// resolver, so the config cannot disagree with the health check (#183/#184).
//
// It edits the file through the claude-code MCPConfigurator (Set the botfam,
// forge, and gopls entries), so any OTHER servers a developer hand-added (e.g.
// codebase-memory-mcp, gopls) are PRESERVED instead of being clobbered. This is
// the merge-not-overwrite fix for #227 (setup wiping unrelated entries) and the
// collisions in #225 — the renderer used to os.WriteFile the whole file.
//
// Both server commands are the absolute `~/bin/<binary>` paths that
// tools/install.sh produces (botfam + the vendored gitea-mcp-server from the
// third_party/gitea-mcp submodule), not bare PATH names — so a brew-installed
// gitea-mcp-server (or a stale botfam) earlier on PATH cannot shadow the
// vendored builds (the ambiguity that bit deep-cuts).
//
// When gopls is installed, its built-in MCP server (`gopls mcp`) is also
// registered, giving the agent Go-aware tooling (diagnostics, symbol
// references, rename, search, vulncheck). gopls is an optional developer tool
// resolved via PATH to an absolute path; its absence is not an error.
func RenderClaudeMCP(worktree, forgeURL, tokenPath string) error {
	if forgeURL == "" {
		return fmt.Errorf("cannot render .mcp.json: forge_url is empty (set it in fam.toml)")
	}
	if tokenPath == "" {
		return fmt.Errorf("cannot render .mcp.json: token path is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	binDir := filepath.Join(home, "bin") // tools/install.sh install target

	cfg := NewClaudeMCPConfigurator(worktree)

	if err := cfg.Set(MCPServerSpec{
		Name:    "botfam",
		Command: filepath.Join(binDir, "botfam"),
		Args:    []string{"serve"},
		Scope:   Project,
	}); err != nil {
		return fmt.Errorf("set botfam server: %w", err)
	}
	if err := cfg.Set(MCPServerSpec{
		Name:    "forge",
		Command: filepath.Join(binDir, "gitea-mcp-server"),
		Args:    []string{"-t", "stdio", "-H", forgeURL},
		Env:     map[string]string{"GITEA_ACCESS_TOKEN_FILE": tokenPath},
		Scope:   Project,
	}); err != nil {
		return fmt.Errorf("set forge server: %w", err)
	}
	// gopls ships an MCP server (`gopls mcp`); register it for Go tooling when
	// installed, resolved to an absolute path so a stale copy can't shadow it.
	// When gopls is absent we leave any existing entry alone rather than
	// deleting it (non-destructive).
	if goplsPath := lookGopls(); goplsPath != "" {
		if err := cfg.Set(MCPServerSpec{
			Name:    "gopls",
			Command: goplsPath,
			Args:    []string{"mcp"},
			Scope:   Project,
		}); err != nil {
			return fmt.Errorf("set gopls server: %w", err)
		}
	}
	return nil
}

// lookGopls returns the absolute path to the gopls binary if it is on PATH,
// else "" (gopls is optional — its absence must not fail the render).
func lookGopls() string {
	p, err := exec.LookPath("gopls")
	if err != nil {
		return ""
	}
	if abs, aerr := filepath.Abs(p); aerr == nil {
		return abs
	}
	return p
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
		host, _ := gitexec.One(worktree, "config", "user.email")
		email = plusAddress(strings.TrimSpace(host), name)
	}
	// Per-worktree config requires the worktreeConfig extension; without it
	// `git config --worktree` fails on a linked worktree (matches worktree.go).
	if _, err := gitexec.Output(worktree, "config", "extensions.worktreeConfig", "true"); err != nil {
		return fmt.Errorf("enable worktreeConfig: %w", err)
	}
	if _, err := gitexec.Output(worktree, "config", "--worktree", "user.name", name); err != nil {
		return fmt.Errorf("set user.name: %w", err)
	}
	if email != "" {
		if _, err := gitexec.Output(worktree, "config", "--worktree", "user.email", email); err != nil {
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
