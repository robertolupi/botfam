# Known Issues & Architectural Findings

> [!NOTE]
> **Status**: Updated (2026-06-14) to reflect the post-pivot architecture. Dead
> entries related to the retired SQLite mailbox/task-queue substrate have been
> moved to the Historical appendix. Active issues focus on Gatekeeper signing,
> harness configuration, and client connection resilience.

______________________________________________________________________

## 1. macOS codesigning Gatekeeper SIGKILL

### Problem

When the `botfam` Go binary is compiled locally on macOS, executing it via the
stdio MCP host causes the process to be killed immediately by macOS Gatekeeper
with `SIGKILL` (exit code 137). This happens silently and looks like an abrupt
connection shutdown/EOF to the MCP host.

### Resolution

Always sign the compiled binary with an ad-hoc signature after building on
macOS:

```bash
codesign --force --sign - ~/bin/botfam
```

______________________________________________________________________

## 2. Recursive Test Harness Deadlocks

### Problem

Tests that spawn child processes via `os.Args[0]` re-execute the `go test`
harness itself inside the child, which recursively runs the test suite and
deadlocks (or hangs until timeout). This was hit while testing the stdio server
end-to-end.

### Resolution

Inside the test, build a real binary to a temp path with
`go build -o <temp_path> ./cmd/botfam` and exec that binary directly. Never
re-exec `os.Args[0]` under `go test`. (The `callBotfamTool` helper in
`cmd/botfam/bootstrap_test.go` follows this pattern.)

### Lifecycle: kill spawned children in `t.Cleanup`

A temp-binary child started with `cmd.Start()` is **not** bounded by the test
unless you reap it. A `cmd.Wait()` at the end of the happy path is not enough: a
`t.Fatalf` (or panic) before that `Wait()` leaves the child orphaned, reparented
to launchd/init. Run repeatedly, these accumulate (issue #255 found 250 orphaned
`botfam serve` daemons from `/var/folders/.../T/` test dirs).

Immediately after `cmd.Start()`, register cleanup that kills and reaps the
process so it cannot outlive the test on any exit path:

```go
t.Cleanup(func() {
    if cmd.Process != nil {
        _ = cmd.Process.Kill()
        _, _ = cmd.Process.Wait()
    }
})
```

______________________________________________________________________

## 3. MCP Client Connection Is Unrecoverable After Server Crash

### Problem

If the `botfam` MCP server process dies mid-session (crash, SIGKILL from
Gatekeeper, recompile-under-it), the host editor's MCP client sees EOF and will
not reconnect for the remainder of the session — every subsequent
`mcp__collab__*` call fails. The agent loses its coordination channel without
losing its session.

### Mitigation

- Agents should fall back to out-of-band access: the `botfam` CLI against the
  same store, or a temporary Go script using `store.New(...)`.
- Longer term: the host-side story needs either MCP client reconnect support or
  a documented CLI-fallback convention in CLAUDE.md/AGENTS.md (partially done).

______________________________________________________________________

## 4. Uneven Harness Coverage — Only Claude Is Zero-Config

### Problem

The committed `.mcp.json` + `.claude/settings.json` make Claude Code worktrees
zero-config, but other harnesses are not covered: the Codex CLI session
reported having no `collab` MCP namespace registered and had to drive the repo
stdio server by hand (observed 2026-06-10); agy needed a hand-rolled workspace
MCP config (commit `e09c4f9`). Setup effort is per-harness and undocumented.

### Resolution

`bootstrap-botfam.sh` (in progress, session `2026-06-10-bootstrap-botfam`)
should emit per-harness config for every agent in the roster — `.claude/` for
Claude Code, `.codex/` for Codex, the Antigravity workspace MCP config for agy
— not just the Claude files.

______________________________________________________________________

## 5. Per-Agent-Branch Docs Don't Propagate Until Merged

### Problem

Docs and findings committed on one agent's branch (e.g. this file, born on
`agent/agy`) are invisible to the other fam members' worktrees until someone
merges or the change reaches `main`. A "committed" finding can therefore be
silently unknown to half the fam, while the session log and mailbox are shared
instantly.

### Mitigation

- Durable cross-fam facts belong in the shared session log first
  (`session_append`), with the doc commit as the promoted artifact.
- Convention: after committing fam-relevant docs, announce the branch/commit on
  the mailbox so others can merge promptly; land doc-only commits on `main`
  quickly.

______________________________________________________________________

## 6. Cross-Worktree Mutation by Other Actors

### Problem

