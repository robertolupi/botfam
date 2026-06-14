# Coordination Protocol

This document defines the core rules for how agents in a family coordinate
their work.

## 1. Identity & IRC Layout

Every agent works in its own git worktree of the repository. Your actor name is
derived from the worktree directory basename. Coordination runs over a local
IRC server.

- **Nicks**: Nicks are equal to the actor name (e.g. `claude`, `agy`),
  NickServ-registered.
- **Channels**:
  - The family's main channel is used for coordination (e.g., #main or #dc).
  - `#session-<slug>` channels are used for per-session working discussions.
- **IRC Client**: Run the Go client background task:
  ```bash
  botfam irc-client <actor>
  ```
- **Scribe**: A scribe bot logs channel events to a shared ledger file
  (`history.jsonl`) to ensure durability across agent restarts.

## 2. Replay-on-Join & Durability

Because offline agents miss live IRC traffic, you must read the durability
ledger:

- **Replay**: When joining or reconnecting, you MUST read the log file and
  parse the missed traffic before taking any action.
- **Formatting**: Format all documents using the project's formatting tools
  (such as `tools/mdformat.sh`) before committing to keep diffs clean.

## 3. Gitea Pull Request Consensus Layer

All changes to the integration branch (`botfam-next`) are governed by Gitea's
branch protection rules.

- **Pull Request Workflow**:
  - Create a branch off `botfam-next`.
  - Open a PR targeting `botfam-next`.
  - Obtain reviews and approvals from peer agents. A correct consensus requires
    meeting the branch protection's approval counts (typically 2 approvals).
  - The repository owner merges the PR once Gitea's requirements are satisfied.
- **Consensus Rules**:
  - Approvals are dismissed automatically on new commits.
  - A request for changes (`REQUEST_CHANGES` review) blocks the merge.

## 4. Worktree Ownership

Other actors' worktrees are **read-only**.

- Do not write to files or manage processes in another agent's worktree.
- If you need to make changes, communicate with the owner on the IRC channel.
- If the owner is offline, you may fast-forward their clean worktree if you
  announce it on the channel immediately.

## 5. Main Checkout Discipline

- **Single writer per operation**: Claim ref-changing operations (merge, reset,
  cherry-pick) on the channel first.
- **main is merge-only**: Never rebase it, never force-push it.
- **Worktree identity**: Each actor sets
  `git config --worktree user.name <actor>` and `user.email` in their own tree.
