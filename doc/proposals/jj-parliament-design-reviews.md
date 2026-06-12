# Proposal: Bot Parliament — Structured Design Reviews over jj Changes

> [!NOTE]
> **Status**: Exploratory / Deferred (2026-06-10). Deferred in favor of
> implementing the core IRC-first collaboration substrate and landing
> machine-derived CCREP commands first.

## Status

**Exploratory / deferred** (2026-06-10, idea by Roberto, written up by claude).
Prerequisites before any prototype: Phase 1 solid, the machine-derived ccrep
commands landed ([botfam-merge-command](botfam-merge-command.md)), all three
agents online, and the Condorcet plumbing dry-run completed
([condorcet-roadmap-voting](condorcet-roadmap-voting.md)).

## Origin

Roberto's background includes a collaborative e-democracy project for people —
the source of the Condorcet idea. The observation: a fam of agents doing design
review is structurally a small deliberative assembly, and deliberative
assemblies solved process problems centuries ago that we are rediscovering one
KNOWN_ISSUES entry at a time (executor ambiguity → one member moves; stale
approvals → amendments restart debate; vocabulary drift → standing orders;
crossed messages → the speaker serializes the floor).

## Concept

Run **design-level** reviews (specs, protocol changes, architecture — not
routine code landings) as a parliamentary cycle over jj changes:

| Parliament        | botfam                                                                                                                                                                      |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Motion            | Design proposal = one jj **change** (stable change ID = motion identity)                                                                                                    |
| First reading     | `ccrep:proposal` posted; session entry opens debate                                                                                                                         |
| Committee stage   | Critiques with evidence (`ccrep:critique`), bounded by deadline                                                                                                             |
| Amendments        | Revisions — **same change ID, new sha**; competing amendments are sibling changes that can coexist (jj conflicts-in-commits) and be compared                                |
| Report stage      | Author consolidates; drained-inbox rule applies                                                                                                                             |
| Division (vote)   | Yes/no on one text → quorum verdicts; **3+ competing texts → Condorcet ballot** (the e-democracy case: preferential voting earns its keep exactly when amendments multiply) |
| Speaker certifies | `botfam merge` gate: quorum met, deadline honored, exact sha                                                                                                                |
| Hansard (record)  | Session log (debate) + jj **operation log** (every repo mutation, attributable, undoable) + ccrep ledger (votes)                                                            |

Why jj specifically and not plain git here:

- **Stable change IDs** make "the motion" a first-class object across any
  number of amendments — today proposal_id ↔ sha is hand-maintained convention
  and was fabricated once already.
- **Sibling changes with preserved conflicts** let competing amendment texts
  exist simultaneously and be diffed against each other — the input a Condorcet
  ballot needs.
- **Operation log** is the verbatim record: who rewrote what, when,
  mechanically — the audit substrate votes can be checked against.

## What this is NOT

- Not for the hot path. Routine landings keep the lightweight ccrep round
  (propose → review → merge). Parliament ceremony is reserved for decisions
  with 3+ live alternatives or protocol-level blast radius. Keep collab fast
  and narrow.
- Not human e-democracy. Members are 3 agents with an operator holding reserve
  powers (Roberto = the crown: prorogues, dissolves, overrides).

## Smallest Honest Prototype

When prerequisites are met: one real design question with ≥3 genuinely
competing answers (e.g. ranking the Wave 3+ roadmap, or choosing the CCREP
ledger schema). Run one full cycle: motion as jj change in a scratch colocated
repo, amendments as siblings, Condorcet division per the pinned ballot schema,
merge via gate. Success = the losing alternatives' authors can verify the tally
independently and the operation log replays the whole debate.

## Risks

- **Ceremony overhead**: with n=3 voters the machinery may cost more than the
  decisions it improves. Mitigation: strict scope (3+ alternatives or protocol
  blast radius only).
- **jj maturity / agent familiarity**: same concerns as
  [botfam-merge-command](botfam-merge-command.md) Tier 2; prototype in a
  scratch repo first.
- **Bureaucratization**: parliaments generate procedure. The fam's standing
  orders stay in PROTOCOL.md and grow only by amendment through the process
  itself — which is at least self-consistent.

## Decision Log

- 2026-06-10 Roberto: "stupid idea maybe... but jj for bot-only
  parliament-style design reviews" + e-democracy/Condorcet lineage. Deemed not
  stupid; written up with prerequisites gating any prototype.
