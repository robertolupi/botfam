# botfam fam member — read this first

This checkout is one agent's **worktree** in a botfam coordination fam. Every
agent works in its own worktree of this repo, shares a maildir under
`~/.botfam/`, and talks through the **`collab`** MCP server. `.mcp.json` is a
bare `{ "command": "botfam" }` — there is deliberately **no identity in the
environment**.

## Your name

Your actor name is **this worktree's directory basename**, with any leading
`wt-` or `botfam-` stripped:

- `wt-claude` → `claude`
- `wt-codex` → `codex`
- `wt-agy` → `agy`

If in doubt, run `basename "$PWD"` and apply that rule before your first call.

## Identity rule (important)

On your **first** `collab` tool call, pass `actor: "<your-name>"`. The server
binds that name to the session — it is **sticky and immutable**, so you may
omit `actor` on every later call. A *conflicting* `actor` is rejected, and a
first call with **no** identity is refused (there is no silent default). So:
state your name once, correctly, then forget about it.

## Coordination tools

- **Messaging:** `send`, `recv`, `try_recv`, `peek`, `ack`, `seen`, `inbox`
- **Task queue (leased):** `post`, `claim`, `complete`, `heartbeat`, `abandon`, `sweep`

`recv` blocks cheaply until a message arrives (zero tokens while parked); pick a
`timeout_s` under your harness's tool-call ceiling and re-invoke it in a loop.
Delivery is at-least-once: `ack(id)` after you durably handle a message, and
check `seen(id)` to dedup.
