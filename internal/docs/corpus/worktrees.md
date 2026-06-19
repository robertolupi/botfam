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
  git -c user.name={{.Actor}} -c user.email={{.OperatorEmail}} merge --no-ff <branch>
  ```

## 3. Delegated Subagents Share *Your* Worktree

A working tree is a single-writer resource — and that includes **your own**
worktree, not just the main checkout or a peer's. When you spawn subagents that
*write* (edit files, commit), they default to *your* checkout and will race
each other: lost updates, and one subagent's branch switch clobbering another's
uncommitted work.

- **Read-only delegation parallelizes freely.** A subagent that only reads,
  builds, or tests in an ephemeral worktree (e.g. `botfam verify <sha>`) and
  returns a *report* touches no shared tree — fan these out (e.g. parallel PR
  reviews).
- **Write delegation needs one writer per tree.** Give each writing subagent
  its **own** `git worktree` (off `origin/<base>`), or **serialize** them —
  spawn one, let it finish, then start the next.
- **Partition by tree; parallelize across trees.** Independent worktrees/repos
  (wiki vs code vs unrelated content) have disjoint write-sets and are safe to
  run in parallel. Same-tree work is the case to control. This is the
  Protected-Object / merge-queue rule applied to the working tree: either the
  overseer serializes writers, or the trees are partitioned.
- **Harness caveat**: Some harnesses' built-in worktree isolation can mis-root
  the worktree (binding it to the wrong repo, or sandboxing writes elsewhere),
  silently collapsing "isolated" subagents onto one checkout — see botfam #304.
  Until fixed, have writing subagents create their own `git worktree`
  explicitly, and verify your checkout's branch and tree after they finish.
