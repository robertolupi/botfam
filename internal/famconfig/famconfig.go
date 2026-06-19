// Package famconfig is the dependency-free leaf that owns botfam configuration:
// the global ~/.botfam/config.toml schema (Config/RepoConfig) and its merged
// in-memory shape (Registry/AgentConfig), the config-backed resolution
// (LoadConfig/MatchRepo/BuildRegistry/ResolveConfig and the canonical
// ResolveFam), and the per-harness token path (HarnessTokenPath). Per-fam
// fam.toml files were retired in favour of one operator-owned config with
// path-keyed `[repo.<k>]` override stanzas (#404).
//
// It has NO internal/* dependencies (only gitexec), so both internal/cli and
// internal/forge import it instead of each other — breaking the cycle that
// forced forge.NewClient and the resolver to re-derive fam identity three
// different ways (#183, #231).
package famconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robertolupi/botfam/internal/gitexec"
)

// AgentConfig is a single `[agent.<name>]`/`[user.<name>]` entry — global in
// ~/.botfam/config.toml or a per-repo `[repo.<k>.agent.<name>]` override. The
// map key (and Name) is the worktree directory basename (the `wt-` prefix is
// retired). Email defaults to the host git email plus-addressed with Name.
// IsUser marks a `[user.<name>]` (human) entry — git identity only, no runtime.
type AgentConfig struct {
	Name      string    `toml:"-"` // filled from the table key
	Harness   string    `toml:"harness,omitempty"`
	ForgeUser string    `toml:"forge_user,omitempty"`
	Email     string    `toml:"email,omitempty"`
	IsUser    bool      `toml:"-"` // true for [user.<name>] entries
	Run       RunConfig `toml:"run,omitempty"`

	// Flags are this agent's per-harness feature-flag overrides
	// ([agent.<name>.flags] in fam.toml); they win over the fam-wide [flags]
	// defaults key-by-key. See ResolveFlags.
	Flags map[string]any `toml:"flags,omitempty"`
}

// RunConfig is the harness-generic default posture for `botfam run`. It records
// operator intent; each harness translates AllowTools to its own permission
// mechanism at launch time.
type RunConfig struct {
	PermissionMode string   `toml:"permission_mode,omitempty"`
	AllowTools     []string `toml:"allow_tools,omitempty"`
}

// Registry is the merged, resolved configuration for one fam — the output of
// BuildRegistry (global defaults ⊕ the matched [repo.<k>] stanza ⊕ git-remote
// derivation). It is the in-memory shape every consumer reads; it is no longer
// unmarshalled directly from a file (the toml tags are vestigial).
type Registry struct {
	Name   string `toml:"name"`
	Slug   string `toml:"slug,omitempty"`
	Branch string `toml:"branch,omitempty"` // deprecated: use IntegrationBranch

	// IntegrationBranch is where bots open PRs (default: <slug>-next).
	// ReleaseBranch is the public release target — bots must never target it
	// unless explicitly instructed (default: main).
	IntegrationBranch string   `toml:"integration_branch,omitempty"`
	ReleaseBranch     string   `toml:"release_branch,omitempty"`
	RootSet           []string `toml:"root_set,omitempty"`
	Origin            string   `toml:"origin,omitempty"`
	Roster            []string `toml:"roster,omitempty"`
	Channels          []string `toml:"channels,omitempty"`
	RepoPaths         []string `toml:"repo_paths,omitempty"`
	ObjectStores      []string `toml:"object_stores,omitempty"`
	CreatedAt         string   `toml:"created_at,omitempty"`

	// ForgeURL is the HTTP(S) forge API base (e.g. http://gitea.home.rlupi.com:3000/).
	// Repository is the org/repo on the forge. Both are explicit in fam.toml so
	// nothing has to guess them from a (possibly SSH) git remote — see #184.
	ForgeURL   string `toml:"forge_url,omitempty"`
	Repository string `toml:"repository,omitempty"`

	// Agents and Users hold the `[agent.<name>]` / `[user.<name>]` tables, keyed
	// by worktree-directory name.
	Agents map[string]AgentConfig `toml:"agent,omitempty"`
	Users  map[string]AgentConfig `toml:"user,omitempty"`

	// Flags is the fam-wide feature-flag table ([flags] in fam.toml). It lets a
	// new codepath be toggled per-fam (and per-agent via AgentConfig.Flags)
	// without changing MCP env/settings — see wiki/ProposalFlagFlips and
	// ResolveFlags. Values may be bool, number, or string; FlagEnabled
	// interprets truthiness.
	Flags map[string]any `toml:"flags,omitempty"`

	// WikiProjections declares curated wiki indexes as "name:glob" entries (#120).
	WikiProjections []string `toml:"wiki_projections,omitempty"`

	// Run holds the resolved repo-wide defaults for `botfam run`; per-agent Run
	// overrides live on AgentConfig and are applied by the command after it has
	// resolved the effective actor/harness.
	Run RunConfig `toml:"run,omitempty"`
}

