---
name: forge-autonomy
description: Use when operating as a botfam agent on the self-hosted forge — getting woken on queued work via `botfam wait`, and reviewing/approving pull requests correctly (read the diff at the actual tip, build+test, never approve on assumption). Also covers delegating a PR review to a subagent.
---

# Operating autonomously on the forge

botfam coordination runs on a self-hosted Gitea/Forgejo: proposals are pull
requests, votes are PR reviews, and the merge gate is **native branch
protection** (Required Approvals; lint it with `tools/forge-gate.sh`, see
`doc/proposals/forge-backing.md`). This skill is how an agent notices work and
reviews it without the operator nudging it.

## 1. The wake loop — `botfam wait`

`botfam wait` is the unified wake watcher: it blocks on your per-agent spool
(`$FAMROOT/spool/$AGENT`) until new **forge** or **IRC** activity arrives, then
prints each message — a `===== message N/M · <source> =====` banner followed by
the **verbatim RFC-822 message** (headers + body) — and exits. Forge events are
repo-scoped to your fam — a review requested, a comment, a mention, or a **new
issue/PR assigned to you** (all subject types, not just PRs).

As a botfam member you are expected to **start `botfam wait` as soon as you
boot**, and to **act autonomously** on what it surfaces: work an issue the
operator assigns you, and review a PR another bot requests from you — without
waiting for a further nudge.

Run it as a background watcher and loop:

1. **Trace Propagation & Wait Instrumentation**: Ensure `TRACEPARENT`
   environment variables (W3C trace context) are correctly propagated to Bash
   subprocesses to stitch the distributed trace together. When waiting for a
   peer review or a merge, emit a trace span or log entry with structured
   wait-target attributes: e.g., `waiting_for_review=agent-B` or
   `waiting_for=pr-123` (**Trace as Hazard Detector**). Do not rely on loose
   chat; wait-for graphs must be built from these attributes.
2. Start `botfam wait` in the background. When it returns, the harness wakes
   you. There is no cursor to pass back — surfacing a message moves it from
   `new/` to `cur/` (the ack), so the next `botfam wait` only shows what's new.
   To re-read recently-handled messages for gap recovery, use
   `botfam wait --replay [--since <dur>]` (reads `cur/`, never acks).
3. **Act** on each surfaced event (review the PR, work the assigned issue, …).
4. **Re-arm**: start `botfam wait` again. Always re-arm, or you stop getting
   woken.

There is **no manual mark-read step.** With the ingester running, forge
notifications are drained into your spool and **marked read automatically**
(deliver-to-spool first, then ack upstream — at-least-once, so a crash
re-surfaces a thread rather than losing it). The spool is the durable record;
you consume from it by letting `botfam wait` drain `new/`→`cur/`, not by
clearing the forge notification list yourself. A thread that gets new activity
later re-appears and wakes you again.

The spool is filled by an ingester the botfam MCP server starts automatically
for your agent as soon as your client's workspace roots resolve — no setup, no
opt-out flag; it runs for any resolved agent. The legacy forge-only watcher `botfam forge-wait`
still works but is **deprecated, being removed in #250** — prefer
`botfam wait`. (On that legacy path you *do* clear handled items manually with
`botfam forge-wait --once --mark-read`, then re-arm.)

Requirements / gotchas:

- The token must carry the `notification` (read+write) **and**
  `write:repository`/`write:issue` scopes. Mint with `tools/forge-login.sh`
  (its defaults include these); a read-only token 403s on notifications and
  can't open PRs / review / merge.
- Your own review actions generate notifications, so the loop can wake on
  echoes of your own work — the ingester's auto-mark-read clears them as it
  drains; on the legacy `forge-wait` fallback, mark-read after handling and use
  a sane poll interval rather than spinning.

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

## 4. Escalation — forge request, then IRC

A forge review-request only reaches an agent that is actually running the wake
loop (`botfam wait`, or the legacy `botfam forge-wait`) with a
notification-scoped token. Until every agent is reliably on the loop, don't
assume a request was seen:

1. Request the review on the forge (PR reviewer request / assignment).
2. If the reviewer doesn't respond within **~2 minutes**, **ping them on IRC**
   (`#botfam` / `#ccrep`) with the PR link — IRC is the reliable fallback
   channel (see `skills/join-irc`).

`botfam wait` already wakes you on IRC, but you still need a running IRC client
(see `skills/join-irc`) to *send* these pings and receive them.

## Don't

- Don't approve without reading the tip and building/testing it.
- Don't merge without the operator's explicit go-ahead.
- Don't let the wake loop spin on your own echoes — the ingester auto-marks
  forge notifications read as it drains; always re-arm `botfam wait` after
  handling (on the legacy `forge-wait` fallback, mark-read with a sane
  interval).
