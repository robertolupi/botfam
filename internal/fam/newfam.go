package fam

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const newfamHelp = `Usage:
  botfam newfam <project-name> --agents agy,claude,codex

Initialize a new botfam project natively in Go. This replaces bootstrap-botfam.sh.
It sets up the registry, creates git worktrees for all agents and the human operator
(based on the current $USER), configures git worktree identities, authorizes Claude
harness permissions, and generates agent documentation.
`

// NewfamCmd is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func NewfamCmd(args []string, out io.Writer) error {
	return runCobra(NewNewfamCmd(), args, out)
}

// NewNewfamCmd builds the `botfam newfam` Cobra command (issue #44).
func NewNewfamCmd() *cobra.Command {
	var agentsCSV string
	c := &cobra.Command{
		Use:           "newfam <project> --agents agy,claude,codex",
		Short:         "Initialize a new botfam project (worktrees, registry, docs)",
		Long:          newfamHelp,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewfam(args[0], splitCSV(agentsCSV), cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&agentsCSV, "agents", "", "comma-separated agent names")
	return c
}

func runNewfam(projectName string, agents []string, out io.Writer) error {
	if projectName == "" {
		return fmt.Errorf("project name is required")
	}
	if len(agents) == 0 {
		return fmt.Errorf("at least one agent is required via --agents")
	}

	// Resolve the human actor name dynamically from $USER
	humanActor := os.Getenv("USER")
	if humanActor == "" {
		return fmt.Errorf("cannot resolve human actor: $USER env variable is empty")
	}

	// Validate names
	if err := validateSetupName("project", projectName); err != nil {
		return err
	}
	for _, agent := range agents {
		if err := validateSetupName("agent", agent); err != nil {
			return err
		}
	}
	if err := validateSetupName("human", humanActor); err != nil {
		return err
	}

	// Verify we are inside a Git repository
	repoRoot := RepoPath(".")
	if repoRoot == "" {
		return fmt.Errorf("not a git repository (run from the repository main checkout)")
	}
	parentDir := filepath.Dir(repoRoot)

	// Resolve registry root
	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(info.Root, 0o755); err != nil {
		return err
	}

	regPath := filepath.Join(info.Root, "fam.toml")
	reg := Registry{}
	stores, err := GitObjectStores(".")
	if err != nil {
		return err
	}

	// Setup roster (agents + human) and worktrees
	roster := unique(append(agents, humanActor))
	var worktrees []string
	for _, actor := range roster {
		worktrees = append(worktrees, filepath.Join(parentDir, "wt-"+actor))
	}

	reg.Name = projectName
	reg.RootSet = info.RootSet
	reg.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	reg.Roster = roster
	reg.RepoPaths = unique(append(append([]string{repoRoot}, worktrees...), reg.RepoPaths...))
	reg.ObjectStores = unique(append(reg.ObjectStores, stores...))

	if err := WriteRegistry(regPath, reg); err != nil {
		return err
	}
	if err := createProjectSymlink(projectName, info.Root); err != nil {
		return err
	}

	fmt.Fprintf(out, "Created Gitea registry: %s\n", regPath)
	fmt.Fprintf(out, "Roster: %s\n", strings.Join(roster, ", "))

	// Write Claude settings and generate agent docs in the main checkout
	fmt.Fprintf(out, "Configuring main checkout at %s...\n", repoRoot)
	if err := writeClaudeSettings(repoRoot); err != nil {
		return fmt.Errorf("failed to write Claude settings in main checkout: %w", err)
	}
	if err := GenerateAgentDocs(repoRoot); err != nil {
		return fmt.Errorf("failed to generate agent docs in main checkout: %w", err)
	}

	// Build list of worktrees to add
	repoCommon, err := gitOne(repoRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(repoCommon) {
		repoCommon = filepath.Clean(filepath.Join(repoRoot, repoCommon))
	}

	for _, actor := range roster {
		wtPath := filepath.Join(parentDir, "wt-"+actor)
		var branch string
		if actor == humanActor {
			branch = "human/" + actor
		} else {
			branch = "agent/" + actor
		}

		fmt.Fprintf(out, "Configuring worktree %s (branch %s)...\n", wtPath, branch)

		if _, err := os.Stat(wtPath); err == nil {
			// Path exists. Validate it.
			isGit, err := gitOne(wtPath, "rev-parse", "--is-inside-work-tree")
			if err != nil || isGit != "true" {
				return fmt.Errorf("path exists but is not a git worktree: %s", wtPath)
			}
			wtCommon, err := gitOne(wtPath, "rev-parse", "--git-common-dir")
			if err != nil {
				return err
			}
			if !filepath.IsAbs(wtCommon) {
				wtCommon = filepath.Clean(filepath.Join(wtPath, wtCommon))
			}
			if wtCommon != repoCommon {
				return fmt.Errorf("existing worktree %s does not belong to %s", wtPath, repoRoot)
			}
			wtBranch, err := gitOne(wtPath, "branch", "--show-current")
			if err != nil {
				return err
			}
			if wtBranch != branch {
				return fmt.Errorf("existing worktree %s is on branch %s, expected %s", wtPath, wtBranch, branch)
			}
			fmt.Fprintf(out, "  worktree already exists: %s\n", wtPath)
		} else {
			// Create new worktree
			hasBranch := false
			if _, err := gitOutput(repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
				hasBranch = true
			}
			if hasBranch {
				fmt.Fprintf(out, "  creating worktree on existing branch %s...\n", branch)
				if _, err := gitOutput(repoRoot, "worktree", "add", wtPath, branch); err != nil {
					return fmt.Errorf("failed to add worktree %s: %w", wtPath, err)
				}
			} else {
				fmt.Fprintf(out, "  creating worktree on new branch %s...\n", branch)
				if _, err := gitOutput(repoRoot, "worktree", "add", "-b", branch, wtPath, "HEAD"); err != nil {
					return fmt.Errorf("failed to create worktree %s: %w", wtPath, err)
				}
			}
		}

		// Configure the worktree
		if err := writeClaudeSettings(wtPath); err != nil {
			return fmt.Errorf("failed to write Claude settings in worktree %s: %w", wtPath, err)
		}
		if err := GenerateAgentDocs(wtPath); err != nil {
			return fmt.Errorf("failed to generate agent docs in worktree %s: %w", wtPath, err)
		}

		// Initialize the worktree git identity
		if err := worktreeInit([]string{actor, wtPath}, out); err != nil {
			return fmt.Errorf("failed to initialize git identity in worktree %s: %w", wtPath, err)
		}

		// Clone the Gitea wiki into the worktree. Self-improvement docs
		// (retrospectives, session reviews) live in the wiki, which has no
		// branch protection, so they don't need double-approval PRs (#55).
		// The wiki is its own git repo and is gitignored in the main repo;
		// this is best-effort (non-load-bearing, may not be initialized).
		cloneWiki(repoRoot, wtPath, out)
	}

	if err := registerMCPServerGlobally(out); err != nil {
		fmt.Fprintf(out, "Warning: failed to register MCP server globally: %v\n", err)
	}

	fmt.Fprintln(out, "\nbotfam bootstrap complete.")
	fmt.Fprintf(out, "Project:     %s\n", projectName)
	fmt.Fprintf(out, "Repository:  %s\n", repoRoot)
	fmt.Fprintf(out, "Agents:      %s\n", strings.Join(agents, ", "))
	fmt.Fprintf(out, "Human:       %s (worktree wt-%s)\n", humanActor, humanActor)
	return nil
}

func registerMCPServerGlobally(out io.Writer) error {
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
		filepath.Join(home, ".claude.json"),
	}

	for _, path := range configPaths {
		parent := filepath.Dir(path)
		if _, err := os.Stat(parent); os.IsNotExist(err) {
			continue
		}

		fmt.Fprintf(out, "Registering collab MCP server in global config: %s...\n", path)

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

		collabSrv := map[string]interface{}{
			"command": execPath,
			"args":    []interface{}{"serve"},
			"env": map[string]interface{}{
				"PATH": os.Getenv("PATH"),
			},
		}
		mcpServers["collab"] = collabSrv
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

// wikiRemoteURL derives the Gitea wiki repo URL from the fam's git remote.
// Gitea serves a repo's wiki as a sibling git repo at <repo>.wiki.git. It
// prefers the "gitea" remote, falling back to "origin".
func wikiRemoteURL(repoRoot string) (string, error) {
	var raw string
	for _, remote := range []string{"gitea", "origin"} {
		if u, err := gitOne(repoRoot, "remote", "get-url", remote); err == nil && u != "" {
			raw = u
			break
		}
	}
	if raw == "" {
		return "", fmt.Errorf("no gitea or origin remote found")
	}
	return strings.TrimSuffix(raw, ".git") + ".wiki.git", nil
}

// cloneWiki clones the Gitea wiki into <wtPath>/wiki. It is best-effort: a
// missing remote, an uninitialized wiki, or a clone failure is reported as a
// warning rather than failing setup, since the wiki is non-load-bearing.
func cloneWiki(repoRoot, wtPath string, out io.Writer) {
	wikiURL, err := wikiRemoteURL(repoRoot)
	if err != nil {
		fmt.Fprintf(out, "  skipping wiki clone: %v\n", err)
		return
	}
	dest := filepath.Join(wtPath, "wiki")
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		fmt.Fprintf(out, "  wiki already present: %s\n", dest)
		return
	}

	// Read git identity config from the worktree to replicate in the cloned wiki repo.
	name, _ := gitOne(wtPath, "config", "user.name")
	email, _ := gitOne(wtPath, "config", "user.email")
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)

	if _, err := gitOutput(repoRoot, "clone", wikiURL, dest); err != nil {
		fmt.Fprintf(out, "  warning: could not clone wiki %s: %v\n", wikiURL, err)
		return
	}
	fmt.Fprintf(out, "  cloned wiki into %s\n", dest)

	if name != "" {
		if _, err := gitOutput(dest, "config", "user.name", name); err != nil {
			fmt.Fprintf(out, "  warning: could not configure wiki user.name: %v\n", err)
		}
	}
	if email != "" {
		if _, err := gitOutput(dest, "config", "user.email", email); err != nil {
			fmt.Fprintf(out, "  warning: could not configure wiki user.email: %v\n", err)
		}
	}
}

func writeClaudeSettings(checkout string) error {
	dir := filepath.Join(checkout, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")

	// Read existing settings, if any
	settingsMap := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &settingsMap)
	}

	// 1. Mutate enabledMcpjsonServers
	var enabledServers []string
	if serversRaw, exists := settingsMap["enabledMcpjsonServers"]; exists {
		_ = json.Unmarshal(serversRaw, &enabledServers)
	}

	// Filter out "collab" if it's there (historical cleanup)
	var newServers []string
	for _, srv := range enabledServers {
		if srv != "collab" {
			newServers = append(newServers, srv)
		}
	}

	serversData, err := json.Marshal(newServers)
	if err != nil {
		return err
	}
	settingsMap["enabledMcpjsonServers"] = serversData

	// 2. Mutate permissions object
	permissionsMap := make(map[string]json.RawMessage)
	if permRaw, exists := settingsMap["permissions"]; exists {
		_ = json.Unmarshal(permRaw, &permissionsMap)
	}

	var allowList []string
	if allowRaw, exists := permissionsMap["allow"]; exists {
		_ = json.Unmarshal(allowRaw, &allowList)
	}

	// Define allowed commands
	allowed := []string{
		"Bash(botfam:*)",
		"Bash(basename:*)",
		"Bash(git status:*)",
		"Bash(git log:*)",
		"Bash(git show:*)",
		"Bash(git diff:*)",
		"Bash(git branch:*)",
		"Bash(git rev-parse:*)",
		"Bash(git worktree list:*)",
		"Bash(git check-ignore:*)",
		"Bash(go build:*)",
		"Bash(go test:*)",
		"Bash(go vet:*)",
		"Bash(gofmt:*)",
	}

	existing := map[string]bool{}
	for _, cmd := range allowList {
		if cmd != "mcp__collab__*" {
			existing[cmd] = true
		}
	}
	for _, cmd := range allowed {
		existing[cmd] = true
	}

	var uniqueAllow []string
	for cmd := range existing {
		uniqueAllow = append(uniqueAllow, cmd)
	}
	sort.Strings(uniqueAllow)

	allowData, err := json.Marshal(uniqueAllow)
	if err != nil {
		return err
	}
	permissionsMap["allow"] = allowData

	permData, err := json.Marshal(permissionsMap)
	if err != nil {
		return err
	}
	settingsMap["permissions"] = permData

	// Marshal settingsMap back to JSON
	data, err := json.MarshalIndent(settingsMap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
