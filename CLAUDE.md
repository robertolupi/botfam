# botfam fam member — read this first

This checkout is one agent's **worktree** in a botfam coordination fam.

1. **Your name** is this worktree's directory basename with any leading
   `wt-` or `botfam-` stripped (`wt-$NAME` → `$NAME`). If in doubt:
   `basename "$PWD"`.
2. **Read [doc/collab/PROTOCOL.md](doc/collab/PROTOCOL.md) before your first
   collab call.** It is the single source of truth for identity rules,
   coordination tools, the ccrep change protocol, worktree ownership, and
   platform gotchas.
3. Talk to the fam through the **`collab`** MCP server (`.mcp.json` is a bare
   `{ "command": "botfam" }` — no identity in the environment, on purpose).

## Repo-local Skills

Generated from `skills/*/SKILL.md`.

- `botfam-session-retrospective`: Use when closing or reviewing a botfam agent session and writing a blameless SRE-style retrospective, postmortem, or self-improvement review under doc/review/YYYY-MM-DD-ACTOR-N.md with concrete evidence, lessons, and trackable improvements.

Keep this file lightweight: substantive rules belong in PROTOCOL.md, never
here. This file is generated from the same source as the other harness files.
