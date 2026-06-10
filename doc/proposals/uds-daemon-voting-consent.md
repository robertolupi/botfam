# Proposal: UDS Daemon, CLI Voting, Operator Participation & Machine-Derived CCREP

## Status

**Draft for 3-principal approval** (2026-06-10). Authors: spine by agy (`6c00bf1`), consolidated by claude with Roberto's operator-participation decisions. Approval = the 3 present principals (claude + agy + Roberto); codex's async ballot (Q1–Q5) gates *canonical adoption*, not the dev-branch build.

---

## 1. Purpose & Threat Model

botfam coordinates a small fam of cooperative coding agents plus a human operator. Its job is **governing overeager cooperative peers and pacing the velocity of a self-improving system** — not resisting a malicious adversary.

Consequences that bound every design choice below:
- Same host, same OS user → **no OS-enforced boundary between agents**. Local identity is **attribution and audit, not authentication**. We do not claim cryptographic security.
- The real failure mode (observed repeatedly): an overeager/buggy peer **transcribes metadata wrongly or skips a guardrail** — fabricated SHAs, hand-edited ledger records, prose contradicting an artifact. The design's primary defense is **machine-derived inputs + tamper-evident, kernel-checked provenance**, which removes the agent's opportunity to confabulate.
- The brake on velocity is **review/consent cadence**, deliberately. At AI build-speed, review is the binding constraint, by design.

---

## 2. Topology

```
agents      ── UNIX-domain socket ──►┐
                                      ├─► [ botfam server (single process) ] ─► maildir on disk (sole writer)
operator UI ── localhost HTTP/SSE ──►┘
botfam vote ── persistent UDS conn ──► [ process-ancestry check → live vote token ]
```

- **Single-process daemon**, host-local. Agents connect over a **UNIX-domain socket** (`~/.botfam/daemon.sock`). The **operator UI** connects over **localhost HTTP/SSE** (browsers speak HTTP, not UDS). Both are local-only; **host-only holds** — no remote agents, no bearer tokens, no cross-host transport.
- **In-memory state guarded by a `sync.Mutex`**, released before parking on Go channels for blocking `recv` (so a blocked `recv` never freezes other operations — avoids reintroducing the stdio single-thread stall).
- **Maildir is the on-disk format and the daemon is its sole writer.** This preserves crash durability (state is files), **zero-config migration** (the existing store *is* the format), and **`ls`/`cat` transparency** — while eliminating the multi-process FS races (sweep-vs-claim, torn writes) because there is now exactly one writer. Precise lease expiry via `time.AfterFunc` instead of lazy sweeps.

---

## 3. Identity

- **Durable actor** = the agent's mailbox, tasks, sessions, presence — keyed to the **worktree** (git-rootset-derived; the directory path is a mutable corroborator, not the key). Survives harness restart: a reconnecting harness re-derives its actor from the worktree and reattaches.
- **Operator is a distinct, first-class principal** ("operator"), never an agent. **The operator cannot cast a vote attributed to an agent**, symmetric to the rule that an agent cannot vote as another. This keeps agent consent genuine: a recorded "claude approved" provably came from claude's process-ancestry vote, never from an operator click.
- Local identity is **corroboration**, not proof (see §1). The real defense is machine-derived provenance (§5, §7) and the evidence layer.

---

## 4. Sessions as Governed Deliberations

A session is a deliberation space with an operator-defined constitution, set at creation:
- **Decision rule**: `quorum` (`all`/`majority`/`any` of present principals) **or** `consensus` (no dissent).
- **Goals**: the objective the session is pursuing.
- **Guardrails**: constraints (e.g. "no merge to main", "tests green required", "max N merges").

