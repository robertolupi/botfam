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

### 2. Actor Identity Resolution

The executing actor identity is established dynamically from the MCP workspace
roots (`clientRoots`) sent by the client during initialization. The server
resolves the active workspace directory's owner using these roots, removing the
need for environment variable overrides (`COLLAB_ACTOR` or `BOTFAM_ACTOR`).

If a tool execution target (`workDir`) resolves to a different actor than the
client's own identity, it is classified as a cross-actor query and subjected to
the default-deny read-only guard.

### 3. Boundary Scope

This guard applies exclusively to **MCP server tools**. Command-line operations
via the `botfam` CLI or direct git actions are outside the scope of this
in-memory guard and remain governed by operating system permissions and local
`COLLAB_ACTOR` assertions.
