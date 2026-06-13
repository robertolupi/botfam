# Tooling backlog — manual one-shots → proper tools/commands

Recurring ad-hoc commands run by hand during sessions that should become
`tools/` scripts or `botfam` subcommands. Captured 2026-06-13 (claude session).

## Net-new gaps (no tool yet)

1. **merge-gate ledger path is manual.** Every gate check needed
   `COLLAB_HISTORY=~/src/botfam-collab/history.jsonl botfam merge-gate --commit <sha> --proposal <id>`
   — the default path resolution (`DefaultHistoryPath(".")`) doesn't find the
   fam's scribe ledger, so the env var is set by hand each time.
   → **`botfam merge-gate` should resolve the fam ledger automatically** (from
   the fam root / fam.toml), no env var.

2. **No `botfam verify <sha>`.** To confirm a proposed commit builds + tests
   without disturbing the worktree, I repeatedly did:
   `git worktree add --detach /tmp/x <sha> && (cd /tmp/x && go build ./... && go test ./...) ; git worktree remove --force /tmp/x`.
   → **`botfam verify <sha> [pkgs…]`**: ephemeral detached worktree, build+test,
   clean up, report. (Used before every ccrep approval.)

3. **Forge git-push auth is a hand-rolled credential helper.** Pushing a branch
   to gitea as the bot without leaking the token:
   `git -c credential.helper='!f(){ echo username=<bot>; echo "password=$(cat ~/.botfam/token-<fam>-<actor>)";};f' push -u gitea <branch>`.
   → **install a botfam git credential helper once** (reads
   `~/.botfam/token-<fam>-<actor>`), or a `botfam forge push` wrapper, so branch
   pushes to the forge "just work" per-agent.

4. **Forge per-agent onboarding is multi-step.** mint token
   (`tools/forge-login.sh`, done) **+** register the forge MCP server
   (`claude mcp add forge …`, harness-specific) **+** ensure repo access.
   → **`botfam forge setup`**: one harness-aware command that does all three so
   adding an agent to the forge is a single step.

5. **Local ccrep merge + `!executed` done by hand.**
   `cd <main-checkout> && git merge --no-ff <sha> -m … ` then post `!executed`.
   → already designed as **`ccrep merge`** in `ccrep-mcp-tools-v1`; tracking
   here as the recurring manual step it replaces.

## Already turned into tools this session

- `tools/forge-login.sh` — mint a forge token (agent-generic, fork-neutral,
  secret-safe). ✅
- `tools/external-review.sh` — multi-model external-review fan-out, out-of-repo
  storage. ✅ (the consolidation-subagent spawn still wants the planned
  `external-review` **skill** to wrap it.)

## Already designed, pending implementation

- **ccrep verb set** (propose/revise/vote/tally/merge/gate) —
  `ccrep-mcp-tools-v1`. Replaces hand-typed `!vote` / `!revision` / SHAs and the
  manual merge+`!executed`.

## Recurring manual, but documented / acceptable

- IRC reconnect after a harness restart: `botfam irc-client <name>` + re-arm
  `irc-wait` (covered by the `join-irc` skill).
- `tools/mdformat.sh <file>` before committing markdown (already a tool).
