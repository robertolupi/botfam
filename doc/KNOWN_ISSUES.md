# Known Issues & Architectural Findings

This document tracks identified bugs, security/integrity vulnerabilities, platform quirks, and specifications debts in the `botfam` coordination system.

---

## 1. Cooperative Identity Binding Vulnerability in `session_append`

### Problem
In the Phase 1 Go implementation, any client/CLI invocation can append entries to any session log while masquerading as any actor. If a process calls the MCP tool `session_append` passing `actor: "claude"`, the server will append it directly to `session.jsonl` under that name. 

While the mailbox tools (`recv`, `ack`, `try_recv`) enforce ownership via a per-actor file lock (`.lock` via `Flock`), `session_append` only checks that the session exists and appends the entry. This allows accidental or unauthorized authorship spoofing (which was observed in the smoke tests, where a separate process appended on behalf of `claude`).

### Severity: Medium (Trusted Environment)
For a v0 trusted-agent worktree family, there is no malicious intent, but it compromises the integrity of the consensus log.

### Mitigation
1. **Session Lock Discipline**: Require the caller of `session_append` to also hold the actor's lock (or verify that they are the resolved actor of the session/worktree).
2. **Strict CLI Validation**: The CLI should refuse `--actor` values that do not match the directory-name-based folder resolution (e.g. `wt-agy` -> `agy`).
3. **Phase 2 Token Identity**: Standardize token-based identity verification in `bottown` (the networked REST sibling).

---

## 2. `session_read` Filter Parameter Collision

### Problem
The `doc/DESIGN_sessions.md` specification initially defined the session log filtering parameter as `actor` (to filter entries by a specific actor). However, the MCP server binds `actor` as the sticky identity parameter for the session. If `session_read(session="...", actor="claude")` is invoked from an `agy` session, the server throws an identity conflict:
> `actor "claude" conflicts with bound session actor "agy"`

### Resolution
The parameter has been renamed from `actor` to `from` in both the specification and implementation to match the envelope field names and avoid name clashes.

---

## 3. macOS codesigning Gatekeeper SIGKILL

### Problem
When the `botfam` Go binary is compiled locally on macOS, executing it via the stdio MCP host causes the process to be killed immediately by macOS Gatekeeper with `SIGKILL` (exit code 137). This happens silently and looks like an abrupt connection shutdown/EOF to the MCP host.

### Resolution
Always sign the compiled binary with an ad-hoc signature after building on macOS:
```bash
codesign --force --sign - ~/bin/botfam
```

---

## 4. Split-Brain Store Path Resolution

### Problem
Different execution entry points (such as the MCP server, Go CLI, and test runner) running with different working directories can resolve different store paths under `~/.botfam/`, leading to fragmented states where one tool cannot see the mailboxes or sessions created by another.

### Resolution
- The resolution logic has been unified and verified in `TestResolver`.
- Use explicit `COLLAB_ROOT` environment variables to override automatic directory-based resolution when running in multi-repository or script contexts.

---

## 5. Missing Append-Time Schema Validation

### Problem
Malformed or incomplete handoff data (e.g., empty strings for `task`, `context`, or `deliverable` inside a `handoff` payload) can be appended directly to the session log, introducing dirty data into the consensus history.

### Resolution
Embed structural schema-shape validation directly into the `session_append` tool backend. Reject any appends containing malformed handoff objects, while keeping style/convention checks as warnings in the `botfam doctor` command.
