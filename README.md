# botfam

> ⚠️ **Preliminary software, under active development.** botfam is experimental
> and changing rapidly; interfaces, layout, and behavior may break without
> notice. It is **not yet ready for general adoption** — expect rough edges and
> use at your own risk.

A tiny, single-binary **CLI tool** that lets a "family" of AI agents
coordinate. The architecture is **forge-first**: a self-hosted Git forge
(Gitea/Forgejo) is the canonical control plane. Issues, pull requests, reviews,
and the wiki are the shared, durable, attributable record; anything host-local
is a private implementation detail of a single process, never inter-agent
coordination.

Agents claim work as issues, do it on their own branches, and open pull
requests; merges to shared branches are gated by the forge's native branch
protections. Coordination, history, and attribution all live in the forge — no
separate messaging substrate to run or trust.

botfam is the lightweight successor to a lineage of multi-agent coordination
experiments (`deep-cuts/collab` → `hydra` → `scriba`; see the **Design
Lineage** page on the [wiki](wiki)). It keeps the ideas that proved out —
attributable messages, leased work, consensus-gated merges — and moves the
protocol surface onto the forge, where attribution (authenticated commits and
comments), durable ordering, and review state come for free.

______________________________________________________________________

## Architecture at a Glance

```
                                        ┌──> Issues      (claimable, leased work)
Agent A (wt-agy)    ── botfam CLI ──────┤    Pull requests + reviews/approvals
Agent B (wt-claude) ── botfam CLI ──────┼──> Gitea/Forgejo  (e.g., 2 for next, 3 for main)
Operator (rlupi)    ── git / web UI ────┤    Wiki         (sessions, reviews, design)
                                        └──> Branch protections (operator-only merges)
```

- **Forge consensus**: Merges to shared branches (such as `botfam-next` or
  `main`) are governed by the forge's native branch protections. This replaces
  the legacy custom `ccrep` consensus engine, enforcing consensus among
  multiple bots (or bots + human) by requiring a set number of independent
  approvals.
- **Forge as control plane**: Issues are the unit of claimable, leased work;
  pull requests and their reviews carry the coordination and decision record;
  the wiki holds sessions, reviews, and design proposals.
- **Agent tooling in the binary**: `wait` (forge wake loop — blocks until
  queued work arrives), `verify` (ephemeral build/test check), and `agent-docs`
  management. The forge is reachable from the CLI and from the bundled MCP
  server as `forge_*` subtools.

## Why this shape?

1. **Lightweight & Portable**: one static Go binary against a standard,
   self-hostable Git forge — nothing bespoke to operate.
2. **Attribution and ordering for free**: authenticated commits, comments, and
   review events replace hand-rolled identity machinery.
3. **Confabulation resistance**: SHAs are pasted, never retyped; the merge gate
   rejects stale approvals (approvals die on new commits).
4. **Everything is reviewable**: coordination happens as issues, pull requests,
   and reviews, with per-session transcripts and reviews rendered on the
   [forge wiki](wiki) (see its **Sessions** index).

______________________________________________________________________

## Developer Quickstart

### 1. Bootstrap a Multi-Agent Workspace

Initialize a repository with a roster of agent worktrees (e.g. `wt-agy`,
`wt-claude`, `wt-codex`):

```bash
botfam newfam <project-name> --agents agy,claude,codex
```

This command will:

- Set up the shared `~/.botfam/` project directories and registry.
- Add git worktrees for each agent on their respective branches
  (`agent/<agent>`) and the human operator (`human/<operator>`).
- Configure the harness settings (`.claude/settings.json`) to allow direct
  execution of `botfam` CLI commands.
- Generate agent documentation and configure git worktree identities.

### 2. Point botfam at a forge

botfam talks to a self-hosted Gitea/Forgejo instance for issues, pull requests,
reviews, and the wiki. Configure the forge endpoint and per-agent tokens in
`~/.botfam/config.toml`, then confirm connectivity:

```bash
botfam whoami        # resolves this worktree's agent identity
botfam wait          # blocks until queued forge work (issues/PRs) arrives
```

A hermetic test forge for integration runs is available via
`docker/bootstrap-test-forgejo.sh`.

### 3. Run Tests

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
[forge wiki](wiki) (see **Sessions** and **Reviews** index; these live in the
wiki because they don't govern architecture and so skip double-approval PRs —
botfam#55), and protocol proposals in `doc/protocol/` and `doc/proposals/`.
