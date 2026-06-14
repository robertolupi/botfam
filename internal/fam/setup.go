package fam

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

// AgentConfig is a single `[agent.<name>]` or `[user.<name>]` entry in fam.toml:
// how botfam configures that worktree. The map key (and Name) is the worktree
// directory basename (the `wt-` prefix is retired). Email is optional and
// defaults to the host git email plus-addressed with Name. IsUser marks a
// `[user.<name>]` (human) entry, which gets a git identity but no harness/runtime.
// See wiki/proposal-unified-fam-config §4.2.
type AgentConfig struct {
	Name      string `toml:"-"` // filled from the table key
	Harness   string `toml:"harness,omitempty"`
	ForgeUser string `toml:"forge_user,omitempty"`
	Email     string `toml:"email,omitempty"`
	IsUser    bool   `toml:"-"` // true for [user.<name>] entries
}

type Registry struct {
	Name         string   `toml:"name"`
	Slug         string   `toml:"slug,omitempty"`
	Branch       string   `toml:"branch,omitempty"`
	RootSet      []string `toml:"root_set,omitempty"`
	Origin       string   `toml:"origin,omitempty"`
	Roster       []string `toml:"roster,omitempty"`
	Channels     []string `toml:"channels,omitempty"`
	RepoPaths    []string `toml:"repo_paths,omitempty"`
	ObjectStores []string `toml:"object_stores,omitempty"`
	CreatedAt    string   `toml:"created_at,omitempty"`

	// ForgeURL is the HTTP(S) forge API base (e.g. http://gitea.home.rlupi.com:3000/).
	// Repository is the org/repo on the forge. Both are explicit in fam.toml so
	// nothing has to guess them from a (possibly SSH) git remote — see #184.
	ForgeURL   string `toml:"forge_url,omitempty"`
	Repository string `toml:"repository,omitempty"`

	// Agents and Users hold the `[agent.<name>]` / `[user.<name>]` tables, keyed
	// by worktree-directory name. Agents may run the botfam runtime; Users are
	// human checkouts (git identity only). See wiki/proposal-unified-fam-config.
	Agents map[string]AgentConfig `toml:"agent,omitempty"`
	Users  map[string]AgentConfig `toml:"user,omitempty"`

	// WikiProjections declares curated wiki indexes as "name:glob" entries
	// (e.g. "reviews:review-*"). Each becomes botfam:///<name>[.json], listing
	// the wiki pages whose name matches the glob. Fam-specific: every fam
	// declares its own set (or none) — see #120.
	WikiProjections []string `toml:"wiki_projections,omitempty"`
}

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
	if err := RegisterMCPServerGlobally(out); err != nil {
		fmt.Fprintf(out, "Warning: failed to register MCP server globally: %v\n", err)
	}
	fmt.Fprintf(out, "botfam root: %s\n", info.Root)
	return nil
}

func EnsureMembership(root string, explicit bool, workDir string) error {
	if explicit {
		return os.MkdirAll(root, 0o755)
	}
	reg, err := ReadRegistry(filepath.Join(root, "fam.toml"))
	if err != nil {
		return fmt.Errorf("fam root %s is not set up or readable; run botfam setup", root)
	}
	stores, err := GitObjectStores(workDir)
	if err != nil {
		return err
	}
	if hasAny(reg.ObjectStores, stores) {
		return nil
	}
	return fmt.Errorf("repo object store is not registered for fam root %s; refusing unverified membership", root)
}

func ReadRegistry(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var reg Registry
	if err := toml.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("parse %s: %w", path, err)
	}
	// TOML map keys aren't injected into the struct value, so backfill the
	// canonical Name (and IsUser for users) from the table key.
	for k, ac := range reg.Agents {
		ac.Name = k
		reg.Agents[k] = ac
	}
	for k, ac := range reg.Users {
		ac.Name = k
		ac.IsUser = true
		reg.Users[k] = ac
	}
	return reg, nil
}

func WriteRegistry(path string, reg Registry) error {
	data, err := toml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal fam.toml: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

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

func RegisterMCPServerGlobally(out io.Writer) error {
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

	configPaths := []string{
		filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
	}

	for _, path := range configPaths {
		parent := filepath.Dir(path)
		if _, err := os.Stat(parent); os.IsNotExist(err) {
			continue
		}

		fmt.Fprintf(out, "Registering botfam MCP server in global config: %s...\n", path)

		var config map[string]interface{}
		data, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(data, &config); err != nil {
				config = make(map[string]interface{})
			}
		} else {
			config = make(map[string]interface{})
		}

		mcpServersVal, exists := config["mcpServers"]
		var mcpServers map[string]interface{}
		if exists {
			if m, ok := mcpServersVal.(map[string]interface{}); ok {
				mcpServers = m
			} else {
				mcpServers = make(map[string]interface{})
			}
		} else {
			mcpServers = make(map[string]interface{})
		}

		botfamSrv := map[string]interface{}{
			"command": execPath,
			"args":    []interface{}{"serve"},
			"env": map[string]interface{}{
				"PATH": os.Getenv("PATH"),
			},
		}
		delete(mcpServers, "collab")
		mcpServers["botfam"] = botfamSrv
		config["mcpServers"] = mcpServers

		newData, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		if err := os.WriteFile(path, newData, 0644); err != nil {
			return fmt.Errorf("failed to write config to %s: %w", path, err)
		}
	}

	return nil
}
