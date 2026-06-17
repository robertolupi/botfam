package famctx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/gitexec"
)

type ResolveMode int

const (
	ModeLocate       ResolveMode = iota // fam may be missing; return diagnostics
	ModeRegistry                        // require a matching [repo.<k>] stanza, allow user/base
	ModeAgentRuntime                    // require declared [agent.<name>]
)

type Source = famconfig.Source

const (
	SourceWorkDir     = famconfig.SourceWorkDir
	SourceClientRoots = famconfig.SourceClientRoots
	SourcePWD         = famconfig.SourcePWD
	SourceGitRoots    = famconfig.SourceGitRoots
)

type ActorRole = famconfig.ActorRole

const (
	RoleAgent   = famconfig.RoleAgent
	RoleUser    = famconfig.RoleUser
	RoleBase    = famconfig.RoleBase
	RoleUnknown = famconfig.RoleUnknown
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
	ClientName  string   // MCP initialize clientInfo.name; "" outside a live serve session
	Mode        ResolveMode
	CallActor   string
	BoundActor  string

	// Resolver, when non-nil, overrides the default git-based identity resolver.
	// Production leaves it nil (uses famconfig.GitResolver{Env}); tests inject a
	// fake so they can drive resolution without manipulating the environment or
	// standing up a git repo (#334).
	Resolver famconfig.Resolver
}

// resolver returns the identity resolver to use: the injected one when set,
// otherwise the default git resolver bound to inputs.Env.
func (inputs Inputs) resolver() famconfig.Resolver {
	if inputs.Resolver != nil {
		return inputs.Resolver
	}
	return famconfig.GitResolver{Env: inputs.Env}
}

