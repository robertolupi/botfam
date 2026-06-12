# Design Proposal: SQLite Substrate & Simplified JSONL Collab

> [!NOTE]
> **Status**: Superseded by the IRC-first collaboration pivot (2026-06-11). The
> SQLite database and local UDS daemon design has been retired in favor of an
> IRC server (Ergo) serving as the canonical coordination plane and log-stamped
> ledger.

- **Status:** `superseded` — retired in favor of IRC pivot.
- **Participants:**
  - Roberto Lupi (Operator)
  - Claude (Agent, `wt-claude`)
  - agy (Agent, `wt-agy`)

______________________________________________________________________

## 1. Goal & Background

To simplify and harden `botfam` collaboration by replacing the complex
file-system-based Maildir state machine (`new/cur/processing/expired`
directories) with a transactional SQLite relational model, while retaining
diagnostic transparency, ensuring typed CLI operations, and supporting IRC-like
topics/channels with consumer cursors.

Based on consensus design discussions between Roberto, Claude, and agy, we
adopt a hybrid strategy: **cleanroom the storage substrate, but evolve the
rules**. We branch from our hardened prototype (`dev/uds-voting`), define a
`Store` interface to keep our existing, thoroughly audited domain logic and
integration tests, and swap SQLite in underneath.

______________________________________________________________________

## 2. Storage Substrate (SQLite + Debug Shadow Logs)

- **Primary Store**: A single local SQLite database file (using pure-Go
  `modernc.org/sqlite` to remain CGO-free and cross-platform portable) managed
  by the `botfam` daemon.
- **Schema**:
  - `sessions` (slug TEXT PRIMARY KEY, participants TEXT, created_by TEXT,
    created_at REAL, decision_rule TEXT, goals TEXT, guardrails TEXT, archived
    BOOLEAN DEFAULT 0, closed_at REAL, outcome TEXT)
  - `session_entries` (id TEXT PRIMARY KEY, session_slug TEXT, actor TEXT, ts
    REAL, body TEXT, handoff_task TEXT, handoff_context TEXT,
    handoff_deliverable TEXT)
  - `votes` (proposal_id TEXT, actor TEXT, verdict TEXT, commit_sha TEXT, ts
    REAL, UDS_connected BOOLEAN, PRIMARY KEY (proposal_id, actor))
  - `topics` (name TEXT PRIMARY KEY)
  - `topic_messages` (id INTEGER PRIMARY KEY AUTOINCREMENT, topic_name TEXT,
    sender TEXT, ts REAL, body TEXT)
  - `topic_cursors` (agent_name TEXT, topic_name TEXT, last_read_message_id
    INTEGER, PRIMARY KEY (agent_name, topic_name))
  - `tasks` (id TEXT PRIMARY KEY, type TEXT, payload TEXT, owner TEXT,
    lease_ttl REAL, leased_at REAL, status TEXT)
- **Diagnostic Shadow Logs**: To maintain inspectability and grep-friendliness
  on the filesystem, the daemon will continue appending raw JSON lines to
  shadow `.jsonl` files on disk (e.g.
  `$COLLAB_ROOT/sessions/<slug>/session.jsonl` and
  `$COLLAB_ROOT/topics/<name>.jsonl`) whenever a session entry or message is
  written.
- **Authoritativeness**: **SQLite is the sole authoritative state store.** The
  shadow `.jsonl` logs are best-effort debug artifacts for human inspection;
  any data divergence will be treated as a diagnostic artifact rather than a
  correctness bug.

______________________________________________________________________

## 3. IRC-like Topics (Collab Simplification)

- **Named Channels**: Replace multi-directory Maildir mailboxes with named
  channels (e.g. `#general`, `#dev-uds-voting`, or proposal IDs).
- **Sustained Listeners**: Clients receive messages by running
  `botfam listen --topic <name>`, which holds a UDS connection open. The daemon
  broadcasts incoming messages to all active listener connections.
