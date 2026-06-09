# botfam — Sessions (design-discussion layer)

Status: **Proposed** (claude draft from claude + agy joint analysis of the
deep-cuts prior art; pending proto-CCREP review) · depends on Phase 1
([DESIGN.md](DESIGN.md))

Sessions are botfam's **discussion record**: an LSM-style append-and-compact log
for multi-agent design discussions. `collab` moves messages (the wake-up), CCREP
proves artifacts (the verdict) — sessions capture the *reasoning in between*,
which today lives only in mailbox envelopes and is effectively write-only. The
three layers compose: talk over `collab`, think in a session, ratchet the
resulting artifact with CCREP.

This ports the proven deep-cuts protocol (`doc/collab/PROTOCOL.md`,
`tools/merge_sessions.py` — self-described "LSM-style compaction"), adapted to
botfam's out-of-repo coordination plane. The design was forced there by a real
incident: three agents editing one shared `session.md` collided, an edit was
lost, and ordering had to be back-filled by hand (deep-cuts, 2026-06-07).

---

## 1. The LSM model

- **Memtables:** each actor appends only to its own `session.<actor>.md`.
  Sole-writer-per-file makes write races impossible by construction — no locks.
- **Compaction:** a merge step folds the per-actor files into one
  chronologically ordered, *generated* `session.md`. Run at closeout, never per
  turn; during the session agents read each other's files (or `session_read`).
- **Promotion:** durable decisions leave the session for permanent homes —
  the compacted log is written into the repo, and accepted designs land in
  `doc/` files via the normal (CCREP-gated) path. Sessions themselves are
  ephemeral working records, like deep-cuts' "thinking out loud" rule.
- **Tombstone:** an `ARCHIVED` marker file ends a session — created **only by
  the human operator**, never by agents (§6).

## 2. State layout

Live state under the fam root, beside the mailboxes:

```
$COLLAB_ROOT/sessions/<YYYY-MM-DD-slug>/
    meta.json              {slug, participants, created_by, created_at}
    session.<actor>.md     per-actor append-only log (server-written)
    ARCHIVED               tombstone (operator-created; any contents)
```

Promoted state in the repo (written at close, committed under normal repo
rules):

```
doc/collab/sessions/<YYYY-MM-DD-slug>/session.md     generated; never hand-edit
```

Living under `$COLLAB_ROOT` (not the repo) means live logs are visible to every
worktree instantly with **no git churn** — deep-cuts' explicitly rejected
default ("commit-and-merge per handoff") stays rejected. Session slugs share
the actor/topic naming restriction (`[A-Za-z0-9_-]+`, [DESIGN.md](DESIGN.md)
§4) — no path traversal.

## 3. Entry format & server stamping

```markdown
## [<actor>, <RFC3339 UTC, server-stamped>]
<body — reasoning, findings, decisions; workspace-relative paths only>

**→ Handoff:**
**Task:** <what the next participant should do>
**Context:** <files, prior decisions, evidence needed>
**Deliverable:** <expected artifact>
```

The handoff block is optional (a session can be a plain log). The header is
**written by the server, not the agent**: `session_append` stamps the bound
actor and the server clock. Agent-supplied timestamps are never trusted. This
removes deep-cuts' clock-skew hazard entirely — one stamping authority per fam,
and entries within a file are monotonic by construction.

## 4. Tool surface

MCP tools (covered by the existing `mcp__collab__*` grant — this is the reason
they are MCP tools at all: `$COLLAB_ROOT` is outside every agent's workspace,
so direct file writes would resurrect the permission prompts the committed
project settings eliminated):

| Tool | Behavior |
|---|---|
| `session_append(session, body, handoff?)` | append one entry to own `session.<actor>.md`; server writes the `## [actor, ts]` header; returns the stamped entry |
| `session_read(session, actor?, since?)` | read-only: entries from all (or one) actor's file, server-merged in timestamp order, optionally only those after `since`; returns a **JSON array of structured entries** `{actor, ts, body, handoff?}`, not raw markdown — markdown is the storage format, structured entries are the wire format |