type ActorRole string

const (
	RoleAgent   ActorRole = "agent"
	RoleUser    ActorRole = "user"
	RoleBase    ActorRole = "base"
	RoleUnknown ActorRole = "unknown"
)

type Source string

const (
	SourceWorkDir     Source = "work_dir"
	SourceClientRoots Source = "client_roots"
	SourcePWD         Source = "pwd"
	SourceGitRoots    Source = "git_roots"
)

// FamIdentity holds the core identity components for a resolved family.
type FamIdentity struct {
	FamDir      string
	FamTOMLPath string
	Name        string
	Actor       string
	ActorRole   ActorRole
	Source      Source
}

// ResolvedFam is the single canonical identity for a worktree, resolved from the
// matched [repo.<k>] stanza in ~/.botfam/config.toml. Every consumer (forge
// client, discovery health, channels, pass-files) goes through ResolveFam so
// they cannot disagree about which fam/token/url applies — the root cause of #183.
type ResolvedFam struct {
	FamIdentity
	Slug         string
	WorktreeRoot string
	ForgeURL     string
	Repository   string
	TokenPath    string
	Agent        AgentConfig
	Registry     Registry

	// Flags is this agent's effective feature flags: the fam-wide [flags]
	// defaults overlaid with the agent's [agent.<name>.flags] overrides. Query
	// with FlagEnabled.
	Flags map[string]any
}

// FlagEnabled reports whether the named feature flag is truthy in this resolved
// fam's effective Flags, returning def when the flag is set nowhere. It errors
// when the flag IS set but its value does not cleanly convert to a boolean. See
// FlagEnabled (package func) for the accepted values.
func (rf ResolvedFam) FlagEnabled(name string, def bool) (bool, error) {
	return flagValue(rf.Flags, name, def)
}

// FamSlug returns the short id used in channels/ledger/pass-files: the explicit
// stanza slug when set, else the fam name.
func FamSlug(reg Registry) string {
	if reg.Slug != "" {
		return reg.Slug
	}
	return reg.Name
}

// FamScopedNick returns the fam-scoped IRC nick for an actor: "<actor>-<slug>"
// (e.g. "claude-botfam", "agy-dc"), so agents from different fams sharing the
// same actor name never collide on a shared IRC server (#137). It is idempotent
// (won't double-suffix) and returns the bare actor when no slug is resolvable.
// Lives in the famconfig leaf so famctx can derive ScopedNick without importing
// internal/fam (which would cycle); internal/fam re-exports it for callers.
func FamScopedNick(actor, famSlug string) string {
	if famSlug == "" || actor == "" {
		return actor
	}
	if strings.HasSuffix(actor, "-"+famSlug) {
		return actor
	}
	return actor + "-" + famSlug
}

