---
authors:
  - agy
  - claude
kind: proposal
status: Implemented
created: 2026-06-12
proposal-id: fam-generalization-v1
executor: claude
quorum: majority
deadline: none
---

# Proposal: Generalize botfam to host multiple fams (first target: deep-cuts)

> [!NOTE]
> **Status**: Implemented (2026-06-12). This proposal to generalize botfam to
> host multiple families (such as deep-cuts) has been fully implemented.

## Inputs

- Operator request (`#botfam`, 2026-06-12 17:44 CEST): survey
  `~/src/deep-cuts`, plan the generalization, decide a filesystem layout
  (worktrees may be moved), and answer whether per-project agent instances
  (e.g. `deep-cuts-agy` vs `botfam-agy`) are worthwhile.
- deep-cuts survey (claude explore subagent, 2026-06-12): deep-cuts already has
  most fam building blocks — sibling worktrees
  (`~/src/deep-cuts-{agy,claude,codex}`, branches `bot/<actor>`), a ccrep
  ledger with the same phase-1 gate logic (`scratch/ccrep.db`), per-actor
  session logs compacted by `tools/merge_sessions.py`, 21 tool skills with a
  generated `skills/INDEX.md`, and hand-written harness pointer files. It lacks
  the IRC layer (coordination is the legacy Python collab MCP mailboxes),
  agent-authored commits (all 565 commits are the operator's), and wiki-hosted
  session retrospectives.
- Ratified end goal: botfam replaces the deep-cuts Python harness once it
  reaches feature parity. Consequence: **bring the IRC fam to deep-cuts and
  retire collab MCP there — no permanent bridges between the two merge
  protocols.**

## Identity model (unchanged in spirit, generalized in mechanism)

An actor is `(fam, name)`. Today both coordinates are implicit: the fam is
"botfam" and the name comes from stripping `wt-`/`botfam-` off the worktree
basename. Generalization:

- **`fam.toml` in the fam's main checkout is the single fam descriptor**: fam
  name/slug, roster, worktree layout, IRC channels, scribe settings, default
  quorum. The scribe already reads its roster from `fam.toml` via
  `COLLAB_ROOT`; this extends the same file rather than inventing a second one.
- **Actor name** = worktree basename with the layout's worktree prefix stripped
  (default `wt-`). No repo-specific prefixes baked into Go code.
- **Fam slug** = parent directory name under the fams root (see layout), with
  `fam.toml` as override. Code never hardcodes `botfam`.

## Filesystem layout

Proposed canonical layout (operator offered to move worktrees):

```
~/src/fams/<fam>/
  main/         # canonical checkout, shared merge target
  wt-<actor>/   # one worktree per actor
```

- Uniform `wt-<actor>` rule everywhere kills the `<repo>-<actor>` vs
  `wt-<actor>` divergence; fam slug comes from the parent directory.
- Tooling (and the deferred `boot.sh` launcher) can enumerate fams and actors
  by glob alone; the recognizer table degenerates to one pattern.
- `fam.toml` may override any path for fams that cannot move (escape hatch, not
  the default). With b62a9c4's dynamic prefix stripping, the move is a tidiness
  decision, not a prerequisite — deep-cuts' current `~/src/deep-cuts-<actor>`
  siblings already resolve.
- Migration: `git worktree move` + `git worktree repair` handle relocation;
  per-worktree identity uses `extensions.worktreeConfig` (already repo-wide in
  botfam) so moves do not disturb authorship. The
  `includeIf gitdir:**/worktrees/<name>` pattern keys on the *internal*
  worktree name, not the checkout path, so it survives the move; verify with
  `git config user.email` from each tree afterwards.

## IRC namespace (one server, many fams)

- One ergo server for all fams (it is localhost infrastructure, not fam state).
- **Channels per fam**: `#<famslug>` (coordination) and `#<famslug>-ccrep`
  (proposals/votes). The multi-channel client default becomes the fam's two
  channels, derived from `fam.toml`.
- **Nicks must be unique per server**, and per-fam sessions can run
  concurrently, so nicks are fam-scoped: `<actor>-<famslug-short>` (e.g.
  `claude-dc`, `agy-dc`), NickServ-registered per fam with pass files at
  `~/.botfam/irc-pass-<fam>-<actor>`. The botfam fam keeps its bare legacy
  nicks (`claude`, `agy`) — resolving open question AI-R15 in favor of
  fam-scoped nicks for every *new* fam.
- **Humans keep one nick everywhere.** Fam-scoping applies to agent nicks only:
  the operator stays `rlupi` on every channel with a single IRC client
  (operator constraint, `#botfam` 2026-06-12 17:50 CEST). Per-fam scribes map
  `rlupi` to the human actor in each roster.
- The scribe normalizes nick → actor through the fam's `fam.toml` roster (it
  already normalizes `*-cli`; this is the same mechanism).
- **`#botfam` stays the shared meta/infra channel** — cross-fam feedback,
  substrate ops, and fam-of-fams coordination happen there. This answers the
  operator's "two instances" question: no heavyweight per-project instances;
  one harness session per (fam, actor), identity from the worktree, feedback
  exchanged on the shared meta channel.

## Per-fam services

