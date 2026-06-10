<!-- RENDERED by botfam session render — DO NOT EDIT (append via session_append) -->

# Session: bootstrap-botfam-setup

## Participants

- codex
- claude
- agy

---

## [codex, 2026-06-10T04:20:53Z]
# Bootstrap setup problems and acceptance notes

Context: user wants a seamless `bootstrap-botfam.sh $REPO --agents agy,codex,claude` flow across Claude, Codex, and agy/Antigravity-like harnesses.

Observed problems from this run:

1. MCP command path mismatch
- `.mcp.json` and `.agents/mcp_config.json` using bare `botfam` can fail when a harness launches with a PATH that does not include `~/bin`.
- Codex specifically needed `.codex/config.toml` with an absolute `/Users/.../bin/botfam` path before `mcp__collab` appeared.
- After MCP config changes, an already-running harness must be restarted; tools are registered only at startup.

2. Sandbox/write permission mismatch
- botfam message receive/ack writes locks and moves mail under `~/.botfam`; Codex sandbox initially could not write there.
- git worktree metadata lives under the main repo `.git/worktrees/<name>` path, which may be outside a harness workspace root; fetch/merge can need explicit permission.

3. One-shot stdio fallback is fragile
- Running `botfam serve` as a one-shot stdio process can reserve a message and then roll it back when the process exits before `ack`.
- Any fallback client must keep one server process alive across `try_recv` and `ack`.

4. Bootstrap must own outer orchestration
- `botfam setup` creates fam root, registry, maildirs, tasks, and project symlink, but it does not create per-agent worktrees or harness config files.
- The bootstrap wrapper should create/reuse `wt-<agent>` worktrees on `agent/<agent>` branches without deleting dirty work.

5. Harness config requirements differ
- Claude can use `.mcp.json` plus `.claude/settings.json` enabling `collab` and allowing `mcp__collab__*`.
- Codex-like harnesses may need `.codex/config.toml` with an absolute command path.
- Antigravity/agy-like harnesses currently use `.agents/mcp_config.json`.
- No committed MCP config should bake in `COLLAB_ACTOR`; identity should come from worktree basename or first call.

6. Existing config must be preserved
- Re-running bootstrap should merge `.claude/settings.json` permissions instead of dropping existing permissions.
- Existing MCP config conflicts should be handled carefully. Ideally merge with jq when available; otherwise fail closed or require `--force` rather than silently clobbering unrelated servers.

7. Worktree identity must be standardized and tested
- The safe convention is sibling worktrees named `wt-agy`, `wt-codex`, `wt-claude`.
- Actor parsing is permissive: basename suffix after the last hyphen becomes identity. Bootstrap should document and test only the `wt-<agent>` convention.
- Local acceptance test: send from one generated worktree and `try_recv` from another without passing actor.

8. macOS binary signing
- If bootstrap builds or installs `~/bin/botfam` on macOS, it should run `codesign --force --sign - ~/bin/botfam` when available or clearly warn; otherwise Gatekeeper can SIGKILL the binary.

9. Idempotency and safety
- Second run should succeed.
- Existing worktrees/branches should be reused.
- Dirty worktrees must not be reset or removed.
- Conflicting paths/symlinks/config should not be overwritten unless `--force` is explicit.
- `botfam setup` currently removes/recreates a project symlink if it points elsewhere; bootstrap should preflight this if it wants safer UX before invoking setup.

Suggested local test coverage:
- Temp repo bootstrap with three agents.
- Re-run idempotency.
- Existing dirty worktree preservation/refusal.
- `.mcp.json`, `.agents/mcp_config.json`, `.codex/config.toml`, `.claude/settings.json` validation.
- End-to-end stdio MCP message exchange across generated worktrees.
- Fail-closed behavior when repo object store is not registered.
