---
name: botfam-sprint
description: Use when a botfam agent should autonomously work a backlog — looping over the forge's open issues and pull requests to claim an issue, resolve it, open a PR, review a peer's PR, and address comments on its own PRs — repeating until no unassigned issues and no reviewable PRs remain. Trigger on "work the backlog", "run a sprint", "grind through the issues", "loop over issues and PRs", "keep taking issues and reviewing", or any standing instruction to keep resolving issues and reviewing PRs on the forge.
---

# Running a botfam sprint

This is the orchestration loop an agent runs to clear a backlog on the
self-hosted forge without the operator hand-feeding it tasks. It composes two
narrower skills — lean on them rather than duplicating their detail:

- **`forge-autonomy`** — how to review a PR correctly (read the diff at the
  *actual tip*, build + test, never approve on assumption) and how
  `botfam forge-wait` wakes you on assigned work.
- **`join-irc`** — how to connect to the fam channel and send/read messages.

Coordination consensus is Gitea PR reviews + native branch protection (PROTOCOL
§3), not the deleted ccrep bang-verbs. The integration branch is
**`botfam-next`**; `main` is merge-only and you cannot push it.

## The loop at a glance

Each iteration, in order:

1. **Triage & Claim**: Inspect the forge for issue coupling.
   - **Coupled Cluster** (labeled `coupled` or sharing a design/contract/data
     model) → One agent claims the **entire cluster** (**Single Owner**). Use
     subagents (hands) for parallel execution under one coherent design. Never
     split coupled labor across peer minds.
   - **Decoupled Backlog** (labeled `decoupled` or sharing no contracts) →
     Distribute to peer agents via atomic issue-claims.
   - **Bounded WIP**: Default to **WIP=1** for coupled work. Decoupled WIP may
     be raised only when total work exceeds one owner's context.
2. **Resolve**: Work the issue on a branch off `botfam-next` under the change
   discipline.
   - **Let It Crash & Supervise**: Monitor your context-fullness. If you
     approach context-window fullness or get stuck, compile a **Handover
     Snapshot** (distilled goals, decisions, branch, blocker, next step) to the
     forge issue/PR, and crash/exit. The harness supervisor will perform a warm
     restart.
3. **Open a PR**: Target `botfam-next` and announce it.
4. **Review**: Review one peer PR, if any is open (full `forge-autonomy`
   discipline). Use peer reviews for critique/judgment, never
   co-authoring/co-execution (**Diversity for Critique**).
5. **Address comments** on your own PRs.

**Exit** when *both* hold: no unassigned issue you can take without colliding
with your own in-flight work, **and** no peer PR left to review. When the only
remaining issues are blocked on your own PRs merging, don't spin — watch
`botfam-next` and resume when it advances.

Announce each transition (claim / PR opened / review posted) on the fam channel
so peers don't double-work. The fam moves fast; a stale plan collides.

## 0. Set up once

Resolve your own identity and the forge's before touching anything:

- **Actor name**: `botfam whoami` (falls back to the worktree basename per
  PROTOCOL §1). Everything keys off this.
- **Forge identity**: the forge MCP `get_me` returns your bot login (e.g.
  `claude` → `claude-bot`). You assign issues and author reviews as that login.
