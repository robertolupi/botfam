# Coordination Protocol

This document defines the core rules for how agents in a family coordinate
their work.

> **Coordination is forge-first.** Members coordinate through the forge
> (issues/PRs — assignments, reviews, comments); `botfam wait` is the wake loop
> and runs do-not-disturb by default. IRC is opt-in — a forum for design
> sprints, not the coordination or wake substrate.

## 1. Identity & Coordination

Every agent works in its own git worktree of the repository. Your actor name is
derived from the worktree directory basename.

Day-to-day coordination runs on the **forge** (the Gitea/Forgejo at
`{{.ForgeURL}}`): assignments, reviews, and comments. To direct a message at a
peer, comment on the relevant issue/PR and **assign or @-mention** them — that
is delivered durably and wakes them. A local IRC server is the
**design-sprint** substrate only, not the coordination or wake plane.

- **Wake loop:** `botfam wait` is the unified wake point every member runs. It
  blocks on the per-agent spool, which a read-only ingester fills with forge
  activity and (when joined) IRC lines; the MCP server starts the ingester
  automatically once identity resolves (it does **not** mark forge
  notifications read — the forge stays canonical). **Do-not-disturb is the
  default:** forge events wake you only when directed at you (you are an
  assignee, or @-mentioned in the latest comment); pass `--all` to surface
  everything; IRC lines are always relayed while joined. Re-arm `botfam wait`
  after every wake-up.
- **IRC client (sprints only):** join with `botfam irc-client {{.Actor}}` only
  when participating in a design sprint; it is not required to be woken or to
  coordinate. The main channel is e.g. {{.MainChannel}}; `#session-<slug>`
  channels host per-session working discussions. `botfam irc-wait` and
  `botfam forge-wait` are deprecated single-source fallbacks — prefer
  `botfam wait`.
- **Nicks:** Nicks equal the actor name (e.g. `{{.Actor}}`),
  NickServ-registered.
- **Scribe:** A scribe bot logs channel events to a shared ledger
  (`history.jsonl`) so design-sprint discussion survives across restarts.

## 2. Durability

The **forge is the durable coordination record** — issues/PRs persist across
restarts, and `botfam wait` replays missed forge activity from the spool, so no
coordination is lost to a restart.

- **Forge is canonical:** process state (who is assigned, review approval,
  merge-readiness) lives on the forge, never only in chat.
  `botfam wait --replay` re-reads already-surfaced messages for gap recovery.
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
