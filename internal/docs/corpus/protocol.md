# Coordination Protocol

This document defines the core rules for how agents in a family coordinate
their work.

> **Coordination is forge-first.** Members coordinate through the forge
> (issues/PRs — assignments, reviews, comments). Wake is moving to a
> **supervisor** (`botfam sprint run`, EventDeliveryV2); until it lands, a
> **human operator manually supervises** — see
> [§1a](#1a-wake--the-manual-supervisor-gap). IRC is opt-in — a forum for
> design sprints, not the coordination or wake substrate.

## 1. Identity & Coordination

Every agent works in its own git worktree of the repository. Your actor name is
derived from the worktree directory basename.

Day-to-day coordination runs on the **forge** (the Gitea/Forgejo at
`{{.ForgeURL}}`): assignments, reviews, and comments. To direct a message at a
peer, comment on the relevant issue/PR and **assign or @-mention** them — that
is delivered durably and wakes them. A local IRC server is the
**design-sprint** substrate only, not the coordination or wake plane.

- **Wake (in transition):** the wake substrate is being replaced by a
  **supervisor** (`botfam sprint run`); see
  [§1a](#1a-wake--the-manual-supervisor-gap). Until it lands, a human operator
  manually re-runs agents — there is no always-on automatic wake loop to rely
  on.
- **`botfam wait` is legacy:** it still blocks on the per-agent spool and
  prints what arrives, but it is **no longer the unified wake loop** and must
  not be treated as one. The spool ingester that fed it is **disabled by
  default** in the current binary (EventDeliveryV2 M0c), so on a fresh binary
  `botfam wait` has nothing to drain. It survives only for fams still running
  the old binary with the `legacy_ingest` opt-in. Do not build new flows on it.
- **IRC client (sprints only):** join with `botfam irc-client {{.Actor}}` only
  when participating in a design sprint; it is not required to be woken or to
  coordinate. The main channel is e.g. {{.MainChannel}}; `#session-<slug>`
  channels host per-session working discussions. `botfam irc-wait`,
  `botfam forge-wait`, and `botfam wait` are all legacy single-binary wake
  fallbacks, not the supervised path.
- **Nicks:** Nicks equal the actor name (e.g. `{{.Actor}}`),
  NickServ-registered.
- **Scribe:** A scribe bot logs channel events to a shared ledger
  (`history.jsonl`) so design-sprint discussion survives across restarts.

## 1a. Wake & the manual-supervisor gap

Wake is migrating from the legacy single-binary `botfam wait` loop to a
**supervisor** process (`botfam sprint run`, EventDeliveryV2). The supervisor
will own session lifetime: it decides when a sprint is done, sends an explicit
end-of-session message, and reaps hung agents on a TTL.

That supervisor has not landed yet. **In the interim the human operator is the
manual supervisor** — there is no automatic wake loop. The expected operating
pattern is:

1. **Finish the task** in front of you (land the PR, post the review, write the
   handover snapshot to the forge).
2. **Yield** — do not spin on a wake loop.
3. **Notify the operator in plain text** that you are done and what is next.
4. **Wait to be run again by hand.** The operator re-runs agents as work
   accrues.

Do not depend on `botfam wait` to keep you alive between tasks: its spool
ingester is disabled by default (EventDeliveryV2 M0c), so on a current binary
it has nothing to surface. Agents still on the old binary keep today's `wait`
semantics until the new supervised path replaces them — that path ships as a
unit, never pushed onto a running agent mid-gap.

## 2. Durability

The **forge is the durable coordination record** — issues/PRs persist across
restarts, so no coordination is lost to a restart: standing work is recovered
by querying the forge worklist, not by replaying an ephemeral wake stream.

- **Forge is canonical:** process state (who is assigned, review approval,
  merge-readiness) lives on the forge, never only in chat. Re-derive your
  worklist from the forge on every boot rather than trusting a wake spool.
- **IRC replay (sprints):** the IRC substrate is ephemeral; when you join or
  reconnect for a sprint, read the durable scribe ledger (via the `irc_replay`
  MCP tool or by tailing `history.jsonl`) before acting — never assume you saw
  all traffic live.
- **Formatting:** Format all documents using the project's formatting tools
  before committing to keep diffs clean.

## 3. Gitea Pull Request Consensus Layer

All changes to the integration branch (`{{.IntegrationBranch}}`) are governed
by Gitea's branch protection rules.

- **Pull Request Workflow**:
  - Create a branch off `{{.IntegrationBranch}}`.
  - Open a PR targeting `{{.IntegrationBranch}}`.
  - Obtain reviews and approvals from peer agents. A correct consensus requires
    meeting the branch protection's approval counts (typically 2 approvals).
  - The repository owner merges the PR once Gitea's requirements are satisfied.
- **Consensus Rules**:
  - Approvals are dismissed automatically on new commits.
  - A request for changes (`REQUEST_CHANGES` review) blocks the merge.

## 3.1 Plane Separation & Ownership Rules

We enforce strict separation of roles and planes to optimize reasoning and
avoid deadlocks:

- **Plane Separation (Control vs. Data)**: Keep the control plane (the Gitea
  forge: issues, PRs, reviews, assignments) distinct from the data plane (the
  git repo: committed code). Process state (such as who is assigned, review
  approval, or merge-readiness) lives exclusively on the forge. The repository
  tip is mutated only by merges on the control plane.
- **Decompose by Coupling & Single Owner**: Group issues by coupling (shared
  design/contracts/data models). A coupled cluster must be assigned to a
  **single owner** who claims all related issues end-to-end to avoid Concept
  Fragmentation. The owner agent can fan out to subagents (hands) under its own
  context for execution. Using subagents where appropriate and efficient is
  actively permitted and encouraged. Use architectural paradigms like
  divide-and-conquer or map-reduce when fanning out tasks to subagents. Peer
  agents must not work concurrently on different parts of a coupled cluster.
- **Bounded WIP**: Default to **WIP=1** for coupled clusters. Juggling multiple
  coupled tasks in one overfull context degrades reasoning and increases cost.

## 4. Worktree Ownership

Other actors' worktrees are **read-only**.

- Do not write to files or manage processes in another agent's worktree.
- If you need to make changes, ask the owner on the relevant forge issue/PR
  (comment and @-mention or assign them).
- If the owner is offline, you may fast-forward their clean worktree if you
  note it on the forge immediately.

## 4.1 Let It Crash & Warm Handover Protocol

Because agents are transient and fragile actors that degrade as their context
windows fill, we design for failure recovery rather than trying to recover
in-place:

- **Let It Crash**: Do not write complex, defensive error-recovery code inside
  an agent. If context-fullness (computed out-of-band by the harness)
  approaches the crash threshold, or if the agent stalls/loops, let it
  crash/exit immediately.
- **Handover Snapshot**: Before crashing (or at regular progress milestones),
  the agent must write a compact **Handover Snapshot** to the control plane
  (the Gitea forge issue or PR comment). The snapshot's distilled reasoning
  state must contain:
  1. The task **goal**.
  2. The **decisions taken so far** and why.
  3. A pointer to the **git branch/PR** (so the product state is referenced,
     not copied).
  4. The **current blocker**, if any.
  5. The **next step** to be taken.
- **Supervision & Warm Restart**: The harness acts as a stateless one-for-one
  supervisor that detects the exit, spins up a fresh agent, and injects the
  Handover Snapshot. The new agent resumes warm from the snapshot and branch,
  avoiding the high onboarding tax of replaying history from genesis.

## 5. Main Checkout Discipline

- **Single writer per operation**: Claim ref-changing operations (merge, reset,
  cherry-pick) on the forge first (an issue/PR comment), not in chat.
- **main is merge-only**: Never rebase it, never force-push it.
- **Worktree identity**: Each actor sets
  `git config --worktree user.name {{.Actor}}` and `user.email` in their own
  tree.
