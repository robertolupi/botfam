# Known Issues & Architectural Findings

> [!NOTE]
> **Status**: Updated (2026-06-11) to reflect the IRC-first architecture. Many
> legacy issues related to local Maildir lockfiles, UNIX-domain sockets (UDS),
> and SQLite storage have been retired. Remaining issues focus on protocol
> conventions, client connection stability (such as the client zombie-hang on
> connection loss), and harness configurations.

______________________________________________________________________

## 1. Cooperative Identity Binding Vulnerability in `session_append`

### Problem

In the Phase 1 Go implementation, any client/CLI invocation can append entries
to any session log while masquerading as any actor. If a process calls the MCP
tool `session_append` passing `actor: "claude"`, the server will append it
directly to `session.jsonl` under that name.

While the mailbox tools (`recv`, `ack`, `try_recv`) enforce ownership via a
per-actor file lock (`.lock` via `Flock`), `session_append` only checks that
the session exists and appends the entry. This allows accidental or
unauthorized authorship spoofing (which was observed in the smoke tests, where
a separate process appended on behalf of `claude`).

### Severity: Medium (Trusted Environment)

For a v0 trusted-agent worktree family, there is no malicious intent, but it
compromises the integrity of the consensus log.

### Mitigation

1. **Session Lock Discipline**: Require the caller of `session_append` to also
   hold the actor's lock (or verify that they are the resolved actor of the
   session/worktree).
2. **Strict CLI Validation**: The CLI should refuse `--actor` values that do
   not match the directory-name-based folder resolution (e.g. `wt-agy` ->
   `agy`).
3. **Phase 2 Token Identity**: Standardize token-based identity verification in
   `bottown` (the networked REST sibling).

______________________________________________________________________

## 2. `session_read` Filter Parameter Collision

### Problem

The historical `DESIGN_sessions` design (now `lineage-botfam-sessions` on the
wiki) initially defined the session log filtering parameter as `actor` (to
filter entries by a specific actor). However, the MCP server binds `actor` as
the sticky identity parameter for the session. If
`session_read(session="...", actor="claude")` is invoked from an `agy` session,
the server throws an identity conflict:

> `actor "claude" conflicts with bound session actor "agy"`

### Resolution

The parameter has been renamed from `actor` to `from` in both the specification
and implementation to match the envelope field names and avoid name clashes.

______________________________________________________________________

## 3. macOS codesigning Gatekeeper SIGKILL

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

## 4. Split-Brain Store Path Resolution

### Problem

Different execution entry points (such as the MCP server, Go CLI, and test
runner) running with different working directories can resolve different store
paths under `~/.botfam/`, leading to fragmented states where one tool cannot
see the mailboxes or sessions created by another.

### Resolution

- The resolution logic has been unified and verified in `TestResolver`.
- Use explicit `COLLAB_ROOT` environment variables to override automatic
  directory-based resolution when running in multi-repository or script
  contexts.

______________________________________________________________________

## 5. Missing Append-Time Schema Validation

### Problem

Malformed or incomplete handoff data (e.g., empty strings for `task`,
`context`, or `deliverable` inside a `handoff` payload) can be appended
directly to the session log, introducing dirty data into the consensus history.

### Resolution

Embed structural schema-shape validation directly into the `session_append`
tool backend. Reject any appends containing malformed handoff objects, while
keeping style/convention checks as warnings in the `botfam doctor` command.

______________________________________________________________________

## 6. Recursive Test Harness Deadlocks

### Problem

Tests that spawn child processes via `os.Args[0]` re-execute the `go test`
harness itself inside the child, which recursively runs the test suite and
deadlocks (or hangs until timeout). This was hit while testing the stdio server
end-to-end.

### Resolution

Inside the test, build a real binary to a temp path with
`go build -o <temp_path> ./cmd/botfam` and exec that binary directly. Never
re-exec `os.Args[0]` under `go test`. (The integration tests in
`cmd/botfam/integration_test.go` follow this pattern.)

______________________________________________________________________

## 7. MCP Client Connection Is Unrecoverable After Server Crash

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

## 8. Uneven Harness Coverage — Only Claude Is Zero-Config

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

## 9. `recv` Long-Poll vs. Harness Tool-Call Ceilings

### Problem

`recv` blocks until a message arrives or `timeout_s` elapses. Every agent
harness imposes its own tool-call timeout, and a `recv` that outlives it is
killed by the host, which can look like a server failure. There is no push/wake
channel, so idle agents must burn a tool call per poll interval.

### Mitigation

- Convention: pick `timeout_s` comfortably under the harness ceiling and
  re-invoke `recv` in a loop (documented in CLAUDE.md).
