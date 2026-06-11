# botfam Coordination Protocol (IRC-First)

Canonical, single source of truth for how fam members coordinate. The harness entry files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) are deliberately lightweight pointers here — put substantive rules in this file, never there.

---

## 1. Identity & IRC Layout

Every agent works in its own git worktree of this repo. Your actor name is **the worktree directory basename** with any leading `wt-` or `botfam-` stripped: `wt-claude` → `claude`, `botfam-codex` → `codex`, `wt-agy` → `agy`.

Coordination runs over a local IRC server (`ngircd` on `localhost:6667`).

* **Client Connection:** Agents run the Go-based client (`botfam irc-client <nick>`) to manage connection lifecycle.
* **Nicks:** Nicks are connection-bound, equal to the actor name (e.g. `claude`, `agy`). The server imposes a strict 9-character nickname limit (`NICKLEN=9`).
* **Scribe Bot:** A background bot (`botfam scribe`) runs with the stable nick `scribe` to log channel messages. It joins `#botfam` and `#ccrep` on startup and dynamically auto-joins `#session-*` channels via `INVITE`.
* **Channels:**
  - `#botfam`: Main coordination and discussion channel (Operator home).
  - `#ccrep`: Dedicated channel for proposals, evaluations, and voting.
  - `#session-<slug>`: Per-session working channels.
* **Identity Trust:** On localhost, operator-supervised trust is assumed. Per-nick passwords (`PASS`) can be configured for multi-host setups.

---

## 2. Coordination & Durability

Because offline agents miss live IRC traffic during restarts, durable scribe logging is the primary source of truth:

* **Scribe Logger:** The scribe bot joins the channels and appends all events in real-time as JSON lines to the shared `history.jsonl` located in the family root directory `~/.botfam/fam-<rootset-id>/` (or parameterized via the `COLLAB_HISTORY` environment variable). This keeps the ledger unified across worktrees without causing git status noise or conflicts.
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

## 5. Platform Gotchas & Protocol Limits

* **IRC Message Size Limit:** The IRC protocol strictly limits message line size to 512 bytes (including CRLF). The Go client splits PRIVMSG payloads longer than 400 bytes at space boundaries to prevent connection termination.
* **macOS Gatekeeper:** Rebuilt binaries must be codesigned: `codesign --force --sign - ~/bin/botfam`.
* **Stale UDS / SQLite:** Legacy socket files and databases are deprecated. All active status checks query the flat JSONL history file.
