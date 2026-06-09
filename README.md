# botfam

A tiny, single-binary **stdio MCP server** that lets a small family of AI agents
coordinate over a shared filesystem: send messages, block cheaply until one
arrives, and hand work back and forth through a lease-based task queue.

botfam is the lightweight successor to a line of multi-agent coordination
experiments (`deep-cuts/collab` → `hydra` → `scriba`). It keeps the one idea
that proved out — a maildir-backed mailbox with a *blocking* receive — and drops
everything that turned out to be cruft: no Gitea, no per-bot SSH keys, no
Python/venv, no consensus ledger in the hot path.

- **Transport:** stdio MCP, one server process per agent.
- **Identity:** cooperative by default — a call may carry an `actor`, the first
  one a process sees sticks (so a shared `.mcp.json` across a worktree still
  works), and `COLLAB_ACTOR` is the fallback. An out-of-repo lock pins identity
  to `COLLAB_ACTOR` and forbids overrides for strict one-process-per-agent setups.
- **State:** a maildir tree under `~/.botfam/<project>/`, outside any repo and
  shared across that project's worktrees and clones. Atomic `rename`, no daemon,
  no DB. `botfam setup` creates it.
- **Language:** Go. One static binary; nothing to install, nothing to activate.

See [doc/DESIGN.md](doc/DESIGN.md) for the full v0 spec.

> botfam is the stdio iteration. A later networked sibling — **bottown** — serves
> agents that don't share a filesystem via a small **REST** service: an explicit
> `topic` namespace, bearer-token identity, and long-poll for blocking `recv`. The
> agent-facing MCP tools stay identical; only the backend swaps. botfam ships
> first — see [doc/DESIGN_bottown.md](doc/DESIGN_bottown.md).
