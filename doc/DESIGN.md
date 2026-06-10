# botfam — v0 Design Spec

Status: **Approved** (agy + codex review; agy APPROVE on Option C, now incorporated) · Transport: **stdio MCP** · Language: **Go**

A single Go binary that exposes a maildir-backed coordination plane to one agent
over stdio. Run one process per agent; they share a state directory and talk
through it. This spec is the contract for v0; the goal is a kernel small enough
to read in one sitting.

**Current implementation note.** The checked-in Go code implements the Phase 1
tool surface, maildir store, session identity binding, per-actor receive lock,
setup command, and subprocess MCP integration tests. Additionally, the **Wave 1**
milestone features are implemented:
- **Claim ergonomics** (claim-by-id targeting and `type`/`suggested_owner` filters) are fully functional.
- **CCREP Merge Gate** (`botfam merge-gate --commit <sha> --proposal <id>`) is implemented, enforcing that merges to `main` have independent approvals bound to the exact commit SHA.
- **Session close promotion** with clean git working directory checks and interactive commits is fully functional.

A few spec details are still early: `recv` uses polling instead of `fsnotify`, and `fam.toml` parsing is minimal. The remaining Tier 1 receive ergonomics (`match_from`/`match_reply_to` filters and the `thread` tool, §5) are specified but not yet implemented. Full CCREP ledger/reducer and bottown remain future phases.

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
  optional `actor`; the **first** one a process sees binds it, and later calls must
  either omit `actor` or pass the *same* one. There is **no silent default** —
  identity must be declared via `COLLAB_ACTOR` or the first call. This lets several agents that
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

The maildir lives **outside any repo** — transient, never committed, and shared by
every worktree/clone whose agents form one *fam*. The fam directory is **named by
history** but **gated by object-store membership** (review consensus — "Option C"):

```
~/.botfam/fam-<rootset12>[-<BOTFAM_FAM>]/     fam dir (name) + fam.toml (registry)
~/.botfam/<name> -> fam-<rootset12>           cosmetic symlink (setup-created)
```

- **Namespace = history.** `<rootset12>` is a short hash of the **sorted set of
  root commits** (`git rev-list --max-parents=0 HEAD`, sorted — a repo can have
  several roots after an unrelated-history merge, so never just `tail -1`). It is
  identical across every worktree and clone of the history, gives clean stable dir
  names, and ports cleanly to bottown.
- **Membership = object store.** "Clone-sharing" means **object-store sharing**, not
  mere shared ancestry: only worktrees and `--shared`/`--reference` clones (which
  physically share Git objects) auto-join. A fork or independent clone with the same
  history does **not** silently join.

`fam.toml` (at the fam dir) is the **registry** of member **canonical object-store
paths** (plus repo paths for humans, and `{name, root_set, origin?}`). Membership is
matched on **Git object identity, not path strings** (codex): resolve
`git rev-parse --git-common-dir`, then its object directory and any
`objects/info/alternates` targets, to absolute `realpath`-cleaned paths — so a
symlink, `..`, case folding, or a moved parent can neither spoof nor break a match.
On startup the server computes the current repo's canonical object-store set and
decides access **fail-closed**:

1. A canonical object-store path of this repo is already registered → **grant**.
2. Else if an `alternates` target resolves to a registered object store →
   **register this repo and grant** — this is what makes the scriba `--shared`
   sandboxes zero-config (their alternates point at the shared parent store).
3. Else → **refuse**: the server fails MCP initialization / rejects tool calls with a
   hard error, so an agent **cannot proceed while membership is unverified** (process
   exit vs. init failure is an implementation detail). Join deliberately via
   `COLLAB_ROOT`, `BOTFAM_FAM`, or `botfam setup [--force]`.

There is **no warn-only mode** — agents don't read stderr, so a warning *is* a
silent collision. The dangerous case (unrelated fork/template, same history) hits
step 3 and fails closed.

**The one residual edge:** a repo deliberately `--shared`-cloned from the same
parent *but meant to be a separate project* shares object storage, so step 2 would
auto-join it. Resolve it explicitly with `BOTFAM_FAM`, which suffixes the dir
(`fam-<rootset12>-fork`) to force isolation. Rare, and operator-driven.

Resolution order: `COLLAB_ROOT` (explicit, wins) › `fam-<rootset12>` (+`BOTFAM_FAM`).
Keep all of these **out of `.mcp.json`** so a committed config stays machine-agnostic
— ideally `.mcp.json` is just `{ "command": "botfam" }`. With no git and no
`COLLAB_ROOT` there is no key to derive, so `botfam setup` (or `COLLAB_ROOT`) is
required.

### Identity modes

Resolution, per call:

