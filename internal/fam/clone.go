package fam

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

// NewCloneCmd builds `botfam clone` — bootstrap a fresh fam from a forge repo.
// It clones the repo into <fam-dir>/main, scaffolds <fam-dir>/fam.toml (outside
// any repo), creates a bare-name worktree per agent, and renders each agent's
// harness config + git identity. See wiki/proposal-unified-fam-config §4.5/§6.
func NewCloneCmd() *cobra.Command {
	var dir, forgeURL, agentsCSV, slug string
	c := &cobra.Command{
		Use:           "clone <git-url> --forge-url URL [--dir DIR] [--agents name=harness,...] [--slug SLUG]",
		Short:         "Clone a forge repo into a fresh fam directory and scaffold it",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClone(args[0], cloneOpts{dir: dir, forgeURL: forgeURL, agentsSpec: agentsCSV, slug: slug}, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "fam directory to create (default: ./<repo-name>)")
	c.Flags().StringVar(&forgeURL, "forge-url", "", "HTTP(S) forge API base, e.g. http://gitea.home.rlupi.com:3000/")
	c.Flags().StringVar(&agentsCSV, "agents", "claude=claude-code", "comma-separated name=harness (harness defaults to claude-code)")
	c.Flags().StringVar(&slug, "slug", "", "fam slug (default: repo name); must be globally unique on the IRC server")
	return c
}

type cloneOpts struct {
	dir        string
	forgeURL   string
	agentsSpec string
	slug       string
}

func runClone(gitURL string, opts cloneOpts, out io.Writer) error {
	// Validate everything that can be checked BEFORE any filesystem/git mutation,
	// so a bad invocation never leaves a half-built fam dir (#200).
	if strings.TrimSpace(opts.forgeURL) == "" {
		return fmt.Errorf("--forge-url is required (e.g. http://gitea.home.rlupi.com:3000/); it cannot be reliably derived from an SSH remote (#184)")
	}
	name, repository := parseCloneURL(gitURL)
	if name == "" {
		return fmt.Errorf("could not derive a repo name from %q", gitURL)
	}
	agents, err := parseAgentsSpec(opts.agentsSpec)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		return fmt.Errorf("no agents specified")
	}
	slug := opts.slug
	if slug == "" {
		slug = name
	}

	famDir := opts.dir
	if famDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		famDir = filepath.Join(cwd, name)
	}
	famDir, err = filepath.Abs(famDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(famDir, 0o755); err != nil {
		return fmt.Errorf("create fam dir: %w", err)
	}

	// Clone into <fam-dir>/main (the base checkout).
	mainDir := filepath.Join(famDir, "main")
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git")); os.IsNotExist(statErr) {
		fmt.Fprintf(out, "Cloning %s into %s...\n", gitURL, mainDir)
		if _, err := gitOutput(famDir, "clone", gitURL, mainDir); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
	} else {
		fmt.Fprintf(out, "main checkout already present at %s\n", mainDir)
	}

	// Secret store: <fam-dir>/.botfam (0700).
	secretDir := filepath.Join(famDir, ".botfam")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		return fmt.Errorf("create secret store: %w", err)
	}

	// Scaffold fam.toml at the fam dir (outside the repo).
	reg := Registry{Name: name, Slug: slug, ForgeURL: opts.forgeURL, Repository: repository, Agents: agents}
	for n := range agents {
		reg.Roster = append(reg.Roster, n)
	}
	if err := WriteRegistry(filepath.Join(famDir, "fam.toml"), reg); err != nil {
		return fmt.Errorf("scaffold fam.toml: %w", err)
	}
	fmt.Fprintf(out, "Scaffolded %s\n", filepath.Join(famDir, "fam.toml"))

	// Per-agent worktree + harness config + git identity.
	for n, ac := range agents {
		wt := filepath.Join(famDir, n)
		if _, statErr := os.Stat(wt); os.IsNotExist(statErr) {
			if _, err := gitOutput(mainDir, "worktree", "add", "-b", "agent/"+n, wt); err != nil {
				return fmt.Errorf("create worktree for %s: %w", n, err)
			}
			fmt.Fprintf(out, "Created worktree %s\n", wt)
		}
		if err := RenderGitIdentity(wt, n, ac.Email); err != nil {
			return fmt.Errorf("git identity for %s: %w", n, err)
		}
		var activeTokenPath string
		if ac.Harness != "" {
			var err error
			activeTokenPath, err = forge.HarnessTokenPath(ac.Harness)
			if err != nil {
				return err
			}
		}
		switch ac.Harness {
		case "claude-code":
			if err := RenderClaudeMCP(wt, opts.forgeURL, activeTokenPath); err != nil {
				return fmt.Errorf("render .mcp.json for %s: %w", n, err)
			}
			fmt.Fprintf(out, "Rendered %s/.mcp.json (claude-code)\n", n)
		case "antigravity", "codex":
			if err := RegisterMCPServerGlobally(opts.forgeURL, slug, out); err != nil {
				fmt.Fprintf(out, "Warning: global MCP registration for %s failed: %v\n", n, err)
			}
		default:
			fmt.Fprintf(out, "Note: agent %q has no/unknown harness %q — set it in fam.toml and re-run setup\n", n, ac.Harness)
		}
		if activeTokenPath != "" {
			if _, err := os.Stat(activeTokenPath); os.IsNotExist(err) {
				fmt.Fprintf(out, "  TODO: mint global %s token → %s\n", ac.Harness, activeTokenPath)
			}
		}
	}

	fmt.Fprintln(out, "\nClone complete. Next steps:")
	fmt.Fprintf(out, "  1. Confirm forge_url in %s (SSH :2222 ≠ HTTP :3000 — can't be derived from an SSH remote).\n", filepath.Join(famDir, "fam.toml"))
	fmt.Fprintf(out, "  2. Mint each harness's global forge token into ~/.botfam/token-<harness> (or run setup with an admin token).\n")
	fmt.Fprintf(out, "  3. Ensure each fam slug is globally unique on the IRC server.\n")
	return nil
}

// parseCloneURL derives (name, owner/repo) from a git URL or org/repo string.
// name is the repo basename without .git; repository is owner/repo when both are
// present in the path, else just the name.
func parseCloneURL(u string) (name, repository string) {
	u = strings.TrimSpace(u)
	u = strings.TrimSuffix(u, ".git")
	if u == "" {
		return "", ""
	}
	// Normalize scp-like (git@host:owner/repo) to a slash path.
	if i := strings.LastIndex(u, ":"); i >= 0 && !strings.Contains(u, "://") {
		u = u[i+1:]
	}
	u = strings.Trim(u, "/")
	parts := strings.Split(u, "/")
	name = parts[len(parts)-1]
	if len(parts) >= 2 {
		repository = parts[len(parts)-2] + "/" + name
	} else {
		repository = name
	}
	return name, repository
}

// parseAgentsSpec parses "claude=claude-code,agy=antigravity,codex" into agent
// entries. A bare name defaults to the claude-code harness.
func parseAgentsSpec(spec string) (map[string]AgentConfig, error) {
	out := map[string]AgentConfig{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, harness, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		harness = strings.TrimSpace(harness)
		if !ok || harness == "" {
			harness = "claude-code"
		}
		if err := validateSetupName("agent", name); err != nil {
			return nil, err
		}
		// Default forge_user to the per-harness bot account convention so the
		// scaffolded fam.toml is complete; the operator can override.
		out[name] = AgentConfig{Name: name, Harness: harness, ForgeUser: name + "-bot"}
	}
	return out, nil
}
