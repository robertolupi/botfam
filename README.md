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

## Implementation status

The repository now contains an initial Go implementation of the Phase 1
coordination layer:

- `botfam` runs a dependency-free stdio MCP server by default.
- `botfam setup <project> --agents alice,bob` creates the fam root, registry,
  mailboxes, task directories, and project symlink.
- Mailbox tools are implemented: `send`, `recv`, `try_recv`, `peek`, `ack`,
  `seen`, and `inbox`.
- Task queue tools are implemented: `post`, `claim`, `complete`, `heartbeat`,
  `abandon`, and `sweep`.
- Identity is sticky per stdio session, with optional actor locking via
  `BOTFAM_LOCK_ACTOR=1` or the out-of-repo botfam config.
- Integration tests launch multiple real `botfam serve` subprocesses over stdio
  with separate actors and a shared temporary `COLLAB_ROOT`.

Still future or incomplete:

- `recv` currently uses a short polling loop rather than `fsnotify`.
- `fam.toml` handling is intentionally minimal and only parses the shape botfam
  writes itself.
- CCREP is not implemented; it remains Phase 2.
- bottown is not implemented; it remains the future networked sibling.

## Developer quickstart

Run tests with Go's default cache if your environment allows it:

```bash
go test ./...
```

In restricted sandboxes, keep Go caches inside the workspace:

```bash
env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
```

Build the binary:

```bash
go build ./cmd/botfam
```

Set up a fam from inside a git repository:

```bash
botfam setup my-project --agents alice,bob
```

Run the stdio MCP server:

```bash
botfam
```

> botfam is the stdio iteration. A later networked sibling — **bottown** — serves
> agents that don't share a filesystem via a small **REST** service: an explicit
> `topic` namespace, bearer-token identity, and long-poll for blocking `recv`. The
> agent-facing MCP tools stay identical; only the backend swaps. botfam ships
> first — see [doc/DESIGN_bottown.md](doc/DESIGN_bottown.md).
