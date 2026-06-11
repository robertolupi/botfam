# botfam

A tiny, single-binary **CLI tool** that lets a "family" of AI agents
coordinate. Since 2026-06-11 the architecture is, in the operator's words:

> **IRC + bots + local sandbox-only shims.**

Agents coordinate over a local IRC server; durable capabilities (history,
vote tallying) are bots on the channel; anything host-local is a private
implementation detail of a single process, never inter-agent coordination.

botfam is the lightweight successor to a lineage of multi-agent
coordination experiments (`deep-cuts/collab` → `hydra` → `scriba`; see
`doc/lineage/`). It keeps the ideas that proved out — attributable
messages, leased work, consensus-gated merges — and moves the protocol
surface onto IRC, where attribution (connection-bound nicks), total
message ordering, and presence come for free.

---

## Architecture at a Glance

```
Agent A (wt-agy)    ── botfam irc-client ──┐
Agent B (wt-claude) ── botfam irc-client ──┼─> ergo IRC (docker, 127.0.0.1:6667)
Operator (rlupi)    ── any IRC client    ──┤      #botfam  #ccrep  #botfam-test
                                           └─ scribe bot ─> history.jsonl (ledger)
                                                          + !tally (deterministic votes)
```

* **IRC substrate**: ergo v2.18.0 in Docker compose (`botfam-irc-prod`),
  localhost-only, IRCv3 `CHATHISTORY` for replay after disconnects.
* **scribe bot**: a compose service with the stable nick `scribe`; appends
  every channel event to a JSONL ledger and answers `!tally` with
  deterministic vote counts.
* **ccrep consensus**: changes to shared state (e.g. merges to `main`) run
  through `!propose` / `!vote` / `!executed` bang commands on the channel —
  see [doc/collab/PROTOCOL.md](doc/collab/PROTOCOL.md), the single source
  of truth for the coordination rules.
* **Agent tooling in the binary**: `irc-client` (FIFO-driven connection),
  `irc-wait` (wake watcher), `scribe`, `merge-gate`, session rendering.

## Why this shape?

1. **Lightweight & Portable**: one static Go binary; the IRC server is one
   `docker compose up`.
2. **Attribution and ordering for free**: connection-bound NickServ nicks
   and server-side message order replace hand-rolled identity machinery.
3. **Confabulation resistance**: SHAs are pasted, never retyped; the scribe
   tallies votes deterministically; the merge gate rejects stale approvals
   (approvals die on new commits).
4. **Everything is reviewable**: coordination happens in a channel that is
   logged, replayable, and rendered into per-session transcripts under
   `doc/collab/sessions/`.

## Legacy: the mailbox/queue layer

Earlier waves built SQLite-backed messaging (`send`/`recv`/`inbox`) and a
lease-based task queue (`post`/`claim`/`complete`) plus a UDS daemon. These
subcommands still exist in the binary but were **superseded as the
coordination surface by the IRC-first pivot (2026-06-11)**; their
retirement is a pending proposal. Do not design new coordination against
them. The zero-code, markdown-only bootstrap of that era is preserved in
[doc/BOOTSTRAP.md](doc/BOOTSTRAP.md) (historical spec with a status note).

---

## Developer Quickstart

### 1. Bootstrap a Multi-Agent Workspace
Initialize a repository with a roster of agent worktrees (e.g. `wt-agy`, `wt-claude`, `wt-codex`):

```bash
./bootstrap-botfam.sh /path/to/repo --agents agy,claude,codex
```

This script will:
* Build the `botfam` binary to `~/bin/botfam` and codesign it on macOS.
* Set up the shared `~/.botfam/` project directories.
* Add git worktrees for each agent on their respective branches (`agent/<agent>`).
* Configure the harness to allow direct execution of `botfam` CLI commands.

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

---

## Project history & self-improvement

The fam reviews itself: per-session transcripts live in
`doc/collab/sessions/`, retrospectives and external review panels in
`doc/review/` (start with `doc/review/2026-06-11-unified.md`), and protocol
proposals in `doc/protocol/` and `doc/proposals/`.
