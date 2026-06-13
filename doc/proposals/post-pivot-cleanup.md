---
authors:
  - claude
  - rlupi
kind: proposal
status: Draft
created: 2026-06-13
proposal-id: post-pivot-cleanup-v1
executor: TBD
quorum: majority
deadline: none
---

# Proposal: Post-Pivot Command Cleanup (retire the SQLite daemon substrate)

> [!NOTE]
> **Status**: Draft (2026-06-13). Planned with the Operator; needs agy's review
> before any phase is proposed for execution. Each phase below lands as its own
> separate ccrep proposal — this doc is the umbrella plan, not a single merge.

- **Participants:**
  - Roberto Lupi (Operator) — direction, decisions
  - Claude (Agent, `wt-claude`) — this draft, code archaeology
  - agy (Agent, `wt-agy`) — review (pending)
- **Scope:** classify every `botfam` subcommand as keep / retire / evolve after
  the [IRC-first pivot](irc-first-collab.md), and lay out a dependency-ordered
  roadmap to remove the legacy SQLite/UDS daemon substrate described in
  [sqlite-collab-design.md](sqlite-collab-design.md). Supersedes that design's
  role as the live coordination store.

______________________________________________________________________

## 1. Rationale: two parallel stacks, only one pivoted

Tracing the code shows **two complete consensus implementations** running side
by side:

| Capability   | Pre-pivot (daemon/SQLite)                                 | Post-pivot (IRC)                                      |
| ------------ | --------------------------------------------------------- | ----------------------------------------------------- |
| Propose      | `propose` → `sendDaemonRequest("session_append")` → store | `irc-propose` / `!propose` → scribe → `history.jsonl` |
| Vote/Approve | `vote`, `approve` → store                                 | `!vote` in `#ccrep` → scribe                          |
| Tally        | `tally` → `CollectCcrepEvents(store)`                     | `!tally` → scribe → `TallyProposal(ledger)`           |
| Merge gate   | `CollectCcrepEvents(store)`                               | `merge-gate` → `CollectIrcCcrepEvents(ledger)`        |

The decisive detail: **`merge-gate` already reads the IRC ledger**, not the
daemon store. The IRC path is the complete, authoritative loop (propose →
scribe → tally → gate), all off `history.jsonl` with NickServ identity. The
daemon consensus verbs are a fully duplicated, **unbridged** dead path —
confirmed when an Operator `botfam propose` wrote to the SQLite store and never
reached `#ccrep` or any agent. The
[consent-model](uds-daemon-voting-consent.md) work already concluded "the
daemon is perf-only"; this proposal acts on that.

## 2. The layering principle

The agent-facing architecture has four roles, kept strictly separate:

- **MCP tools = immediate write/commands** (write, propose, vote, claim,
  heartbeat) — fire and return. Preferred over CLI for bots: CLI argv payloads
  defeat harness auto-approval, and typed tool params remove shell-quoting /
  stringly-typed errors.
- **MCP resources = reads** under a `botfam://` namespace — the symmetric read
  side of the tool write side. See §5.
- **I/O (log tail, FIFO, wake watcher) = polling/waiting** — the async
  notification channel; holds no tool slot open.
- **IRC = the log** — durable, human-readable, multi-bot audit stream and the
  source of truth.

CLI subcommands remain for humans and debugging; they are not the agent
surface.

## 3. Command classification

- **A — Keep (post-pivot core / transport-neutral):** `irc-client`, `irc-wait`,
  `irc-propose`, `scribe`, `merge-gate`, `irclog2sessions`, `agent-docs`,
  `setup`, `serve` (slimmed).
- **B — Retire (consensus duplicates, superseded by `#ccrep`):** `propose`,
  `vote`, `tally`, `approve`, `merge`, plus the legacy
  `CollectCcrepEvents(store)` reader and its tests.
- **C — Retire (the host-local substrate):** `server` (UDS/TCP daemon) and the
  actor message bus `send`, `recv`, `try-recv`, `peek`, `ack`, `seen`, `post`,
  `inbox` — and their MCP tools. IRC channels + DMs (`irc_*` MCP tools)
  replaced all of it.
- **D — Evolve (no IRC equivalent yet):** `claim`, `complete`, `heartbeat`,
  `abandon`, `sweep` — task/lease coordination. See §6.
- **E — Evolve (orthogonal to consensus):** `session`, `session-append`,
  `session-read` — durable handoff log. Decouple from consensus events, re-back
  on the ledger.

## 4. Backing store: the scribe ledger

The scribe's out-of-repo `history.jsonl` is already the backing store for
consensus (`merge-gate` folds it directly). Route tasks (D) and sessions (E)
through the same ledger. Decisions:

- **Scribe stays thin** — append-only. It does *not* become a query server.
- **The "API" = in-process projection functions** that fold the ledger (the
  `merge-gate` / `TallyProposal` pattern), invoked by whatever reads (MCP
  tools, CLI, gate). One open question for review: is the scribe answering
  `!tally` in-channel the model to extend, or the exception to retire in favour
  of fold-on-read?
- **Compaction/snapshots = a separate janitor job**, not a scribe duty (prior
  art: deep-cuts `merge_sessions.py`). Mitigates O(n) ledger folds as history
  grows.
- ergo's `CHATHISTORY` already persists channels, so the ledger is rebuildable
  from the server or raw client logs — durability does not depend on the scribe
  being up.

## 5. Read-side resource API (`botfam://`)

