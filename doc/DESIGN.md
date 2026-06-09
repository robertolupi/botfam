# botfam — v0 Design Spec

Status: **Draft for review** · Transport: **stdio MCP** · Language: **Go**

A single Go binary that exposes a maildir-backed coordination plane to one agent
over stdio. Run one process per agent; they share a state directory and talk
through it. This spec is the contract for v0; the goal is a kernel small enough
to read in one sitting.

---

## 1. What botfam is (and is not)

**Is:** lightweight messaging + a lease-based task queue between a few agents,
served as MCP tools over stdio, backed by a shared maildir tree. The one
blocking operation is `recv` — an agent parks on it cheaply until a message
arrives instead of paying tokens to poll.

**Not, in v0:**
- No CCREP / consensus ledger — that is **Phase 2**, specified in
  [DESIGN_ccrep.md](DESIGN_ccrep.md). It stays a separate layer with the ledger
  quarantined to it, never in the messaging hot path.
- No HTTP transport, no shared server, no token auth. (That is **bottown**.)
- No git, no Gitea, no SSH keys, no PATs, no credential rotation.
- No Python, no venv, no path-discovery wrapper scripts.

---

## 2. Architecture

```
agent A harness ──stdio──> botfam (COLLAB_ACTOR=alice) ─┐
                                                        ├─> $COLLAB_ROOT (shared maildir)
agent B harness ──stdio──> botfam (COLLAB_ACTOR=bob)   ─┘
```

- **One process per agent.** Each agent's harness launches its own `botfam`
  via `.mcp.json`, with that agent's identity pinned in the environment.
- **Identity is cooperative by default, lockable on demand.** Every tool takes an
  optional `actor`; when present it selects the mailbox, otherwise `COLLAB_ACTOR`
  is the default. This is what lets several agents that **share one worktree (and
  therefore one `.mcp.json`)** each self-identify per call. An **out-of-repo**
  lock (§3, *Identity modes*) flips the server to ignore `actor` and trust only
  `COLLAB_ACTOR` — the anti-spoofing guarantee for one-process-per-agent setups.
  (Cryptographic identity proper arrives with tokens in bottown.)
- **Coordination = the filesystem.** All shared state lives under `$COLLAB_ROOT`.
  Atomicity comes from `os.Rename` within one filesystem; there is no broker.

---

## 3. Environment contract

| Var | Required | Default | Meaning |
|---|---|---|---|
| `COLLAB_ACTOR` | no | `claude` | default / fallback identity for this server |
| `COLLAB_ROOT` | no | *derived* (see *Coordination root*) | explicit override of the fam's coordination root |
| `BOTFAM_FAM` | no | — | name suffix to run two independent fams on one history |

`.mcp.json` (per agent):

```json
{
  "mcpServers": {
    "collab": {
      "command": "botfam",
      "env": { "COLLAB_ACTOR": "alice", "COLLAB_ROOT": "/abs/path/.botfam" }
    }
  }
}
```

Grant `mcp__collab__*` once; no further prompts.

### Coordination root

