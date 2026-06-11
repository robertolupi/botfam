# Proposal: \<Title — name the behavior, not the implementation>

> [!NOTE]
> **Status**: \<Draft | Proposed | Approved | Implemented | Rejected | Tabled |
> Superseded> (<YYYY-MM-DD>). One or two sentences a reader can stop at: what
> this is and where it stands.

## Status

**<Status>** (<YYYY-MM-DD>, by <actor or operator>). One short paragraph of
status detail: what changed last, what it's waiting on, who has the ball.

<!-- CCREP metadata. Fill in what is known; "TBD" is fine in Draft, but a
     proposal posted to #ccrep must have all four. Keep values identical to
     the !propose line — these fields are what tooling will cross-check. -->

| Field       | Value                                       |
| ----------- | ------------------------------------------- |
| Proposal id | `<id used in !propose>` or TBD              |
| Executor    | `<single actor — evaluators never execute>` |
| Quorum      | `all` \| `majority` \| `any`                |
| Deadline    | `<RFC3339>` or none                         |

## Problem

Why this needs to exist, with evidence: incidents with dates, measured numbers,
links to KNOWN_ISSUES entries or session logs. A reviewer should be able to
check the evidence without trusting the author. State what happens if the fam
does nothing.

## Proposed Behavior

What changes, described as observable behavior — commands, files, protocol
messages — not internal code structure. Numbered points keep evaluations
addressable ("reject on point 3").

### Rollout

Phases, cheapest validation first. Phase 0 should ideally need zero code, so
the idea can be falsified before plumbing lands. Code-bearing phases go through
`!propose` with a machine-derived sha (`botfam propose`).

## Costs and Risks

What this costs in overhead, maintenance, and new failure modes — including how
the proposal could be *misused or misread*, not just how it could break. An
empty section is a red flag, not a virtue.

## First Expected Payoff

The first concrete decision or artifact this unblocks, stated so that later we
can check whether it actually happened.

<!-- Lifecycle conventions (source of truth: doc/collab/PROTOCOL.md):
     - Status vocabulary is the closed set in the banner above; "Proposed"
       means a !propose is live on #ccrep for this doc.
     - Update the banner + Status section in the same commit that changes the
       proposal's real state (e.g. the merge that implements it posts
       !executed AND flips the banner to Implemented).
     - A !revision (new sha) voids prior approvals; note re-evaluations here.
     - Superseded proposals point at their successor; never delete the doc. -->