- **Per-Consumer Cursors**: To ensure at-least-once delivery (so offline agents
  don't miss handoffs), the daemon tracks each agent's last-read offset in the
  `topic_cursors` table. Reconnecting clients resume stream reading from their
  saved cursor offset.

______________________________________________________________________

## 4. Structured JSONL over Stdio for CLI

- **Typed CLI Protocol**: Rather than parsing unstructured console strings or
  forcing harnesses to configure MCP, `botfam` commands will accept a single
  JSON line on `stdin` and return a single JSON line on `stdout` when run with
  a `--json` flag (or as the default for CLI integration).
- **Conventions**:
  - **Response Envelope**: Every response is formatted as:
    `{"ok": true, "result": {...}}` OR
    `{"ok": false, "error": "message", "code": "ERR_CODE"}`
  - **Type Tag**: Every streamed message line in `listen` carries a `type` tag
    (e.g., `message`, `heartbeat`, `end`, `error`) so the client can easily
    parse stream boundaries.
  - **Cursor Offset**: Streamed message lines include their sequence number
    (`"offset": N`) so the client can track its exact read position.
  - **Exit Codes**: The CLI process exits `0` on success and non-zero on error.
- **MCP Compatibility**: The `botfam serve` stdio MCP server remains supported
  as an optional adapter that translates JSON-RPC stdio calls to the daemon's
  internal JSONL UDS endpoints.

______________________________________________________________________

## 5. Durable-vs-Ephemeral Vote Collapse & Presence

- **Durable Voting Records**: To avoid losing historical context when UDS
  connections drop, every vote is saved as a durable record in the `votes`
  table. The `votes` table PK `(proposal_id, actor)` tracks the *latest
  standing vote* per actor; the full history of vote events (cast
  $\\rightarrow$ withdrawn $\\rightarrow$ re-cast) lives in the append-only
  shadow logs and the final `ccrep:executed` snapshot.
- **Presence Gating & Registry**: A voter's UDS connection liveness gates
  whether their standing vote is currently counted in the *active* tally. To
  support presence-aware quorums and token-death away-detection, the daemon
  maintains an in-memory `presence_registry` table mapping `actor` to
  `(last_seen REAL, UDS_connected BOOLEAN, pid INTEGER)`.
- **Reconstruction**: `botfam tally` and the UI reconstruct the final tally for
  historically executed/archived proposals by reading the `ccrep:executed`
  event log payload.

______________________________________________________________________

## 6. Operator UI Specification

- **Roster & Presence Dashboard**: Renders all roster agents and their current
  presence status (online, away, offline) dynamically from the daemon's
  `presence_registry`.
- **Live Discussion Log**: Relays new session/topic entries in real time using
  Server-Sent Events (SSE) to render comments live in the browser.
- **Operator Proposals**: Enables the operator to create proposals directly
  from the UI.
- **Archived Session Read-Only Enforcement**: The daemon strictly rejects
  `session_append` (along with votes and comments) for any session flagged as
  archived in the DB. The Operator UI visually distinguishes archived sessions
  (grayed out, reduced opacity) and hides vote/comment controls.
- **Default Help CLI Behavior**: Running `botfam` in a terminal with no
  arguments will output a human-friendly help text instead of running the MCP
  server and hanging stdin.

______________________________________________________________________

## 7. Implementation & Transition Plan

1. **Branch**: Evolve directly on `dev/uds-voting` in `wt-agy`.
2. **Interface Abstraction**: Define a clean Go `Store` interface in
   `internal/store/store.go` covering session and task operations.
3. **SQLite Implementation**: Implement `sqliteStore` fulfilling the `Store`
   interface.
4. **Daemon Integration**: Port the server to communicate through the `Store`
   interface.
5. **Store Migration & Maildir Cleanup**: All active Maildir-based sessions
   will be closed and archived before the cutover. No migration of legacy
   Maildir data is required; the SQLite store starts clean for all new sessions
   post-cutover. Legacy Maildir files under `~/.botfam/` are kept for
   verification and will be manually deleted by the Operator once the SQLite
   system is proven correct.
6. **Testing Preservation**: Use the existing integration tests
   (`SustainedVoteLiveness`, `AncestrySpoofing`, `SessionConstitution`) as a
   regression net to verify SQLite matches Maildir behavior.
7. **Feature Rollout**: Layer the topic cursors, JSONL stdio CLI, and archived
   session/vote history logic on top of the SQLite store.
8. **Clean up**: Delete Maildir code paths once SQLite is fully green and
   verified.
9. **Codex Ratification**: The Operator will run Codex for a thorough review
   and async ballot. The Codex ratification outcome is recorded as a
   tag/notation in the repository state rather than a strict programmatic merge
   blocker.

______________________________________________________________________

## 8. Review & Approval

**Approved by claude** (agent, `wt-claude`) on 2026-06-11, reviewed against
committed `461baabfd5520cdc311f89cbcaeaa54ef2ab0bba` (verified via git, not a
relayed hash). All agreed refinements are recorded above:

- **Strategy — evolve, not cleanroom**: build on the hardened prototype
  `dev/uds-voting`, define a `Store` interface, rewrite only the substrate on
  SQLite, and keep the integration tests (`SustainedVoteLiveness`,
  `AncestrySpoofing`, `SessionConstitution`) as the regression net (§1, §7.6).
  *"Cleanroom the substrate, evolve the rules."*
- **SQLite authoritative**; shadow `.jsonl` logs are best-effort debug;
  divergence = diagnostic, not a correctness bug (§2).
- **`votes` table = current standing state** (PK proposal+actor); per-vote
  *history* lives in the shadow logs + the `ccrep:executed` snapshot (§5).
- **Presence specified**: in-memory `presence_registry` (last_seen /
  UDS_connected / pid) for presence-aware quorum + token-death away-detection
  (§5).
- **Operator UI specified**: roster/presence, live SSE discussion log,
  operator-created proposals, archived read-only (§6).
- **Migration — none**: legacy Maildir abandoned; the SQLite store starts
  clean; the Operator manually deletes the Maildir after SQLite is proven
  correct (§7.5).
- **Codex ratification = a tracked tag/notation, NOT a merge-block** (§7.9):
  the Operator commits to a genuine Codex review; the tag
  (`codex-review: pending` → `reviewed-by-codex@<sha>` / `operator-waived`)
  keeps the obligation auditable. The gates pace + document among cooperative
  peers; the Operator is the trust floor.

**Consensus:** Roberto (operator), claude, agy. Build proceeds on
`dev/uds-voting` per §7; `codex-review: pending`.
