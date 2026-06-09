# Lineage v0 — deep-cuts `collab` + `ccrep`

**Where:** `~/src/deep-cuts/tools/collab_mcp/` and `tools/ccrep/`, with protocol
docs under `doc/collab/` and skills under `skills/{bot-collab,collab,ccrep}/`.
**Status:** working, in production use inside a real app. **This is the origin.**

deep-cuts is a macOS audio-analysis app. While building it with several agents
(Claude, agy, Codex, Gemini), the author grew the coordination machinery as a
side effect of needing the agents to actually work together. That machinery —
not the app — is botfam's ancestor.

---

## What it was

Three orthogonal coordination patterns, discovered in order of increasing need:

1. **FIFO handoff** (`doc/collab/fifo-handoff-design.md`, `skills/collab`) — a
   two-party serial baton over a named pipe. `mkfifo` is the atomic
   create-or-fail: the creator waits, the loser goes first. Dead simple, serial
   by design, used for cross-model review where quality > speed.
2. **Actor mailboxes** (`tools/collab_mcp/`, the `collab` MCP) — N agents, a
   maildir backend, blocking `recv`, plus a lease-based task queue. **This is the
   direct parent of botfam.**
3. **CCREP quality ratchet** (`tools/ccrep/`) — an append-only event ledger +
   pure reducer that gates an artifact (code change, review, or design doc)
   behind eval + independent critique + a consensus gate.

The decisive architectural choice: **collab and ccrep are two separate layers.**
Messaging and the task queue are *pure filesystem renames with no ledger*; the
hash-chained ledger exists only inside ccrep and is paid for only when an
artifact must be *proven* better, not merely coordinated.

## How it worked

- `MailStore` (`collab_mcp/store.py`, ~270 LOC, pure stdlib) over a maildir:
  `tmp/`, `<actor>/{new,cur}/`, `tasks/{open,claimed/<actor>,done}/`.
- Atomicity from `os.replace` / `os.rename`: `send` is write-then-rename;
  `claim` is a rename race where exactly one worker wins.
- `recv(timeout_s)` blocks on a `watchfiles` directory watch (poll fallback),
  the single blocking point of the loop.
- `server.py` is a thin FastMCP wrapper; every tool takes an optional `actor`,
  resolved per call via `_get_store(actor)`.

## Pros

- **Two-layer separation.** You don't touch a ledger to say "your turn."
- **Blocking `recv` = zero-token waiting.** Park until woken instead of polling.
- **Pure-stdlib store**, unit-testable with no MCP dependency.
- **Atomic rename as the only concurrency primitive** — no broker, no DB, no flock.
- **Human-readable audit trail** — the maildir *is* the log; `ls` is your debugger.
- **Composable** — coordinate over `collab`, ratchet with `ccrep`, independently.

## Cons

- **Python + venv brittleness.** The venv "kept overstepping"; editable installs
  bled across worktrees.
- **Packaging tax.** `run_collab_mcp.py`, `PYTHONPATH=tools`, and canonical-root
  discovery existed *only* to paper over Python packaging and worktree layout —
  pure accidental complexity.
- **Not standalone.** It lived inside a large app; the reusable kernel had to be
  mentally extracted from domain code.
- **Client-supplied `actor`.** The per-call `actor` override is harmless under
  stdio-per-agent but is a spoofing seam the moment a server is shared.

## Lessons carried into botfam

- **Keep** the maildir kernel, atomic-rename concurrency, and blocking `recv`.
- **Keep** the two-layer split: botfam v0 is *only* the collab layer; CCREP
  returns later as an optional second surface, never in the messaging hot path.
- **Drop** Python and everything built to cope with it (venv, wrapper scripts,
  `PYTHONPATH`). Go gives one static binary instead.
- **Keep** the per-call `actor` argument — agents that share a worktree share one
  `.mcp.json`, so they must self-identify per call (botfam pins it on first use).
  Add an *out-of-repo* lock that pins identity to `COLLAB_ACTOR` and forbids
  overrides, for strict one-process-per-agent setups.
- **Extract** it as a standalone tool, not an appendage of an app.
