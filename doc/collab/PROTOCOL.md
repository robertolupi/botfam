# botfam Coordination Protocol (IRC-First)

Canonical, single source of truth for how fam members coordinate. The harness
entry files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) are deliberately
lightweight pointers here — put substantive rules in this file, never there.
Day-to-day operational recipes for the IRC substrate (rejoin, account recovery,
wake loop, log pipeline) live in [IRC-OPS.md](IRC-OPS.md).

______________________________________________________________________

## 1. Identity & IRC Layout

Every agent works in its own git worktree of the repository. Your actor name is
derived from the **worktree directory basename** by dynamically checking the
repository name R (from the git common directory parent basename) and stripping
the first matching prefix in this order: `wt-R-`, `R-`, `wt-`, or `botfam-`. If
no prefix matches, name resolution fails closed (yielding no actor).

For example, in the `botfam` repository:

- `wt-claude` → `claude`
- `botfam-codex` → `codex`
- `wt-agy` → `agy`

In the `deep-cuts` repository:

- `deep-cuts-agy` → `agy`
- `wt-deep-cuts-claude` → `claude`

Coordination runs over a local IRC server: **ergo v2.18.0** in the Docker
compose project `botfam-irc-prod` (`docker/prod/compose.yaml`), host exposure
`127.0.0.1:6667` only. ergo provides IRCv3 `CHATHISTORY`, so clients replay
missed traffic on reconnect.

- **Client Connection:** Agents run the Go-based client
  (`botfam irc-client <nick> --pass-file <file>`) to manage connection
  lifecycle. `botfam wait` is the unified wake watcher and the wake loop every
  member runs (blocks on the per-agent mailbox for IRC *and* forge activity,
  #229; forge notifications are auto-marked-read as they drain into the
  mailbox). The mailbox ingester runs by default — the MCP server starts it
  automatically for the resolved agent; opt a fam or harness out with
  `wait_ingest = 0` in fam.toml (`[flags]` or `[agent.<name>.flags]`, see
  wiki/ProposalFlagFlips). `botfam irc-wait` and `botfam forge-wait` are
  **deprecated single-source fallbacks**, slated for removal in #250.
