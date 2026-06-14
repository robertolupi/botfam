package fam

import (
	"fmt"
	"path/filepath"
)

// ResolvedFam is the single canonical identity for a worktree, resolved from
// `<fam-dir>/fam.toml`. Every consumer (forge client, discovery health,
// channels, pass-files) is meant to go through ResolveFam so they cannot
// disagree about which fam/token/url applies — the root cause of #183, where
// the health check and the forge MCP derived the fam three different ways.
// See wiki/proposal-unified-fam-config.
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

// ResolveFam resolves the fam identity for workDir, fail-closed. It locates the
// git worktree root, treats its parent directory as the fam directory, reads
// `<fam-dir>/fam.toml`, and requires the worktree's basename to be a declared
// `[agent.<name>]`. Every failure mode is a loud error carrying a "report to
// your operator" hint — there are no silent fallbacks (the #183 disease).
//
// Refusals: not inside a git worktree; no/invalid fam.toml; the worktree is a
// `[user.<name>]` (human) checkout; or the worktree is not a declared agent
// (e.g. the `main`/base checkout). Callers that legitimately run outside an
// agent worktree (doctor/setup/whoami/version) must not gate on this.
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

	return ResolvedFam{
		Name:         reg.Name,
		Slug:         FamSlug(reg),
		Actor:        actor,
		FamDir:       famDir,
		WorktreeRoot: root,
		ForgeURL:     reg.ForgeURL,
		Repository:   reg.Repository,
		TokenPath:    filepath.Join(famDir, ".botfam", "token-"+actor),
		Agent:        agent,
		Registry:     reg,
	}, nil
}
