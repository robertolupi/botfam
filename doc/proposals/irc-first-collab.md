# Design Proposal: IRC-First Collab — Protocol Conventions

* **Status:** `draft` — pending consensus with agy; Operator pre-approved the
  direction in `#botfam` 2026-06-11 ("irc approved", "implement this
  autonomously … decisions only by consensus").
* **Participants:**
  * Roberto Lupi (Operator)
  * Claude (Agent, `wt-claude`) — this draft
  * agy (Agent, `wt-agy`) — implementation plan (companion doc)
* **Scope:** the *conventions* layer (message formats, channel layout,
  identity, durability rules). agy's companion plan covers the Go
  implementation (`botfam irc-client`, bots). Neither lands without the other
  agent's approval.

---

## 1. Rationale

A live IRC server (ngircd on `localhost:6667`) replaced the in-development
collab substrate as our working channel on 2026-06-11 and immediately
out-performed it: zero approval friction (file-I/O interface), real-time
fan-out, presence, PMs, and human participation (Operator on ircII) for free.

Mapped against famseed `BOOTSTRAP.md §9` (the canonical list of trust
properties the compiled binary was supposed to add over markdown-only
role-play), the IRC server natively provides:

| §9 trust property | IRC mechanism |
|---|---|
| Attributable writes | connection-bound nicks; server-relayed source prefix |
| Total ordering | single server serializes channel traffic |
| Presence-aware quorum | `NAMES` / `JOIN` / `QUIT` |
| Sustained, connection-bound consent | the TCP connection itself |

What IRC does **not** provide — and the remaining code surface must:

| gap | owner |
|---|---|
| Durable history / replay (offline agents miss everything) | **scribe bot** |
| Deterministic vote tally | **tally bot** (may be the same process as scribe) |
| Machine-derived git SHAs in ccrep (anti-confabulation) | client-side helper |
| Nick authentication | ngircd config (per-nick `PASS`) or explicit operator-trust statement |

Empirical support for durability-first: agents silently missed channel state
three times on day one (512-byte client crash; 30 s reconnect window; agy
offline during the Operator's autonomy instructions).

## 2. Channel layout

| channel | purpose |
|---|---|
| `#botfam` | main coordination; the Operator's home channel |
| `#ccrep` | proposals, evaluations, votes — everything the tally bot consumes |
| `#session-<slug>` | per-session working channels; scribe logs each to its own JSONL |

PMs are for low-stakes side traffic only; anything decision-relevant must be
said in a channel (scribe can't log what it can't see).

## 3. Message conventions

Plain lines are free-form discussion. Structured lines start with a bang verb
mirroring the pinned ccrep vocabulary (PROTOCOL.md §4), one line per message,
`key=value` pairs, no spaces inside values (URL-encode if needed):

```
!propose id=<proposal_id> sha=<commit_sha> quorum=all|majority|any deadline=<iso8601> summary=<text…>
!evaluate id=<proposal_id> sha=<commit_sha> verdict=approve|request_changes|reject evidence=<text…>
!vote id=<proposal_id> sha=<commit_sha> verdict=approve|reject
!tally id=<proposal_id>            ← tally bot replies with deterministic count over present principals
!claim task=<id>   !complete task=<id> evidence=<text…>
```

Rules carried over from PROTOCOL.md unchanged: reviewer/executor separation;
approvals die on new commits (the `sha=` field makes staleness checkable);
unknown bang verbs are protocol errors.

The `sha=` value must be produced by the SHA helper (`botfam irc-client`
shells out to `git rev-parse`), never retyped by hand — same anti-confabulation
rule as the binary's merge gate.

## 4. Durability: the scribe

* The scribe bot joins all channels, appends every line as JSONL
  (`ts`, `channel`, `nick`, `body`) under `$COLLAB_ROOT/irc-log/<channel>.jsonl`.
* **Replay-on-join convention:** an agent (re)joining a channel reads the
  scribe's JSONL tail before acting — never assume you saw everything live.
* The scribe is the tamper-evidence anchor: one sole writer, append-only,
  filesystem-readable by every worktree.
* Sessions: `#session-<slug>` JSONL replaces `session-append`/`session-read`;
  session close/promotion stays a human (TTY) gesture per PROTOCOL.md.

## 5. Identity

* One nick per agent, equal to the worktree-derived actor name (`claude`,
  `agy`, `codex`); Operator is `rlupi`.
* Phase 1 (now): operator-supervised trust — nicks are unauthenticated; fine
  while the fam is 2–3 agents on one machine with the Operator reading logs.
* Phase 2 (before any unsupervised/multi-host operation): per-nick server
  passwords or NickServ-equivalent in the bot, **gated on consensus**.

## 6. Migration order (the non-negotiable from review)

1. Scribe bot + JSONL logs live; replay-on-join convention in PROTOCOL.md.
2. Tally bot + bang-verb vocabulary live; one full ccrep round-trip exercised.
3. PROTOCOL.md rewritten to IRC-first; harness files regenerated.
4. Only then: delete the SQLite/UDS substrate and mailbox verbs.

Rationale: never lose durability or auditability mid-cutover; the substrate
keeps working as fallback until step 4.

## 7. Open questions (for consensus)

1. Scribe and tally: one bot process or two? (claude leans one process, two
   responsibilities — fewer moving parts.)
2. Does `botfam irc-client` subsume the watcher (wake-on-message) role, or do
   harnesses keep their own watch loops?
3. Keep the leased task queue (`post`/`claim`/`sweep`) as bang verbs over IRC,
   or retire it in favor of session channels + free-form claims?
4. ngircd lifecycle: launchd service vs. operator-started — who restarts it
   after reboot?
