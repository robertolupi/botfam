---
name: forge-autonomy
description: Use when operating as a botfam agent on the self-hosted forge — noticing queued work by querying the forge (the supervised wake path is landing; `botfam wait` is legacy), and reviewing/approving pull requests correctly (read the diff at the actual tip, build+test, never approve on assumption). Also covers delegating a PR review to a subagent.
---

# Operating autonomously on the forge

botfam coordination runs on a self-hosted Gitea/Forgejo: proposals are pull
requests, votes are PR reviews, and the merge gate is **native branch
protection** (Required Approvals; lint it with `tools/forge-gate.sh`, see the
wiki's `archived-forge-backing` page). This skill is how an agent notices work
and reviews it without the operator nudging it.

## 1. Noticing work — query the forge (wake is in transition)

Wake is migrating from the legacy `botfam wait` loop to a **supervisor**
(`botfam sprint run`, EventDeliveryV2). The supervisor will own session
lifetime and re-run agents on queued work. **It has not landed yet, so a human
operator is the manual supervisor** — there is no always-on wake loop to start.
(The legacy `botfam wait` spool ingester is **disabled by default**, so on a
current binary it has nothing to surface; don't build on it.)

So the forge itself is your work queue. **At boot, query it directly** with the
forge MCP tools rather than blocking on a spool:

- **Notifications:** `forge_notification_read` (`method=list`, `status=unread`,
  repo-scoped) — reviews requested, comments, mentions, and **new issues/PRs
  assigned to you** (all subject types).
- **Assigned issues / review requests:** `forge_search_issues` /
  `forge_list_issues` filtered to your assignee, and the PRs awaiting your
  review.

Act **autonomously** on what you find: work an issue the operator assigns you,
review a PR another bot requests from you. Then follow the manual-supervisor
operating pattern:

1. **Trace Propagation & Wait Instrumentation**: Ensure `TRACEPARENT`
   environment variables (W3C trace context) are correctly propagated to Bash
   subprocesses to stitch the distributed trace together. When you depend on a
   peer review or a merge, emit a trace span or log entry with structured
   wait-target attributes: e.g., `waiting_for_review=agent-B` or
   `waiting_for=pr-123` (**Trace as Hazard Detector**). Do not rely on loose
   chat; wait-for graphs must be built from these attributes.
2. **Finish the task** in front of you and write a handover snapshot to the
   forge (the durable record).
3. **Yield** — do not spin on a wake loop.
4. **Notify the operator in plain text** that you are done and what is next,
   then **wait to be run again by hand.**

The forge is canonical and durable — **mark a notification read only when you
have actually handled it** (`forge_notification_write`), so a crash mid-task
leaves the thread unread and the next boot re-discovers it. Your token must
carry the `notification` (read+write) **and** `write:repository`/`write:issue`
scopes (mint with `tools/forge-login.sh`); a read-only token 403s on
notifications and can't open PRs / review / merge.

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
   Reviews are for critique and judgment, not co-authoring or taking over
   execution (**Diversity for Critique**). Let the PR owner integrate feedback
   and make decisions. Keep the data plane (git commits) separate from the
   control plane (reviews, approvals, claims) (**Plane Separation**). Quorum is
   the branch rule's Required Approvals (independent; the author can't
   self-approve). The **merge itself is the operator's** — don't merge without
   an explicit go-ahead (and the gate enforces the approval count anyway). On
   completion, **spawn the `meta-review` subagent** (STEP 2,
   `skills/meta-review`) with `{PR index + repo, corpus index}` but **not**
   your verdict — it emits advisory `risk/*` suggestions in an isolated
   context.
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

## 4. Escalation — forge request, then operator

A forge review-request only reaches a peer the next time that agent is run and
queries the forge. In the manual-supervisor gap there is no automatic wake, so
**don't assume a request was seen**:

1. Request the review on the forge (PR reviewer request / assignment).
2. If the reviewer doesn't respond, **tell the operator in plain text** that
   the PR is blocked on a peer review and name the PR — the operator runs the
   reviewer.

## Don't

- Don't approve without reading the tip and building/testing it.
- Don't merge without the operator's explicit go-ahead.
- Don't treat `botfam wait` as a wake loop — its spool ingester is disabled by
  default; query the forge at boot and yield when done.
