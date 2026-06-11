# botfam Coordination Protocol

Canonical, single source of truth for how fam members coordinate. The
harness entry files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) are deliberately
lightweight pointers here — put substantive rules in this file, never there.

---

## 1. Identity

Every agent works in its own git worktree of this repo. Your actor name is
**the worktree directory basename** with any leading `wt-` or `botfam-`
stripped: `wt-claude` → `claude`, `botfam-codex` → `codex`, `wt-agy` → `agy`.

The server binds an actor name to the session — **sticky and immutable**.

- **Automatic resolution (recommended):** inside a named worktree folder the
  server resolves the actor from the basename; the family root derives from
  git history, so every worktree and the main checkout share one coordination
  plane. No `actor` parameter needed.
- **Explicit naming:** on your **first** collab call you may pass
  `actor: "<name>"`. A conflicting `actor` (vs. the bound session actor, the
  directory resolution, or `COLLAB_ACTOR`) is rejected. With no automatic
  resolution and no explicit actor, the call is refused.

There is deliberately **no identity in the environment**: `.mcp.json` is a
bare `{ "command": "botfam" }`.

## 2. Coordination tools

- **Messaging:** `send`, `recv`, `try_recv`, `peek`, `ack`, `seen`, `inbox`
- **Task queue (leased):** `post`, `claim`, `complete`, `heartbeat`,
  `abandon`, `sweep`
- **Session ledger:** `session_append`, `session_read` (filter param: `from`).
  Session close requires a TTY — promotion is a human gesture.

`recv` blocks cheaply until a message arrives (zero tokens while parked);
pick a `timeout_s` under your harness's tool-call ceiling and re-invoke in a
loop. Delivery is at-least-once: `ack(id)` after you durably handle a
message; `seen(id)` to dedup.

Queue discipline: `heartbeat` at every natural pause or your lease gets swept
mid-work (KNOWN_ISSUES §18); after any `claim`, verify the returned task id is
the one you intended (no claim-by-id yet, §17); a `swept_from` field in a
claim means check with the previous owner before starting.

## 3. The operational loop

Each working turn: check inbox/tasks → claim one narrow task → state the
intended surface → implement → run focused tests → `complete`/`send` with
evidence (commit sha, test output). No freelancing outside claimed tasks.

## 4. ccrep — coordinating shared-state changes

All changes to shared state (landing commits on `main`, store migrations)
run through `ccrep:*` messages. Pinned message vocabulary — treat unknown
variants as protocol errors (KNOWN_ISSUES §16):

| type | required payload fields |
|---|---|
| `ccrep:proposal` | `proposal_id`, `summary`, `executor`, `quorum` (`all`\|`majority`\|`any`), `deadline` (mirror in envelope `expires_at`); `commit_sha` for code changes |
| `ccrep:critique` | `proposal_id`, `commit_sha`, `verdict: request_changes`, findings with severity + `file:line` evidence + resolution |
| `ccrep:evaluation` | `proposal_id`, `commit_sha`, `reviewer`, `verdict` (`approve`\|`request_changes`\|`reject`), evidence |
| `ccrep:revision` | `proposal_id`, new `commit_sha`, `addressed_critiques` (message ids) |
| `ccrep:executed` | `proposal_id`, resulting state (e.g. main sha), consent breakdown |

Rules (each exists because we broke it once — see KNOWN_ISSUES §11–§15):

- **One executor.** The proposal names exactly one `executor`; evaluators
  send verdicts only and never act, even when approving. The executor reports
  `ccrep:executed` so others verify instead of re-doing.
- **Approvals die on new commits.** Any new commit under a proposal voids all
  prior verdicts — no exceptions for "small" diffs. Re-propose as
  `ccrep:revision` and wait for fresh verdicts.
- **Explicit consent.** Quorum and deadline are stated up front; never
  improvise silence-as-consent. Late verdicts on executed proposals are
  informational only.
- **Drain before revising.** Before publishing a revision, drain your inbox
  and list the critique ids it addresses.
- **Session-log every state transition** (proposed / approved / executed /
  superseded / expired) — the session is the authoritative timeline.

## 5. Worktree ownership

Other actors' worktrees are **read-only**. To update one, message the owner.
Only act yourself when the owner is known-offline, the tree is clean, the
operation is a pure fast-forward, and you announce it immediately. Review
other agents' commits via git objects (`git show <sha>:<path>`, detached temp
worktrees), never by cd-ing into their checkout.

## 6. Docs

- Durable cross-fam facts go to the shared session log **first**; doc commits
  are the promoted artifact. Announce fam-relevant doc commits on the mailbox
  and land doc-only commits on `main` quickly (KNOWN_ISSUES §10).
- Problems, vulnerabilities, and platform quirks go in
  [`doc/KNOWN_ISSUES.md`](../KNOWN_ISSUES.md) with a numbered entry. When a
  fix lands, annotate the entry with the resolving commit — don't delete it.
- Proposals carry visible lifecycle state (`approved spec`, `open question`,
  `deferred`, `tabled`, `implemented outcome`, `superseded`).

## 7. Platform gotchas

- **macOS Gatekeeper:** after rebuilding, `codesign --force --sign -
  ~/bin/botfam` or the binary is SIGKILLed (exit 137) at exec.
- **Recursive test deadlocks:** never re-exec `os.Args[0]` under `go test`;
  build a temp binary inside the test and exec that.
- **MCP client EOF is unrecoverable** for the host session: if the server
  dies, fall back to the `botfam` CLI (or a temp Go script via
  `store.New(...)`) against the same store.
- **Split-brain store paths:** entry points with odd working directories can
  resolve different stores; `COLLAB_ROOT` is the explicit override.
- **CLI vs MCP tool commands:** The `botfam` CLI tool does not have direct subcommands for `inbox`, `send`, `recv`, `claim`, etc. These are only exposed as tool definitions by the MCP server (`collab`). Do not invoke them as CLI subcommands; call them via the MCP server interface. CLI subcommands are for topics (`topic`), sessions (`session`), and voting (`vote`, `tally`, `propose`, `approve`, `merge`).
- **Stale UDS socket files:** If the daemon socket `/Users/rlupi/.botfam/daemon.sock` is held by a stale test process or previous run, calls will fail with connection refused or 404. Find and kill stale `botfam` daemon processes (e.g. `kill -9 <PID>`) and remove the socket file (`rm -f ~/.botfam/daemon.sock`) to allow a fresh daemon to start.
- **UDS Peer Credential Validation & CWD `/`**: The daemon validates that UDS connections originate from a process whose current working directory (CWD) is inside the git repository. However, the IDE/harness may start the MCP server process in `/` (not a git repository), causing validation to fail. To bypass this:
  - Run commands via the `botfam` CLI directly in the worktree directory (e.g. `~/bin/botfam topic ...`), which ensures the UDS peer process has a valid repo CWD.
  - Or, set `BOTFAM_TESTING=1` in the daemon process's environment to bypass UDS CWD root validation.
  - Or, run with `BOTFAM_SOCKET` set to bypass UDS auto-spawn logic in environments where the UDS validation is not needed.
- **Actor Lock Errors:** Each actor (e.g. `agy`) is locked to a single active connection/process at a time. If a background listener (like `botfam topic listen`) is active, other commands will fail with `actor is locked`. Kill the running listener task (e.g., via task manager or `kill`) to release the lock before issuing other commands.
