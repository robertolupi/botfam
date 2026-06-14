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
	if err := RegisterMCPServerGlobally(reg.ForgeURL, out); err != nil {
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

func RegisterMCPServerGlobally(forgeURL string, out io.Writer) error {
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
		filepath.Join(home, ".codex", "config.toml"),
	}

	for _, path := range configPaths {
		parent := filepath.Dir(path)
		if _, err := os.Stat(parent); os.IsNotExist(err) {
			continue
		}

		var harness string
		if strings.Contains(path, "antigravity") {
			harness = "antigravity"
		} else if strings.Contains(path, ".codex") {
			harness = "codex"
		}

		isTOML := strings.HasSuffix(path, ".toml")
		mcpServersKey := "mcpServers"
		if isTOML {
			mcpServersKey = "mcp_servers"
		}

		fmt.Fprintf(out, "Registering botfam MCP server in global config: %s...\n", path)

		var config map[string]interface{}
		data, err := os.ReadFile(path)
		if err == nil {
			if isTOML {
				if err := toml.Unmarshal(data, &config); err != nil {
					config = make(map[string]interface{})
				}
			} else {
				if err := json.Unmarshal(data, &config); err != nil {
					config = make(map[string]interface{})
				}
			}
		} else {
			config = make(map[string]interface{})
		}
		if config == nil {
			config = make(map[string]interface{})
		}

		mcpServersVal, exists := config[mcpServersKey]
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

		// Configure botfam server (merge to preserve existing properties like cwd, startup_timeout_sec, tools)
		var botfamSrv map[string]interface{}
		if existing, ok := mcpServers["botfam"].(map[string]interface{}); ok {
			botfamSrv = existing
		} else {
			botfamSrv = make(map[string]interface{})
		}
		botfamSrv["command"] = execPath
		botfamSrv["args"] = []interface{}{"serve"}
		if envVal, ok := botfamSrv["env"].(map[string]interface{}); ok {
			envVal["PATH"] = os.Getenv("PATH")
			botfamSrv["env"] = envVal
		} else {
			botfamSrv["env"] = map[string]interface{}{
				"PATH": os.Getenv("PATH"),
			}
		}
		delete(mcpServers, "collab")
		mcpServers["botfam"] = botfamSrv

		// Configure forge server (merge to preserve existing properties like cwd, startup_timeout_sec, tools)
		if forgeURL != "" && harness != "" {
			tokenPath, err := forge.HarnessTokenPath(harness)
			if err != nil {
				return err
			}
			var forgeSrv map[string]interface{}
			if existing, ok := mcpServers["forge"].(map[string]interface{}); ok {
				forgeSrv = existing
			} else {
				forgeSrv = make(map[string]interface{})
			}
			forgeSrv["command"] = filepath.Join(home, "bin", "gitea-mcp-server")
			forgeSrv["args"] = []interface{}{"-t", "stdio", "-H", forgeURL}
			if envVal, ok := forgeSrv["env"].(map[string]interface{}); ok {
				envVal["GITEA_ACCESS_TOKEN_FILE"] = tokenPath
				forgeSrv["env"] = envVal
			} else {
				forgeSrv["env"] = map[string]interface{}{
					"GITEA_ACCESS_TOKEN_FILE": tokenPath,
				}
			}
			mcpServers["forge"] = forgeSrv
		} else {
			delete(mcpServers, "forge")
			if forgeURL == "" {
				fmt.Fprintln(out, "Warning: forge_url is empty; skipping global forge MCP registration")
			}
		}

		config[mcpServersKey] = mcpServers

		var newData []byte
		if isTOML {
			newData, err = toml.Marshal(config)
		} else {
			newData, err = json.MarshalIndent(config, "", "  ")
		}
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		if err := os.WriteFile(path, newData, 0644); err != nil {
			return fmt.Errorf("failed to write config to %s: %w", path, err)
		}
	}

	return nil
}
