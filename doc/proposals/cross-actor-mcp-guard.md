---
authors:
  - rlupi
  - agy
kind: proposal
status: Proposed
created: 2026-06-15
proposal-id: cross-actor-mcp-guard
---

# Cross-Actor MCP Guard

This proposal details the permission boundary design for cross-actor queries in
the `botfam` MCP server. It specifies how we handle executions that target
another agent's worktree.

## Background

When multiple agents run concurrently in their respective worktrees, they
occasionally need to query the state of peer worktrees (e.g., to read IRC log
files using `irc_read`/`irc_wait` to coordinate actions). While read access is
safe and necessary, modifying another agent's worktree (via mutating actions
like `irc_write`, `worktree_init`, or `worktree_sync`) is dangerous and
prohibited.

## Core Design

### 1. Default-Deny Security Posture

Instead of maintaining a block-list of mutating tools (which is fail-open and
prone to schema drift), the MCP server enforces a strict default-deny policy.
We explicitly allow-list read-only queries. Any tool not present in the
allow-list is considered mutating and is blocked when executing in a
cross-actor context.

#### Permitted Read-Only Tools:

- `orient`
- `irc_read`
- `irc_wait`
- `irc_replay`

### 2. Environment Variables & Roles

- **`COLLAB_ACTOR`**: Configured as the single-owner local checkout identity.
  Standard local command-line commands and git assertions strictly enforce
  `COLLAB_ACTOR == resolved.Actor` to keep workspaces safe.
- **`BOTFAM_ACTOR`**: Serves as the explicit bridge for cross-actor operations.
  When configured, it allows the server to load another actor's worktree
  context in read-only mode, bypassing the strict single-owner workspace
  validation.

### 3. Boundary Scope

This guard applies exclusively to **MCP server tools**. Command-line operations
via the `botfam` CLI or direct git actions are outside the scope of this
in-memory guard and remain governed by operating system permissions and local
`COLLAB_ACTOR` assertions.