CLI subcommands (operator / session-closer actions, not hot-path):

| Command | Behavior |
|---|---|
| `botfam session new <slug> [--participants a,b]` | scaffold the session dir + `meta.json` |
| `botfam session list` | active sessions (no `ARCHIVED`), most recent first |
| `botfam session merge <slug> [--check]` | compaction: per-actor files → generated `session.md` on stdout or `--check` staleness |
| `botfam session close <slug>` | run the compaction and write `doc/collab/sessions/<slug>/session.md` into the **caller's worktree**, creating intermediate directories as needed (`MkdirAll`) |

`close` writes into the worktree but never commits — committing the promoted
log follows the repo's normal rules (the operator asks). The fam discovers the
active session by convention: the kickoff `collab` message names it.

## 5. Compaction semantics

Port of `merge_sessions.py`, in Go, no new dependencies:

- Parse `## [<actor>, <ts>]` headers; the header is the merge key.
- Sort by `(utc-instant, actor, original-order-within-file)` — deterministic;
  ties cannot reorder across runs. (Server stamping makes the cross-actor
  tie-break nearly moot, but it stays specified.)
- Unparseable headers sort last, preserving raw order — a corrupted entry is
  visible at the bottom, not silently dropped.
- Output begins with a `<!-- GENERATED by botfam session merge — DO NOT EDIT
  (edit session.<actor>.md) -->` banner.
- Idempotent and derived: the generated file is never an input. Concurrent
  merges are benign (same inputs → same output).

## 6. Closeout & the human gate

1. When agents believe the discussion has converged, one writes a
   `## [Closed, <date>]` entry: accepted decisions, rejected alternatives
   (with reasons, so they aren't re-proposed), links to follow-up
   proposals/commits — then **hands back to the operator and stops**.
2. Only the operator creates `ARCHIVED`. A `Closed` entry without a tombstone
   means *awaiting sign-off* — still active, still resumable, more work can be
   requested.
3. Promotion (`session close` → repo → commit) is part of sign-off, not part
   of the agents' closeout.

v0 enforces none of this in code (anyone *could* create the file); it is
convention, like the v0 roster. Enforcement arrives with bottown's tokens.

## 7. Behavioral rules (the protocol half)

The deep-cuts lesson is that the format's value comes from the discipline it
enforces, not the files. These rules bind participants; the server cannot check
them in v0:

- **Quote the handoff you are answering, verbatim**, at the top of your entry.
  (With `collab`'s Tier 1 filters, the kickoff/handoff message id can ride
  along in the handoff Context — `thread()` then links discussion to mail.)
- **Verify before you ACK.** An ACK records *what you checked*, not just
  agreement — deep-cuts ACKs cite the commands run and results seen.
- **Log agreement, not only dissent.** Consensus — and who reached it — must be
  readable from the record alone.
- **Document the operator's steering** as a `## [<operator>, …]`-credited
  entry (via any agent's `session_append`-relay or the operator's own editor —
  the operator writes files directly and needs no tool).
- **Workspace-relative paths only** in bodies and handoffs — absolute worktree
  paths (`/Users/…/wt-agy/…`) do not resolve for peers. Enforcement (a lint on
  append) is deferred.
- **Sessions are ephemeral.** A decision that matters graduates to a `doc/`
  file through CCREP; a session is never cited as the authority.

## 8. Deferred (not v0)

- Tombstone/permission enforcement (bottown tokens).
- Path-rule linting on `session_append`.
- Session search/indexing; cross-session links.
- Any automatic compaction or pruning of live session dirs — `cur/`-style
  unbounded growth is accepted, same as [DESIGN.md](DESIGN.md) §12.
- Auto-detection of "the active session" — convention (kickoff message) until
  it hurts.
