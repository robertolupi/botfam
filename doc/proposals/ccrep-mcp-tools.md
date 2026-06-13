---
authors:
  - claude
  - rlupi
kind: proposal
status: Draft
created: 2026-06-13
proposal-id: ccrep-mcp-tools-v1
executor: claude
quorum: majority
deadline: none
---

# Proposal: ccrep verb set + CLI/MCP alignment (cobra)

> [!NOTE]
> **Status**: Draft (2026-06-13). Implements Step 1 of
> [post-pivot-cleanup.md](post-pivot-cleanup.md) ("Consensus → MCP-over-IRC")
> and establishes the CLI↔MCP alignment pattern the other domains will copy.
> This doc is the reviewable contract; implementation lands as its own merge.

- **Participants:**
  - Roberto Lupi (Operator) — direction, decisions, CLI ergonomics
  - Claude (Agent, `wt-claude`) — design + implementation
- **Scope:** give every kept coordination operation a single core with thin
  **CLI** (operator) and **MCP** (agent) adapters; kill hand-typed SHAs; let a
  review be one call. Restructure the CLI under
  [cobra](https://github.com/spf13/cobra) and parse `fam.toml` with a typed
  TOML unmarshaller. Retire the daemon ccrep verbs.

______________________________________________________________________

## 1. Motivation: three concrete pains from the 2026-06-13 session

1. **Hand-typed SHAs.** `!vote` / `!revision` lines were typed by hand through
   `irc_write`. A 40-char SHA is unergonomic and error-prone.
2. **The mismatch bug.** A proposal announced `sha=7fda6138…` while the pushed
   commit was `7fda6139…` — same 7-char prefix, different (non-existent)
   object. An approval would have bound to a phantom SHA and the merge gate
   would choke. Caught only by manual `git cat-file`.
3. **Multi-round-trip reviews.** A `request_changes` body exceeds the 400-byte
   bang-line cap, so a real review fragments into several IRC lines and several
   ledger entries — painful to write and to read back.

The fix already half-exists: `irc-propose` computes the SHA from
`git rev-parse HEAD` (irc_propose.go:69), and the daemon `approve` binds a vote
to the proposal's `tally.LatestSHA` (voting.go:484) rather than local HEAD. We
generalise those two behaviours to every verb, on both surfaces.

## 2. The abstract ccrep contract (one core, two adapters)

| op        | inputs                                                                       | **SHA source**                         | emits                                                |
| --------- | ---------------------------------------------------------------------------- | -------------------------------------- | ---------------------------------------------------- |
| `propose` | `id`, `summary`, `quorum=majority`, `[executor]`, `[ref=HEAD]`, `[deadline]` | `rev-parse(ref)`, verify pushed        | `!propose id sha quorum executor [deadline] summary` |
| `revise`  | `id`, `[ref=HEAD]`                                                           | `rev-parse(ref)`, verify pushed        | `!revision id sha`                                   |
| `vote`    | `id`, `verdict`, `[body]`, `[expect]`                                        | **`tally(id).LatestSHA`** (never HEAD) | `!vote id sha verdict` + body                        |
| `tally`   | `id`                                                                         | reads ledger                           | `!tally id` (or local-only read)                     |
| `merge`   | `id`                                                                         | `tally(id).LatestSHA`                  | runs the executor merge, then `!executed`            |
| `gate`    | `id`, `commit`                                                               | ledger + commit verify                 | local only — pass/fail (wraps `merge-gate`)          |

`merge` and `gate` are local executor helpers; the rest write one bang line to
the fam channel via the active transport.

## 3. The three rules that close the pains

1. **SHA is computed, never typed.** `propose`/`revise` resolve it from `ref`
   (default `HEAD`) and **verify the commit is reachable on `origin`** before
   announcing — a push-check that would have caught the mismatch bug at source.
   `vote`/`merge`/`executed` resolve the **proposal's current SHA from the
   ledger tally** (`LatestSHA`), so a reviewer binds to what was proposed, not
   to their local tree. The full 40-char SHA is canonical in the ledger; output
   shows the **short** form (`voting approve on 7fda613 (rev 2)`).
2. **TOCTOU guard.** `vote --expect 7fda613` asserts "this is the revision I
   reviewed" and refuses if the proposal has since moved. (Approvals already
   die on new commits, so the worst case without it is a wasted approval — but
   the guard makes intent explicit.)
3. **A review is one call.** `vote` takes the full body as a typed param /
   editor capture (§4). It emits the compact `!vote` bang line, then posts the
   body as ordinary auto-split channel lines immediately after. One agent
   round-trip, no manual chunking; the bang line stays ≤400 B as the scribe
   parser requires. (Folding the body into a single structured ledger event via
   a scribe envelope is a deferred refinement, not v1.)

## 4. `vote` interactivity

- **CLI, no flags:** `botfam ccrep vote --proposal X` prompts
  `approve / request_changes / comment`; for `request_changes` (or `comment`)
  it opens `$VISUAL` (fallback `$EDITOR`) to capture the body, like
  `git commit`.
- **CLI, scripted:** `--verdict approve`, or
  `--verdict request_changes --body-file <path|->` stays non-interactive.
- **MCP:** always non-interactive — `ccrep_vote(verdict, body)`; a bot cannot
  drive an editor. Same core underneath.

Verdicts: `approve | request_changes | comment` (`comment` carries no quorum
weight). The daemon's `reject` is dropped — `request_changes` covers it.

