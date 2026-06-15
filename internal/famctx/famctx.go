package famctx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/robertolupi/botfam/internal/famconfig"
)

type ResolveMode int

const (
	ModeLocate ResolveMode = iota // fam may be missing; return diagnostics
	ModeRegistry                 // require fam.toml, allow user/base
	ModeAgentRuntime             // require declared [agent.<name>]
)

type Source string

const (
	SourceWorkDir       Source = "work_dir"
	SourceClientRoots   Source = "client_roots"
	SourcePWD           Source = "pwd"
	SourceGitRoots      Source = "git_roots"
)

type ActorRole string

const (
	RoleAgent   ActorRole = "agent"
	RoleUser    ActorRole = "user"
	RoleBase    ActorRole = "base"
	RoleUnknown ActorRole = "unknown"
)

type Location string

const (
	LocationMainRepo  Location = "main"
	LocationWiki      Location = "wiki"
	LocationSubmodule Location = "submodule"
	LocationForeign   Location = "foreign"
)

type Diagnostic struct {
	Severity string // "error" | "warning"
	Message  string
}

type Inputs struct {
	WorkDir     string   // command/tool work_dir; default os.Getwd()
	Env         []string // testable env, nil means os.Environ()
	PWD         string   // launching shell PWD for system-wide MCP mounts
	ClientRoots []string // MCP roots, already decoded from file:// URIs
	Mode        ResolveMode
	CallActor   string
	BoundActor  string
}

type Context struct {
	FamDir       string
	FamTOMLPath  string
	Name         string
	Slug         string
	Registry     famconfig.Registry

	WorktreeRoot string
	WorkDir      string
	Source       Source

	Actor     string
	ActorRole ActorRole
	Agent     famconfig.AgentConfig
	Flags     map[string]any

	MailboxPath string
	IRCLogDir   string
	TokenPath   string
	ScopedNick  string

	RootSet     []string
	RootSetID   string
	Diagnostics []Diagnostic
}

