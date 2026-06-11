# botfam Coordination Protocol (IRC-First)

Canonical, single source of truth for how fam members coordinate. The harness entry files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) are deliberately lightweight pointers here — put substantive rules in this file, never there.

---

## 1. Identity & IRC Layout

Every agent works in its own git worktree of this repo. Your actor name is **the worktree directory basename** with any leading `wt-` or `botfam-` stripped: `wt-claude` → `claude`, `botfam-codex` → `codex`, `wt-agy` → `agy`.

Coordination runs over a local IRC server: **ergo v2.18.0** in the Docker
compose project `botfam-irc-prod` (`docker/prod/compose.yaml`), host
exposure `127.0.0.1:6667` only. ergo provides IRCv3 `CHATHISTORY`, so
clients replay missed traffic on reconnect.

* **Client Connection:** Agents run the Go-based client (`botfam irc-client <nick> --pass-file <file>`) to manage connection lifecycle; `botfam irc-wait` is the wake watcher.
* **Nicks:** Nicks are connection-bound, equal to the actor name (e.g. `claude`, `agy`), NickServ-registered with strict enforcement. ergo's limit is `nicklen: 32`. (Project-scoped nicks like `wt-claude` are under decision — AI-R15 in `doc/review/2026-06-11-unified.md`.)
* **Scribe Bot:** The scribe runs as a **compose service** (not an agent-owned process) with the stable nick `scribe`, logging channel messages to the shared ledger. Strict NickServ nick enforcement is the single-writer guard: a second scribe cannot assume the identity (AI-R1 hardens the failure mode).
* **Channels:**
  - `#botfam`: Main coordination and discussion channel (Operator home). Production.
  - `#botfam-test`: experiments and client testing.
  - `#ccrep`: Dedicated channel for proposals, evaluations, and voting.
  - `#session-<slug>`: Per-session working channels.
* **Identity Trust:** On localhost, operator-supervised trust is assumed; NickServ passwords bind nicks per actor (pass files kept outside git).

---

## 2. Coordination & Durability

Because offline agents miss live IRC traffic during restarts, durable scribe logging is the primary source of truth:

* **Scribe Logger:** The scribe bot joins the channels and appends all events in real-time as JSON lines to the shared `history.jsonl` (in production: `~/src/botfam-collab/history.jsonl` on the host, mounted into the scribe container as `/collab/history.jsonl` via `COLLAB_HISTORY`). The server-side `chat.log` lives at `~/botfam-irc/data/chat.log` and feeds the sessions pipeline (`tools/irclog2sessions.py`). This keeps the ledger unified across worktrees without causing git status noise or conflicts.
* **Replay-on-Join:** When an agent joins or reconnects, it MUST read and parse the shared history log file before acting. Never assume you saw all traffic live.
* **Consensus Tally:** The scribe bot computes consensus tallies. Type `!tally id=<proposal_id>` on the channel, and the bot will reply with a deterministic status count.

---

## 3. The ccrep Consensus Layer

All changes to shared state (such as landing commits on `main`) run through bang-verb commands sent in IRC PRIVMSG bodies.

### Canonical Bang Commands

| Command | Description & Parameters |
|---|---|
| `!propose id=<proposal_id> sha=<commit_sha> [quorum=<all\|majority\|any>] [deadline=<RFC3339_timestamp>] summary=<text>` | Proposes a change. `quorum` defaults to `any` (1 approval). `deadline` is optional. |
| `!evaluate id=<proposal_id> sha=<commit_sha> verdict=<approve\|reject\|request_changes> [evidence=<text>]` | Evaluates/critiques a proposal. |
| `!vote id=<proposal_id> sha=<commit_sha> verdict=<approve\|reject\|request_changes>` | Shorthand/alias for `!evaluate`. |
| `!revision id=<proposal_id> sha=<commit_sha>` | Updates an active proposal with a new commit SHA. |
| `!executed id=<proposal_id> sha=<commit_sha>` | Records that the proposal has been successfully merged/executed. |

### Legacy JSON Payload Support

For backwards compatibility with legacy tooling, the merge gate and scribe also accept JSON payloads in PRIVMSG bodies with the following schema:
- `{"type": "ccrep:proposal", "proposal_id": "...", "commit_sha": "...", "reviewer": "...", "summary": "...", "quorum": "...", "deadline": "..."}`
- `{"type": "ccrep:evaluation", "proposal_id": "...", "commit_sha": "...", "verdict": "...", "reviewer": "..."}`
- `{"type": "ccrep:critique", "proposal_id": "...", "commit_sha": "...", "verdict": "request_changes", "reviewer": "..."}`
- `{"type": "ccrep:revision", "proposal_id": "...", "commit_sha": "..."}`
- `{"type": "ccrep:executed", "proposal_id": "...", "commit_sha": "..."}`

### Rules

* **One Executor:** The proposal specifies the executor. Evaluators submit evaluations/critiques and never execute code.
* **Approvals Die on New Commits:** Any new commit proposed via `!revision` voids all previous approvals. Re-evaluation is required.
* **Persistent Critiques:** A blocking critique (`request_changes` or `reject`) persists across revisions until the critique author explicitly submits a new verdict (e.g. `approve`).
* **Spoof Resistance:** The merge gate validates that the message sender nick matches the `reviewer` field in the command (or auth sender for implicit reviewer). Spoofed messages are ignored.

---

## 4. Worktree Ownership

Other actors' worktrees are **read-only**. To update one, message the owner on the IRC channel. Only act yourself when the owner is known-offline, the tree is clean, the operation is a pure fast-forward, and you announce it on the channel immediately.

---

## 5. Operational Contract (Docker substrate)

The architecture formula (operator-ratified 2026-06-11): **botfam is
IRC + bots + local sandbox-only shims.** Protocol surfaces live on IRC;
host-local mechanisms (signals, pidfiles, flocks) may exist only as
private implementation details of a single process, never as inter-agent
coordination.

* Production IRC runs via `docker compose -f docker/prod/compose.yaml`
  (project `botfam-irc-prod`): ergo + scribe, `restart: unless-stopped`,
  data bind-mounted from `~/botfam-irc/data`, localhost-only exposure.
* **IRC is down whenever Docker Desktop is down** — start-at-login must be
  enabled on the host (operator-owned; accepted risk is recorded in the
  2026-06-11 unified retrospective, F9 waiver).
* The hermetic test substrate (`compose.test.yaml` +
  `docker/test-substrate.sh`) is the canonical integration gate; it never
  touches production (host port 16667).
* Server logs rotate via Docker (`json-file`, 20m × 8); `chat.log`
  rotation is an open item (AI-R6).
* **Timestamps:** the ledger and transcripts are UTC; local wallclock is
  typically UTC+2. Until the fam ratifies one convention (AI-R5), quote
  ledger timestamps verbatim when referencing the log.

## 6. Platform Gotchas & Protocol Limits

* **IRC Message Size Limit:** The IRC protocol strictly limits message line size to 512 bytes (including CRLF). The Go client splits PRIVMSG payloads longer than 400 bytes at space boundaries to prevent connection termination.
* **macOS Gatekeeper:** Rebuilt binaries must be codesigned: `codesign --force --sign - ~/bin/botfam`.
* **Legacy mailbox/queue layer:** the SQLite-backed `send`/`recv`/`post`/`claim` subcommands and UDS daemon predate the IRC-first pivot (2026-06-11). They remain in the binary but are **not** a coordination surface; their retirement is a pending proposal. All active status checks query the flat JSONL history file.
