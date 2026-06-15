package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

// AgentConfig and Registry now live in the dependency-free leaf
// internal/famconfig (#231); these aliases keep the fam.AgentConfig /
// fam.Registry API for existing callers. See famconfig for field docs.
type AgentConfig = famconfig.AgentConfig

type Registry = famconfig.Registry

// Setup is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func Setup(args []string, out io.Writer) error {
	return runCobra(NewSetupCmd(), args, out)
}

// NewSetupCmd builds the `botfam setup` Cobra command.
func NewSetupCmd() *cobra.Command {
	var agentsCSV string
	var force bool
	c := &cobra.Command{
		Use:           "setup <project> --agents alice,bob [--force]",
		Short:         "Configure an existing botfam project (registry, worktrees, docs)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(args[0], splitCSV(agentsCSV), force, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&agentsCSV, "agents", "", "comma-separated agent names")
	c.Flags().BoolVar(&force, "force", false, "proceed even if the registry already exists with other object stores")
	return c
}

func runSetup(project string, agents []string, force bool, out io.Writer) error {
	if project == "" {
		return fmt.Errorf("project name is required")
	}
	for _, agent := range agents {
		if err := validateSetupName("agent", agent); err != nil {
			return err
		}
	}
	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	stores, err := GitObjectStores(".")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(info.Root, 0o755); err != nil {
		return err
	}
	regPath := filepath.Join(info.Root, "fam.toml")
	reg := Registry{}
	if _, err := os.Stat(regPath); err == nil {
		reg, err = ReadRegistry(regPath)
		if err != nil {
			return err
		}
		if !force && !hasAny(reg.ObjectStores, stores) {
			return fmt.Errorf("%s already exists and this repo is not a registered member; use --force, COLLAB_ROOT, or BOTFAM_FAM deliberately", info.Root)
		}
	}
	if reg.Name == "" {
		reg.Name = project
		reg.RootSet = info.RootSet
		reg.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		reg.WikiProjections = []string{"memory:memory-*"}
	}
	reg.Roster = unique(append(reg.Roster, agents...))
	reg.RepoPaths = unique(append(reg.RepoPaths, RepoPath(".")))
	reg.ObjectStores = unique(append(reg.ObjectStores, stores...))
	if err := WriteRegistry(regPath, reg); err != nil {
		return err
	}
	if err := createProjectSymlink(project, info.Root); err != nil {
		return err
	}
	if err := RegisterMCPServerGlobally(reg.ForgeURL, FamSlug(reg), out); err != nil {
		fmt.Fprintf(out, "Warning: failed to register MCP server globally: %v\n", err)
	}
	fmt.Fprintf(out, "botfam root: %s\n", info.Root)
	return nil
}

// EnsureMembership moved to the internal/provision leaf (#311); re-exported in
// worktree.go.

// ReadRegistry / WriteRegistry delegate to famconfig (#231), kept as fam-package
// wrappers so existing callers don't change.
func ReadRegistry(path string) (Registry, error) { return famconfig.ReadRegistry(path) }

func WriteRegistry(path string, reg Registry) error { return famconfig.WriteRegistry(path, reg) }

func createProjectSymlink(project, target string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := validateSetupName("project", project); err != nil {
		return err
	}
	link := filepath.Join(home, ".botfam", project)
	if existing, err := os.Readlink(link); err == nil && existing == target {
		return nil
	}
	_ = os.Remove(link)
	return os.Symlink(target, link)
}

func validateSetupName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	for _, r := range name {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return fmt.Errorf("invalid %s %q: use letters, digits, underscore, or dash", kind, name)
		}
	}
	return nil
}

func splitCSV(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func hasAny(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if set[x] {
			return true
		}
	}
	return false
}

func RegisterMCPServerGlobally(forgeURL string, slug string, out io.Writer) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = filepath.Abs(execPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute executable path: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	configurators := []MCPConfigurator{
		NewAntigravityMCPConfigurator(),
		NewCodexMCPConfigurator(),
	}

	for _, cfg := range configurators {
		harness := cfg.Harness()

		var dir string
		var path string
		if harness == "antigravity" {
			dir = filepath.Join(home, ".gemini", "antigravity")
			path = filepath.Join(dir, "mcp_config.json")
		} else if harness == "codex" {
			dir = filepath.Join(home, ".codex")
			path = filepath.Join(dir, "config.toml")
		}

		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		fmt.Fprintf(out, "Registering botfam MCP server in global config: %s...\n", path)

		// 1. Configure botfam server (merge to preserve existing properties like cwd, tools)
		botfamSpec, ok, _ := cfg.Get("botfam", Global)
		env := map[string]string{
			"PATH": os.Getenv("PATH"),
		}
		if ok && botfamSpec.Env != nil {
			for k, v := range botfamSpec.Env {
				env[k] = v
			}
		}
		env["PATH"] = os.Getenv("PATH")

		err = cfg.Set(MCPServerSpec{
			Name:    "botfam",
			Command: execPath,
			Args:    []string{"serve"},
			Env:     env,
			Scope:   Global,
		})
		if err != nil {
			return fmt.Errorf("failed to register botfam for %s: %w", harness, err)
		}

		// Remove legacy collab server
		_ = cfg.Remove("collab", Global)

		// 2. Configure forge server (merge to preserve existing properties)
		if forgeURL != "" {
			tokenPath, err := forge.HarnessTokenPath(harness)
			if err != nil {
				return err
			}

			// Scope the global registration by slug to prevent collisions (Issue #225)
			forgeName := "forge"
			if slug != "" && slug != "botfam" {
				forgeName = "forge-" + slug
			}

			forgeSpec, ok, _ := cfg.Get(forgeName, Global)
			forgeEnv := map[string]string{
				"GITEA_ACCESS_TOKEN_FILE": tokenPath,
			}
			if ok && forgeSpec.Env != nil {
				for k, v := range forgeSpec.Env {
					forgeEnv[k] = v
				}
			}
			forgeEnv["GITEA_ACCESS_TOKEN_FILE"] = tokenPath

			err = cfg.Set(MCPServerSpec{
				Name:    forgeName,
				Command: filepath.Join(home, "bin", "gitea-mcp-server"),
				Args:    []string{"-t", "stdio", "-H", forgeURL},
				Env:     forgeEnv,
				Scope:   Global,
			})
			if err != nil {
				return fmt.Errorf("failed to register %s for %s: %w", forgeName, harness, err)
			}
		} else {
			// Issue #227 fix by construction: never delete the forge entry if forgeURL is empty.
			// Just output a warning so the user knows.
			fmt.Fprintln(out, "Warning: forge_url is empty; skipping global forge MCP registration")
		}
	}

	return nil
}
