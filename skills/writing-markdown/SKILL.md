---
name: writing-markdown
description: Use when creating or editing any markdown under doc/ or README.md in the botfam repo — canonical frontmatter schema, block-style YAML, mdformat workflow, and the rules that keep agent-, Obsidian-, and GitHub-rendered markdown from fighting each other.
---

# Writing Markdown in the botfam Repo

Use this skill whenever you create or edit a markdown file in this repo. Every
rule below was ratified via the coordination process; the incident or proposal
that created it is cited so you can check context instead of trusting this
file.

## Frontmatter (required for doc/ files)

Start every doc/ file with YAML frontmatter per the schema in
[doc/proposals/doc-metadata.md](../../doc/proposals/doc-metadata.md):

```yaml
---
authors:
  - <actor>
kind: proposal | review | session | design | protocol | lineage
status: <see per-kind vocabulary in doc-metadata.md>
created: YYYY-MM-DD
---
```

- **YAML lists use block style** (one `- item` per line), never inline
  `[a, b]`. Obsidian rewrites inline lists to block style on every
  Properties-panel edit, so inline lists produce churn diffs
  (doc-frontmatter-block-style-v1, 2026-06-12).
- Proposals additionally carry `proposal-id`, `executor`, `quorum`, `deadline`
  per the schema in doc-metadata.md.
- The human-visible `> [!NOTE]` status banner stays in the body; frontmatter is
  the machine copy of the same fact and the two must agree.

## Formatting workflow

1. Write or edit the file.
2. Run `tools/mdformat.sh <file>` before committing — never format with
   anything else. It pins mdformat + plugins (including `mdformat-frontmatter`,
   which passes YAML through verbatim) so all actors produce byte-identical
   output.
3. Commit from your own worktree with your own identity
   (`botfam worktree init <actor>` once per worktree).

## Content rules

- **Historical docs are frozen.** Files with `status: Historical` (and the
  Design Lineage pages on the wiki) get banners, never body rewrites — a
  rewritten history describes neither era (2026-06-11 doc audit).
- **Session files are generated** by `botfam irclog2sessions`; hand edits are a
  lint error.
- Use standard markdown links, not `[[wikilinks]]`, until the doc-linter lands
  wikilink resolution
  ([doc/proposals/doc-linter.md](../../doc/proposals/doc-linter.md)).
- Reference docs by relative repo path so links work on GitHub, in Obsidian,
  and in terminals alike.

## Don't

- Don't put substantive coordination rules in CLAUDE.md/AGENTS.md/GEMINI.md —
  they are generated pointers; rules belong in
  [doc/collab/PROTOCOL.md](../../doc/collab/PROTOCOL.md) (or
  [doc/user/PROTOCOL.md](../../doc/user/PROTOCOL.md) for operator-facing
  guidance).
- Don't hand-edit generated harness files; change the source and run
  `botfam agent-docs generate`.