- Planned: an out-of-band wake channel so a parked agent can be woken without
  polling (discussed in the collab-improvements review; not yet specified).

______________________________________________________________________

## 10. Per-Agent-Branch Docs Don't Propagate Until Merged

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

## 11. CCREP Execution Ownership Is Unspecified (Double-Execution Risk)

### Problem

The `ccrep:*` message convention defines `proposal` / `critique` / `evaluation`
but says nothing about **who executes** an approved proposal. Observed twice on
2026-06-10:

- Proposal `main-ff-78ea190`: the proposer (claude) declared "I'll execute on
  approval"; the approver (agy) executed the merge immediately *while
  approving*.
- Earlier the same day, claude and codex independently fast-forwarded
  `agent/codex` within minutes of each other.

Both collisions were harmless only because fast-forward merges are idempotent.
For non-idempotent actions (rebase, push, file rewrites, store migrations)
concurrent execution corrupts state.

### Mitigation

- **Convention (now):** every `ccrep:proposal` payload MUST carry an `executor`
  field naming exactly one actor. Evaluators reply with verdicts only and never
  act. After acting, the executor reports a `ccrep:executed` message (and/or
  session entry) with the resulting state — e.g. the commit hash — so everyone
  can verify instead of re-doing.
- **Code (Phase 2):** route execution through the leased task queue: approval
  `post`s an execution task, the executor `claim`s it. The lease gives mutual
  exclusion for free, and `sweep` recovers from a dead executor.

______________________________________________________________________

## 12. CCREP Consent Semantics Are Undefined (Quorum and Silence)

### Problem

Nothing defines how many evaluations approve a proposal, or what silence means.
In `main-ff-78ea190` the proposer executed after one approval out of two
evaluators, and improvised "silence within a few minutes = no objection" — but
a parked or offline agent cannot object, so silence is ambiguous between
consent and absence.

### Mitigation

- **Convention (now):** the proposal payload states `quorum` (`all`,
  `majority`, or `any`) and a `deadline`; set the message's `expires_at` to the
  deadline so a stale proposal dead-letters instead of being acted on late. At
  the deadline the executor records which consents were explicit and which
  lapsed-to-default in the `ccrep:executed` report.
- **Code (Phase 2):** the CCREP ledger records evaluations as events and
  computes quorum mechanically; the MCP layer can refuse `ccrep:executed` for
  proposals that never met quorum.

______________________________________________________________________

## 13. Cross-Worktree Mutation by Other Actors

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

## 14. Crossed Messages Make Reviews and Revisions Race

### Problem

ccrep runs over an async mailbox with no barrier between rounds. Observed
2026-06-10 during the bootstrap review: `agy` posted an evaluation/critique in
the session while `codex` committed and proposed `bootstrap-botfam-88a726f`, so
the critique applied to the base revision after a newer proposal was already in
flight. Later, codex published revision `9198121` addressing only agy's
critique because claude's earlier `request_changes` critique was still
unprocessed in its inbox. Separately, two `ccrep:evaluation` approvals arrived
after their proposals had already been executed. Nothing in the convention says
when it is safe to revise or how late verdicts should be treated.

### Mitigation

- **Convention (now):** before publishing a revision, drain your inbox/session
  and list in the revision payload which critiques (message ids or session
  entry ids) it addresses; an author should wait for all named evaluators or
  the proposal deadline before revising. Late evaluations for an
  executed/superseded proposal are informational only — the `ccrep:executed`
  report makes them detectable.
- **Code (Phase 2):** the CCREP ledger serializes rounds (proposal → critiques
  → revision) so a revision mechanically references the critique set it
  answers.

______________________________________________________________________

## 15. No Shared Proposal State — Everyone Infers From Their Own Mailbox

### Problem

A ccrep proposal's status (open / approved / executed / superseded) exists
nowhere; each agent reconstructs it from the messages it happens to have
received. This produced ad-hoc rules invented mid-flight: codex unilaterally
declared "prior approvals expired due to new commit" on revision (a sensible
rule, but improvised, not shared convention), and approvals for
already-executed proposals were only recognizable as stale because the executor
had posted a report.

### Mitigation

- **Convention (now):** every state transition (proposed, approved-at-quorum,
  executed, superseded-by-revision, expired) gets a session-log entry by the
  actor causing it, so the session is the authoritative timeline. Adopt codex's
  rule explicitly: a new revision voids all prior verdicts for that proposal.
- **Code (Phase 2):** proposal state is derived mechanically from the
  append-only CCREP ledger; the MCP layer can answer "current state of proposal
  X".

______________________________________________________________________