The maildir lives **outside any repo** — it is transient, must not be committed,
and must be shared by every worktree/clone whose agents form one *fam*. A
repo-local `.botfam/` fails that last point (worktrees wouldn't share it), so the
default is derived under `${BOTFAM_HOME:-~/.botfam}/`:

```
~/.botfam/<repo-slug>-<shortkey>[-<fam>]/      e.g. ~/.botfam/scriba-1a151b/
```

- `<repo-slug>` — the main worktree's directory name, taken from
  `git rev-parse --git-common-dir` so it is **identical across linked worktrees**
  (not from cwd, which differs per worktree).
- `<shortkey>` — first 6 of the **root commit** hash
  (`git rev-list --max-parents=0 HEAD`): **stable across every worktree *and*
  clone of the same history**, so separate per-agent clones (the scriba topology)
  land in one fam, while unrelated same-named repos do not collide. (Trade-off:
  forks of one history share a key — disambiguate with `BOTFAM_FAM`.)

Resolution order: `COLLAB_ROOT` (explicit, wins) › derived path, suffixed by
`BOTFAM_FAM` if set. Keep all three **out of `.mcp.json`** so a committed config
stays machine-agnostic and no worktree can fork the fam by accident — ideally
`.mcp.json` is just `{ "command": "botfam" }`.

`botfam setup` resolves and **creates** the root (plus a `fam.toml` roster) and
prints what it chose, so the fam dir is deliberate, not conjured on first message.
With no git and no `COLLAB_ROOT` there is no stable key to derive, so setup (or an
explicit `COLLAB_ROOT`) is required.

### Identity modes

Resolution, per call:

1. **Locked** (out-of-repo switch set) → identity = `COLLAB_ACTOR`; any call whose
   `actor` *differs* is rejected. No spoofing. For one-process-per-agent setups.
2. **Cooperative** (default) → the call's `actor` if present, else the **session
   actor**, else `COLLAB_ACTOR`.

**Bind-on-first-use:** under stdio each agent gets its own botfam process even when
agents share a worktree and a `.mcp.json`. So in cooperative mode the *first*
`actor` a process sees sticks as the session actor; the agent states its name once
and may omit it afterward. A per-call `actor` still overrides for that one call.
This keeps the shared-config case working **and** makes accidental
wrong-actor sends hard, without per-call boilerplate.

The lock lives **outside the repo** so a shared, committed `.mcp.json` can neither
grant nor revoke it:

```toml
# ${XDG_CONFIG_HOME:-~/.config}/botfam/config   (authoritative)
lock_actor = true
```

`BOTFAM_LOCK_ACTOR=1` is also honored — but do **not** put it in `.mcp.json`, which
would move the switch back into the repo and defeat the purpose.

---

## 4. State layout

All paths under `$COLLAB_ROOT`:

```
tmp/                       write-staging (rename source; never read directly)
<actor>/new/               delivered, unread     (this is what recv watches)
<actor>/cur/               read/processed        (audit trail; never deleted)
tasks/open/                postable work
tasks/claimed/<actor>/     atomically claimed
tasks/done/                completed (+ result)
```

**Filename:** `<zero-padded-unix-nanos>-<id>.json` so lexicographic sort ==
chronological order.

**Message envelope:**
```json
{ "id": "...", "from": "alice", "to": "bob", "type": "handoff",
  "payload": { }, "ts": 1733760000.0, "in_reply_to": "...", "expires_at": null }
```
`in_reply_to` and `expires_at` are optional. `type` is free-form (`handoff`,
`ack`, … — convention, not enforced). If `expires_at` is set and in the past when
a reader scans `new/`, the message is **not delivered** — it is swept aside
(`new/→cur/`, marked expired) so a stale handoff is never acted on late. Expiry is
lazy: enforced on `recv`/`peek`/`try_recv` scans, never by a daemon.

**Actor / topic names** become directory components, so they are restricted to
`[A-Za-z0-9_-]+`; `send`/`recv`/`actor` reject anything else (no path traversal,
no junk mailboxes).

**Task envelope:** the posted payload plus a lifecycle:
`status` (`open`→`claimed`→`done`), `owner`, `claimed_at`, `lease_expires_at`,
`result`, `completed_at`, and on release: `abandoned_*` / `swept_*`.

---

## 5. Tools

Mailbox:

| Tool | Blocking | Behavior |
|---|---|---|
| `send(to, type, payload, in_reply_to?)` | no | write-then-rename into `to`'s `new/`; returns the envelope |
| `recv(match_type?, timeout_s=120)` | **yes** | block until a (matching) message lands in own `new/`, then consume it (`new/→cur/`) and return it; return null on timeout |
| `try_recv(match_type?)` | no | oldest matching message, consumed (`new/→cur/`), or null |
| `peek(match_type?)` | no | oldest matching message **without consuming it** (stays in `new/`), or null |
| `inbox()` | no | read-only snapshot: pending `new/`, recent `cur/`, task counts |

`recv`/`try_recv` are **destructive-on-read** (at-most-once): a crash after the
`new/→cur/` move loses the message. `peek` is the non-consuming alternative — look
without taking. Full `ack`/redelivery is deliberately deferred (see §11).

Task queue:

| Tool | Behavior |
|---|---|
| `post(payload, type="task")` | enqueue into `tasks/open/` (sweeps expired leases in passing) |
| `claim(lease_ttl=120)` | rename one open task into own `claimed/`; exactly one claimer wins; stamps `lease_expires_at` (sweeps expired leases in passing) |
| `complete(task_id, result)` | move own claimed task → `tasks/done/` with `result` |
| `heartbeat(task_id, lease_ttl=120)` | extend the lease so a sweep won't reclaim |
| `abandon(task_id, reason)` | release own task back to `open/` with a reason |
| `sweep()` | explicit coordinator op: return every actor's expired-lease tasks to `open/` |

**Lease reclamation is lazy, no daemon.** An expired lease is reclaimed when any
agent next calls `claim` or `post` (they sweep in passing), or when a coordinator
calls `sweep` explicitly — so a crashed worker's task self-heals on the next queue
op rather than lingering forever.

Every tool also accepts an optional `actor?` — honored in cooperative mode
(per-call override; pins the session actor on first use), ignored/validated under
the lock (see §3, *Identity modes*).

Tasks are identified by `env["id"]`, never by filename (filenames carry the ns
prefix and differ across moves).

---

## 6. Blocking semantics (the point of botfam)

`recv` is the single blocking call, and that is deliberate: an agent spawns a
background turn, calls `recv(timeout_s=N)`, and consumes **zero** tokens until a
message wakes it — versus paying per poll. "Block until woken" is the lowest
common denominator every harness supports, including ones that cannot schedule
their own wakeups.

Implementation: a goroutine waits on an fsnotify watch of `<actor>/new/`; on any
event (or a ~1s safety tick that re-scans, so a missed event can't wedge it) it
re-runs `try_recv`. If fsnotify is unavailable, fall back to a 200 ms poll. The
deadline is honored on every wakeup; on expiry `recv` returns null, not an error.
The server also honors client cancellation: if the harness cancels the call, the
goroutine stops *without* consuming a message.

**`recv` is meant to be re-invoked in a loop.** Many harnesses cap a single
tool-call's duration, so pick `timeout_s` *below* that ceiling and call `recv`
again (typically from a background turn) — a null return just means "nothing yet,
ask again," not "give up." `timeout_s=120` is a default, not a guarantee your
client will wait that long.

---

## 7. Atomicity & concurrency

- **send / post:** stage in `tmp/`, then `os.Rename` to the destination — a
  reader in `new/` never sees a partial write.
- **recv / claim:** `os.Rename` of the source file is the lock. Exactly one
  caller wins; the loser gets `ENOENT` and moves on. No flock, no lease DB,
  no CAS retries — the rename *is* the compare-and-swap.
- Same-filesystem rename is required (keep `tmp/` and the mailboxes under one
  `$COLLAB_ROOT` on one volume).

---

## 8. Build & run

```bash
go build -o botfam ./cmd/botfam               # one static binary
cp botfam ~/bin/                              # or anywhere on PATH
botfam setup <project> --agents alice,bob     # create the fam root + roster, print it
```

No interpreter, no venv, no `PYTHONPATH`. Cross-compiles with `GOOS`/`GOARCH`.

`botfam setup <project> --agents a,b,c` (run once per project, from inside the
repo): resolve the coordination root (*Coordination root*, §3), create it and a
`fam.toml` roster from the named agents, and print the resolved path plus the
per-agent `.mcp.json` snippet. The roster is **advisory** in v0 — it documents the
fam and feeds setup's output, but does not gate delivery: `send` to an unlisted
actor still works (lazy mailbox creation). Enforcement waits for bottown's tokens.

**Open build-time choices** (decide when scaffolding, not now):
- MCP SDK: official `modelcontextprotocol/go-sdk` vs. `mark3labs/mcp-go`.
- fsnotify dependency vs. pure-stdlib poll (leaning: fsnotify + poll fallback).

---

## 9. Lineage

[v0 deep-cuts](lineage/v0-deep-cuts.md) (Python, stdio, maildir — proved the
model) → [v1 hydra](lineage/v1-hydra.md) (folded coordination into a hash-chained
ledger — too heavy) → [v2 scriba](lineage/v2-scriba.md) (Gitea-native — much too
heavy) → **botfam** (back to the maildir kernel, in Go) → **bottown** (HTTP,
shared server, token identity — later).

Each `lineage/` doc is a retrospective on a prior attempt: what it was, its pros
and cons, and the specific lessons that shaped botfam.

## 10. Phases

- **Phase 1 — `collab` (this doc):** maildir messaging + lease task queue over
  stdio MCP. The whole of botfam v0.
- **Phase 2 — CCREP ([DESIGN_ccrep.md](DESIGN_ccrep.md)):** the quality ratchet —
  a second stdio MCP server (`botfam ccrep`) with the consensus ledger isolated
  to it. Built *after* Phase 1 and, ideally, *coordinated over* Phase 1 — CCREP
  ratchets its own development into existence (DESIGN_ccrep §2).

Later phases (revision budgets, voting math, escalation) are deferred — see
DESIGN_ccrep §10.

The deliberate inheritance from deep-cuts is the two-layer split: this is only
the *coordination* layer. A quality ratchet (CCREP) returns later as an optional
second tool surface, never in the messaging hot path.

---

## 11. Open questions for review

For peer reviewers (agy, codex) — the decisions most worth a second opinion:

- **Destructive `recv` vs. `ack`/redelivery `[priority]`.** v0 ships
  destructive-on-read `recv`/`try_recv` (at-most-once) plus a non-consuming
  `peek`; full `ack` + redelivery is deferred. This was an explicit hydra scar
  ("destructive recv"), and MCP client cancellation makes the lost-message window
  real. Is `peek` + at-most-once enough for a trusted fam, or should v0 carry an
  `ack`-based exactly-once path from the start? **This is the one we most want
  challenged.**
- **Message TTL semantics.** `expires_at` is enforced lazily on scan (no daemon).
  Is silent sweep-to-`cur/` the right disposition for an expired message, or
  should expired mail land somewhere visible (a dead-letter / lost+found)?
- **Lazy lease reclamation.** Sweeping on `claim`/`post` (plus explicit `sweep`)
  replaces a daemon. Acceptable, or do idle fams need a periodic sweeper?
- **Cooperative identity.** Bind-on-first-use with an out-of-repo lock — does the
  trust model hold for your harness, or do you need the lock on by default?

## 12. Known limitations (v0, accepted)

- `<actor>/cur/` is an audit trail and **grows unbounded** — no auto-prune in v0.
- **Same local filesystem only.** Atomic-rename semantics do not hold over NFS;
  keep `$COLLAB_ROOT` (incl. `tmp/`) on one local volume.
- `inbox(other_actor)` exposes another actor's filenames and counts (not payloads)
  — fine within a trusted fam; revisited under bottown's tokens.