// Resolve resolves the family context for the given inputs.
func Resolve(ctx context.Context, inputs Inputs) (Context, error) {
	// Normalize workDir
	workDir := inputs.WorkDir
	if workDir == "" {
		if wd, err := os.Getwd(); err == nil {
			workDir = wd
		} else {
			workDir = "."
		}
	}
	absDir, err := filepath.Abs(workDir)
	if err == nil {
		workDir = absDir
	}
	if eval, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = eval
	}

	var candidateDirs []string
	var sources []Source

	// 1. Inputs.WorkDir
	candidateDirs = append(candidateDirs, workDir)
	sources = append(sources, SourceWorkDir)

	// 2. ClientRoots
	for _, root := range inputs.ClientRoots {
		if root != "" {
			candidateDirs = append(candidateDirs, root)
			sources = append(sources, SourceClientRoots)
		}
	}

	// 3. PWD
	if inputs.PWD != "" {
		candidateDirs = append(candidateDirs, inputs.PWD)
		sources = append(sources, SourcePWD)
	}

	// Try each candidate directory using the walk-up resolver
	var resolvedFamDir string
	var resolvedActor string
	var resolvedRole ActorRole
	var resolvedReg famconfig.Registry
	var resolvedSource Source
	var resolveErr error

	for i, dir := range candidateDirs {
		fd, act, rle, reg, err := resolveWalk(dir)
		if err == nil {
			resolvedFamDir = fd
			resolvedActor = act
			resolvedRole = rle
			resolvedReg = reg
			resolvedSource = sources[i]
			break
		}
		if resolveErr == nil {
			resolveErr = err
		}

		// Try legacy fallback for this candidate before moving to the next candidate
		fd, name, roots, id, legacyErr := resolveLegacyGitHash(dir, inputs.Env)
		if legacyErr == nil {
			if inputs.Mode == ModeAgentRuntime {
				return Context{}, fmt.Errorf("strict agent runtime required: no fam.toml found (resolved role: unknown)")
			}
			resolvedFamDir = fd
			repoName := famconfig.ResolveRepoName(dir)
			resolvedActor = famconfig.ParseActor(filepath.Base(dir), repoName)
			resolvedRole = RoleUnknown
			resolvedSource = SourceGitRoots
			resolvedReg = famconfig.Registry{
				Name: name,
			}
			var diags []Diagnostic
			diags = append(diags, Diagnostic{
				Severity: "warning",
				Message:  "Using legacy git-history fallback. Run 'botfam setup' to migrate.",
			})

			c := Context{
				FamDir:       resolvedFamDir,
				FamTOMLPath:  "",
				Name:         name,
				Slug:         name,
				Registry:     resolvedReg,
				WorktreeRoot: gitRoot(dir),
				WorkDir:      dir,
				Source:       resolvedSource,
				Actor:        resolvedActor,
				ActorRole:    RoleUnknown,
				RootSet:      roots,
				RootSetID:    id,
				Diagnostics:  diags,
			}
			return c, nil
		}
	}

	if resolvedFamDir == "" {
		if inputs.Mode == ModeLocate {
			return Context{
				WorkDir:     workDir,
				Diagnostics: []Diagnostic{{Severity: "error", Message: "No family context resolved"}},
			}, nil
		}
		return Context{}, fmt.Errorf("no family config resolved: %w", resolveErr)
	}

	// Resolve the Actor from bound inputs if empty (permissive modes only)
	actor := resolvedActor
	role := resolvedRole
	if actor == "" {
		envActor := lookupEnv(inputs.Env, "COLLAB_ACTOR")
		if inputs.CallActor != "" {
			actor = inputs.CallActor
		} else if inputs.BoundActor != "" {
			actor = inputs.BoundActor
		} else if envActor != "" {
			actor = envActor
		}

		if actor != "" {
			if _, ok := resolvedReg.Agents[actor]; ok {
				role = RoleAgent
			} else if _, ok := resolvedReg.Users[actor]; ok {
				role = RoleUser
			} else {
				role = RoleUnknown
			}
		}
	}

	// Check for actor conflicts
	envActor := lookupEnv(inputs.Env, "COLLAB_ACTOR")
	if envActor != "" && resolvedActor != "" && envActor != resolvedActor {
		return Context{}, fmt.Errorf("COLLAB_ACTOR %q conflicts with resolved directory actor %q", envActor, resolvedActor)
	}

	// Check strict modes
	if inputs.Mode == ModeAgentRuntime {
		if role != RoleAgent {
			return Context{}, fmt.Errorf("strict agent runtime required: resolved role is %s (actor: %s)", role, actor)
		}
	}

	c := Context{
		FamDir:       resolvedFamDir,
		FamTOMLPath:  filepath.Join(resolvedFamDir, "fam.toml"),
		Name:         resolvedReg.Name,
		Slug:         famconfig.FamSlug(resolvedReg),
		Registry:     resolvedReg,
		WorktreeRoot: gitRoot(workDir),
		WorkDir:      workDir,
		Source:       resolvedSource,
		Actor:        actor,
		ActorRole:    role,
		Flags:        famconfig.ResolveFlags(resolvedReg, actor),
	}

	if role == RoleAgent && actor != "" {
		c.Agent = resolvedReg.Agents[actor]
	} else if role == RoleUser && actor != "" {
		c.Agent = resolvedReg.Users[actor]
	}

	// Populate derived paths (stable)
	if actor != "" {
		c.MailboxPath = filepath.Join(resolvedFamDir, actor+".mailbox")
		c.IRCLogDir = filepath.Join(resolvedFamDir, actor+"-collab") // wait, is it? Claude will override if wrong
		if c.Agent.Harness != "" {
			if tokenPath, err := famconfig.HarnessTokenPath(c.Agent.Harness); err == nil {
				c.TokenPath = tokenPath
			}
		}
		// ScopedNick is populated using famconfig.FamScopedNick once Claude moves it
		// For now we'll do actor + "-" + slug
		c.ScopedNick = actor
		if c.Slug != "" {
			c.ScopedNick = actor + "-" + c.Slug
		}
	}

	return c, nil
}

// ResolveAgentRuntime resolves the family context under strict agent-runtime expectations.
func ResolveAgentRuntime(workDir string) (Context, error) {
	return Resolve(context.Background(), Inputs{
		WorkDir: workDir,
		Mode:    ModeAgentRuntime,
	})
}

// ResolveRegistry resolves the registry/fam.toml for workDir.
func ResolveRegistry(workDir string) (Context, error) {
	return Resolve(context.Background(), Inputs{
		WorkDir: workDir,
		Mode:    ModeRegistry,
	})
}

// ResolveForMCP resolves the family context for an MCP server session.
func ResolveForMCP(ctx context.Context, inputs Inputs) (Context, error) {
	return Resolve(ctx, inputs)
}

// FlagEnabled reads the already-resolved flag set and returns the boolean value.
func (c *Context) FlagEnabled(name string, def bool) (bool, error) {
	return famconfig.FlagFromMap(c.Flags, name, def)
}

// CurrentBranch returns the live Git branch of the worktree.
func (c *Context) CurrentBranch() (string, error) {
	if c.WorktreeRoot == "" {
		return "", fmt.Errorf("not inside a git worktree")
	}
	return gitOne(c.WorktreeRoot, "rev-parse", "--abbrev-ref", "HEAD")
}