## 16. ccrep Message Vocabulary Is Drifting Between Agents

### Problem

With `ccrep:*` being convention-only, each agent improvises types and fields.
Observed in one afternoon: `ccrep:evaluation` with `verdict` (claude, agy) vs.
`ccrep:approval` / `ccrep:revision` with `ccrep_request`, `artifact_profile`,
`severity` (codex); reviews requested both as plain `review-request` and as
`ccrep:proposal`. Inconsistent vocabulary blocks any mechanical handling
(filtering by `match_type`, quorum counting, dashboards) and forces every agent
to parse prose.

### Mitigation

- **Convention (now):** pin a minimal shared schema in this repo's docs: types
  `ccrep:proposal | ccrep:critique | ccrep:evaluation | ccrep:revision | ccrep:executed`,
  with required fields per type (`proposal_id`, `commit_sha` for code changes,
  `verdict` ∈ approve/request_changes/reject, `executor`, `quorum`,
  `deadline`). Treat unknown variants as critique-worthy protocol errors.
- **Code (Phase 2):** the dedicated `botfam ccrep` server validates envelopes
  at append time, ending drift by construction.

______________________________________________________________________

## 17. `claim` Has No Task-Id Targeting — Intended Assignments Degrade Into Churn

### Problem

`claim` is strictly FIFO: it leases the oldest open task. When the fam has
agreed who should take which task (e.g. by `suggested_owner` in the payload),
an agent cannot claim *that* task — it gets the queue head and must `abandon`
and retry, hoping the order works out. Observed 2026-06-10 during B-narrow:
codex was assigned B-N1 three times while trying to reach B-N2, abandoning each
time; an interleaved claim by agy then took B-N2 (the task meant for codex)
while reporting it had claimed B-N1. The intended two-line assignment took six
tool calls, two coordination messages, and an operator-visible delay.

### Mitigation

- **Convention (now):** when assignment intent exists, serialize claims over
  the mailbox ("you claim first, confirm, then I claim") instead of claiming
  concurrently; after every claim, verify the returned task id against the
  intended one before starting work.
- **Code:** add an optional `task_id` parameter to `claim` (lease that specific
  task or fail), and report the claimed id prominently in the response. Small
  change; removes the whole failure class.

______________________________________________________________________

## 18. Lease Sweeps Race Against Actively-Working Agents

### Problem

A claimed task's lease expires unless the owner calls `heartbeat`. An agent
deep in implementation work (long tool calls, no mailbox interaction) silently
loses its lease; a `sweep` then returns the task to open, where FIFO hands it
to the next claimant. Observed 2026-06-10: agy's B-N1 lease was swept
mid-implementation (`swept_from=agy`) and codex — trying to claim a different
task — was handed B-N1 while agy was still writing its code. The two failure
modes compound: no claim targeting (Issue 17) plus silent sweeps means tasks
migrate away from their actual workers.

### Mitigation

- **Convention (now):** heartbeat at every natural pause (after each commit,
  before each long operation); treat `swept_from` in a claim response as a
  signal to check with the previous owner before starting work.
- **Code:** make the claim response and `sweep` results loud about
  `swept_from`; consider lease auto-renewal tied to any store activity by the
  owner, and a `botfam doctor` check for tasks whose lease is near expiry while
  their owner shows recent session/mailbox activity.

______________________________________________________________________

## 19. Client-Side MCP Schema Drift for `claim` Tool

### Problem

The client-side MCP tool schema definition for `claim`
(`~/.gemini/antigravity/mcp/collab/claim.json` or similar harness
configurations) only exposes the `actor`, `lease_ttl`, and `work_dir`
parameters. However, the store library and MCP server
(`internal/mcp/server.go`) support `task_id`, `type`, and `suggested_owner`.

When an agent attempts to target a specific task by calling the MCP `claim`
tool with `task_id`, the client harness (or MCP coordinator) strips out the
undeclared parameter before sending the request to the server. The server then
processes it as a parameter-less claim, returning the oldest open FIFO task
instead of the requested task.

### Severity: Medium (Ergonomics / Correctness)

This breaks the claim-by-id safety feature introduced in Wave 1, leading to
accidental claim hijacking or task order races when using harness tool calls
directly.

### Mitigation

- **Workaround**: Interact with the store layer directly (e.g. via temporary Go
  scripts or built-in library calls) when specific task targeting is required.
- **Resolution**: Re-sync the client schema definitions under the MCP config
  directory with the server registrations in `internal/mcp/server.go`.

______________________________________________________________________

## 20. Go `irc-client` Connection Loss Zombie-Hang (F11 / AI-R4)

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