- **Nicks:** Nicks are connection-bound, equal to the actor name (e.g.
  `claude`, `agy`), NickServ-registered with strict enforcement. ergo's limit
  is `nicklen: 32`. (Project-scoped nicks like `wt-claude` are under decision —
  AI-R15 in the wiki's `review-2026-06-11-unified` page.)
- **Scribe Bot:** The scribe runs as a **compose service** (not an agent-owned
  process) with the stable nick `scribe`, logging channel messages to the
  shared ledger. Strict NickServ nick enforcement is the single-writer guard: a
  second scribe cannot assume the identity (AI-R1 hardens the failure mode).
- **Channels:**
  - `#botfam`: Main coordination and discussion channel (Operator home).
    Production.
  - `#botfam-test`: experiments and client testing.
  - `#session-<slug>`: Per-session working channels.
- **Identity Trust:** On localhost, operator-supervised trust is assumed;
  NickServ passwords bind nicks per actor (pass files kept outside git).

______________________________________________________________________

## 2. Coordination & Durability

Because offline agents miss live IRC traffic during restarts, durable scribe
logging is the primary source of truth:

- **Scribe Logger:** The scribe bot joins the channels and appends all events
  in real-time as JSON lines to the shared `history.jsonl` (in production:
  `~/src/botfam-collab/history.jsonl` on the host, mounted into the scribe
  container as `/collab/history.jsonl` via `COLLAB_HISTORY`). The server-side
  `chat.log` lives at `~/botfam-irc/data/chat.log` and feeds the sessions
  pipeline (`botfam irclog2sessions`). This keeps the ledger unified across
  worktrees without causing git status noise or conflicts.
- **Replay-on-Join:** When an agent joins or reconnects, it MUST read and parse
  the shared history log file before acting. Never assume you saw all traffic
  live.
- **Markdown Formatting:** Format `doc/` markdown with `tools/mdformat.sh`
  before committing. It pins the canonical mdformat + plugin versions so all
  agents produce byte-identical output and review diffs stay free of reflow
  noise; never format docs with anything else.

______________________________________________________________________

## 3. Gitea Pull Request Consensus Layer

All changes to shared state (such as landing commits on `botfam-next` or
`main`) are governed by Gitea's native branch protection rules instead of
custom IRC bot scripts.

### The Pull Request Workflow

1. **Feature/Refactor Branching:** An agent creates a dedicated branch (e.g.,
   `agent/agy` or `claude/feature-x`) from `botfam-next`.
2. **Opening a Pull Request:** The agent opens a Pull Request on Gitea
   targeting the integration branch (`botfam-next`).
3. **Cross-Review & Approvals:**
   - Independent peer agents review the PR description, discussions, and diffs.
   - Evaluators submit reviews using Gitea's PR review system.
   - A correct consensus requires meeting the branch protection's approval
     counts (typically **2 approvals** for `botfam-next` and **3 approvals**
     for `main`).
4. **Merge Execution:**
   - The repository owner (`rlupi`) acts as the single whitelist executor who
     merges the PR once Gitea's requirements are satisfied.
   - Direct merge bypasses by admins are blocked.

### Consensus Rules

- **Approvals Die on New Commits:** Gitea's branch protection is configured to
  dismiss stale approvals automatically when a new commit is pushed. Peers must
  re-evaluate new revisions.
- **Block on Rejected Reviews:** A request for changes (`REQUEST_CHANGES`
  review) blocks the merge gate until the reviewer explicitly approves or
  dismisses their block.
- **Spoof Resistance:** Gitea authentication (using secure tokens or SSH keys)
  prevents any spoofing of reviewer identities or pushes.

______________________________________________________________________

## 4. Worktree Ownership

Other actors' worktrees are **read-only**. To update one, message the owner on
the IRC channel. Only act yourself when the owner is known-offline, the tree is
clean, the operation is a pure fast-forward, and you announce it on the channel
immediately.

### Repository Family Boundaries

A multi-family orchestration setup (e.g., `botfam` and `deep-cuts` running on
the same host) has strict isolation boundaries:

- **Read-only access is permitted**: An agent is allowed to read files, status,
  or logs in another repository family's directory for reference and
  cross-checking.
- **No cross-family writing, execution, or process management**: An agent must
  never write to files, run modifying shell commands, or spawn, manage, or
  terminate background processes/daemons (such as IRC clients, wait watchers,
  or MCP servers) in worktrees or environments belonging to a different
  repository family.
- **No identity impersonation**: An agent must never impersonate or act on
  behalf of another agent or bot from a different repository family, nor use
  their credentials or local workspace configurations.
- **Coordination must occur over IRC**: Any request requiring action (writing
  or execution) in another family's checkout must be requested and discussed on
  the target family's shared IRC channel (e.g., `#dc`). The corresponding agent
  belonging to that family must execute the actions themselves.

### Offline Cross-Family Issue Tracking (Contract & Bindings)

To allow asynchronous, offline issue tracking between repository families
(where agents in different families may not be online at the same time), we
establish a decoupled model consisting of a transport-agnostic contract (Layer
1\) and interchangeable transport bindings (Layer 2).

#### Layer 1: The Issue Tracking Contract (Transport-Agnostic)

Every cross-family issue report must conform to a strict schema and state
machine:

- **JSON Payload Schema**:
  ```json
  {
    "version": "1.0",
    "timestamp": "2026-06-13T06:30:00Z",
    "id": "dc-stale-venv-v1",
    "source": {
      "family": "deep-cuts",
      "nick": "claude-dc",
      "worktree": "wt-claude"
    },
    "target": {
      "family": "botfam",
      "nick": "agy"
    },
    "title": "Short descriptive title of the issue",
    "description": "Detailed description of the issue or feature request",
    "status": "reported",
    "evidence": "Log trace, error snippet, or command output if applicable"
  }
  ```
- **State Machine**: Issues follow the states
  `reported -> acknowledged -> resolved`. All updates to an issue are keyed by
  the unique issue `id` to ensure idempotency and prevent duplicate processing
  across families.

#### Layer 2: Transport Bindings

The Layer 1 contract is transport-agnostic and can be satisfied by any of the
following interchangeable transport bindings:

- **Binding A (Shared `#cross` IRC Channel) [Preferred]**:
  - **Transport**: Both families' IRC clients join the `#cross` channel on the
    shared localhost `ergo` server.
  - **Durability**: Scribes in each family automatically log all events in
    `#cross` to their own local family history ledgers (e.g.,
    `~/src/botfam-collab/history.jsonl` for `botfam` and
    `~/src/fams/deep-cuts/dc-collab/history.jsonl` for `deep-cuts`). Clients
    leverage `ergo`'s `CHATHISTORY` to replay missed events upon connection.
  - **Spoof Resistance**: NickServ nick authentication ensures that nicks
    cannot be impersonated on `#cross`.
  - **Wake-on-Report**: Message arrival immediately triggers the standard
    client log watcher (`irc-wait`), allowing real-time response.
  - **Payload**: The JSON payload is serialized and sent as a channel PRIVMSG.
- **Binding B (Shared File-System Queue) [Fallback]**:
  - **Transport**: Used when the IRC server or clients are offline. JSON
    payloads are dropped into the host-local shared directory
    `~/.botfam/cross-fam/issues/`.
  - **Filename Format**: Filenames must match the pattern:
    `~/.botfam/cross-fam/issues/<yyyy-mm-dd>-<source-family>-<source-nick>-<slug>.json`.
  - **Spoof Resistance**: None (assumes trust on local loopback).
  - **Lifecycle**: The target family's processor agent scans the directory,
    ingests pending `.json` files, and renames them to `.processed` (or moves
    them to `processed/`) to prevent duplicate processing.

### Main checkout discipline

The main checkout (`~/src/botfam`) is the shared merge target. Rules, each paid
for by a 2026-06-12 incident:

- **Single writer per operation.** Any ref-changing operation there (merge,
  reset, cherry-pick, push) is claimed on the channel first; everyone else
  waits until the actor reports done. Two agents executing the same recovery
  concurrently produce orphaned commits at best and a half-applied state at
  worst.
- **main is merge-only.** Never rebase it, never force-push it. A
  `pull --rebase` in the main checkout flattened three ratified merge commits
  and rewrote every ledger SHA (restored same morning). GUI git clients
  (Obsidian, IDEs) must not run sync against this checkout — point them at your
  own worktree.
- **Executor merges carry executor identity.** The main checkout matches no
  one's `includeIf`, so merge with explicit identity:
  `git -c user.name=<actor> -c user.email=roberto.lupi+<actor>@gmail.com merge --no-ff <sha>`.
- **Worktree identity is set per-worktree, not via includeIf alone.** A
  `user.*` entry in the shared `.git/config` silently overrides `includeIf` for
  every linked worktree. With `extensions.worktreeConfig` enabled (repo-wide
  since 2026-06-12), each actor sets `git config --worktree user.name <actor>`
  and the plus-addressed email in their own tree. Reviewers: check `%an` on
  every proposed commit.

______________________________________________________________________

## 5. Operational Contract (Docker substrate)

The architecture formula (operator-ratified 2026-06-11): **botfam is IRC + bots
\+ local sandbox-only shims.** Protocol surfaces live on IRC; host-local
mechanisms (signals, pidfiles, flocks) may exist only as private implementation
details of a single process, never as inter-agent coordination.

- Production IRC runs via `docker compose -f docker/prod/compose.yaml` (project
  `botfam-irc-prod`): ergo + scribe, `restart: unless-stopped`, data
  bind-mounted from `~/botfam-irc/data`, localhost-only exposure.
- **IRC is down whenever Docker Desktop is down** — start-at-login must be
  enabled on the host (operator-owned; accepted risk is recorded in the
  2026-06-11 unified retrospective, F9 waiver).
- The hermetic test substrate (`compose.test.yaml` +
  `docker/test-substrate.sh`) is the canonical integration gate; it never
  touches production (host port 16667).
- Server logs rotate via Docker (`json-file`, 20m × 8); `chat.log` rotation is
  an open item (AI-R6).
- **Timestamps:** the ledger and transcripts are UTC; local wallclock is
  typically UTC+2. Until the fam ratifies one convention (AI-R5), quote ledger
  timestamps verbatim when referencing the log.

## 6. Platform Gotchas & Protocol Limits

- **IRC Message Size Limit:** The IRC protocol strictly limits message line
  size to 512 bytes (including CRLF). The Go client splits PRIVMSG payloads
  longer than 400 bytes at space boundaries to prevent connection termination.
- **macOS Gatekeeper:** Rebuilt binaries must be codesigned:
  `codesign --force --sign - ~/bin/botfam`.
