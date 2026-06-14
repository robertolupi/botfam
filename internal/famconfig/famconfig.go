// Package famconfig is the dependency-free leaf that owns fam.toml: its schema
// (Registry/AgentConfig), location (FindFamTOMLPath), parsing (ReadRegistry/
// WriteRegistry), and the canonical identity resolution (ResolveFam) plus the
// per-harness token path (HarnessTokenPath).
//
// It has NO internal/* dependencies, so both internal/fam and internal/forge
// import it instead of each other — breaking the fam→forge cycle that forced
// forge.NewClient and fam.Resolver to re-derive fam identity three different
// ways (#183, #231). internal/fam re-exports these via type aliases / thin
// wrappers, so existing callers are unaffected.
package famconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// AgentConfig is a single `[agent.<name>]` or `[user.<name>]` entry in fam.toml.
// The map key (and Name) is the worktree directory basename (the `wt-` prefix is
// retired). Email defaults to the host git email plus-addressed with Name.
// IsUser marks a `[user.<name>]` (human) entry — git identity only, no runtime.
type AgentConfig struct {
	Name      string `toml:"-"` // filled from the table key
	Harness   string `toml:"harness,omitempty"`
	ForgeUser string `toml:"forge_user,omitempty"`
	Email     string `toml:"email,omitempty"`
	IsUser    bool   `toml:"-"` // true for [user.<name>] entries
}

// Registry is the parsed fam.toml.
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
	// by worktree-directory name.
	Agents map[string]AgentConfig `toml:"agent,omitempty"`
	Users  map[string]AgentConfig `toml:"user,omitempty"`

	// WikiProjections declares curated wiki indexes as "name:glob" entries (#120).
	WikiProjections []string `toml:"wiki_projections,omitempty"`
}

// ResolvedFam is the single canonical identity for a worktree, resolved from
// `<fam-dir>/fam.toml`. Every consumer (forge client, discovery health,
// channels, pass-files) goes through ResolveFam so they cannot disagree about
// which fam/token/url applies — the root cause of #183.
type ResolvedFam struct {
	Name         string
	Slug         string
	Actor        string
	FamDir       string
	WorktreeRoot string
	ForgeURL     string
	Repository   string
	TokenPath    string
	Agent        AgentConfig
	Registry     Registry
}

// ReadRegistry parses the fam.toml at path, backfilling the canonical Name (and
// IsUser) onto each agent/user from its table key.
func ReadRegistry(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var reg Registry
	if err := toml.Unmarshal(data, &reg); err != nil {
		return Registry{}, fmt.Errorf("parse %s: %w", path, err)
	}
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

// WriteRegistry atomically writes reg as TOML to path.
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

// FamSlug returns the short id used in channels/ledger/pass-files: the explicit
// fam.toml slug when set, else the fam name.
func FamSlug(reg Registry) string {
	if reg.Slug != "" {
		return reg.Slug
	}
	return reg.Name
}

// HarnessTokenPath returns the per-harness token path ~/.botfam/token-<harness>.
func HarnessTokenPath(harness string) (string, error) {
	if harness == "" {
		return "", fmt.Errorf("harness is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".botfam", "token-"+harness), nil
}

// FindFamTOMLPath locates the canonical fam.toml for workDir, in priority order:
//
//  1. $COLLAB_ROOT/fam.toml (explicit override), when it exists;
//  2. <parent of the git worktree top-level>/fam.toml, when it exists.
//
// Returns "" when none is found. env is an os.Environ()-style "K=V" slice to
// read COLLAB_ROOT from; nil falls back to the process environment. This is the
// one fam.toml locator; ResolveFam (the strict agent path) and forge.NewClient
// (which also tolerates non-agent/legacy checkouts) both build on it.
func FindFamTOMLPath(workDir string, env []string) string {
	if cr := lookupEnv(env, "COLLAB_ROOT"); cr != "" {
		if p := filepath.Join(cr, "fam.toml"); fileExists(p) {
			return p
		}
	}
	if root, err := gitOne(workDir, "rev-parse", "--show-toplevel"); err == nil && root != "" {
		if eval, err := filepath.EvalSymlinks(root); err == nil {
			root = eval
		}
		if p := filepath.Join(filepath.Dir(root), "fam.toml"); fileExists(p) {
			return p
		}
	}
	return ""
}

// ResolveFam resolves the fam identity for workDir, fail-closed. It locates the
// git worktree root, treats its parent as the fam dir, reads `<fam-dir>/fam.toml`,
// and requires the worktree's basename to be a declared `[agent.<name>]`. Every
// failure mode is a loud error carrying a "report to your operator" hint — no
// silent fallbacks (the #183 disease).
//
// Refusals: not inside a git worktree; no/invalid fam.toml; the worktree is a
// `[user.<name>]` (human) checkout; or it is not a declared agent (e.g. the
// `main`/base checkout). Callers that legitimately run outside an agent worktree
// (doctor/setup/whoami/version) must not gate on this.
func ResolveFam(workDir string) (ResolvedFam, error) {
	root, err := gitOne(workDir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ResolvedFam{}, fmt.Errorf("not inside a git worktree (%s); report this to your operator", workDir)
	}
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	famDir := filepath.Dir(root)
	actor := filepath.Base(root)
	tomlPath := filepath.Join(famDir, "fam.toml")

	reg, err := ReadRegistry(tomlPath)
	if err != nil {
		return ResolvedFam{}, fmt.Errorf("no readable fam.toml at %s: run `botfam setup`; if it persists, report to your operator (%v)", tomlPath, err)
	}
	if _, isUser := reg.Users[actor]; isUser {
		return ResolvedFam{}, fmt.Errorf("worktree %q is a [user.%s] (human) checkout; the botfam runtime only runs in [agent.<name>] worktrees — report to your operator", actor, actor)
	}
	agent, ok := reg.Agents[actor]
	if !ok {
		return ResolvedFam{}, fmt.Errorf("worktree %q is not a declared [agent.<name>] in %s (base checkout or unknown agent); the runtime refuses to start here — report to your operator", actor, tomlPath)
	}

	tokenPath, err := HarnessTokenPath(agent.Harness)
	if err != nil {
		return ResolvedFam{}, err
	}

	return ResolvedFam{
		Name:         reg.Name,
		Slug:         FamSlug(reg),
		Actor:        actor,
		FamDir:       famDir,
		WorktreeRoot: root,
		ForgeURL:     reg.ForgeURL,
		Repository:   reg.Repository,
		TokenPath:    tokenPath,
		Agent:        agent,
		Registry:     reg,
	}, nil
}

// --- lightweight, dependency-free helpers (leaf package) ---------------------

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