type Context struct {
	famconfig.FamIdentity
	Slug     string
	Registry famconfig.Registry

	WorktreeRoot string
	WorkDir      string

	Agent famconfig.AgentConfig
	Flags map[string]any

	// Harness is the effective harness: the runtime-detected one (MCP clientInfo
	// or inherited env) when available, else the fam.toml-declared value. Token
	// resolution and the health report key on this, not Agent.Harness, so a
	// misdeclared fam.toml can't diverge the token path from the harness actually
	// running (#371). Empty for non-agent contexts.
	Harness string

	SpoolDir   string
	IRCLogDir  string
	TokenPath  string
	ScopedNick string

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
	if idx := strings.Index(workDir, string(filepath.Separator)+"wiki"); idx >= 0 {
		candidateDirs = append(candidateDirs, workDir[:idx])
		sources = append(sources, SourceWorkDir)
	}

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

	var resolveErr error

	for i, dir := range candidateDirs {
		var cSource Source = sources[i]

		if inputs.Mode == ModeAgentRuntime {
			resolved, err := famconfig.ResolveFam(dir)
			if err != nil {
				if resolveErr == nil {
					resolveErr = err
				}
				continue
			}

			var rootSet []string
			var rootSetID string
			if info, err := inputs.resolver().ResolveIdentity(dir); err == nil {
				rootSet = info.RootSet
				rootSetID = info.RootSetID
			}

			identity := resolved.FamIdentity
			identity.Source = cSource
			c := Context{
				FamIdentity:  identity,
				Slug:         resolved.Slug,
				Registry:     resolved.Registry,
				WorktreeRoot: resolved.WorktreeRoot,
				WorkDir:      dir,
				Agent:        resolved.Agent,
				Flags:        resolved.Flags,
				SpoolDir:     filepath.Join(resolved.FamDir, "spool", resolved.Actor),
				IRCLogDir:    filepath.Join(resolved.WorktreeRoot, "scratch", "irc", resolved.Actor),
				TokenPath:    resolved.TokenPath,
				ScopedNick:   famconfig.FamScopedNick(resolved.Actor, resolved.Slug),
				RootSet:      rootSet,
				RootSetID:    rootSetID,
			}
			return c, nil
		}

		// Resolve walk-up/legacy root and actor name through the (possibly
		// injected) resolver.
		info, err := inputs.resolver().ResolveIdentity(dir)
		if err != nil {
			if resolveErr == nil {
				resolveErr = err
			}
			continue
		}

		evalRoot := info.FamDir
		if eval, err := filepath.EvalSymlinks(info.FamDir); err == nil {
			evalRoot = eval
		}

		// Resolve the merged Registry from ~/.botfam/config.toml (#404). No
		// matching [repo.<k>] stanza is a loud failure (fail-loud invariant);
		// there is no legacy git-history fallback. ModeLocate records the error
		// and falls through to its diagnostic, ModeRegistry returns it.
		reg, regErr := famconfig.ResolveConfig(dir)
		if regErr != nil {
			if inputs.Mode == ModeRegistry {
				return Context{}, regErr
			}
			if resolveErr == nil {
				resolveErr = regErr
			}
			continue
		}
		cfgPath, _ := famconfig.ConfigPath()

		var agent famconfig.AgentConfig
		role := RoleUnknown

		// Determine actor and role
		actor := info.Actor
		// Bound actor overrides (only for permissive modes)
		if actor == "" {
			if inputs.CallActor != "" {
				actor = inputs.CallActor
			} else if inputs.BoundActor != "" {
				actor = inputs.BoundActor
			}
		}

		isAgent := false
		isUser := false
		if actor != "" {
			agent, isAgent = reg.Agents[actor]
			_, isUser = reg.Users[actor]
			if isAgent {
				role = RoleAgent
			} else if isUser {
				role = RoleUser
			} else {
				role = RoleUnknown
			}
		} else {
			// empty actor: check if it's the base checkout
			gitRoot, _ := gitexec.One(dir, "rev-parse", "--show-toplevel")
			if eval, err := filepath.EvalSymlinks(gitRoot); err == nil {
				gitRoot = eval
			}
			if gitRoot != "" && gitRoot == evalRoot {
				role = RoleBase
			}
		}

		// Resolve the effective harness from runtime signals (MCP clientInfo,
		// then inherited env), falling back to the declared roster value, and key
		// the token path on it (#371). A declared-vs-detected mismatch is a
		// misconfigured roster: surface it rather than silently following the
		// runtime.
		var hres famconfig.HarnessResolution
		tokenPath := ""
		if isAgent {
			hres = famconfig.ResolveHarness(agent.Harness, inputs.ClientName, inputs.Env)
			if hres.Effective != "" {
				if tp, err := famconfig.HarnessTokenPath(hres.Effective); err == nil {
					tokenPath = tp
				}
			}
		}

		slug := famconfig.FamSlug(reg)
		identity := info.FamIdentity
		identity.Actor = actor
		identity.ActorRole = role
		identity.Source = cSource
		identity.FamTOMLPath = cfgPath
		identity.Name = reg.Name

		c := Context{
			FamIdentity:  identity,
			Slug:         slug,
			Registry:     reg,
			WorktreeRoot: gitRoot(dir),
			WorkDir:      dir,
			Agent:        agent,
			Flags:        famconfig.ResolveFlags(reg, actor),
			Harness:      hres.Effective,
			SpoolDir:     filepath.Join(evalRoot, "spool", actor),
			IRCLogDir:    filepath.Join(gitRoot(dir), "scratch", "irc", actor),
			TokenPath:    tokenPath,
			ScopedNick:   famconfig.FamScopedNick(actor, slug),
			RootSet:      info.RootSet,
			RootSetID:    info.RootSetID,
		}
		if hres.Mismatch {
			c.Diagnostics = append(c.Diagnostics, Diagnostic{
				Severity: "warning",
				Message: fmt.Sprintf("config.toml declares harness %q for [agent.%s] but this is running under %q (via %s); using %q. Fix the roster harness to match.",
					hres.Declared, actor, hres.Detected, hres.Source, hres.Effective),
			})
		}
		return c, nil
	}

	if inputs.Mode == ModeLocate {
		return Context{
			WorkDir:     workDir,
			Diagnostics: []Diagnostic{{Severity: "error", Message: "No family context resolved"}},
		}, nil
	}
	if resolveErr == nil {
		resolveErr = fmt.Errorf("no family config resolved")
	}
	return Context{}, resolveErr
}

// ResolveAgentRuntime resolves the family context under strict agent-runtime expectations.
func ResolveAgentRuntime(workDir string) (Context, error) {
	return Resolve(context.Background(), Inputs{
		WorkDir: workDir,
		Mode:    ModeAgentRuntime,
	})
}

// ResolveRegistry resolves the merged Registry (from ~/.botfam/config.toml) for workDir.
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

// --- Internal Helpers ---------------------------------------------------------

func gitRoot(dir string) string {
	root, err := gitexec.One(dir, "rev-parse", "--show-toplevel")
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

func gitLines(workDir string, args ...string) ([]string, error) {
	return gitexec.Lines(workDir, args...)
}

func gitOne(workDir string, args ...string) (string, error) {
	return gitexec.One(workDir, args...)
}