1. **Locked** (out-of-repo switch set) → identity = `COLLAB_ACTOR`; any call whose
   `actor` *differs* is rejected. No spoofing. For one-process-per-agent setups.
2. **Cooperative** (default) → the call's `actor` if present, else the **session
   actor**, else `COLLAB_ACTOR`. If none of these is set, the op is **refused** —
   no silent default.

**Bind-on-first-use:** under stdio each agent gets its own botfam process even when
agents share a worktree and a `.mcp.json`. So in cooperative mode the *first*
`actor` a process sees binds as the session actor; the agent states its name once
and may omit it afterward. The bind is **sticky and immutable**: a later call may
omit `actor` or repeat the bound one, but a *conflicting* `actor` is **rejected**
(no mid-session identity switch). This keeps the shared-config case working **and**
makes wrong-actor sends impossible after bind, without per-call boilerplate.

The lock lives **outside the repo** so a shared, committed `.mcp.json` can neither
grant nor revoke it:

```toml
# ${XDG_CONFIG_HOME:-~/.config}/botfam/config   (authoritative)
lock_actor = true
```

`BOTFAM_LOCK_ACTOR=1` is also honored — but do **not** put it in `.mcp.json`, which
would move the switch back into the repo and defeat the purpose.

**Threat model.** No silent default identity, and the bind is sticky (above). The
"recycled / shared process" race a reviewer might fear is an HTTP/daemon threat
model; under stdio each client spawns its *own* server process, so it does not
arise here — it returns as a real concern in bottown. (Two processes accidentally
sharing *one* actor is a different hazard, handled by the per-actor `flock` in §7.)

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
`ack`, … — convention, not enforced). One prefix is reserved *by convention*:
`ccrep:*` (`ccrep:proposal`, `ccrep:critique`, `ccrep:evaluation`) is how Phase 2
will be prototyped over plain messages before the dedicated ledger exists — the
server treats it like any other type. If `expires_at` is set and in the past when
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
| `recv(match_type?, match_from?, match_reply_to?, timeout_s=120)` | **yes** | block until a **matching** message lands in own `new/`, then **reserve** it (`new/→processing/`) and return it; return null on timeout |
| `try_recv(match_type?, match_from?, match_reply_to?)` | no | oldest matching message, reserved (`new/→processing/`), or null |
| `peek(match_type?, match_from?, match_reply_to?)` | no | oldest matching message **without reserving it** (stays in `new/`), or null |
| `thread(id, limit?)` | no | read-only: reconstruct the conversation containing `id` via `in_reply_to` links; reserves nothing |
| `ack(id, outcome?)` | no | confirm a reserved message processed (`processing/→cur/`), recording optional `outcome`; without it the message is redelivered after a crash |
| `seen(id)` | no | has this `id` already been acked? (durable dedup check against `cur/`) |
| `inbox()` | no | read-only snapshot: pending `new/`, in-flight `processing/`, recent `cur/` (with ids), task counts |

**Delivery is at-least-once (review round 1).** `recv`/`try_recv` *reserve* a
message into `processing/` and return it; the consumer calls `ack(id, outcome?)`
once it has durably acted, moving it to `cur/`. A crash before `ack` leaves the
message in `processing/`, where the actor's next server start **rolls it back to
`new/`** for redelivery (§7). Because redelivery means a message can arrive twice,
consumers must dedup — and botfam gives them a **durable place to do it**: the
acked-`id` set in `cur/`, queryable via `seen(id)` (and surfaced by `inbox`). A
consumer checks `seen(id)` before acting and `ack`s after. The one irreducible
window — an *external* side effect performed, then a crash *before* `ack`, then
redelivery — cannot be closed by any queue; make such effects idempotent by keying
them on the message `id`. `peek` reserves nothing — look without taking.

**Matching (Tier 1).** `match_type`, `match_from`, and `match_reply_to` AND
together; the oldest message in `new/` satisfying *all* provided filters is
taken, and non-matching mail is left untouched (a filtered wait neither blocks
nor is blocked by unrelated deliveries). `match_reply_to` is the request/reply
primitive: send a request, then `recv(match_reply_to=<request id>)` parks until
*that* conversation advances instead of waking on every delivery — without it,
two concurrent threads between the same actors cross (observed in the first
dogfooded discussion). A blocked filtered `recv` re-checks all of `new/` on each
wakeup, so a non-matching head never wedges the wait.

