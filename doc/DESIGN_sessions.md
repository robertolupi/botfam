# botfam — Sessions (design-discussion layer)

Status: **Proposed** (claude draft from claude + agy joint analysis of the
deep-cuts and hydra prior art; pending proto-CCREP review) · depends on Phase 1
([DESIGN.md](DESIGN.md))

Sessions are botfam's **discussion record**: an append-only log for multi-agent
design discussions. `collab` moves messages (the wake-up), CCREP proves
artifacts (the verdict) — sessions capture the *reasoning in between*, which
today lives only in mailbox envelopes and is effectively write-only. The three
layers compose: talk over `collab`, think in a session, ratchet the resulting
artifact with CCREP.

This synthesizes two prior arts. From **deep-cuts** (`doc/collab/PROTOCOL.md`):
the session lifecycle, the behavioral rules, the human-gated tombstone, and the
promotion of compacted records into the repo — proven over ~20 live sessions,
shaped by a real three-writer collision on a shared log (2026-06-07). From
**hydra** (`hydra/ledger.py`): JSONL as the storage format with flock'd
appends. Deliberately *not* taken from hydra: the seq/`prev_hash`/SHA-256
chain and CAS — hydra recomputed them by re-parsing the whole file inside the
exclusive lock on every append (O(N) per write), which is exactly the "ledger
costs on the coordination path" mistake botfam's lineage retired
([v1-hydra](lineage/v1-hydra.md)). Tamper-evidence is a bottown (token
identity) concern; like the CCREP ledger ([DESIGN_ccrep.md](DESIGN_ccrep.md)
§3), hash-chaining can be added later without changing the tool surface.

---

## 1. The model

- **One log per session:** `session.jsonl`, append-only, one JSON entry per
  line. Total order = append order; there is no merge step and no timestamp
  tie-breaking. Appends are **blind** — lock, write one line, unlock; never
  read-before-write.
- **Server-mediated:** agents never touch the file; `session_append` stamps
  the bound actor and the server clock. JSONL is both the storage and the wire
  format.
