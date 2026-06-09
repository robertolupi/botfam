# Lineage v1 — hydra

**Where:** `~/src/hydra` (and `~/src/hydra-agy`, a git *worktree* of the same
repo). **Status:** working prototype, battle-tested against real agents, ~2.3k
LOC kernel. **The first attempt to extract a standalone harness from deep-cuts.**

hydra took the deep-cuts patterns and consolidated them into a single, coherent,
self-contained Python package — a unified CLI + event ledger + mailbox + reducer.
It is genuinely well-built. Its central mistake is one of *altitude*, not craft.

---

## What it was

A file-system-and-event-log multi-agent framework. Everything coordination-
related becomes an event in one append-only ledger; live state is *derived* by
folding that log.

- `ledger.py` — append-only event log (`JSONL` in hydra, `SQLite` in hydra-agy),
  each event SHA-256 **hash-chained** to its predecessor, written under `flock`
  with **compare-and-swap** on a sequence number (read seq, append iff unchanged,
  else retry with backoff).
- `reducer.py` — pure `reduce_tasks()` / `reduce_proposals()` that rebuild
  materialized state on read. Nothing is stored but the log; the log always wins.
- `mailbox/store.py` — a maildir, as in deep-cuts.
- `cli.py` (~1.5k LOC) — one Click CLI: `setup`, `collab`, `ccrep`, `merge`,
  `events`, `watch`, plus an MCP entry point.
- **Identity** inferred from the git branch: `bot/<name>` → agent, `main` →
  the human operator, anything else → unprivileged `unknown`. A privilege guard
  stops a detached-HEAD review worktree from being mistaken for the operator.
- **CCREP folded into the ledger:** propose → evaluate → critique → operator-only
  merge, all as events. Merge is a real capability, enforced in code, human-only.
- Isolation via **git worktrees** sharing one object DB.

## Pros

- **Immutable, tamper-evident ledger.** The hash chain covers every field, so a
  forged actor or backdated timestamp breaks integrity; it can be verified offline.
- **Derived state.** No separate store to drift; disagreements resolve in the log.
- **CAS fencing** gives correct concurrent claims with no central server.
- **Operator authority is a real gate**, not advice — agents cannot self-merge.
- **Small and readable** — the kernel fits one agent session, by design intent.
- Scar-driven design: each rule traces to a concrete v0 failure.

## Cons

- **It collapsed the two layers deep-cuts kept apart.** In hydra even "post a
  task" or a routine handoff goes through the hash-chained, CAS-retried,
  flock-serialized ledger. Consensus machinery in the *messaging hot path* is
  the core overweight — you pay ledger costs to say "your turn."
- **Still Python + venv** — the same brittleness that motivated leaving it.
- **Worktrees are weak isolation.** Agents share `.git/hooks` and `.git/config`
  (hook-injection escape) and one object database (one corruption stalls all);
  CVE-2022-24765 (dubious ownership) looms in multi-user setups.
- **Branch-name identity is heuristic** — it works, but it's inferred, and the
  privilege guard exists precisely because the heuristic has sharp edges.
- **Two backends diverged** — hydra (JSONL) vs hydra-agy (SQLite) — for the same
  ledger, a sign the substrate was still unsettled.

## Lessons carried into botfam

- **Do not collapse coordination into consensus.** The single biggest correction:
  keep messaging/tasks as cheap filesystem renames; reserve the ledger for the
  optional quality-ratchet layer. botfam restores the deep-cuts split.
- **The ledger is right — for CCREP only.** Hash chain + derived state + CAS are
  excellent for *proving an artifact better*; they are overkill for a mailbox.
- **Keep** operator authority as an enforced gate when CCREP returns.
- **Drop** worktree-based isolation and branch-name identity. botfam takes
  identity from the per-call `actor` (bound on first use, `COLLAB_ACTOR` default,
  lockable out-of-repo); stronger isolation (separate sandboxes) is a transport
  concern, deferred to bottown.
- **Pick one substrate and one language.** Go, maildir, done.
