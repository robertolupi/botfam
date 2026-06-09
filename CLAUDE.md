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

The server binds an actor name to the session — it is **sticky and immutable**.

- **Automatic resolution (Recommended):** If you run inside a named worktree folder (e.g., `wt-agy`), the server automatically parses the directory basename to resolve the actor as `agy`; the family root is derived from the repository git history, so every worktree and the main checkout share one coordination plane. In this case, you do not need to pass the `actor` parameter on your tool calls.
- **Explicit naming:** Alternatively, on your **first** `collab` tool call, you can pass `actor: "<your-name>"`. A *conflicting* `actor` is rejected. If no automatic resolution is possible (e.g. running from an unnamed directory) and no `actor` is provided on the first call, it is refused.

## Coordination tools

- **Messaging:** `send`, `recv`, `try_recv`, `peek`, `ack`, `seen`, `inbox`
- **Task queue (leased):** `post`, `claim`, `complete`, `heartbeat`, `abandon`, `sweep`

`recv` blocks cheaply until a message arrives (zero tokens while parked); pick a
`timeout_s` under your harness's tool-call ceiling and re-invoke it in a loop.
Delivery is at-least-once: `ack(id)` after you durably handle a message, and
check `seen(id)` to dedup.

## Lessons Learned & Gotchas (For Future Reference)

- **macOS Gatekeeper / Codesigning**: If you recompile the `botfam` binary, it might get killed with `SIGKILL` (exit code 137) when executed if it lacks a valid signature. Always run `codesign --force --sign - ~/bin/botfam` after building.
- **Recursive Test Deadlocks**: Spawning test processes using `os.Args[0]` while running `go test` causes the test harness to run recursively in child processes, causing hangs/deadlocks. Build the binary using `go build -o <temp_path>` inside the test and execute that binary directly.
- **MCP Connection Recovery**: If the MCP server crashes, the host editor's MCP client connection terminates and won't accept future calls (returning EOF). When this happens, bypass the MCP connection by writing temporary Go CLI scripts (e.g. using `store.New(...)`) to perform out-of-band communication or run commands directly.
- **Split-Brain Store Paths**: Different entry points (MCP vs CLI vs libraries) running with different working directories might resolve different store paths. Use explicit `COLLAB_ROOT` environment variables, or ensure resolution logic resolves working directories to the same store folder uniformly (verified in `TestResolver`).

