---
authors:
  - claude
  - rlupi
kind: protocol
status: Live
created: 2026-06-12
---

# botfam Operator Guide (Human PROTOCOL)

The human companion to [doc/collab/PROTOCOL.md](../collab/PROTOCOL.md). Agents
read that file; you read this one. Same rules, seen from the operator's chair —
every "don't" below is annotated with the incident that created it.

## Where to work

- **Work in your own worktree** (`~/src/wt-rlupi`), exactly like the agents do.
  Your branch, your tree, your identity.
- **Never work in `~/src/botfam`.** The main checkout is the shared merge
  target; only a claimed executor touches it, one operation at a time.
- Point Obsidian (and any other GUI tool) **only at your worktree**. Opening
  the main checkout as a vault is how 2026-06-12 happened.

## Obsidian: do

- Commit and push from obsidian-git on your own branch — that part of the
  plugin is safe.
- Give every doc the frontmatter properties from
  [doc/proposals/doc-metadata.md](../proposals/doc-metadata.md): `kind`,
  `status`, `authors`, `created` (they render as the Properties panel you
  like).
- Prefer standard markdown links: Settings → Files & Links → turn off "Use
  \[[Wikilinks]\]". Wikilink support is pending the doc-linter.
- To pull the latest main into your worktree, run `botfam worktree sync`
  (bind it to an Obsidian Shell-commands hotkey). It refuses to run in the main
  checkout, stashes local changes if dirty, and merges — never rebases.
- Ask on `#botfam` when you want something done to a shared tree: "claude: sync
  wt-rlupi to main" is the protocol working as designed.

## Obsidian: don't

- **Don't let any plugin pull, sync, or rebase the main checkout.**
  obsidian-git's sync ran `git pull --rebase` there on 2026-06-12 and silently
  rewrote three ratified merges and every ledger SHA. Restored the same
  morning, but only because origin hadn't been pushed yet.
- **Don't let setup wizards write your identity into a shared `.git/config`.**
  Repo-local `user.*` overrides every worktree's identity; it misattributed
  agent commits within minutes on 2026-06-12. In your worktree run
  `botfam worktree init rlupi` once instead — it sets the per-worktree config
  that nothing can override.
- **Don't keep anything you need in `scratch/`.** It is /tmp by convention; a
  cleanup destroyed claude's IRC credentials there on 2026-06-12.
- **Don't edit other actors' worktrees** — theirs are read-only to you, as
  yours is to them.
- `.obsidian/` is gitignored; don't force-add it. Custom plugins will be
  versioned under `obsidian/` with an install symlink (doc-metadata TODO 5).

## Landing your changes

- Commit in your worktree, then either ask an agent on `#botfam` to review and
  merge, or run the CCREP flow yourself:
  `!propose id=<id> sha=<sha> quorum=majority executor=<actor> summary="…"`,
  wait for votes, `!tally id=<id>`.
- As operator you *can* commit straight to main — but every direct commit skips
  the ledger, so prefer the flow above when agents are awake.
- **Never rebase or force-push main.** Merge only. No exceptions, including for
  tools acting on your behalf.
- Run `tools/mdformat.sh` before committing doc changes, or ask an agent to
  format for you.

## When something breaks

- Say so on `#botfam` and **name one fixer** ("claude: take this"). Two
  well-meaning fixers executing the same recovery concurrently nearly corrupted
  main on 2026-06-12.
- Don't reset, delete, or rewrite anything shared before announcing it — the
  reflog forgives most mistakes, but only if nobody builds on top of them
  first.
- The durable record is the scribe ledger (`history.jsonl`) and git history; if
  IRC is down (Docker Desktop off), fix that first — the fam is blind without
  it.
