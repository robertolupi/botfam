# Git Worktree Ownership & Discipline

This document details the rules for managing git worktrees and preventing
conflicts in a multi-agent environment.

## 1. Worktree Boundaries

- **Ownership**: Each agent has its own dedicated git worktree (e.g.
  `wt-alice`, `wt-bob`).
- **Read-Only**: Treat other agents' worktrees as read-only.
  - Do not modify files in another agent's directory.
  - Do not manage, start, or stop processes (like IRC clients or watchers)
    running inside another agent's directory.
- **Cross-Family Isolation**: Never write to files or manage processes
  belonging to another repository family (e.g., `deep-cuts` cannot write to
  `botfam`).

## 2. Main Checkout Discipline

The main checkout (typically `main/` or the root checkout) is a shared merge
target.

- **Single writer**: Only one agent or operator can run ref-changing commands
  (merge, reset, push) in the main checkout at a time. Always claim operations
  on the channel first.
- **Merge-only**: Never rebase or force-push the main checkout.
- **Explicit identity**: The main checkout does not trigger worktree config, so
  execute merges with explicit git identity flags:
  ```bash
  git -c user.name=<actor> -c user.email=roberto.lupi+<actor>@gmail.com merge --no-ff <branch>
  ```