- **Render, then promote:** at close, the log is *rendered* to a human-readable
  `session.md` (deep-cuts' compaction step, reduced to a pure projection) and
  written into the repo. Markdown is output-only — never parsed, never an
  input. Durable decisions then graduate to `doc/` files via the normal
  (CCREP-gated) path; sessions themselves are ephemeral working records.
- **Tombstone:** an `ARCHIVED` marker file ends a session — created **only by
  the human operator**, never by agents (§6).

This is the same storage pattern as the Phase 2 CCREP ledger (append-only
JSONL under `flock`, consensus *derived* by reading, never written) — one Go
primitive serves both layers.

## 2. State layout

Live state under the fam root, beside the mailboxes:

```
$COLLAB_ROOT/sessions/<YYYY-MM-DD-slug>/
    meta.json          {slug, participants, created_by, created_at}
    session.jsonl      append-only entry log (server-written, flock on append)
    ARCHIVED           tombstone (operator-created; any contents)
```

Promoted state in the repo (written at close, committed under normal repo
rules):

```
doc/collab/sessions/<YYYY-MM-DD-slug>/session.md     rendered; never hand-edit
```

Living under `$COLLAB_ROOT` (not the repo) means the live log is visible to
every worktree instantly with **no git churn** — deep-cuts' explicitly rejected
default ("commit-and-merge per handoff") stays rejected. Session slugs share
the actor/topic naming restriction (`[A-Za-z0-9_-]+`, [DESIGN.md](DESIGN.md)
§4) — no path traversal.

## 3. Entry format

One JSON object per line:

```json
{ "id": "...", "actor": "claude", "ts": 1781042500.123,
  "body": "reasoning, findings, decisions — workspace-relative paths only",
  "handoff": { "task": "...", "context": "...", "deliverable": "..." } }
```

- `id`, `actor`, `ts` are **server-stamped**; agent-supplied values are never
  trusted. One stamping authority per fam kills the clock-skew hazard the
  deep-cuts merge had to engineer around, and the per-actor `actor` field
  preserves authorship without per-actor files.
- `handoff` is optional (a session can be a plain log) and structured —
  deep-cuts' Task/Context/Deliverable block as fields, machine-checkable.
- `body` is markdown text; it renders verbatim into the entry body at close.

## 4. Tool surface

MCP tools (covered by the existing `mcp__collab__*` grant — this is the reason
they are MCP tools at all: `$COLLAB_ROOT` is outside every agent's workspace,
so direct file writes would resurrect the permission prompts the committed
project settings eliminated):

| Tool | Behavior |
|---|---|
| `session_append(session, body, handoff?)` | append one entry under `flock`; server stamps `id`/`actor`/`ts`; returns the stamped entry |
| `session_read(session, actor?, since_ts?, limit?)` | read-only: parse the log, optionally filter by actor / entries after `since_ts`; returns a JSON array of entries |

CLI subcommands (operator / session-closer actions, not hot-path):

| Command | Behavior |
|---|---|
| `botfam session new <slug> [--participants a,b]` | scaffold the session dir + `meta.json` |
| `botfam session list` | active sessions (no `ARCHIVED`), most recent first |
| `botfam session render <slug>` | project `session.jsonl` → markdown on stdout |
| `botfam session close <slug>` | render and write `doc/collab/sessions/<slug>/session.md` into the **caller's worktree**, creating intermediate directories as needed (`MkdirAll`); **refuses to run without a TTY on stdin** (§6) |

`close` writes into the worktree but never commits — committing the promoted
log follows the repo's normal rules (the operator asks). The fam discovers the
active session by convention: the kickoff `collab` message names it.

## 5. Concurrency & rendering semantics

- **Append:** open `O_APPEND`, take exclusive `flock`, write one
  `\n`-terminated line, release. No read inside the lock. Lock scope is a
  single line write — contention among a handful of agents is negligible.
  Same-filesystem rule applies ([DESIGN.md](DESIGN.md) §12: no NFS).
- **Read:** lock-free scan; a torn final line (reader racing a writer) is
  ignored — it is complete on the next read.
- **Render** (deterministic, idempotent): entries in file order;
  `## [<actor>, <RFC3339 UTC from ts>]` headers; body verbatim; handoff as the
  deep-cuts `**→ Handoff:**` block. Output begins with a
  `<!-- RENDERED by botfam session render — DO NOT EDIT (append via
  session_append) -->` banner. An unparseable line renders as a visible
  `## [corrupt entry]` stanza at its position — surfaced, not silently
  dropped. The rendered file is derived, never an input; concurrent renders
  are benign.

## 6. Closeout & the human gate

1. When agents believe the discussion has converged, one appends a **closeout
   entry**: accepted decisions, rejected alternatives (with reasons, so they
   aren't re-proposed), links to follow-up proposals/commits — then **hands
   back to the operator and stops**.
2. Only the operator creates `ARCHIVED`. A closeout entry without a tombstone
   means *awaiting sign-off* — still active, still resumable, more work can be
   requested.
3. Promotion (`session close` → repo → commit) is part of sign-off, not part
   of the agents' closeout.

One mechanical guard exists in v0 (operator decision, Roberto 2026-06-10):
**`session close` refuses to run unless stdin is a TTY.** Harness-driven agents
have no TTY, so the promotion gesture cannot be performed by a bot; the human
at a real terminal always can. It is a guardrail, not security — a determined
process can fake a pty — consistent with the trusted-fam posture. `session
render` (read-only, stdout) stays bot-callable; only the gesture that writes
into the repo is gated. The `ARCHIVED` tombstone remains pure convention in
v0 (anyone *could* create the file), like the roster. Real enforcement
arrives with bottown's tokens.

## 7. Behavioral rules (the protocol half)

The deep-cuts lesson is that the format's value comes from the discipline it
enforces, not the files. These rules bind participants; the server cannot check
them in v0:

- **Quote the handoff you are answering, verbatim**, at the top of your entry.
  (With `collab`'s Tier 1 filters, the kickoff/handoff message id can ride
  along in the handoff context — `thread()` then links discussion to mail.)
- **Verify before you ACK.** An ACK records *what you checked*, not just
  agreement — deep-cuts ACKs cite the commands run and results seen.
- **Log agreement, not only dissent.** Consensus — and who reached it — must be
  readable from the record alone.
- **Document the operator's steering** as an entry crediting the operator
  (relayed via any agent's `session_append`; the body names the operator as
  the source).
- **Workspace-relative paths only** in bodies and handoffs — absolute worktree
  paths (`/Users/…/wt-agy/…`) do not resolve for peers. Enforcement (a lint on
  append) is deferred.
- **Sessions are ephemeral.** A decision that matters graduates to a `doc/`
  file through CCREP; a session is never cited as the authority.

## 8. Deferred (not v0)

- seq / `prev_hash` / hash-chain tamper-evidence — bottown, and only if a
  threat model demands it; addable without changing the tool surface (same
  clause as the CCREP ledger).
- Tombstone/permission enforcement (bottown tokens).
- Path-rule linting on `session_append`.
- Session search/indexing; cross-session links.
- Any automatic pruning of live session dirs — `cur/`-style unbounded growth
  is accepted, same as [DESIGN.md](DESIGN.md) §12.
- Auto-detection of "the active session" — convention (kickoff message) until
  it hurts.
