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
)

const newfamHelp = `Usage:
  botfam newfam <project-name> --agents agy,claude,codex

Initialize a new botfam project natively in Go. This replaces bootstrap-botfam.sh.
It sets up the registry, creates git worktrees for all agents and the human operator
(based on the current $USER), configures git worktree identities, authorizes Claude
harness permissions, and generates agent documentation.
`

// NewfamCmd handles "botfam newfam <project-name> --agents alice,bob" (issue #44).
func NewfamCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: botfam newfam <project-name> --agents agy,claude,codex")
	}
	projectName := args[0]
	if projectName == "-h" || projectName == "--help" || projectName == "help" {
		fmt.Fprint(out, newfamHelp)
		return nil
	}

	var agents []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--agents="):
			agents = splitCSV(strings.TrimPrefix(arg, "--agents="))
		case arg == "--agents":
			i++
			if i >= len(args) {
				return fmt.Errorf("--agents requires a comma-separated value")
			}
			agents = splitCSV(args[i])
		default:
			return fmt.Errorf("unknown setup argument %q", arg)
		}
	}

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
	cleanLegacyMCP(repoRoot)
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
		cleanLegacyMCP(wtPath)
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
	}

	fmt.Fprintln(out, "\nbotfam bootstrap complete.")
	fmt.Fprintf(out, "Project:     %s\n", projectName)
	fmt.Fprintf(out, "Repository:  %s\n", repoRoot)
	fmt.Fprintf(out, "Agents:      %s\n", strings.Join(agents, ", "))
	fmt.Fprintf(out, "Human:       %s (worktree wt-%s)\n", humanActor, humanActor)
	return nil
}

func writeClaudeSettings(checkout string) error {
	dir := filepath.Join(checkout, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")

	type Permissions struct {
		Allow []string `json:"allow"`
	}
	type Settings struct {
		EnabledMcpjsonServers []string     `json:"enabledMcpjsonServers,omitempty"`
		Permissions           *Permissions `json:"permissions,omitempty"`
	}

	var s Settings
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}

	var newServers []string
	for _, srv := range s.EnabledMcpjsonServers {
		if srv != "collab" {
			newServers = append(newServers, srv)
		}
	}
	s.EnabledMcpjsonServers = newServers

	if s.Permissions == nil {
		s.Permissions = &Permissions{}
	}

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
	for _, cmd := range s.Permissions.Allow {
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
	s.Permissions.Allow = uniqueAllow

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func cleanLegacyMCP(checkout string) {
	mcpPath := filepath.Join(checkout, ".mcp.json")
	if !isGitTracked(checkout, mcpPath) {
		_ = os.Remove(mcpPath)
	}
	agentsPath := filepath.Join(checkout, ".agents", "mcp_config.json")
	if !isGitTracked(checkout, agentsPath) {
		_ = os.Remove(agentsPath)
	}
	_ = os.Remove(filepath.Join(checkout, ".codex", "config.toml"))

	_ = os.Remove(filepath.Join(checkout, ".agents"))
	_ = os.Remove(filepath.Join(checkout, ".codex"))
}

func isGitTracked(checkout string, path string) bool {
	rel, err := filepath.Rel(checkout, path)
	if err != nil {
		return false
	}
	_, err = gitOne(checkout, "ls-files", "--error-unmatch", rel)
	return err == nil
}