- **Repo**: `botfam/botfam` on the `gitea` remote (`git remote -v`).
- **Token** (for raw API calls the MCP doesn't cover):
  `~/.botfam/token-botfam-<actor>`.
- **Connect to IRC** (`join-irc`) and replay history before acting — this is
  your coordination channel for claims, hand-offs, and merge nudges.

Then survey the board: forge MCP `list_issues {state: open}` and
`list_pull_requests {state: open}`.

## 1. Pick an issue (Coupling Triage)

Survey the board (`list_issues {state: open}`). Before picking, determine the
coupling of the backlog:

- **Check Labels**: Inspect the labels on the forge issue (e.g. `coupled` or
  `decoupled`). If the triage has not occurred yet, run **Coupling Triage**
  yourself: group issues by shared design, contracts, file paths, or data
  models.
- **Coupled Cluster → Single Owner**: If an issue belongs to a coupled cluster,
  it must have **exactly one owner** for all issues in that cluster end-to-end
  to prevent **Concept Fragmentation** and concurrency deadlock. If a peer has
  already claimed any issue in the cluster, do not claim the other parts of it.
  If you claim the cluster, assign **all** related issues to yourself.
- **Decoupled Backlog**: If issues are genuinely decoupled, distribute them
  among peers atomicly.
- **Bounded WIP**: Enforce **WIP=1** on coupled work (only claim one coupled
  cluster/PR at a time). For decoupled work, you may carry WIP > 1 if total
  work exceeds one owner's context, but default to WIP=1 to prevent
  context-window degradation.

Pick an issue that:

1. Is unassigned and has not been claimed by a peer (assignee check required).
2. For coupled work, belongs to a cluster you own (or are claiming the whole
   of).
3. Doesn't touch code your own open PRs are changing (see
   [Avoiding self-collision](#avoiding-self-collision)).

> ⚠️ `list_issues` does **not** return assignees. Check them explicitly before
> claiming, or you'll grab something already in flight:
>
> ```bash
> TOK=$(cat ~/.botfam/token-botfam-<actor>)
> curl -s "http://gitea:3000/api/v1/repos/botfam/botfam/issues/<n>" \
>   -H "Authorization: token $TOK" \
>   | python3 -c 'import sys,json;d=json.load(sys.stdin);print([a["login"] for a in (d.get("assignees") or [])])'
> ```

## 2. Claim it

Two steps, both required, and check for a race first (scan the channel + the
assignee API — a peer may have claimed it seconds ago):

1. Assign on the forge (the control plane): MCP
   `issue_write {method: "update", issue_number: N, assignees: ["<actor>-bot"]}`.
   (For a coupled cluster, claim **all** issues in that cluster).
2. Announce on IRC:
   `Claimed #N (<title>) [coupled cluster: #A, #B, ...]. Fixing + will open a PR.`
   (or note if decoupled).

## 3. Resolve it

Branch off a **fresh** `botfam-next`, then work under the change discipline:

```bash
git fetch gitea botfam-next
git checkout -b <actor>/issue-<n>-<slug> FETCH_HEAD
```

The discipline (this is what makes a review trivial to approve):

- **Build**: `go build ./...`
- **Test**: `go test ./...`; add `-race` for any concurrency change.
- **Vet / format**: `go vet ./...`, `gofmt -l <pkg>` (must print nothing).
- **Docs**: format with `tools/mdformat.sh` and verify with
  `tools/mdformat.sh --check <files>` — never another formatter (see
  `writing-markdown`).
- **Write a regression test that has teeth.** A test that passes both before
  and after your fix proves nothing. Confirm it *fails on the unfixed code*:
  temporarily revert the fix (or the lock, or the guard), watch the test go
  red, then restore. For data races, assert under `-race` against an
  *unsynchronized* sink so the detector actually fires without the fix.

Commit with **per-worktree identity** (the main/worktree config won't match you
otherwise) and a trailer that closes the issue:

```bash
git -c user.name=<actor> -c user.email=roberto.lupi+<actor>@gmail.com \
  commit -m "fix(scope): one-line what+why (#<n>)

Body: what was wrong, why it mattered, what changed. Note anything a
reviewer should know (e.g. why you didn't take the issue's suggested
approach).

Closes #<n>

Co-Authored-By: <your model> <noreply@anthropic.com>"
git push gitea HEAD
```

Write the body to explain the *why*, not just the *what* — the reviewer reads
it before the diff.

### Context-Window Fullness, Handover Snapshots, and Let It Crash

An agent's context window is finite, and its reasoning degrades as the context
fills. Rather than trying to nurse a degraded agent, follow the **Let It Crash
and Supervise** pattern:

- **Monitor Context**: Do not guess your token consumption or context size; use
  computed metrics from the harness (`input_tokens` / `context_window` on the
  latest `claude_code.llm_request` span).
- **Prepare Handover Snapshot**: If context-fullness crosses 80% (or the crash
  threshold), or if you loop/stall, write a compact **Handover Snapshot** to
  the control plane (the forge, e.g., as a comment on the issue or PR). The
  snapshot must capture the distilled reasoning state:
  ```yaml
  goal: <what you are trying to achieve>
  decisions_so_far:
    - <what was chosen and why>
  branch: <pointer to the branch/PR on the repo>
  current_blocker: <what is blocking you, if any>
  next_step: <the next immediate action the replacement should take>
  ```
- **Crash Gracefully**: Exit or crash the process. The harness/CI supervisor
  will detect the exit and spawn a fresh agent.
- **Warm Restart**: The replacement agent will read the Handover Snapshot from
  the forge and checkout the branch (already on the repo), avoiding the
  onboarding tax of replaying the entire task history from genesis.

## 4. Open the PR

MCP
`pull_request_write {method: "create", base: "botfam-next", head: "<branch>", title, body}`.
Mirror the commit's reasoning in the body and state the verification you ran
(build/test/-race/vet/gofmt/mdformat all clean). Then announce on IRC with the
PR number.

## 5. Review a peer PR

If any peer PR is open, review one per iteration using the full
**`forge-autonomy`** discipline — the short version:

1. Read the diff **at the head SHA** (MCP
   `pull_request_read {method: "get_diff"}`); note the exact `head.sha`.
2. Check out that tip and actually **build + test + vet + fmt** (or
   `mdformat --check` for docs). Verify claims; don't trust the description.
3. Submit a review
   (`pull_request_review_write {method: "create", commit_id: <head-sha>, state: "APPROVED" | "REQUEST_CHANGES"}`)
   with **evidence** — what you ran, what you confirmed, and any non-blocking
   notes. Approving on assumption is the one unforgivable move.

Before deleting/removing code a PR claims is dead, grep for callers yourself.
Before approving a doc change, confirm any new links/paths resolve.

## 6. Address comments on your own PRs

Each iteration, check your open PRs
(`pull_request_read {method: "get_reviews"}` and the issue comments endpoint).
On a `REQUEST_CHANGES` or a substantive comment: make the fix, push, and
re-announce. Approvals **dismiss on a new commit** (branch protection), so
expect to re-request review after any push. Empty or approving reviews need no
action.

## Avoiding self-collision

This is the lesson that's easy to miss and expensive to ignore. You may have
several PRs open at once, all unmerged. **Don't claim an issue whose fix edits
the same file/function as one of your own open PRs** — you'll fight yourself in
the merge queue and create rework for whoever merges.

Two clean ways out:

- **Defer.** Note the dependency, leave the issue for later, and pick something
  independent now. When your blocking PR merges (watch `botfam-next` advance),
  take it on the clean base.
- **Stack.** Branch the new work off your *existing* branch rather than
  `botfam-next`, and open its PR against that branch. Say so in the body
  ("stacked on #X — merge that first") and retarget to `botfam-next` once the
  parent merges. Stacking keeps the diff reviewable and conflict-free; use it
  when the follow-up genuinely builds on the parent.

Watch the integration branch to know when you're unblocked:

```bash
git ls-remote gitea refs/heads/botfam-next | cut -c1-7   # poll for change
```

## Coordinate, don't collide

IRC is the steering wheel. Use it to:

- announce claims/PRs/reviews so peers don't double-work;
- **nudge merges** — your approved PRs unblock both your follow-ups and other
  agents' issues; ask the operator (who merges) to prioritize them;
- flag merge-order or conflict risks you notice between open PRs;
- hand off when you're blocked or out of non-conflicting work.

When you think a PR conflicts, **verify before crying wolf** — a Gitea
`mergeable: false` can be a transient recompute after the base moved. Confirm
with a real local test-merge:

```bash
git checkout -B tmp gitea/<their-branch>
git merge --no-commit --no-ff gitea/botfam-next   # exit 0 + no CONFLICT == clean
git merge --abort
```

## Gotchas (quick reference)

- **`list_issues` omits assignees** — always check via the API before claiming.
- **A branch checkout reverts your working-tree edits to that branch's
  content.** If you `Read` a file, then switch branches, re-`Read` before
  editing — the file on disk changed under you.
- **Commit identity must be set per-commit**
  (`git -c user.name=… -c user.email=roberto.lupi+<actor>@gmail.com`); the
  shared config matches no one in the main checkout and can override
  `includeIf` in worktrees.
- **`main` is merge-only and unpushable** by agents; always target
  `botfam-next`.
- **Rebuilt `botfam` binaries need codesigning** on macOS:
  `codesign --force --sign - ~/bin/botfam`.
- **`scratch/` is gitignored / local-only** — never report breakage there as a
  repo finding.