Reads are MCP **resources**, the mirror of the write-side tools (§2): writes =
tools, reads = resources, waiting = I/O, durability = the ledger. Today agents
peek at peer files with raw `cat` across worktrees — no boundary, no shared
projection layer. The resource namespace fixes both.

- **Authority = fam selector** (the `file://` pattern). An **empty authority**
  means *this* fam: `botfam:///docs/protocol`, `botfam:///tasks`. A **named
  authority** addresses another fam: `botfam://deep-cuts/docs/...`. Local vs
  cross-fam is visible right in the URI, and cross-fam reads are exactly the
  ones the boundary below must gate.
- **Projections, not files.** `botfam:///tasks` and `botfam:///issues` are not
  files — they are the in-process fold of the ledger (the §4 "API = projection
  functions" pattern) surfaced as resources, so every reader stops
  reimplementing the fold.
- **Mediated file access.** `botfam:///files/...` (and the cross-fam
  `botfam://<fam>/files/...`) is the one sanctioned way to read across
  worktrees. It **MUST** sandbox paths — scoped to fam roots plus permitted
  read-only cross-fam roots, rejecting `../` traversal (reuse the
  `ValidateHistoryPath` pattern). Without this it is an arbitrary-file-read
  hole, not a feature.
- **Namespace coherence.** Every resource lives under this one scheme; no flat
  sibling URIs. agy's `botfam://protocol` + `botfam://ops` were the first
  resources, folded to `botfam://fam/docs/*` in
  `botfam-cli-worktree-commands-v1` (merged); adopting the authority-selector
  form migrates those to `botfam:///docs/*` — cheap now, before more resources
  accrete.

## 6. Async task protocol (bucket D)

Tasks become structured events on a `#tasks` log; the scribe records them.
**Ownership is computed from the log by a deterministic resolver, never
asserted by the claimant** (the same property that makes ccrep safe).

Events: `task:open {id, scope, guard}`, `task:claim {id, by, ts}`,
`task:ack {id, by, claimant}`, `task:heartbeat {id, by}`,
`task:complete {id, by, result}`, `task:abandon {id, by, reason}`.

Resolver: `holder(id)` = earliest valid `claim` whose guard is satisfied
**and** whose lease (last heartbeat + TTL) has not expired.

**Anti-overstepping** (governance/pacing, not crypto):

1. **Act-gate.** An agent may begin work only after reading back
   `holder(id) == self` from the log. Claiming is intent; speed buys nothing.

2. **Per-task `guard` ladder** — escalating trust to unlock the act-gate:

   | guard    | unlocks when…                   | use for                               |
   | -------- | ------------------------------- | ------------------------------------- |
   | `none`   | claimant wins the resolution    | cheap, reversible                     |
   | `ack`    | any one peer acks               | needs a cross-check                   |
   | `quorum` | quorum of peers ack             | group consensus                       |
   | `human`  | a verified human-tier nick acks | risky / irreversible / outward-facing |

   `human` acks bind to the `(task, claimant)` pair and are one-shot — they die
   on re-claim, mirroring ccrep's "approvals die on new commits". This is the
   hard brake: no amount of agent eagerness or agent consensus opens a `human`
   gate.

3. **Lease TTL + heartbeat + `sweep`** self-heal a claimant that grabs and then
   stalls or crashes.

**Identity:** the `human` guard requires a **nick→tier roster** (human vs
agent) declared in `fam.toml`, validated against NickServ ("is logged in as").
Without it an agent could self-authorize a risky action.

## 7. Roadmap (one surface per step, dependency-ordered)

The daemon backs everything legacy, so it dies **last**:

1. **Consensus → MCP-over-IRC.** Add `ccrep_propose/vote/tally` tools (thin
   `irc_write` wrappers, as `irc-propose` already is). Retire bucket B. Daemon
   stays.
2. **Retire the message bus** (bucket C, minus `server`): delete
   `send/recv/try-recv/peek/ack/seen/post` + their MCP tools.
3. **Read-side resources (§5).** Add the `botfam:///files/...` sandboxed reader
   - the authority-as-fam-selector namespace; surface existing folds as
     resources (docs already done — migrate to `botfam:///docs/*`). The `tasks`
     / `issues` projections attach in steps 4–5 as those buckets land on the
     ledger.
4. **Port tasks (D)** → `#tasks` ledger + resolver + guard ladder + `fam.toml`
   roles. Remove daemon task code.
5. **Port sessions (E)** → ledger + janitor compaction. Remove daemon session
   code.
6. **Retire the daemon (`server`)** — nothing depends on it.

Each step is its own ccrep proposal with its own review + tests.

## 8. Spawned follow-ups (out of the daemon spine, tracked here)

Cleanup items thrown off by `botfam-cli-worktree-commands-v1` (merged), parked
here so they are not lost:

- **Reconcile `tools/setup-worktree-identity.sh`.** `worktree init` now derives
  per-worktree git identity dynamically; the shell script duplicates that
  logic. Reconcile or explicitly supersede/delete it.
- **CLI-vs-MCP surface for `worktree init` / `worktree sync`.** These are
  agent- run operations, so per the layering principle (§2) they likely want
  MCP-tool surfaces, not just CLI. Decide before they calcify as CLI-only.

## 9. Non-goals

- `git push origin main` stays a **manual Operator step** — not automated, not
  a `guard: human` agent task. Agents stop at the local `merge --no-ff` +
  `!executed` and hand the push to the Operator.
- No data migration: there is no live SQLite consensus state to preserve (the
  IRC ledger is already authoritative). Maildir/store directories are deleted
  as a manual step once each phase is verified.
