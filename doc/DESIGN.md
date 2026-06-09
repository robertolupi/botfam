# botfam — v0 Design Spec

Status: **Draft — review round 1 incorporated** · Transport: **stdio MCP** · Language: **Go**

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
  optional `actor`; when present it selects the mailbox, otherwise the session
  actor pinned on first use applies. There is **no silent default** — identity must
  be declared via `COLLAB_ACTOR` or the first call. This lets several agents that
  **share one worktree (and therefore one `.mcp.json`)** each self-identify per
  call. An **out-of-repo** lock (§3, *Identity modes*) flips the server to ignore
  `actor` and trust only `COLLAB_ACTOR` — the anti-spoofing guarantee for
  one-process-per-agent setups. (Cryptographic identity proper arrives with tokens
  in bottown.)
- **Coordination = the filesystem.** All shared state lives under `$COLLAB_ROOT`.
  Atomicity comes from `os.Rename` within one filesystem; there is no broker.

---

## 3. Environment contract

| Var | Required | Default | Meaning |
|---|---|---|---|
| `COLLAB_ACTOR` | no* | — | this server's identity. *No silent default: identity must come from `COLLAB_ACTOR` or the first call's `actor`, else mailbox ops are refused. |
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
default is **content-addressed by the repo's root commit** under
`${BOTFAM_HOME:-~/.botfam}/`:

```
~/.botfam/fam-<rootcommit12>[-<BOTFAM_FAM>]/     authoritative dir
~/.botfam/<name> -> fam-<rootcommit12>           cosmetic symlink (setup-created)
```

- `<rootcommit12>` — first 12 of the **root commit** hash
  (`git rev-list --max-parents=0 HEAD | tail -1`). Identical across every worktree
  *and* clone of the same history, so linked worktrees and separate per-agent
  clones (the scriba topology) all resolve to the **same** fam — the whole point.
  (Earlier drafts keyed on a `<repo-slug>` from the local directory name; that
  silently *broke* clone-sharing, since the dir name differs per clone.
  Content-addressing fixes it.)
- `<name>` is a human-readable symlink `botfam setup` creates for browsing; it is
  **not** authoritative, so it cannot fork the fam.

Resolution order: `COLLAB_ROOT` (explicit, wins) › `fam-<rootcommit12>`, suffixed
by `BOTFAM_FAM` if set. Keep all of these **out of `.mcp.json`** so a committed
config stays machine-agnostic — ideally `.mcp.json` is just `{ "command": "botfam" }`.

**Collisions are caught at setup, not papered over at runtime.** Two unrelated
repos can share a root commit (forks, or repos cut from one template). `botfam
setup` writes `fam.toml` recording `{name, root_commit, origin?}`; if a fam already
exists at that root commit under a *different* `name` (or a different git `origin`,
when both have one), setup **refuses without `--force` or a distinct `BOTFAM_FAM`**.
At runtime the server only *warns* (stderr) on an `origin` mismatch — never blocks.
This keeps clone-sharing automatic while making accidental fork cross-talk loud and
operator-gated. (`origin` is a disambiguation *hint* only; botfam still requires no
remote.)

With no git and no `COLLAB_ROOT` there is no stable key to derive, so `botfam
setup` (or an explicit `COLLAB_ROOT`) is required.

> **F2 is under active review.** This content-addressed scheme is the author's
> current answer to the clone-share / fork-isolate tension — see §11.

### Identity modes

Resolution, per call:

1. **Locked** (out-of-repo switch set) → identity = `COLLAB_ACTOR`; any call whose
   `actor` *differs* is rejected. No spoofing. For one-process-per-agent setups.
2. **Cooperative** (default) → the call's `actor` if present, else the **session
   actor**, else `COLLAB_ACTOR`. If none of these is set, the op is **refused** —
   no silent default.

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

**Hardening (review round 1).** No silent default identity (above), and the session
bind is **sticky** — once a process is bound, a *conflicting* later `actor` is
rejected even in cooperative mode, preventing identity drift. (The
"recycled / shared process" race a reviewer might fear is an HTTP/daemon threat
model; under stdio each client spawns its *own* server process, so it does not
arise here — it returns as a real concern in bottown.)

---

## 4. State layout

All paths under `$COLLAB_ROOT`:

```
tmp/                       write-staging (rename source; never read directly)
<actor>/new/               delivered, unread          (this is what recv watches)
<actor>/processing/        recv'd, awaiting ack       (rolled back to new/ on restart)
<actor>/cur/               acked / processed          (audit trail; never deleted)
<actor>/expired/           TTL-expired, undelivered   (visible dead-letter)
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
a reader scans `new/`, the message is **not delivered** — it is moved to
`<actor>/expired/` (a visible dead-letter, **not** mixed into `cur/`) so a stale
handoff is never acted on late. Expiry is lazy: enforced on `recv`/`peek`/`try_recv`
scans, never by a daemon.

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
| `recv(match_type?, timeout_s=120)` | **yes** | block until a (matching) message lands in own `new/`, then **reserve** it (`new/→processing/`) and return it; return null on timeout |
| `try_recv(match_type?)` | no | oldest matching message, reserved (`new/→processing/`), or null |
| `peek(match_type?)` | no | oldest matching message **without reserving it** (stays in `new/`), or null |
| `ack(id)` | no | confirm a reserved message processed (`processing/→cur/`); without it the message is redelivered after a crash |
| `inbox()` | no | read-only snapshot: pending `new/`, in-flight `processing/`, recent `cur/`, task counts |

**Delivery is at-least-once (review round 1).** `recv`/`try_recv` *reserve* a
message into `processing/` and return it; the consumer calls `ack(id)` once it has
durably acted, moving it to `cur/`. A crash before `ack` leaves the message in
`processing/`, where the actor's next server start **rolls it back to `new/`** for
redelivery (§7). Because redelivery means a message can arrive twice, **consumers
must dedup on `id`** (idempotent handling). `peek` reserves nothing — look without
taking.

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
agent next calls `claim`/`post` (they sweep in passing), when a **blocked `recv`
wakes on its safety tick** (so even an all-idle fam self-heals — §6), or when a
coordinator calls `sweep` explicitly. A crashed worker's task returns to `open/`
rather than lingering forever.

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
goroutine stops *without* reserving a message.

**The safety tick also sweeps leases (review round 1).** On each ~1s re-scan the
waiter runs the lazy lease sweep, so an all-idle fam — every agent blocked on
`recv`, nobody calling `claim`/`post` — still reclaims a crashed worker's expired
task. This closes the idle-deadlock that pure on-`claim`/`post` sweeping left open.

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
- **ack / rollback:** `recv` reserves into `processing/`; `ack` moves it to `cur/`.
  On startup an actor reclaims its own stale `processing/` back to `new/` (crash
  redelivery). "One live server per actor" is an **enforced invariant**, not an
  assumption: a server takes an exclusive advisory `flock` on `<actor>/.lock` when
  its identity binds, and **`recv`, `ack`, and the startup rollback require holding
  it**. A second concurrent server for the same actor (two IDE windows, a stray CLI)
  fails to acquire the lock and refuses receive ops with a loud error, rather than
  racing the rollback and silently losing in-flight mail. Sends only touch *other*
  actors' `new/`, so they are unaffected and need no lock.
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
repo): resolve the content-addressed root (§3), create it, write `fam.toml`
(`name`, `root_commit`, `origin?`, roster), make the `~/.botfam/<project>` symlink,
and print the resolved path plus the per-agent `.mcp.json` snippet. It **refuses on
a name/origin collision** (a different fam already at that root commit) unless given
`--force` or a distinct `BOTFAM_FAM`. The roster is **advisory** in v0 — it
documents the fam but does not gate delivery: `send` to an unlisted actor still
works (lazy mailbox creation). Enforcement waits for bottown's tokens.

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

**Resolved in review round 1** (reviewer: agy, Gemini family):

- Destructive `recv` → **at-least-once** via `ack` + `processing/` + dedup-on-`id` (§5, §7).
- Message TTL disposition → **visible `expired/` dead-letter**, not mixed into `cur/` (§4).
- Idle-fam lease deadlock → the **`recv` safety tick also sweeps leases** (§5, §6).
- Identity foot-guns → **no silent default** + **sticky bind** (§3).

**Still open — this round:**

- **F2 — fam keying `[priority]`.** A reviewer pushed for a path-hash key (isolate
  clones, distinguish forks); the author rejected it because **clone-sharing is the
  deliberate model** (the scriba per-agent-clone topology) — but the push exposed a
  real bug: the old `<repo-slug>` component silently *broke* clone-sharing. Current
  answer: **content-address the fam by root commit, catch fork/template collisions
  at `setup` (with an optional `origin` hint), warn at runtime** (§3). Does this
  hold, or is there a cleaner key? **Most-wanted challenge this round.**
- **Identity trust model.** Bind-on-first-use (now sticky, no silent default) with
  an out-of-repo lock — right default for your harness, or should the lock be on by
  default?

## 12. Known limitations (v0, accepted)

- `<actor>/cur/` is an audit trail and **grows unbounded** — no auto-prune in v0.
- **Same local filesystem only.** Atomic-rename semantics do not hold over NFS;
  keep `$COLLAB_ROOT` (incl. `tmp/`) on one local volume.
- `inbox(other_actor)` exposes another actor's filenames and counts (not payloads)
  — fine within a trusted fam; revisited under bottown's tokens.
