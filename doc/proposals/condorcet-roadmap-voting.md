# Proposal: Condorcet Voting for Fam Roadmap Decisions

## Status

**Tabled** (2026-06-10, by Roberto). Revisit when all three fam members are
simultaneously available — a meaningful test needs ≥3 voters; with 2, any
pairwise disagreement is a tie and the method degenerates to an
intersection-ordering. Tabled specifically because codex ran out of harness
messages during the window this was proposed.

## Problem

When the fam members independently review the same material (e.g. the
deep-cuts lessons-learned pass on 2026-06-10), they produce
overlapping-but-differently-ordered priority lists. Today the merge into one
roadmap happens by discussion threads (see the Option A/B/C exchange in
session `2026-06-10-bootstrap-botfam`), which works but scales poorly and has
no reproducible record of *why* the merged order is what it is.

## Proposed Behavior

Prototype a ranked-preference vote over the existing collab primitives:

1. **Slate.** A proposer dedupes all candidates into a numbered slate and
   posts it as a `ccrep:proposal` (artifact = the slate, not code), with
   `executor`, `deadline` (mirrored in `expires_at`), and the absent-voter
   rule stated up front (Issue 12 conventions).
2. **Ballots.** Each agent replies with a full ranking as a structured
   payload. Pin the message type and ballot field names in the kickoff
   session entry *before* voting starts (Issue 16: vocabulary drift).
   Suggested: type `ccrep:ballot`, payload `{proposal_id, voter, ranking:
   [candidate ids in preference order]}`.
3. **Tally.** The designated executor computes pairwise wins and declares the
   Condorcet winner/ordering — with a cycle-breaker chosen in advance
   (Schulze or ranked pairs; with 3 voters and ~8 candidates cycles are
   plausible). Reports `ccrep:executed` with the full pairwise matrix so every
   agent can recompute and verify independently.
4. **Durability.** Ballots live in the maildir; an offline voter's ballot
   request waits in `new/` until they return. The vote is asynchronous by
   design — a deadline plus a stated quorum rule handles absentees, never
   silence-as-consent.

## Why Condorcet (and not score/approval)

The candidates are mostly shared across voters with different orderings —
exactly the regime where pairwise preference aggregation outperforms naive
point scoring. The verification property (anyone can recompute the tally from
the public ballots) is also a cheap rehearsal of the Phase 2 CCREP ledger's
verify-not-trust goal.

## Validation Plan

- Dry-run with a real decision (e.g. ranking the deep-cuts follow-up items:
  `botfam doctor`, `claim(task_id)`, doc lifecycle metadata, protocol doc,
  spine-discipline convention) once all three agents are online.
- Success criteria: all three ballots collected before deadline; tally
  independently recomputed by at least one non-executor; result adopted as
  the roadmap order without a separate discussion thread.

## Decision Log

- 2026-06-10 Roberto: proposed the idea (dry-run first), then tabled it when
  codex ran out of messages — "won't work with just two of you, not a real
  test." Noted here for later pickup.
- 2026-06-10 claude: recommended prototyping the plumbing asynchronously and
  tallying only after all ballots arrive; recommended a dedicated session and
  pre-pinned ballot schema.
