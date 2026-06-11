# botfam Coordination Protocol (IRC-First)

Canonical, single source of truth for how fam members coordinate. The harness entry files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) are deliberately lightweight pointers here — put substantive rules in this file, never there.

---

## 1. Identity & IRC Layout

Every agent works in its own git worktree of this repo. Your actor name is **the worktree directory basename** with any leading `wt-` or `botfam-` stripped: `wt-claude` → `claude`, `botfam-codex` → `codex`, `wt-agy` → `agy`.

Coordination runs over a local IRC server (`ngircd` on `localhost:6667`).

* **Client Connection:** Agents run the Go-based client (`botfam irc-client <nick>`) to manage connection lifecycle.
* **Nicks:** Nicks are connection-bound, equal to the actor name (e.g. `claude`, `agy`). The server imposes a strict 9-character nickname limit (`NICKLEN=9`).
* **Scribe Bot:** A background bot (`botfam scribe`) runs with nick `sc-<suffix>` to log channel messages.
* **Channels:**
  - `#botfam`: Main coordination and discussion channel (Operator home).
  - `#ccrep`: Dedicated channel for proposals, evaluations, and voting.
  - `#session-<slug>`: Per-session working channels.
* **Identity Trust:** On localhost, operator-supervised trust is assumed. Per-nick passwords (`PASS`) can be configured for multi-host setups.

---

## 2. Coordination & Durability

Because offline agents miss live IRC traffic during restarts, durable scribe logging is the primary source of truth:

* **Scribe Logger:** The scribe bot joins the channels and appends all events in real-time as JSON lines to the shared `doc/collab/history.jsonl` (or parameterized via the `COLLAB_HISTORY` environment variable).
* **Replay-on-Join:** When an agent joins or reconnects, it MUST read and parse the shared history log file before acting. Never assume you saw all traffic live.
* **Consensus Tally:** The scribe bot computes consensus tallies. Type `!tally id=<proposal_id>` on the channel, and the bot will reply with a deterministic status count.

---

## 3. The ccrep Consensus Layer

All changes to shared state (such as landing commits on `main`) run through `ccrep:*` messages sent as JSON payloads in IRC PRIVMSG bodies.

### Message Schema

A ccrep event payload must be a single JSON object sent as the body of an IRC PRIVMSG:

| `type` | Description & Required Fields |
|---|---|
| `ccrep:proposal` | Proposes a change. Fields: `proposal_id`, `commit_sha`, `reviewer` (author), `summary`, `quorum` (`all`\|`majority`), `deadline` |
| `ccrep:critique` | Blocks a proposal. Fields: `proposal_id`, `commit_sha`, `verdict: request_changes`, `evidence` |
| `ccrep:evaluation`| Evaluates/approves. Fields: `proposal_id`, `commit_sha`, `verdict` (`approve`\|`reject`), `reviewer` |
| `ccrep:revision`   | Updates a proposal with a new commit. Fields: `proposal_id`, `commit_sha` |
| `ccrep:executed`   | Records execution. Fields: `proposal_id`, `commit_sha` |

### Rules

* **One Executor:** The proposal specifies the executor. Evaluators submit evaluations/critiques and never execute code.
* **Approvals Die on New Commits:** Any new commit proposed via `ccrep:revision` voids all previous approvals. Re-evaluation is required.
* **Persistent Critiques:** A blocking critique (`request_changes` or `reject`) persists across revisions until the critique author explicitly submits a new verdict (e.g. `approve`).
* **Spoof Resistance:** The merge gate validates that the message sender nick matches the `reviewer` field in the JSON payload. Spoofed messages are ignored.

---

## 4. Worktree Ownership

Other actors' worktrees are **read-only**. To update one, message the owner on the IRC channel. Only act yourself when the owner is known-offline, the tree is clean, the operation is a pure fast-forward, and you announce it on the channel immediately.

---

## 5. Platform Gotchas & Protocol Limits

* **IRC Message Size Limit:** The IRC protocol strictly limits message line size to 512 bytes (including CRLF). The Go client splits PRIVMSG payloads longer than 400 bytes at space boundaries to prevent connection termination.
* **macOS Gatekeeper:** Rebuilt binaries must be codesigned: `codesign --force --sign - ~/bin/botfam`.
* **Stale UDS / SQLite:** Legacy socket files and databases are deprecated. All active status checks query the flat JSONL history file.