// LocationOf classifies a path relative to the Context worktree.
func (c *Context) LocationOf(path string) (Location, error) {
	if path == "" {
		path = c.WorkDir
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return LocationForeign, err
	}
	eval, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = eval
	}

	innerTop, err := gitOne(abs, "rev-parse", "--show-toplevel")
	if err != nil || innerTop == "" {
		wikiDir := filepath.Join(c.WorktreeRoot, "wiki")
		if c.WorktreeRoot == "" {
			wikiDir = filepath.Join(c.FamDir, "wiki")
		}
		if abs == wikiDir || strings.HasPrefix(abs, wikiDir+string(filepath.Separator)) {
			return LocationWiki, nil
		}
		if abs == c.FamDir || strings.HasPrefix(abs, c.FamDir+string(filepath.Separator)) {
			return LocationMainRepo, nil
		}
		return LocationForeign, nil
	}

	if evalInner, err := filepath.EvalSymlinks(innerTop); err == nil {
		innerTop = evalInner
	}

	wikiDir := filepath.Join(c.WorktreeRoot, "wiki")
	if c.WorktreeRoot == "" {
		wikiDir = filepath.Join(c.FamDir, "wiki")
	}
	if evalWiki, err := filepath.EvalSymlinks(wikiDir); err == nil {
		wikiDir = evalWiki
	}
	if abs == wikiDir || strings.HasPrefix(abs, wikiDir+string(filepath.Separator)) {
		return LocationWiki, nil
	}

	evalRoot := c.WorktreeRoot
	if eval, err := filepath.EvalSymlinks(c.WorktreeRoot); err == nil {
		evalRoot = eval
	}
	if c.WorktreeRoot != "" {
		if innerTop == evalRoot {
			return LocationMainRepo, nil
		}
	}

	super, err := gitOne(abs, "rev-parse", "--show-superproject-working-tree")
	if err == nil && super != "" {
		if evalSuper, err := filepath.EvalSymlinks(super); err == nil {
			super = evalSuper
		}
		if super == c.WorktreeRoot {
			return LocationSubmodule, nil
		}
	}

	return LocationForeign, nil
}

// --- Walk-up resolution helpers -----------------------------------------------

func resolveWalk(workDir string) (famDir string, actor string, role ActorRole, reg famconfig.Registry, err error) {
	curr := filepath.Clean(workDir)
	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home)
	}

	for {
		parent := filepath.Dir(curr)
		tomlPath := filepath.Join(parent, "fam.toml")
		if fileExists(tomlPath) {
			if r, rerr := famconfig.ReadRegistry(tomlPath); rerr == nil {
				base := filepath.Base(curr)
				repoName := famconfig.ResolveRepoName(curr)
				actor := famconfig.ParseActor(base, repoName)
				if actor == "" {
					actor = base
				}
				if _, ok := r.Agents[actor]; ok {
					return parent, actor, RoleAgent, r, nil
				}
				if _, ok := r.Users[actor]; ok {
					return parent, actor, RoleUser, r, nil
				}
			}
		}

		if curr == home || parent == curr {
			break
		}
		curr = parent
	}

	curr = filepath.Clean(workDir)
	for {
		tomlPath := filepath.Join(curr, "fam.toml")
		if fileExists(tomlPath) {
			if r, rerr := famconfig.ReadRegistry(tomlPath); rerr == nil {
				role := RoleUnknown
				if root, err := gitOne(curr, "rev-parse", "--show-toplevel"); err == nil && root != "" {
					role = RoleBase
				}
				return curr, "", role, r, nil
			}
		}

		parent := filepath.Dir(curr)
		if curr == home || parent == curr {
			break
		}
		curr = parent
	}

	return "", "", RoleUnknown, famconfig.Registry{}, fmt.Errorf("no fam.toml found")
}

func resolveLegacyGitHash(workDir string, env []string) (famDir string, name string, roots []string, id string, err error) {
	roots, err = gitLines(workDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", "", nil, "", err
	}
	sort.Strings(roots)
	sum := sha256.Sum256([]byte(strings.Join(roots, "\n")))
	id = hex.EncodeToString(sum[:])[:12]
	name = "fam-" + id
	if suffix := lookupEnv(env, "BOTFAM_FAM"); suffix != "" {
		name += "-" + sanitizeSuffix(suffix)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", nil, "", err
	}
	famDir = filepath.Join(home, ".botfam", name)
	return famDir, name, roots, id, nil
}

func gitRoot(dir string) string {
	root, err := gitOne(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ""
	}
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		return eval
	}
	return root
}

func lookupEnv(env []string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func gitOutput(workDir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	return cmd.Output()
}

func gitLines(workDir string, args ...string) ([]string, error) {
	out, err := gitOutput(workDir, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func gitOne(workDir string, args ...string) (string, error) {
	lines, err := gitLines(workDir, args...)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("git %s returned no output", strings.Join(args, " "))
	}
	return lines[0], nil
}

func sanitizeSuffix(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

