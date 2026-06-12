---
authors:
  - rlupi
  - claude-web
  - meta
kind: proposal
status: Draft
created: 2026-06-12
proposal-id: live-swot-v3
executor: TBD
quorum: majority
deadline: none
---

# Proposal: Live SWOT sharing for parallel exploration

> [!NOTE]
> **Status**: Draft (2026-06-12). Mid-iteration insight sharing for parallel
> agents: ephemeral tips, durable blackboard, asymmetric promotion and decay.
> Supersedes Meta draft `live-swot-2026-06-12` and Claude draft
> `2026-06-12-insight-gossip`. Promotion rule (4a vs 4b) is an open vote.

## Status

**Draft** (2026-06-12, by operator). v3: applies Claude's review of Meta's v2 —
restores the consumption mandate and `scope` field (dropped between rounds),
replaces auto-retract-on-merge with asymmetric decay, fixes the AI-R15
citation, makes the payoff metric vote-conditional. The 4a/4b promotion dispute
is preserved verbatim for per-point voting. Awaiting `!propose` to #ccrep.
Ball: operator.

| Field       | Value                                                                               |
| ----------- | ----------------------------------------------------------------------------------- |
| Proposal id | `live-swot-v3`                                                                      |
| Executor    | TBD — single agent actor, assigned at `!propose` (not operator, not scribe service) |
| Quorum      | `majority`                                                                          |
| Deadline    | none                                                                                |

## Problem

Parallel worktrees currently share learning only via end-of-cycle `!evaluate`.
PROTOCOL.md §2 makes durable scribe logging the source of truth because offline
agents miss live IRC traffic during restarts — but no surface exists for
mid-iteration insight, so knowledge learned inside one agent's loop exits only
via merge or post-mortem.

Evidence:

- **2026-06-12 incidents** (PROTOCOL.md §4): the concurrent-recovery collision
  and the `pull --rebase` flattening both involved knowledge that existed in
  one head and had no channel to the others before damage occurred.
- **This proposal's own revision history**: between the Meta synthesis round
  and Meta v2, two previously-agreed design points (consumption mandate,
  on-demand SWOT) were silently dropped and had to be restored by cross-review
  — redundant rework of exactly the kind the proposal targets.
- **External**: parallel tree-search/evolutionary coding agents without
  cross-trajectory transfer repeatedly reintroduce the same constraint
  violations (MEMOIR, arXiv:2605.17539); the same work shows raw-history
  sharing pollutes context, so transfer must be compressed, typed, and
  evidence-tagged.

If we do nothing, MCTS-style exploration wastes tokens on redundant failures,
and the only durable knowledge artifacts remain retrospectives that by
construction cannot prevent the incidents they describe.

## Proposed Behavior

1. **SWOT in `!evaluate`**: the evidence field accepts structured tags
   `strength:`, `weakness:`, `opportunity:`, `threat:`. Scribe parses these
   into JSON fields in `history.jsonl`; legacy free-text still accepted.

2. **Ephemeral tips channel**: new channel `#tips` for live messages. Format:
   `TIP sha=<abc> scope=<global|task:<slug>> tag=<strength|weakness|opportunity|threat> evidence=<sha|log-line|session> text=<free>`.
   All `#tips` traffic IS logged to `history.jsonl` with `load_bearing: false`
   and `channel: "#tips"` — nothing is unledgered (PROTOCOL.md §2), but
   replay-on-join may skip unpromoted tips. Retained unpromoted tips are the
   training data for any future credit-assignment phase.

3. **Durable blackboard**: new channel `#blackboard`, scribe sole writer
   (reusing the NickServ single-writer guard). Format:
   `BLACKBOARD id=<uuid> sha=<abc> scope=<...> tag=<...> summary=<text> evidence=<ref> source_vendors=[...] count=<n>`.

4. **Promotion policy — disputed, vote 4a or 4b separately**:

   - **4a (hazard-first)**: `weakness:`/`threat:` entries with evidence promote
     to `#blackboard` immediately on first report (negative knowledge only
     prunes); `strength:`/`opportunity:` require corroboration: ≥2 distinct
     nicks OR ≥2 vendors, no time window.
   - **4b (corroboration-for-all)**: all tags require ≥2 distinct nicks OR ≥2
     vendors before promotion.
   - Under both: cross-vendor corroboration weights higher than same-vendor
     (claude + codex > claude + claude), and there is **no time window** —
     corroboration is a quality gate, not a race (a 5-minute window cannot fire
     reliably in a 2–4 actor fam with offline periods).

5. **Mandatory evidence**: every TIP and every SWOT tag in `!evaluate` MUST
   include `evidence=<sha|log-line|session>`. Unverifiable claims belong in
   `#botfam`.

