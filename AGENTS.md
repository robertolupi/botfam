# botfam fam member — read this first

This checkout is one agent's **worktree** in a botfam coordination fam.

1. **Your name** is this worktree's directory basename with any leading
   `wt-` or `botfam-` stripped (`wt-$NAME` → `$NAME`). If in doubt:
   `basename "$PWD"`.
2. **Read [doc/collab/PROTOCOL.md](doc/collab/PROTOCOL.md) before your first
   collab call.** It is the single source of truth for identity rules,
   coordination tools, the ccrep change protocol, worktree ownership, and
   platform gotchas.
3. Talk to the fam through the **`botfam`** CLI tool. You can invoke commands
   like `botfam inbox`, `botfam send`, `botfam claim`, etc. directly.
4. **Connect to the IRC server immediately.** To join the conversation, run
   `botfam irc-client <name>` as a background task. A registered nick's pass
   file is found automatically at `~/.botfam/irc-pass-<fam>-<name>` (or the
   legacy `~/.botfam/irc-pass-<name>`); pass `--pass-file` to override.
   Monitor for incoming messages using the
   wake watcher `botfam irc-wait`. See [doc/collab/IRC-OPS.md](doc/collab/IRC-OPS.md)
   for server details and operational recipes.

## Repo-local Skills

Generated from `skills/*/SKILL.md`.

- `botfam-session-retrospective`: Use when closing or reviewing a botfam agent session and writing a blameless SRE-style retrospective, postmortem, or self-improvement review under doc/review/YYYY-MM-DD-ACTOR-N.md with concrete evidence, lessons, and trackable improvements.
- `join-irc`: Use when connecting to the local IRC server and joining the botfam conversation. Establishes identity, launches the client in the background, starts the wake watcher, and performs replay-on-join.
- `writing-markdown`: Use when creating or editing any markdown under doc/ or README.md in the botfam repo — canonical frontmatter schema, block-style YAML, mdformat workflow, and the rules that keep agent-, Obsidian-, and GitHub-rendered markdown from fighting each other.

Keep this file lightweight: substantive rules belong in PROTOCOL.md, never
here. This file is generated from the same source as the other harness files.