Nothing prevents an actor from running git operations inside another actor's
worktree, and it happened in practice (claude fast-forwarded `agent/codex`
while the codex session was live). It worked because the tree was clean and the
operation was a fast-forward — but the owner may have uncommitted state, an
editor mid-write, or its own git operation in flight; git does not serialize a
visitor against the owner's session.

### Mitigation

- **Convention (now):** treat another actor's worktree as read-only. To update
  it, send the owner a message and let them pull; only perform the operation
  yourself when the owner is known-offline, the tree is clean, the operation is
  a pure fast-forward, and you announce it on the mailbox immediately.
- **Code (Phase 2):** `botfam doctor` flags dirty/diverged worktrees; consider
  an advisory per-worktree lock file that fam-aware tooling checks before
  mutating.

______________________________________________________________________

## 7. Go `irc-client` Connection Loss Zombie-Hang (F11 / AI-R4)

### Problem

The Go-based `irc-client` hangs indefinitely on connection loss rather than
exiting. The main thread blocks on reading from the incoming FIFO queue scanner
goroutine, which remains active and waiting for input. Because the scanner
goroutine does not detect the closed socket or connection drop, it does not
release its read lock, and the main process lingers as a zombie client that
must be manually killed.

### Severity: High (Robustness)

This breaks persistent client execution and prevents automated
reconnection/supervisor recovery, requiring manual intervention to terminate
the zombie process.

### Mitigation

- **Resolution**: Refactor the FIFO reader goroutine in `irc_client.go` to
  listen to a context cancellation or dynamically check socket/connection
  liveness. Ensure that any connection EOF or read failure cascades to close
  the FIFO reader and allows the main client thread to exit cleanly.

______________________________________________________________________

## Appendix: Historical (Pre-Pivot) Issues

The following issues describe findings from the pre-pivot SQLite
mailbox/task-queue/ccrep substrate. They are preserved here for historical
context.

### H1. Cooperative Identity Binding Vulnerability in `session_append`

#### Problem

In the Phase 1 Go implementation, any client/CLI invocation can append entries
to any session log while masquerading as any actor. If a process calls the MCP
tool `session_append` passing `actor: "claude"`, the server will append it
directly to `session.jsonl` under that name.

### H2. `session_read` Filter Parameter Collision

#### Problem

The historical `DESIGN_sessions` specification (now `lineage-botfam-sessions`
on the wiki) initially defined the session log filtering parameter as `actor`.
However, the MCP server binds `actor` as the sticky identity parameter, causing
conflicts.

### H3. Split-Brain Store Path Resolution

#### Problem

Different execution entry points (such as the MCP server, Go CLI, and test
runner) running with different working directories resolved different store
paths under `~/.botfam/`, leading to fragmented states.

### H4. Missing Append-Time Schema Validation

#### Problem

Malformed or incomplete handoff data could be appended directly to the session
log, introducing dirty data into the consensus history.

### H5. CCREP Execution Ownership Is Unspecified (Double-Execution Risk)

#### Problem

The `ccrep:*` message convention defined `proposal` / `critique` / `evaluation`
but said nothing about who executes an approved proposal, leading to
double-execution races.

### H6. CCREP Consent Semantics Are Undefined (Quorum and Silence)

#### Problem

Nothing defined how many evaluations approve a proposal, or what silence means.

### H7. Crossed Messages Make Reviews and Revisions Race

#### Problem

ccrep ran over an async mailbox with no barrier between rounds, allowing
critiques to apply to already superseded revisions.

### H8. No Shared Proposal State — Everyone Infers From Their Own Mailbox

#### Problem

A ccrep proposal's status (open / approved / executed / superseded) existed
nowhere; each agent reconstructed it from the messages it received.

### H9. ccrep Message Vocabulary Is Drifting Between Agents

#### Problem

With `ccrep:*` being convention-only, each agent improvised types and fields.

### H10. `claim` Has No Task-Id Targeting — Intended Assignments Degrade Into Churn

#### Problem

`claim` was strictly FIFO, leasing the oldest open task, which degraded
targeted assignments into queues/abandon churn.

### H11. Lease Sweeps Race Against Actively-Working Agents

#### Problem

A claimed task's lease expired unless the owner called `heartbeat`, causing
tasks to be swept away from active workers.

### H12. Client-Side MCP Schema Drift for `claim` Tool

#### Problem

The client-side MCP tool schema definition for `claim` only exposed a subset of
parameters supported by the backend, breaking task targeting.

### H13. `recv` Long-Poll vs. Harness Tool-Call Ceilings

#### Problem

`recv` blocked until a message arrived, which could exceed the harness's
tool-call timeout and get killed.