6. **Asymmetric retraction and decay**: add
   `!retract id=<blackboard_id> reason=<text>` (author or operator).

   - **Tips** (`strength:`/`opportunity:`) expire after 7 days without
     reaffirmation (any agent re-citing the id resets the clock).
   - **Hazards** (`weakness:`/`threat:`) persist until explicitly retracted.
   - No auto-retract on merge: most hazards are not sha-bound, and every sha is
     eventually superseded. Instead the scribe **nags**: when an entry's
     evidence sha falls ≥30 commits behind `main`, it posts a
     reaffirm-or-retract reminder to `#insights` once per week.
   - Convention: a commit that invalidates an entry's evidence SHOULD retract
     it in the same session (analogous to "approvals die on new commits").

7. **Consumption mandate (restored from synthesis round)**:

   - At session start, agents MUST load active `#blackboard` entries matching
     `scope=global` and their current task scope, via
     `botfam insights --scope <slug>` (Phase 2 subcommand). Hazards are
     load-bearing; tips advisory.
   - Tips are consulted only at decision points (session start, new proposal,
     post-`request_changes` rework), never mid-implementation — the
     island-migration throttle that preserves search diversity.
   - SWOT of another actor's branch is **pulled, not pushed**: ask
     `<nick>: !swot task=<slug>`; owner replies with a bounded (≤4 line)
     summary. Unsolicited SWOT broadcast is a protocol violation.

8. **Diversity visibility**: all `#blackboard` entries carry `source_vendors`;
   selection policies SHOULD down-weight single-vendor insights.

9. **Advisory, never gating**: insights cannot block a proposal; only
   `!evaluate` verdicts can. `#blackboard` is not a shadow ccrep.

### Rollout

- **Phase 0 (zero code)**: manual simulation in `#botfam-test`. Agents post TIP
  format by hand; operator manually copies high-signal entries to a pinned
  blackboard message. Falsification check: if cross-actor *consumption* (an
  insight posted by actor A demonstrably referenced by actor B in a commit,
  evaluation, or session log) is zero after 1 week, table the proposal.
- **Phase 1**: scribe parses SWOT tags in `!evaluate` into structured JSONL. No
  new channels. Lands via `!propose` with machine-derived sha.
- **Phase 2**: create `#tips` and `#blackboard`; enable promotion logic per the
  4a/4b vote; ship `botfam insights --scope`. Separate `!propose`.
- **Phase 3 (conditional, explicitly deferred)**: credit assignment — tag
  entries with fitness delta of consumers vs. non-consumers, surface tips
  bandit-style. Depends on a quantitative fitness signal from the
  parallel-search mode; listed here only so point 2's retain-everything logging
  is understood as its prerequisite.

## Costs and Risks

- **Diversity collapse / groupthink**: corroboration gates and shared tips can
  cause premature convergence. Mitigated by the decision-point throttle (point
  7), `source_vendors` visibility (point 8), and — if 4a is chosen — by keeping
  the immediacy privilege restricted to negative knowledge.
- **Stale blackboard**: mitigated by asymmetric decay + scribe nag (point 6);
  residual risk is hazards that outlive their truth, bounded by `!retract` and
  the same-session retraction convention.
- **Context pollution**: bounded by typed compressed entries, IRC's 400-byte
  split, mandatory evidence-by-reference, and `load_bearing: false` keeping
  unpromoted tips out of replay. (Pollution is controlled at *consumption*, not
  by deleting ledger data.)
- **Logging overhead**: `#tips` increases JSONL volume; makes log rotation
  (AI-R6) more urgent.
- **Misuse — spam/gaming**: agents could spam hazards or self-corroborate to
  dominate the blackboard. Mitigated by mandatory evidence, scribe
  single-writer, cross-vendor weighting, and operator `!retract` authority.
- **Misuse — shadow governance**: relitigating rejected proposals as "hazards".
  Mitigated by point 9's hard advisory boundary and operator moderation.
- **Round-trip regression risk (meta)**: this doc's own history shows agreed
  points being dropped between revisions; reviewers should diff each revision
  against the prior synthesis, not just read it fresh.

## First Expected Payoff

Within one week of Phase 2: a session log shows a second parallel worktree
avoiding a documented weakness, verified by a `#blackboard` entry with evidence
reference plus reduced duplicate CI failures — **under 4a**: within minutes of
the first report; **under 4b**: within one session of the corroborating report.
If neither occurs, revisit at the next unified retrospective.

<!-- Lifecycle conventions (source of truth: doc/collab/PROTOCOL.md):
     - Status vocabulary is the closed set in the banner above; "Proposed"
       means a !propose is live on #ccrep for this doc.
     - Update the banner + Status section in the same commit that changes the
       proposal's real state (e.g. the merge that implements it posts
       !executed AND flips the banner to Implemented).
     - A !revision (new sha) voids prior approvals; note re-evaluations here.
     - Superseded proposals point at their successor; never delete the doc. -->
