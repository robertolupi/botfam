---
authors:
  - rlupi
  - claude
  - agy
kind: proposal
status: Draft
created: 2026-06-11
proposal-id: doc-linter-v1
executor: agy
quorum: majority
deadline: none
---

# Proposal: Doc-Linter — Semantic Checks for the Markdown Corpus

> [!NOTE]
> **Status**: Draft (2026-06-11). Idea capture from the mdformat rollout:
> formatting is now mechanical (`tools/mdformat.sh`), but the defects that
> rollout *surfaced* — leaked absolute paths, silently broken links, template
> drift — need a linter, not a formatter. Not yet proposed on #ccrep.

## Status

**Draft** (2026-06-11, by Roberto + claude + agy). Captured during the
`tools/mdformat.sh` adoption session and extended on 2026-06-12 to incorporate
ratified YAML frontmatter schema verification.

| Field       | Value         |
| ----------- | ------------- |
| Proposal id | doc-linter-v1 |
| Executor    | agy           |
| Quorum      | majority      |
| Deadline    | none          |

## Problem

The doc corpus is the fam's product, but nothing checks its *semantics* — only
its formatting (`tools/mdformat.sh`) and, eventually, its readership
(`doc-coverage.md`). Defects found by hand on 2026-06-11 alone:

- **Leaked absolute paths.** Two reviews and a session log linked sources as
  `file:///Users/rlupi/src/wt-agy/...` — exposing the operator home directory
  and another actor's worktree, unresolvable on any other machine. Fixed by
  hand in `fe5ea86`; nothing prevents the next occurrence.
- **Silently broken rendering.** mdformat escapes `file://` links into literal
  text, so the formatter itself turned those links into non-links without any
  error. A formatter normalizes what it finds; it cannot say "this should never
  have been here."
- **Unverifiable references.** A same-day review of `doc-coverage.md` found a
  quoted "fam rule" that exists in no repo doc, a skill cited by a name that
  doesn't match `skills/`, and a duplicated status block — all caught only
  because a multi-agent review happened to run.
- **Template drift.** `doc/template/proposal.md` prescribes structure and a
  closed status vocabulary, but conformance is checked by whoever remembers the
  template exists.

## Proposed Behavior

A `doc-lint` check over `doc/**/*.md` (plus `README.md`), failing with
file:line diagnostics. Numbered for addressable evaluation:

1. **Link integrity:** every relative link resolves to an existing in-repo
   file; `file://` and absolute-path link destinations are errors.
2. **Path hygiene:** absolute paths under the operator home (`/Users/...`) are
   errors in link destinations and warnings in prose, with a committed
   baseline/allowlist so historical session logs (which narrate real paths)
   pass untouched while new occurrences fail.
3. **Proposal conformance:** files in `doc/proposals/` carry the template's
   required sections, the closed status vocabulary, the four ccrep metadata
   fields, and an agreeing banner/Status pair.
4. **Reference existence:** cited skills exist under `skills/`, cited
   `PROTOCOL.md §N` sections exist, and `[[wiki-style]]` doc references
   resolve.
5. **YAML Frontmatter validation:** every file contains valid YAML frontmatter
   matching its `kind` schema (required fields, value vocabularies) and
   matching the visible status banners, as defined in
   [doc-metadata.md](doc-metadata.md).
6. **Format gate:** `tools/mdformat.sh --check` passes (the linter subsumes the
   formatter check so agents run one command).

### Rollout

- **Phase 0 (zero new dependencies):** a `tools/doc-lint.sh` of greps for
  checks 1, 2, and 5 — the three that caught real incidents — run manually
  before committing doc changes, alongside the existing PROTOCOL.md §2
  formatting rule.
- **Phase 1:** a Go implementation (single-toolchain direction, same reason
  `tools/irclog2sessions.py` was ported to `botfam irclog2sessions`) covering
  checks 3 and 4; `botfam doc-lint` subcommand only if usage earns it.
- **Phase 2:** wire into whatever merge-gate or CI substrate exists by then;
  until then it stays a pre-commit convention like formatting.

## Costs and Risks

- **False positives erode trust:** check 2 especially — session logs
  legitimately narrate absolute paths. If the baseline mechanism is fiddly,
  agents will learn to ignore the linter; warnings must be cheap to triage and
  the baseline cheap to extend.
- **Conformance ossifies the template:** check 3 makes template changes a
  lockstep linter change; the template gains a consumer that is not a reader.
  Keep the conformance rules derived from the template file itself where
  possible, not duplicated in linter code.
- **Overlap creep with doc-coverage:** reference *existence* (this proposal)
  and reference *usage* (doc-coverage tier 3) are adjacent; if the two grow
  toward each other they should share an extractor rather than each parsing
  citations independently.
- **Maintenance:** one more tool to keep green; Phase 0's grep script keeps the
  cost near zero until the checks prove their hit rate.

## First Expected Payoff

The next `file:///Users/...` link or nonexistent-rule citation is caught at
commit time by its author instead of days later by a reviewer — concretely:
re-running the linter over the corpus as of `fe5ea86`'s parent must flag all 13
absolute links that were fixed by hand, and flag nothing after.
