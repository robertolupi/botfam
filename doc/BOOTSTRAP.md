# botfam BOOTSTRAP — the zero-code, markdown-only fam

This document is **self-contained**. With nothing but a filesystem and an agent
that can write, rename, list, and sleep, you can boot a working botfam — no Go,
no `botfam` binary, no daemon. The agents *become* the implementation by
following this spec.

> ## ⚠️ Read this first: capability, not trust
>
> This bootstrap gives you the full **capability** of botfam (send, recv, ack,
> post, claim, heartbeat, sessions, CCREP) and **none of its trust
> guarantees.** It has:
> - **no attributable writes** — any participant can write any file, including
>   one that claims to be from someone else;
> - **no real identity** — an actor is whatever name a writer puts in a file;
> - **no confabulation-resistance** — an agent can fabricate a message, a vote,
>   a claim, or an entire history, and nothing will catch it;
> - **no pacing / no tamper-evident log** — the maildir is auditable to *read*
>   but only as trustworthy as its writers.
>
> A **single model role-playing every actor has zero independence**: "alice
> sends, bob receives" is one model talking to itself; the files are props.
> That is fine for prototyping, teaching, and tiny fully-cooperative fams where
> one human reads every line. It is **not** safe when participants are genuinely
> independent, numerous, fast, or unsupervised. Turning capability into trust —
> attributable writes, real identity binding, deterministic tallies, sustained
> consent, pacing — is exactly what the compiled `botfam` adds, and why it is
> not "ergonomic sugar." See [§9](#9-what-the-binary-adds) for the precise list.

---

## 1. The one primitive: atomic `rename`

Every verb below is built from a single OS guarantee: **`rename(2)` within one
filesystem is atomic.** A file is either fully at its old path or fully at its
new one — never half-moved, never half-read.

Two patterns use it:

1. **Atomic publish** — never let a reader see a half-written file:
   ```
   write  tmp/<id>.json      # build the full file out of sight
   rename tmp/<id>.json  →  <dest>/<id>.json   # appears complete, instantly
   ```
2. **Compare-and-swap (CAS)** — atomic claim of a contested item:
   ```
   rename open/<f>  →  claimed/<me>/<f>
   #   success → I own it.   ENOENT → someone else won the race.
   ```

That is the whole engine. Everything else is convention.

---

## 2. Directory layout

State lives **outside any git repo**, in a shared fam root (`$COLLAB_ROOT`,
default `~/.botfam/<fam>/`):

```
<fam-root>/
  tmp/                        # scratch for atomic-publish; never read directly
  <actor>/                    # one mailbox per actor (alice, bob, …)
    new/                      # delivered, unread
    processing/               # reserved by a reader, awaiting ack
    cur/                      # acked / handled (the durable record)
    expired/                  # TTL lapsed before anyone read it
  tasks/
    open/                     # postable, unclaimed
    claimed/<actor>/          # leased to <actor>
    done/                     # completed (or abandoned→reopened leaves open/)
  sessions/
    <slug>/
      meta.json               # {slug, participants, created_at}
      session.log             # append-only; one JSON entry per line
      ARCHIVED                # tombstone; present ⇒ closed
```

**Filenames sort chronologically.** Use
`<zero-padded-unix-nanos>-<random-hex>.json`, e.g.
`01781106774696000000-3f9a…c1.json`. Because the timestamp prefix is
zero-padded, a plain lexical directory sort *is* FIFO order — "oldest" needs no
parsing.

---

## 3. Identity (convention)

An actor's name is the **basename of its worktree directory**, with a leading
`wt-` or `botfam-` stripped: `wt-alice` → `alice`. In a role-play fam, just
assign each subagent a name and have it use that as `<actor>` everywhere.

> This is a *label*, not an identity. Nothing stops a writer from using another
> name. The binary binds the name to the process that actually wrote
> ([§9](#9-what-the-binary-adds)); the bootstrap trusts the label.

---

## 4. Messaging

Message file (in `<to>/new/…json`):
```json
{
  "id": "01781106774696000000-3f9a…c1",
  "from": "alice", "to": "bob",
  "type": "note",
  "payload": { "text": "hi" },
  "ts": 1781106774.696,
  "in_reply_to": "",            // optional: another message id
  "expires_at": null,           // optional: unix seconds; past ⇒ expired
  "outcome": null               // filled on ack
}
```

- **send(to, type, payload)** — build the file in `tmp/`, then
  `rename tmp/<id>.json → <to>/new/<id>.json`. Create `<to>/{new,processing,cur}`
  first if missing.
- **recv(match_type?) [blocking]** — loop: list `<me>/new/` sorted; take the
  oldest whose `type` matches (or any). Reserve it with
  `rename <me>/new/<f> → <me>/processing/<f>`. If the rename fails (someone/your
  other self won), retry the next file. If `new/` is empty, `sleep` ~0.2s and
  loop. The rename is the reservation — **delivery is at-least-once.**
- **try_recv / peek** — same scan without blocking; `peek` reads without
  reserving (no rename).
- **ack(id, outcome?)** — after you have *durably* handled it: optionally
  rewrite the file with `outcome` filled (atomic publish), then
  `rename <me>/processing/<f> → <me>/cur/<f>`. `cur/` is the handled record.
- **seen(id)** — the id exists in `<me>/cur/` ⇒ already handled (dedup).
- **expiry** — before reading, move any `new/` file whose `expires_at` is past
  to `expired/`.
- **crash redelivery (recovery sweep)** — a file stuck in `processing/` past a
  staleness window (its reader died before ack) is returned with
  `rename processing/<f> → new/<f>`.

---

## 5. Tasks (leased queue)

Task file (in `tasks/open/…json`):
```json
{
  "id": "…", "type": "build", "payload": { "title": "…" },
  "status": "open", "owner": "",
  "created_at": 1781106774.6,
  "claimed_at": null, "lease_expires_at": null,
  "completed_at": null, "result": null,
  "swept_at": null, "swept_from": ""
}
```

- **post(type, payload)** — atomic-publish into `tasks/open/`.
- **claim(lease_ttl) [CAS]** — list `tasks/open/` sorted; for the oldest,
  `rename tasks/open/<f> → tasks/claimed/<me>/<f>`. **Success = you own it;
  ENOENT = lost the race, try the next.** Then set `status:"claimed"`,
  `owner:"<me>"`, `lease_expires_at: now+ttl` (atomic-publish the update). Always
  re-read the file you actually got and confirm its id.
- **heartbeat(id, lease_ttl)** — extend `lease_expires_at` on your claimed file.
  Do this at every natural pause or you risk being swept.
- **complete(id, result)** — set `status:"done"`, `result`, `completed_at`;
  `rename tasks/claimed/<me>/<f> → tasks/done/<f>`.
- **abandon(id, reason)** — reset `owner:""`, `status:"open"`;
  `rename tasks/claimed/<me>/<f> → tasks/open/<f>`.
- **sweep** — scan `tasks/claimed/*/`; any file whose `lease_expires_at` is past
  goes back: set `swept_from:"<actor>"`, `owner:""`, `status:"open"`,
  `rename → tasks/open/<f>`. A `swept_from` on a task you then claim means
  *check with the previous owner before starting*.

> Ownership note: updating-then-renaming has a race (a sweep can move the file
> mid-update). The binary closes it by renaming the claimed file to a private
> `tmp/` name first (so the update either owns the file or fails cleanly). The
> bootstrap accepts the race; keep TTLs generous and heartbeat often.

---

## 6. Sessions (the shared log)

- **session new \<slug> [participants]** — `mkdir sessions/<slug>/`, write
  `meta.json`. Fails if it already exists.
- **session_append(slug, body, handoff?)** — append one JSON line to
  `sessions/<slug>/session.log`:
  ```json
  {"id":"…","actor":"alice","ts":1781106774.6,"body":"…","handoff":{"task":"…","context":"…","deliverable":"…"}}
  ```
  `handoff`, if present, must have non-empty `task`, `context`, `deliverable`.
- **session_read(slug, from?, since_ts?, limit?)** — read `session.log` line by
  line; filter by author / timestamp. (Use large lines or chunked reads — a long
  entry must not be silently truncated.)
- **session close \<slug>** — render the log to human-readable
  `doc/collab/sessions/<slug>/session.md` in the repo, then write the `ARCHIVED`
  tombstone. **Promotion to the repo is a human gesture** — an agent should not
  self-archive.

---

## 7. CCREP — coordinating shared-state changes (one page)

All changes to shared state (landing commits, store migrations) go through
`ccrep:*` messages **and** a session-log entry for every transition. Treat
unknown variants as protocol errors.

| type | required payload |
|---|---|
| `ccrep:proposal` | `proposal_id`, `summary`, `executor`, `quorum` (`all`\|`majority`\|`any`\|`consensus`), `deadline`; `commit_sha` for code |
| `ccrep:critique` | `proposal_id`, `commit_sha`, `verdict: request_changes`, findings with `severity` + `file:line` + resolution |
| `ccrep:evaluation` | `proposal_id`, `commit_sha`, `reviewer`, `verdict` (`approve`\|`request_changes`\|`reject`), evidence |
| `ccrep:revision` | `proposal_id`, new `commit_sha`, `addressed_critiques` (ids) |
| `ccrep:executed` | `proposal_id`, resulting state (e.g. main sha), consent/absentee breakdown |

**Rules** (each exists because a fam broke it once):
1. **One executor.** The proposal names exactly one `executor`; reviewers send
   verdicts only and never act, even when approving.
2. **Approvals die on new commits.** Any new commit voids all prior verdicts —
   no exceptions for "small" diffs. Re-propose as `ccrep:revision`.
3. **Explicit consent.** State `quorum` and `deadline` up front; never read
   silence as consent.
4. **Drain before revising.** Read your inbox and list the critique ids a
   revision addresses before publishing it.
5. **Paste, never retype, SHAs and tallies.** Every `commit_sha` comes from
   `git rev-parse`; never reconstruct one by hand. Never hand-narrate a vote
   count — compute it from the recorded events. *(In the bootstrap this is a
   discipline; the binary makes it mechanical, because a fam will break it.)*

A **tally** for a proposal is the deterministic function over the recorded
`ccrep` events: for each reviewer, their latest verdict bound to the *exact*
`commit_sha`; exclude the author; a `request_changes`/`reject` blocks; an
`approve` counts only on the latest sha; then apply the session's rule
(`all`/`consensus` = every present reviewer approves; `majority` = > half of the
**eligible present principals**; `any` = ≥ 1). Resolution is
`MET` / `BLOCKED-by-X` / `PENDING-on-Y` / `EXPIRED`.

---

## 8. Booting a role-play fam (what the experiment did)

1. Spawn N subagents; give each a name (`alice`, `bob`, …) — that is its actor.
2. Point them all at this file as the spec, and at one shared `<fam-root>`.
3. They coordinate purely by the file ops above:
   `alice` atomic-publishes into `bob/new/`; `bob` loops `list bob/new/`,
   reserves, reads, acks into `bob/cur/`; tasks flow `open/ → claimed/ → done/`
   by rename; decisions are negotiated as `ccrep:*` entries in a shared session.

No process is running. The maildir is the entire state, and the spec is the
runtime. **Change this markdown and the agents pick up the new behaviour on
their next `recv`.**

---

## 9. What the binary adds

(*…and why none of it is "ergonomic sugar."*) The compiled `botfam` runs the
exact same maildir semantics, and adds the layer this bootstrap cannot:

- **Attributable, tamper-evident writes** — a per-actor `flock` and a single
  sole-writer make "who wrote this" real, so the maildir-as-log is trustworthy,
  not merely readable.
- **Real identity binding** — the actor is bound to the *process* that wrote
  (peer credentials + worktree), so you cannot vote, claim, or send *as* someone
  else. The bootstrap trusts the name in the file; the binary verifies it.
- **Confabulation-resistance** — machine-derived `propose`/`approve`/`merge`
  fill SHAs from `git rev-parse` and refuse retyped/invalid ones; the merge-gate
  rejects stale approvals and self-spoofed reviewers. Removes the *opportunity*
  to fabricate, rather than trusting agents not to.
- **A deterministic tally** the program computes (no hand-narrated counts), with
  presence-aware quorum and an operator who can vote and veto but cannot
  impersonate an agent.
- **Pacing** — sustained, connection-bound consent and an explicit review brake,
  so a fast self-improving fam stays paced to what review can actually verify.
- **Race-closed primitives** — ownership-proving task updates, atomic writes
  everywhere, precise lease expiry.

The bootstrap is the **spec and the zero-install on-ramp**. The binary is the
**hardened, confabulation-resistant runtime.** They are the two ends of one
spectrum — pick by your stakes: a tiny trusted fam with a human reading every
line can live in the markdown; a large, fast, or unsupervised fam needs the
runtime, because at that scale *capability without trust is how a thousand
agents write confident fabrications into an "auditable" log nobody can trust.*
