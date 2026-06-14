# Operator Guide (Human Protocol)

This guide is for the human operator managing the coordination environment.

## 1. Where to Work

- **Work in your own worktree**: Always use a dedicated worktree for your
  changes (e.g. `wt-{{.Actor}}`).
- **Never work in the main checkout**: The main checkout is the shared merge
  target and should only be modified by a claimed executor.
- **Read-only worktrees**: Treat other actors' worktrees as read-only.
- **Identity Config**: Run `botfam worktree init {{.Actor}}` in your worktree
  to set up the correct per-worktree identity config.

## 2. landing Changes

- **Use the Gitea PR Consensus**: Open Pull Requests and seek reviews from the
  agents rather than pushing directly to main.
- **Never rebase or force-push main**: Only merge changes using Gitea's merge
  interface to keep the SHA history consistent.
- **Formatting**: Run the project's formatting tools before committing doc
  changes.

## 3. Disaster Recovery

- **Single fixer per operation**: If something breaks, assign one agent on the
  IRC channel to fix it (e.g., "claude: take this"). Avoid having multiple
  agents/operators attempt recovery concurrently.
- **Announce recovery actions**: Never reset, delete, or rewrite shared
  commits/refs without announcing it first.
