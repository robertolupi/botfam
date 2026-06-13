# botfam

A tiny, single-binary **CLI tool** that lets a "family" of AI agents
coordinate. Since 2026-06-11 the architecture is, in the operator's words:

> **IRC + bots + local sandbox-only shims.**

Agents coordinate over a local IRC server; durable capabilities (history, vote
tallying) are bots on the channel; anything host-local is a private
implementation detail of a single process, never inter-agent coordination.

botfam is the lightweight successor to a lineage of multi-agent coordination
experiments (`deep-cuts/collab` → `hydra` → `scriba`; see `doc/lineage/`). It
keeps the ideas that proved out — attributable messages, leased work,
consensus-gated merges — and moves the protocol surface onto IRC, where
attribution (connection-bound nicks), total message ordering, and presence come
for free.

______________________________________________________________________

## Architecture at a Glance

```
                                        ┌──> Gitea/Forgejo (git hub & consensus gate)
                                        │    - PR reviews & approvals (e.g., 2 for next, 3 for main)
                                        │    - Operator-only merges
Agent A (wt-agy)    ── botfam CLI ──────┼──> local IRC (ergo docker, 127.0.0.1:6667)
Agent B (wt-claude) ── botfam CLI ──────┤    #botfam  #botfam-test
Operator (rlupi)    ── any IRC client ──┤    └─ scribe bot ─> history.jsonl (ledger)
```

- **Gitea consensus**: Merges to shared branches (such as `botfam-next` or
  `main`) are governed by Gitea's native branch protections. This replaces the
  legacy custom `ccrep` consensus engine, enforcing consensus among multiple
  bots (or bots + human) by requiring a set number of independent approvals.
- **IRC substrate**: ergo v2.18.0 in Docker compose (`botfam-irc-prod`),
  localhost-only, IRCv3 `CHATHISTORY` for replay after disconnects.
- **scribe bot**: A compose service with the stable nick `scribe`; appends
  every channel event to a JSONL ledger for reviewability and session
  transcripts.
- **Agent tooling in the binary**: `irc-client` (FIFO-driven connection),
  `irc-wait` (wake watcher), `verify` (ephemeral build/test check), and
  `agent-docs` management.

## Why this shape?

1. **Lightweight & Portable**: one static Go binary; the IRC server is one
   `docker compose up`.
2. **Attribution and ordering for free**: connection-bound NickServ nicks and
   server-side message order replace hand-rolled identity machinery.
3. **Confabulation resistance**: SHAs are pasted, never retyped; the scribe
   tallies votes deterministically; the merge gate rejects stale approvals
   (approvals die on new commits).
4. **Everything is reviewable**: coordination happens in a channel that is
   logged, replayable, and rendered into per-session transcripts under
   `doc/collab/sessions/`.

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
- Configure the harness settings (`.claude/settings.json`) to allow direct execution of `botfam` CLI commands.
- Generate agent documentation and configure git worktree identities.

### 2. Start the IRC substrate

```bash
# production (persistent, localhost-only)
docker compose -f docker/prod/compose.yaml up -d

# hermetic test substrate (never touches prod; port 16667)
docker/test-substrate.sh
```

See [docker/README.md](docker/README.md) for the operational contract.

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

The fam reviews itself: per-session transcripts live in `doc/collab/sessions/`,
retrospectives and external review panels in `doc/review/` (start with
`doc/review/2026-06-11-unified.md`), and protocol proposals in `doc/protocol/`
and `doc/proposals/`.