**`thread(id, limit?)`** reconstructs a conversation: starting from any message
`id`, follow `in_reply_to` ancestors and collect descendants, returning
envelopes sorted by `ts` (oldest first, capped at `limit`). Read-only, like
`peek` — it reserves nothing and never moves a file. Because an envelope is
stored only in the *recipient's* mailbox, a two-party thread spans two
mailboxes: in v0 `thread` may scan other actors' directories read-only — the
same trusted-fam posture as `inbox(other)` (§12), revisited under bottown.
Files that vanish mid-scan (rename races) are skipped; they reappear in the
target directory on the next call. Descendant collection is O(total history) if
unbounded, so the scan itself is capped, not just the result: by default only
files younger than a scope bound (default 30 days) are examined, with a ceiling
on total files scanned — filename timestamps make the age cut free, no file
needs opening. Pass an explicit `since`/`scan_limit` to widen the window.

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
runs its throttled sweep** (so even an all-idle fam self-heals — §6), or when a
coordinator calls `sweep` explicitly. A crashed worker's task returns to `open/`
rather than lingering forever.

Every tool also accepts an optional `actor?` — in cooperative mode it binds the
session actor on first use and must match it thereafter; under the lock it is
validated against `COLLAB_ACTOR` (see §3, *Identity modes*).

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

**A blocked `recv` also sweeps leases — but throttled.** So an all-idle fam (every
agent on `recv`, nobody calling `claim`/`post`) still reclaims a crashed worker's
expired task, closing the idle-deadlock. To avoid making every idle agent do
filesystem work every second (codex F6), the sweep is **decoupled from the 1s
mailbox tick**: it runs at most once per coarse interval, or is scheduled from the
nearest `lease_expires_at`. The mailbox wake stays cheap; lease maintenance happens
on its own slow cadence.

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
repo): resolve the fam dir from the root-commit set (§3), create it, write
`fam.toml` (`name`, `root_set`, `origin?`, the member **canonical object-store
paths** + repo paths, roster), register *this* repo's object store as the first
member, make the `~/.botfam/<project>` symlink,
and print the resolved path plus the per-agent `.mcp.json` snippet. Membership
afterward is automatic for object-store-linked clones (§3 step 2) and fail-closed
otherwise; `--force` / `BOTFAM_FAM` handle the deliberate-fork edge. The roster is
**advisory** in v0 — it
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

**Resolved across review rounds** (agy, Gemini · codex, GPT-5):

- Destructive `recv` → **at-least-once** via `ack`/`processing/`, with a durable
  dedup surface (`seen(id)` over `cur/`) and the irreducible window documented (§5, §7).
- Message TTL disposition → **visible `expired/` dead-letter** (§4).
- Idle-fam lease deadlock → a blocked `recv` sweeps leases, **throttled** off the 1s
  tick so idle stays cheap (§5, §6).
- Identity foot-guns + self-contradiction → **no silent default**, **one sticky,
  immutable bind** (a conflicting later `actor` is rejected) (§2, §3, §5).
- Two-processes-as-one-actor → **enforced per-actor `flock`** on `<actor>/.lock`;
  `recv`/`ack`/rollback require it (§7). *(Both reviewers flagged this independently.)*
- CCREP gate categories → **rule-based detection**, never agent self-declaration
  (DESIGN_ccrep §8).

- F2 fam keying → **consensus: Option C** (agy + codex). History-namespaced
  (`fam-<rootset12>`, sorted root set — also fixes multi-root), membership gated on
  **canonical Git object identity** (worktrees / `--shared` clones auto-join via
  `realpath`-resolved `alternates`, not path strings), **fail-closed** otherwise (no
  warn-only; agent can't proceed while unverified), deliberate-fork edge via
  `BOTFAM_FAM` (§3). agy: **APPROVE** conditioned on this; codex: recommends C + the
  canonicalization refinement, now incorporated.

- **Tier 1 receive ergonomics** (claude + agy consensus, first dogfooded
  discussion *over collab itself*, 2026-06): type-only filtering made an agent
  awaiting a specific reply wake on every unrelated delivery, and two concurrent
  threads between the same actors crossed. Added `match_from`/`match_reply_to`
  and read-only `thread(id)` (§5) as prerequisites for prototyping CCREP over
  collab, plus the `ccrep:*` type convention (§4). Deferred by the same
  consensus: task dependency mapping (workflow-layer concern — the hydra
  lesson), pub/sub topics (revisit as a broadcast recipient if a need shows up),
  server-side auto-heartbeat (harness concern).

**Still open (minor):**

- **Identity trust model.** Sticky bind + out-of-repo lock as the *default*, or
  lock-on by default? Low stakes — the cooperative default is fine for a trusted fam.

## 12. Known limitations (v0, accepted)

- `<actor>/cur/` is an audit trail and **grows unbounded** — no auto-prune in v0.
- **Same local filesystem only.** Atomic-rename semantics do not hold over NFS;
  keep `$COLLAB_ROOT` (incl. `tmp/`) on one local volume.
- `inbox(other_actor)` exposes another actor's filenames and counts (not payloads)
  — fine within a trusted fam; revisited under bottown's tokens.
