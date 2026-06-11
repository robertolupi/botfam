# Design Proposal: SQLite Substrate & Simplified JSONL Collab

* **Status:** `open question` (pending final review)
* **Participants:**
  * Roberto Lupi (Operator)
  * Claude (Agent, `wt-claude`)
  * agy (Agent, `wt-agy`)

---

## 1. Goal & Background

To simplify and harden `botfam` collaboration by replacing the complex file-system-based Maildir state machine (`new/cur/processing/expired` directories) with a transactional SQLite relational model, while retaining diagnostic transparency, ensuring typed CLI operations, and supporting IRC-like topics/channels with consumer cursors.

Based on consensus design discussions between Roberto, Claude, and agy, we adopt a hybrid strategy: **cleanroom the storage substrate, but evolve the rules**. We branch from our hardened prototype (`dev/uds-voting`), define a `Store` interface to keep our existing, thoroughly audited domain logic and integration tests, and swap SQLite in underneath.

---

## 2. Storage Substrate (SQLite + Debug Shadow Logs)

* **Primary Store**: A single local SQLite database file (using pure-Go `modernc.org/sqlite` to remain CGO-free and cross-platform portable) managed by the `botfam` daemon.
* **Schema**:
  * `sessions` (slug TEXT PRIMARY KEY, participants TEXT, created_by TEXT, created_at REAL, decision_rule TEXT, goals TEXT, guardrails TEXT, archived BOOLEAN DEFAULT 0)
  * `session_entries` (id TEXT PRIMARY KEY, session_slug TEXT, actor TEXT, ts REAL, body TEXT, handoff_task TEXT, handoff_context TEXT, handoff_deliverable TEXT)
  * `votes` (proposal_id TEXT, actor TEXT, verdict TEXT, commit_sha TEXT, ts REAL, UDS_connected BOOLEAN, PRIMARY KEY (proposal_id, actor))
  * `topics` (name TEXT PRIMARY KEY)
  * `topic_messages` (id INTEGER PRIMARY KEY AUTOINCREMENT, topic_name TEXT, sender TEXT, ts REAL, body TEXT)
  * `topic_cursors` (agent_name TEXT, topic_name TEXT, last_read_message_id INTEGER, PRIMARY KEY (agent_name, topic_name))
  * `tasks` (id TEXT PRIMARY KEY, type TEXT, payload TEXT, owner TEXT, lease_ttl REAL, leased_at REAL, status TEXT)
* **Diagnostic Shadow Logs**: To maintain inspectability and grep-friendliness on the filesystem, the daemon will continue appending raw JSON lines to shadow `.jsonl` files on disk (e.g. `$COLLAB_ROOT/sessions/<slug>/session.jsonl` and `$COLLAB_ROOT/topics/<name>.jsonl`) whenever a session entry or message is written.

---

## 3. IRC-like Topics (Collab Simplification)

* **Named Channels**: Replace multi-directory Maildir mailboxes with named channels (e.g. `#general`, `#dev-uds-voting`, or proposal IDs).
* **Sustained Listeners**: Clients receive messages by running `botfam listen --topic <name>`, which holds a UDS connection open. The daemon broadcasts incoming messages to all active listener connections.
* **Per-Consumer Cursors**: To ensure at-least-once delivery (so offline agents don't miss handoffs), the daemon tracks each agent's last-read offset in the `topic_cursors` table. Reconnecting clients resume stream reading from their saved cursor offset.

---

## 4. Structured JSONL over Stdio for CLI

* **Typed CLI Protocol**: Rather than parsing unstructured console strings or forcing harnesses to configure MCP, `botfam` commands will accept a single JSON line on `stdin` and return a single JSON line on `stdout` when run with a `--json` flag (or as the default for CLI integration).
* **Conventions**:
  * **Response Envelope**: Every response is formatted as:
    `{"ok": true, "result": {...}}` OR `{"ok": false, "error": "message", "code": "ERR_CODE"}`
  * **Type Tag**: Every streamed message line in `listen` carries a `type` tag (e.g., `message`, `heartbeat`, `end`, `error`) so the client can easily parse stream boundaries.
  * **Cursor Offset**: Streamed message lines include their sequence number (`"offset": N`) so the client can track its exact read position.
  * **Exit Codes**: The CLI process exits `0` on success and non-zero on error.
* **MCP Compatibility**: The `botfam serve` stdio MCP server remains supported as an optional adapter that translates JSON-RPC stdio calls to the daemon's internal JSONL UDS endpoints.

---

## 5. Durable-vs-Ephemeral Vote Collapse

* **Durable Voting Records**: To avoid losing historical context when UDS connections drop, every vote is saved as a durable record in the `votes` table.
* **Presence Gating**: A voter's UDS connection liveness gates whether their standing vote is currently counted in the *active* tally. If the connection is active, the vote is counted; if it drops, the vote stops counting but remains recorded in history.
* **Reconstruction**: `botfam tally` and the UI reconstruct the final tally for historically executed/archived proposals by reading the `ccrep:executed` event log payload.

---

## 6. Security & CLI UX Guardrails

* **Archived Session Read-Only Enforcement**: The daemon strictly rejects `session_append` (along with votes and comments) for any session containing the `ARCHIVED` file tombstone or flagged as archived in the DB.
* **Default Help CLI Behavior**: Running `botfam` in a terminal with no arguments will output a human-friendly help text instead of running the MCP server and hanging stdin.

---

## 7. Implementation & Transition Plan

1. **Branch**: Evolve directly on `dev/uds-voting` in `wt-agy`.
2. **Interface Abstraction**: Define a clean Go `Store` interface in `internal/store/store.go` covering session and task operations.
3. **SQLite Implementation**: Implement `sqliteStore` fulfilling the `Store` interface.
4. **Daemon Integration**: Port the server to communicate through the `Store` interface.
5. **Testing Preservation**: Use the existing integration tests (`SustainedVoteLiveness`, `AncestrySpoofing`, `SessionConstitution`) as a regression net to verify SQLite matches Maildir behavior.
6. **Feature Rollout**: Layer the topic cursors, JSONL stdio CLI, and archived session/vote history logic on top of the SQLite store.
7. **Clean up**: Delete Maildir storage files and code paths once SQLite is fully green and verified.