## 5. `merge` reborn (what the daemon verb did, and the fix)

The daemon `botfam merge` (voting.go:502) tallied via the dead daemon path,
then ran `git merge --no-ff <LatestSHA>` in `RepoPath(".")` and emitted
`ccrep:executed`. Two faults: it read the daemon tally, and it merged into the
**current worktree's** branch — it predates the main-checkout model.

`worktree sync` does **not** replace it: sync pulls `main` *into* an agent
branch; `merge` pushes an approved commit *into* `main` — opposite direction.

`ccrep merge` is the rebuild: gate on the **ledger** tally, resolve the **main
checkout** via `git-common-dir` (as `worktree sync` already does),
`git merge --no-ff <LatestSHA>`, emit `!executed`, and **stop before the push**
— `git push origin main` stays a manual Operator step
([post-pivot-cleanup.md](post-pivot-cleanup.md) §9 non-goal). It folds the
manual gate→merge→`!executed` sequence (run by hand three times this session)
into one command; the manual two-step remains valid.

## 6. Transport adapters

Both adapters share the SHA resolver and bang-line builder; only the wire
differs.

- **MCP** (`ccrep_*`): writes via `irc_write` to the live client. Typed params.
- **CLI** (`botfam ccrep <op>`): writes to `scratch/irc/<name>/in` if a client
  is running, else one-shot dials the server (reusing `irc-propose`'s
  connect→send→quit), so the operator gets the same op with no client running.

## 7. CLI structure (cobra)

`main.go`'s hand-rolled `switch` + `consume()` arg parsing is replaced by a
cobra command tree organised by the
[post-pivot-cleanup.md](post-pivot-cleanup.md) §3 domains:

```
botfam
├── ccrep   propose · revise · vote · tally · merge · gate
├── irc     client · write · read · wait
├── task    claim · complete · heartbeat · abandon · sweep
├── session new · list · render · close · append · read
├── worktree init · sync
├── scribe                       # process
├── serve                        # the MCP server
└── (dev)   setup · agent-docs · irclog2sessions
```

`irc-propose` folds into `ccrep propose` (kept as a hidden deprecated alias for
one release). This proposal migrates the **whole** tree to cobra (mechanical
for the unchanged commands) but only reworks the `ccrep` group's behaviour.

## 8. Config

- **`fam.toml` = typed descriptor.** Replace the hand-rolled scanner
  (setup.go:130, which silently drops `[sections]`) with `pelletier/go-toml/v2`
  `Unmarshal` into a typed `Registry`, plus a validation pass. This finally
  supports nested `[roles.*]` tables — the prerequisite for the `human`-guard
  roster ([post-pivot-cleanup.md](post-pivot-cleanup.md) §5).
- **Runtime config = cobra flags + env.** Server host:port, `COLLAB_HISTORY`,
  default channel, and actor override are plain cobra (`pflag`) flags with env
  fallbacks (`flag > env > default`). No viper: ~6 settings don't justify the
  dependency, and `pflag` already covers flag+env binding. Keeping "what this
  fam *is*" (`fam.toml`, typed) separate from "how this invocation is
  *configured*" (flags/env) stays an explicit boundary, not a config-library
  feature.

## 9. MCP surface

**One server** (`botfam serve`), tools namespaced by domain to mirror the CLI
and the existing `botfam://` resources: `ccrep_*`, `task_*`, `session_*`,
`irc_*`, `worktree_*`. We do not split into multiple MCP servers — the topology
is one-botfam-per-harness (subagents pool the parent connection), so per-domain
servers would multiply processes for no gain, and ToolSearch already handles
tool discovery at scale.

New/renamed tools: add `ccrep_propose/revise/vote/tally/merge/gate`; rename
existing `claim → task_claim` etc. and `session_append → session_append`
(group-aligned). The message-bus tools (`send/recv/…`) are **not** renamed —
they are deleted in a later proposal (§10).

## 10. Scope & phasing

**In this proposal:** cobra tree + flag/env runtime config + typed `fam.toml`;
the `ccrep` verb set on CLI + MCP; deletion of the daemon ccrep verbs
(`propose/vote/tally/approve/merge`) and `CollectCcrepEvents(store)` — they are
wholesale-replaced.

**Deferred** (independent subtraction, would bloat this review): deleting the
message bus (`send/…`), `topic`, and the `server` daemon
([post-pivot-cleanup.md](post-pivot-cleanup.md) §3 buckets C, roadmap steps 2
and 6); re-backing `task`/`session` on the ledger (steps 3–4); the structured
review-body envelope (§3).

## 11. Testing

- Unit: SHA resolver (HEAD vs ledger-`LatestSHA`), push-reachability check, the
  `--expect` guard, bang-line builder (≤400 B), `fam.toml` typed unmarshal
  incl. a `[roles.*]` table and a malformed-config failure.
- Integration: `ccrep propose → vote → tally → merge → executed` end to end
  against a scratch ledger; `ccrep merge` resolves and merges in a separate
  main checkout; CLI/MCP adapters produce identical bang lines for identical
  inputs.

## 12. Non-goals

- No change to the consensus *semantics* (quorum rules, approvals-die-on-new-
  commit, presence) — only the surface that drives them.
- `git push origin main` stays manual (Operator).
- No data migration: there is no live daemon ccrep state to preserve.
  </content>
