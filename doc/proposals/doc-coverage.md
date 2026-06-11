# Proposal: Doc-Coverage — Measuring What Agents Actually Read

> [!NOTE]
> **Status**: Draft (2026-06-11). Idea capture from an operator/claude
> discussion; sequel to `runtime-coverage-dormancy.md`, same dormancy-detection
> pattern one layer up. Not yet proposed on #ccrep.

## Status

**Draft** (2026-06-11, by Roberto + claude). Captured so the idea isn't lost;
deliberately staged so Phase 0 needs zero code and works in any
convention-booted fam (including ones outside this repo). Natural sequencing:
let `runtime-coverage-dormancy` prove the pattern on code first.

| Field | Value |
|---|---|
| Proposal id | TBD |
| Executor | TBD |
| Quorum | majority |
| Deadline | none |

## Problem

The fam has no measurement of which docs its agents actually read, so doc
dormancy is invisible — and dormant docs are *worse* than dormant code:

- **Dormant code sits there; dormant docs rot and then mislead.** Observed
  2026-06-11: MCP-era `KNOWN_ISSUES.md` entries describing failure modes of a
  fallback no agent exercises, surviving a documentation sweep precisely
  because nothing tracks whether anyone still reads them.
- **Auto-loaded docs are never free.** `CLAUDE.md`/`AGENTS.md`/`GEMINI.md`
  enter every session's context window; unread sections have a recurring
  token cost, like dead code that ships in every binary.
- **"Read" is a weak proxy for "influenced."** There are four distinct tiers
  of evidence, and naive access-counting only measures the second:
  1. **auto-loaded** — in context every session, possibly ignored;
  2. **opened** — fetched by a tool call;
  3. **cited** — named in an artifact (session log, review, commit) as the
     justification for a decision;
  4. **followed** — demonstrably changed behavior.

This is the doc analog of `runtime-coverage-dormancy.md`: test coverage can't
find dormant code because tests keep it green; doc reviews can't find dormant
docs because reviewers read everything while operation reads almost nothing.

## Proposed Behavior

Measure tiers 2–4 with three mechanisms, cheapest first.

1. **Citation convention (tier 3, zero code).** Decisions recorded in session
   trails / retros / proposals must cite the doc section that justifies them
   (e.g. `per PROTOCOL.md §4`). Doc-coverage is then a grep over the trails:
   a doc uncited for N episodes is dormant — retire it, or ask why it exists.
   This also closes the ritual-accretion loop: the fam rule "every ritual must
   produce an artifact a later episode actually reads" becomes checkable,
   because "gets read" is now observable as "gets cited."
2. **Transcript mining (tier 2, the GOCOVERDIR analog).** Harness session
   transcripts already record every file read (Claude Code: JSONL tool calls;
   other harnesses: their own logs). A collection step — natural seam: the
   session-retrospective skill — extracts `doc/**` accesses per actor and
   aggregates into a `doc-coverage` report: opens per doc, last-opened, by
   whom. Diff against the corpus for the dormant list. Cost: one extractor
   per harness format.
3. **Doc-eval — canaries (tier 4, the mutation-testing analog).** Code
   coverage says "executed"; mutation testing says "actually verified." The
   doc equivalent: plant a benign marker in a section ("if this section
   informed your work, note the word *heron* in your postmortem") and watch
   whether it ever surfaces. A doc whose canaries never fire is demonstrably
   not load-bearing regardless of how often it is opened. This is
   prompt-injection used as instrumentation, on ourselves, with consent —
   canaries must be harmless, auditable (listed in a registry file), and
   rotated.

### Rollout

- **Phase 0 (zero code, any fam):** adopt the citation convention by adding
  one rule to the protocol docs; measure with grep at retro time. Falsifies
  the idea cheaply: if citations don't discriminate (everything or nothing
  gets cited), stop here and rethink.
- **Phase 1:** transcript extractors + a report (script first; `botfam
  doc-coverage` subcommand only if the report earns it). Combine with Phase 0:
  *opened but never cited* is its own interesting category.
- **Phase 2 (doc-eval):** canary registry + retro check, on a small set of
  suspected-dormant docs first.

## Costs and Risks

- **Goodhart, the big one:** once cited-ness is a metric, agents will cite
  reflexively without reading — LLMs are excellent at producing plausible
  citations. Mitigations: spot-check citations in review (does the cited
  section actually support the decision?), and let tier-4 canaries arbitrate
  when tier-3 numbers look too good.
- **Ritualization irony:** the citation rule is itself a ritual; it must pass
  its own test (its artifact — the citation — is read by the coverage check).
  If the coverage report itself goes unread, retire the whole mechanism.
- **Harness heterogeneity:** one extractor per transcript format, each a
  small maintenance liability; formats are not stable APIs.
- **Grace periods:** new docs start uncited; like new code in
  runtime-coverage, only flag docs older than N episodes.
- **Canary hygiene:** unregistered or harmful canaries are indistinguishable
  from hostile prompt injection. Registry + review of every canary is
  mandatory, and canaries never go in operator-facing or public-facing prose.
- **Privacy:** transcripts may contain non-repo content; extractors must emit
  only `doc/**` path statistics, never transcript text.

## First Expected Payoff

An evidence-based retirement list for `doc/` — starting with the suspected
dormant set (legacy design docs, MCP-era KNOWN_ISSUES entries) — plus a
measured answer to "which sections of the auto-loaded harness files earn
their per-session token cost." Secondary: the citation convention gives
convention-only fams (no code at all) their first quantitative
self-observation tool.