// HarnessTokenPath returns the per-harness token path ~/.botfam/token-<harness>,
// keyed by the canonical harness name (see CanonicalHarness, defined in
// harness.go) so e.g. harness 'claude' and 'claude-code' resolve to the same
// token-claude-code (#371). Callers that have a live runtime should prefer
// ResolveHarness(...).Effective over a raw declared value.
func HarnessTokenPath(harness string) (string, error) {
	if harness == "" {
		return "", fmt.Errorf("harness is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".botfam", "token-"+CanonicalHarness(harness)), nil
}

// UserTokenPath returns the per-user forge token path ~/.botfam/token-<name> —
// the human-operator analogue of HarnessTokenPath. The name is literal (no
// harness canonicalization), so user 'rlupi' resolves to token-rlupi.
func UserTokenPath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("user name is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".botfam", "token-"+name), nil
}

// ResolveFam resolves the fam identity for workDir, fail-closed. It locates the
// git worktree root, treats its parent as the fam dir, resolves the merged
// Registry from ~/.botfam/config.toml (ResolveConfig), and requires the
// worktree's basename to be a declared `[agent.<name>]`. Every failure mode is a
// loud error carrying a "report to your operator" hint — no silent fallbacks
// (the #183/#362 invariant).
//
// Refusals: not inside a git worktree; no matching `[repo.<k>]` stanza; the
// worktree is a `[user.<name>]` (human) checkout; or it is not a declared agent
// (e.g. the `main`/base checkout). Callers that legitimately run outside an
// agent worktree (doctor/setup/whoami/version) must not gate on this.
func ResolveFam(workDir string) (ResolvedFam, error) {
	root, err := gitexec.One(workDir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ResolvedFam{}, fmt.Errorf("not inside a git worktree (%s); report this to your operator", workDir)
	}
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	famDir := filepath.Dir(root)
	repoName := ResolveRepoName(root)
	actor := ParseActor(filepath.Base(root), repoName)
	if actor == "" {
		actor = filepath.Base(root)
	}

	reg, err := ResolveConfig(workDir)
	if err != nil {
		return ResolvedFam{}, err
	}
	if _, isUser := reg.Users[actor]; isUser {
		return ResolvedFam{}, fmt.Errorf("worktree %q is a [user.%s] (human) checkout; the botfam runtime only runs in [agent.<name>] worktrees — report to your operator", actor, actor)
	}
	agent, ok := reg.Agents[actor]
	if !ok {
		return ResolvedFam{}, fmt.Errorf("worktree %q is not a declared [agent.<name>] for this repo (base checkout or unknown agent); the runtime refuses to start here — report to your operator", actor)
	}

	tokenPath, err := HarnessTokenPath(agent.Harness)
	if err != nil {
		return ResolvedFam{}, err
	}

	cfgPath, _ := ConfigPath()
	return ResolvedFam{
		FamIdentity: FamIdentity{
			FamDir:      famDir,
			FamTOMLPath: cfgPath,
			Name:        reg.Name,
			Actor:       actor,
			ActorRole:   RoleAgent,
			Source:      SourceWorkDir,
		},
		Slug:         FamSlug(reg),
		WorktreeRoot: root,
		ForgeURL:     reg.ForgeURL,
		Repository:   reg.Repository,
		TokenPath:    tokenPath,
		Agent:        agent,
		Registry:     reg,
		Flags:        ResolveFlags(reg, actor),
	}, nil
}

// ResolveFlags returns the effective feature flags for actor: the fam-wide
// [flags] table overlaid with the agent's [agent.<actor>.flags] overrides.
// Agent overrides win per key; an unknown actor yields just the fam defaults.
// The result is a fresh map and never nil.
func ResolveFlags(reg Registry, actor string) map[string]any {
	out := make(map[string]any, len(reg.Flags))
	for k, v := range reg.Flags {
		out[k] = v
	}
	if ac, ok := reg.Agents[actor]; ok {
		for k, v := range ac.Flags {
			out[k] = v
		}
	}
	return out
}

// FlagEnabled reports whether the named feature flag resolves truthy for actor
// in reg, returning def when the flag is set in neither the fam-wide [flags] nor
// the agent's overrides. It errors when the flag IS set but its value does not
// cleanly convert to a boolean (a likely typo) — callers should surface that
// rather than silently treating a misconfigured flag as off.
//
// Accepted values: a bool; any number (non-zero is true); or a string (after
// trim + lowercase) in {"1","true","t","on","yes","y"} (true) or
// {"0","false","f","off","no","n"} (false).
func FlagEnabled(reg Registry, actor, name string, def bool) (bool, error) {
	return flagValue(ResolveFlags(reg, actor), name, def)
}

// FlagFromMap reads name from an already-resolved effective flag map (e.g.
// famctx.Context.Flags), applying FlagEnabled's conversion rules and returning
// def when the flag is absent. It lets consumers that hold a pre-merged flag set
// share the one truthiness interpreter instead of re-deriving it.
func FlagFromMap(flags map[string]any, name string, def bool) (bool, error) {
	return flagValue(flags, name, def)
}

// flagValue looks name up in flags and converts it per FlagEnabled's rules,
// returning def when it is absent.
func flagValue(flags map[string]any, name string, def bool) (bool, error) {
	v, ok := flags[name]
	if !ok {
		return def, nil
	}
	b, err := parseFlagBool(v)
	if err != nil {
		return def, fmt.Errorf("config flag %q = %#v: %w", name, v, err)
	}
	return b, nil
}

// parseFlagBool converts a fam.toml flag value (go-toml yields bool/int64/
// float64/string) to a boolean, erroring on anything that is not unambiguously
// truthy or falsy.
func parseFlagBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case int64:
		return x != 0, nil
	case int:
		return x != 0, nil
	case float64:
		return x != 0, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "t", "on", "yes", "y":
			return true, nil
		case "0", "false", "f", "off", "no", "n":
			return false, nil
		}
		return false, fmt.Errorf("%q is not a boolean", x)
	default:
		return false, fmt.Errorf("unsupported flag type %T", v)
	}
}
