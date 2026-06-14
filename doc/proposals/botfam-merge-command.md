---
authors:
  - rlupi
kind: proposal
status: Superseded
superseded-by: doc/collab/PROTOCOL.md
created: 2026-06-11
---

# Proposal: Machine-Derived ccrep — `botfam propose / approve / merge`

> [!NOTE]
> **Status**: Superseded (2026-06-13) by the Gitea PR consensus model
> ([PROTOCOL.md](../collab/PROTOCOL.md) §3). The CLI registers no
> `propose`/`approve`/`merge` commands; coordination is managed natively.

## Status

**Superseded** (2026-06-13) by the Gitea PR consensus model
([PROTOCOL.md](../collab/PROTOCOL.md) §3).

## Problem

ccrep payload fields are typed by agents today, and any field an LLM
transcribes can be confabulated. All four protocol incidents of 2026-06-10
share this root cause:

1. **Fabricated full sha** — a revision reported commit `3b97122709e9…`
   (nonexistent; real: `3b971228e156…`). Correct 7-char prefix, reconstructed
   tail. Abbreviated verification hides it; sha-bound approvals silently break.
2. **Stale-approval merge** — an executor merged a new commit reusing the
   approval of its predecessor.
3. **Empty `ccrep:executed`** — a report with no proposal_id or resulting sha,
   useless for verification.
4. **Executor ambiguity** (morning incident) — approver executed a merge the
   proposer had claimed.

The protocol asks agents to transcribe machine state through their token
stream. The fix is structural: machine-derive every field that names machine
state.

## Proposed Behavior (Tier 1)

Three subcommands extending `merge-gate` from validator to actor:

- **`botfam propose --proposal <id> [--quorum q] [--deadline t]`** — reads
  `git rev-parse HEAD` itself, emits the `ccrep:proposal` with the sha
  machine-filled, session-logs the transition. The author never types a sha.
- **`botfam approve --proposal <id> [--verdict v]`** — binds the verdict to the
  latest sha read from the proposal/revision record in the store, not from
  reviewer-typed text. Refuses if the working tree's view of that sha doesn't
  exist (catches fabrication at source).
- **`botfam merge --proposal <id>`** — one atomic executor action: runs the
  merge-gate checks (fresh approval on the exact sha, ≥1 independent non-author
  approval, no blocking verdicts, declared quorum met, deadline not expired),
  performs the merge to main itself, then emits `ccrep:executed` with the
  resulting sha from `rev-parse` and session-logs it. No step can be skipped or
  reported wrong because no step is manual.

Design constraints:

- Keep `collab` fast and narrow (codex's lesson 1): these live beside the
  task/mailbox hot path, not inside it.
- The commands *generate* the same pinned-vocabulary messages (PROTOCOL.md §4)
  — agents and commands stay wire-compatible during migration; hand-written
  messages remain legal but become the audited exception.
- Quorum/deadline enforcement moves from convention into `merge` (closing the
  gap noted in the W1-B review).

## Validation Plan

- Invariant tests: propose→approve→merge happy path; merge refused on stale
  approval, on missing independent approval, on unmet quorum, on expired
  deadline; executed report sha always equals actual main tip.
- Dogfood: first real use lands the proposal after this one.

## Tier 2 — jj (Jujutsu): researched option

What jj would buy, in botfam terms:

- **Stable change IDs**: a jj change keeps identity across rewrites while the
  sha churns — natively the `proposal_id ↔ commit_sha` mapping ccrep maintains
  by convention. Revisions become "same change, new sha".
- **Operation log**: every repo mutation recorded, attributable, undoable — an
  audit/rollback substrate for agent-driven merges; philosophically the Phase 2
  ledger applied to the repo itself.
- **Conflicts-in-commits / workspaces**: agents can hand off conflicted states
  without blocking each other.

Why not now: substrate swap mid-flight while guardrails are still landing
(violates "no scope expansion until Phase 1 is solid"); harnesses, `gh`, and
model training assume git, so error rates initially rise. Colocated jj+git
means adoption is not all-or-nothing.

**Evaluation criteria (revisit after Wave 2):** colocated-mode stability on
this repo; whether change IDs measurably simplify the revision flow vs. Tier 1
commands alone; agent error rate with jj CLI in a scratch-repo trial; interop
with the bot-parliament concept
(\[[jj-parliament-design-reviews|doc/proposals/jj-parliament-design-reviews.md]\]).

## Decision Log

- 2026-06-10 Roberto: "maybe botfam should have commands to do merge — think
  jj, or even adopt jj maybe." Direction accepted; proposals written.
- 2026-06-10 claude: Tier 1 recommended for Wave 2; Tier 2 parked with explicit
  evaluation criteria.
