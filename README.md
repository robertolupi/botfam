# botfam

A tiny, single-binary **CLI tool** that lets a "family" of AI agents
coordinate.

botfam is the lightweight successor to a lineage of multi-agent coordination
experiments (`deep-cuts/collab` → `hydra` → `scriba`; see the **Design
Lineage** page on the [wiki](wiki)). It keeps the ideas that proved out —
attributable messages, leased work, consensus-gated merges — and coordinates over Gitea/Forgejo.

______________________________________________________________________

## Architecture at a Glance

```
                                        ┌──> Gitea/Forgejo (git hub & consensus gate)
                                        │    - PR reviews & approvals (e.g., 2 for next, 3 for main)
                                        │    - Operator-only merges
Agent A (wt-agy)    ── botfam CLI ──────┤
Agent B (wt-claude) ── botfam CLI ──────┘
```

- **Gitea consensus**: Merges to shared branches (such as `botfam-next` or
  `main`) are governed by Gitea's native branch protections. This replaces the
  legacy custom `ccrep` consensus engine, enforcing consensus among multiple
  bots (or bots + human) by requiring a set number of independent approvals.
- **Agent tooling in the binary**: `wait` (legacy spool wake — being replaced by a supervisor; its ingester is
  off by default), `verify` (ephemeral build/test check), and `agent-docs`
  management.

## Why this shape?

1. **Lightweight & Portable**: one static Go binary.
2. **Confabulation resistance**: SHAs are pasted, never retyped; the merge gate rejects stale approvals (approvals die on new commits).
3. **Everything is reviewable**: coordination happens in a repository that is logged, reviewable, and tracked on Gitea.

______________________________________________________________________

## Developer Quickstart

### 1. Bootstrap a Multi-Agent Workspace

Initialize a repository with a roster of agent worktrees (e.g. `wt-agy`,
`wt-claude`, `wt-codex`):

```bash
botfam newfam <project-name> --agents agy,claude,codex
```

This command will:

- Set up the shared `~/.botfam/` project directories and registry (`fam.toml`).
- Add git worktrees for each agent on their respective branches
  (`agent/<agent>`) and the human operator (`human/<operator>`).
- Configure the harness settings (`.claude/settings.json`) to allow direct
  execution of `botfam` CLI commands.
- Generate agent documentation and configure git worktree identities.

### 2. Run Tests

```bash
# Standard test run
go test ./...

# Sandbox-isolated cache run
env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
```

### 4. Build the Binary

```bash
go build ./cmd/botfam
# macOS: codesign --force --sign - ~/bin/botfam
```

______________________________________________________________________

## Project history & self-improvement

The fam reviews itself: per-session transcripts and reviews live on the
[Gitea wiki](wiki) (see **Sessions** and **Reviews** index; these live in the
wiki because they don't govern architecture and so skip double-approval PRs —
botfam#55), and protocol proposals in `doc/protocol/` and `doc/proposals/`.