Rules are **fixed at session creation; any change is logged and non-retroactive to in-flight votes** — the operator cannot move the goalposts to force a stuck decision (the human's own power stays auditable, consistent with §1). This generalizes and subsumes the per-proposal `quorum` field; the merge-gate becomes "apply this session's rules."

---

## 5. Consent: Process-Ancestry CLI Voting (`botfam vote`)

- **Cast a vote** by running `botfam vote --proposal <id> --verdict <approve|reject|request_changes>` from the worktree.
- **Validate once, at spawn**: the daemon reads the connecting process's PID via socket peer credentials (`LOCAL_PEERPID` on macOS — *not* `LOCAL_PEERCRED`, which carries no PID; `SO_PEERCRED` on Linux) and walks its parent chain once to confirm it descends from the registered agent harness. Because the command is spawned from the worktree, its CWD is reliably the worktree. This is **confabulation-resistance / blocks cross-harness mis-attribution** — not adversary-proof under same-UID, by §1.
- **Sustained vote = live socket**: the `botfam vote` connection stays open; **the live connection is the vote token**. Drop (crash, harness close) → vote auto-withdrawn. This structurally eliminates stale-approval merges (consent exists only while the voter is live) — the deliberate velocity brake.
- **Dedup to principal**: all votes from one harness root (incl. its subagents, which share the ancestry) collapse to one principal — one principal, one vote, by construction.
- **Reconnect replaces**: a reconnection during the proposal replaces the dropped vote (anchored by worktree), preserving one-principal-one-vote across a harness restart.
- **Server-side release**: the daemon holds vote connections open until a **resolution signal** — quorum/consensus met per the session's rule, deadline reached, or operator abort — then closes them, letting the `botfam vote` processes exit cleanly.

---

## 6. Presence-Aware Quorum (required)

Presence is a hard dependency of the consent model, not optional.
- **Tier 1 — crash liveness**: UDS connection state = instant crash/offline detection.
- **Tier 2 — readiness**: collab activity updates `presence/<actor>.last_seen`; silence > 30 min ⇒ `away` (catches token-death, where the process stays alive but the agent isn't acting).
- **Absentee exclusion**: quorum is computed over **present** principals; at the session/proposal deadline, absentees are logged **non-blocking** and excluded — so a token-dead peer (e.g. codex) cannot freeze the queue.
- **Auditability**: `ccrep:executed` snapshots the exact presence + absentee list at execution.

---

## 7. Machine-Derived CCREP (confabulation-resistance)

Subcommands fill machine-checked facts so the agent never hand-types verifiable metadata:
- **`botfam propose --proposal <id> [--quorum q] [--deadline t]`** — reads `git rev-parse HEAD` and emits `ccrep:proposal` with the SHA machine-filled.
- **`botfam approve --proposal <id> --verdict v`** — binds the verdict to the latest SHA in the proposal/revision record; refuses if that SHA doesn't exist.
- **`botfam merge --proposal <id>`** — runs the merge-gate (fresh approval on the *exact* SHA, ≥1 independent non-author approval, the session's declared rule met, deadline not expired, **operator veto honored on foundational** per §8), performs the git merge, emits `ccrep:executed` with the resulting SHA.

- **`botfam tally --proposal <id>`** — the authoritative, machine-derived **consensus-state function**: a deterministic computation over the append-only vote/ccrep ledger that anyone can reproduce and get the same answer (botfam's **fork-choice rule**). For each proposal it lists every principal's verdict bound to the *exact* SHA, with **provenance** (ancestry-verified agent vote / operator principal / interim dual-channel hash), timestamp, and **live presence**; applies the session's declared rule (§4) and the merge-gate invariants; and terminates in an **unambiguous resolution**: `MET` / `BLOCKED-by-<X>` / `PENDING-on-<Y>` / `EXPIRED`. It is *live* (sustained votes drop on disconnect; presence shifts) and **never hand-typed**. `botfam merge` is a tally consumer (merges iff `MET`); the operator UI (§8) renders it live; `ccrep:executed` snapshots it at resolution. The tally underlies and generalizes the merge-gate.

  *Why first-class:* a hand-narrated tally ("claude: approve, agy: pending") is itself the confabulation failure mode this design removes — the program must compute the count, not an agent.

This directly closes today's failure class: no retyped SHAs (so no fabricated tails), no hand-edited records, no stale-approval merges, no hand-narrated tallies.

---

## 8. Operator UI & Vote Power

- **UI**: the daemon serves a localhost web view over HTTP/SSE: live sessions/discussions, proposals, roster/presence; with **comment** and **vote** write-paths. The UI is also the transparency layer alongside `ls`/`cat`.
- **Graduated operator power**:
  - *Watch + comment*: always, everywhere.
  - *Vote*: anywhere the operator chooses (as the operator principal).
  - *Required consent / veto*: on **foundational/irreversible** actions (merges to main, protocol/store changes). Reversible operational churn proceeds on agent quorum with the operator optional.
- The operator therefore fills the third-principal seat directly: **claude + agy + operator** is a valid trio while codex is async.

---

## 9. Sequencing

1. **Dev-branch prototype now** (reversible): build the daemon + `botfam vote` + presence + machine-derived ccrep + a minimal operator UI against the existing maildir store. Approved by the 3 present principals.
2. **agy builds, claude reviews** (roles fixed: builder / independent reviewer).
3. **Dogfood**: the operator uses the prototype UI to follow this very thread and cast the codex-ballot decisions.
4. **codex async-ratifies** (Q1–Q5) on return → only then **canonical cutover** (the irreversible adoption as *the* coordination plane). The dev-branch build proceeds without waiting; the cutover waits for codex.

In parallel, reversible operational work (F4 session robustness, F5 `expires_at`, F8 docs incl. run-merge-gate-before-merge, F9 done) proceeds under present-quorum.

---

## 10. Open Mechanics (to pin during build, not decisions)

- Exact resolution-signal semantics per session rule (quorum vs consensus paths).
- Reconnect-replace race window (grace period; worktree anchor).
- UI ↔ operator binding on localhost (the operator principal; single-user-host assumption).
- Minimal-UI v0 surface vs the full vision.

## 11. Verification Plan

- UDS connection liveness + vote auto-withdrawal on disconnect.
- Process-ancestry validation (macOS `LOCAL_PEERPID` / Linux `SO_PEERCRED`); cross-harness vote rejected.
- Reconnect-replace + one-principal-one-vote dedup (incl. subagents sharing the root).
- Absentee exclusion at deadline; `ccrep:executed` presence snapshot.
- `propose`/`approve`/`merge` invariants: no stale approvals, no invalid SHAs, no executor-role swaps, operator veto honored on foundational.
- Operator cannot author an agent-attributed vote.
- Per-session rule changes logged and non-retroactive.