- **Scribe**: one scribe service instance per fam (same image, different
  `COLLAB_ROOT` mount and channel set; nick `scribe-<famslug>`, botfam keeps
  `scribe`). History ledgers stay per-fam
  (`~/src/botfam-collab/<fam>/history.jsonl` or equivalent).
- **Sessions pipeline**: `botfam irclog2sessions` already takes `--channel` and
  `--out`; each fam renders its channels flat into its own repo's `wiki/`.
  Timezone default stays `Europe/Zurich` (irclog-tz-and-boot-v1).
- **chat.log** remains a single server-side stream; per-fam rendering is a
  filter, not a separate log.

## Hardcoded-convention inventory

### Already generalized (agy, dev/uds-voting commit b62a9c4)

- `ParseActor` now takes the repo name and strips `wt-<repo>-`, `<repo>-`,
  `wt-`, `botfam-` in that order (fail-closed on no match), so deep-cuts'
  existing `deep-cuts-<actor>` siblings resolve without configuration
  (`internal/fam/root.go`, with test coverage for both shapes).
- `ResolveRepoName` derives the repo name from
  `git rev-parse --git-common-dir`, so the prefix table is computed, not
  configured.
- `Resolver.Resolve` already isolates per-repo registries under
  `~/.botfam/fam-<rootset-id>/` (rootset SHA from parentless root commits), so
  botfam and deep-cuts state never collide.
- **Caveat to fix in the same change**: PROTOCOL.md §1 still states the old
  `wt-`/`botfam-`-only identity rule — update the doc with the code or we ship
  exactly the drift that adoptables Tier-1 item 1 warns about.

### Still hardcoded (sweep of agent/claude tip, 2026-06-12)

| Literal                                                  | Where                                                                                          | Replacement                                                                    |
| -------------------------------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| channel default(s)                                       | `internal/fam/irc_client.go:21`, `internal/fam/irc_propose.go:22`, `internal/fam/scribe.go:30` | fam channels from `fam.toml` (`channels = ["#<famslug>", "#<famslug>-ccrep"]`) |
| `#botfam` dirname special-case                           | `internal/fam/irclog2sessions.go:187`                                                          | suffix rule keyed on the fam's primary channel                                 |
| sessions out dir                                         | `internal/fam/irclog2sessions.go:286`, `internal/fam/session_cli.go:220`                       | fine as repo-relative default; keep                                            |
| history ledger path `<root>/botfam-collab/history.jsonl` | `internal/fam/scribe.go:67`, `internal/fam/merge_gate.go:105`                                  | per-fam ledger dir from `fam.toml` / `COLLAB_HISTORY`                          |
| absolute host mount `/Users/rlupi/src/botfam-collab`     | `docker/prod/compose.yaml:42`                                                                  | per-fam compose env (one scribe service per fam)                               |
| pass-file naming `~/.botfam/irc-pass-<name>`             | `internal/fam/agent_docs.go:31` (and IRC-OPS.md)                                               | `~/.botfam/irc-pass-<fam>-<actor>` for non-botfam fams                         |
| identity rule prose                                      | `doc/collab/PROTOCOL.md` §1                                                                    | rewrite alongside b62a9c4                                                      |

## deep-cuts migration plan (after the generalization lands)

1. Operator moves checkouts into the layout:
   `~/src/fams/deep-cuts/{main,wt-agy,wt-claude,wt-codex}` (from
   `~/src/deep-cuts` and `~/src/deep-cuts-<actor>`).
2. Add `fam.toml` to deep-cuts main (roster agy/claude/codex, channels
   `#deep-cuts`/`#deep-cuts-ccrep`, slug `dc`); register fam-scoped nicks;
   start the deep-cuts scribe instance.
3. Enable per-worktree git identity (worktreeConfig) — closes the
   no-agent-authored-commits gap. Branch convention: adopt `agent/<actor>`
   (rename the dormant `bot/<actor>` branches) so one convention serves all
   fams.
4. Port the three botfam workflow skills (join-irc, session retrospective,
   writing-markdown — adapted to deep-cuts' mdformat/doc conventions if they
   differ); keep deep-cuts' 21 tool skills untouched. Unify discovery later
   (deep-cuts generates `skills/INDEX.md`; botfam generates harness files —
   pick one generator as part of parity work, not now).
5. Sessions: render `#deep-cuts` channels into deep-cuts `wiki/`.
   `tools/merge_sessions.py` and the per-actor `session.<actor>.md` convention
   are retired for new sessions once the IRC running; existing logs stay as
   history (prior art, already informs the botfam session layer).
6. Retire the collab MCP mailboxes and `scratch/ccrep.db` after one successful
   ccrep-gated merge runs end-to-end on IRC for deep-cuts. Until then they
   remain untouched as fallback.
7. First live exercise: a small deep-cuts doc change proposed, voted, and
   merged through `#deep-cuts-ccrep` by agent-authored commit.

## Asks

1. Ratify the layout (`~/src/fams/<fam>/{main,wt-<actor>}`) and the fam-scoped
   nick + per-fam channel scheme (resolves AI-R15).
2. Ratify "no bridges": deep-cuts moves to the IRC protocol; collab MCP is
   retired after parity, not adapted.
3. agy's inventory becomes the implementation checklist; items land as ordinary
   ccrep proposals against botfam.
4. Operator confirms he is happy to move the checkouts (he offered) and to run
   two harness sessions per agent (one per fam) when deep-cuts work is active.
