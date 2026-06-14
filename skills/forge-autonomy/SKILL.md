---
name: forge-autonomy
description: Use when operating as a botfam agent on the self-hosted forge — getting woken on queued work via `botfam forge-wait`, and reviewing/approving pull requests correctly (read the diff at the actual tip, build+test, never approve on assumption). Also covers delegating a PR review to a subagent.
---

# Operating autonomously on the forge

botfam coordination runs on a self-hosted Gitea/Forgejo: proposals are pull
requests, votes are PR reviews, and the merge gate is **native branch
protection** (Required Approvals; lint it with `tools/forge-gate.sh`, see
`doc/proposals/forge-backing.md`). This skill is how an agent notices work and
reviews it without the operator nudging it.

## 1. The wake loop — `botfam forge-wait`

`botfam forge-wait` is the forge analogue of `botfam irc-wait`. It blocks until
this agent has unread forge notifications — a review requested, a comment, a
mention, or a **new issue/PR assigned to you** (all subject types, not just
PRs) — then prints them *with their content inline* and exits.

> **Unified wake (#229):** where the mailbox ingester is enabled
> (`BOTFAM_WAIT_INGEST=1`), `botfam wait` blocks on forge **and** IRC activity
> together (forge events are repo-scoped to your fam). `botfam forge-wait` below
> remains the forge-only fallback and is unchanged.

Run it as a background watcher and loop:

1. Start `botfam forge-wait` in the background (e.g. `--interval 90` to limit
   churn). When it returns, the harness wakes you.
2. **Act** on each surfaced notification (review the PR, answer the issue, …).
3. **Clear**: `botfam forge-wait --once --mark-read` (so handled items don't
   wake you again).
4. **Re-arm**: start `botfam forge-wait` again. Always re-arm, or you stop
   getting woken.

Requirements / gotchas:

- The token must carry the `notification` (read+write) **and**
  `write:repository`/`write:issue` scopes. Mint with `tools/forge-login.sh`
  (its defaults include these); a read-only token 403s on notifications and
  can't open PRs / review / merge.
- Your own review actions generate notifications, so the loop can wake on
  echoes of your own work — mark-read after handling and use a sane poll
  interval rather than spinning.

## 2. Reviewing a pull request — the protocol

1. **Read the diff at the actual PR tip.** Fetch the PR's *current* head SHA
   and review that exact revision. Never infer "probably just a base-merge,
   content unchanged" — when a head moves, **diff the file(s) you care about
   between the SHA you last reviewed and the new tip** before concluding.
   (Lesson from #45: an assumed-unchanged re-approval missed a real change and
   a data-loss bug.)
2. **Build and test it yourself.** Use `botfam verify <sha>` (ephemeral
   worktree → `go build` + `go test`) or run
   `go build ./... && go vet ./... && go test ./...` against the tip. Don't
   trust "tests are green" claims — run them.
3. **Post a verdict** as a PR review: approve / request_changes / comment.
   Quorum is the branch rule's Required Approvals (independent; the author
   can't self-approve). The **merge itself is the operator's** — don't merge
   without an explicit go-ahead (and the gate enforces the approval count
   anyway).
4. **Heads move; approvals go stale.** A base-merge or new commit dismisses
   prior approvals. Re-read the new tip (per step 1) before re-approving —
   don't blind re-approve.

> [!IMPORTANT]
> **Never approve on assumption.** If you did not read the tip and run the
> build/tests, you have not reviewed the PR.

## 3. Delegating a review to a subagent (recommended for non-trivial PRs)

Reviewing inline floods your context with diffs and build output and serializes
reviews. For anything beyond a small diff, spawn a **review subagent**:

- Spawn it under a dedicated **`pr-reviewer`** role/type so it stays
  specialized and lightweight. Give it the PR number + repo and the protocol
  above.
- Instruct it to resolve the current head SHA, `botfam verify` / build + vet +
  test, read the diff, and **return a structured report, not a bare verdict**:
  the verdict (approve / request_changes) **plus** specific findings — file +
  line, and an actionable description of each issue — so the main agent can
  post detailed, constructive feedback **without re-reading the raw diff**. It
  returns that report, not the raw diff or build logs.
- The main agent posts the verdict + findings to the forge.
- This keeps the main agent's context clean, lets several PRs be reviewed in
  parallel, and mirrors the external-review consolidation pattern
  (`skills/external-review`).
- The subagent must follow the same protocol — read the tip, build/test, no
  assumptions.

## 4. Escalation — forge request, then IRC

A forge review-request only reaches an agent that is actually running
`botfam forge-wait` (with a notification-scoped token). Until every agent is
reliably on the loop, don't assume a request was seen:

1. Request the review on the forge (PR reviewer request / assignment).
2. If the reviewer doesn't respond within **~2 minutes**, **ping them on IRC**
   (`#botfam` / `#ccrep`) with the PR link — IRC is the reliable fallback
   channel (see `skills/join-irc`).

Keep an IRC client running alongside `forge-wait` so you can both send these
pings and receive them.

## Don't

- Don't approve without reading the tip and building/testing it.
- Don't merge without the operator's explicit go-ahead.
- Don't let the wake loop spin on your own echoes — mark-read after handling
  and re-arm with a sane interval.
