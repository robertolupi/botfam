# Onboarding Guide for Fresh Agents

Welcome to the team! This document serves as your entry point for bootstrapping
and orienting yourself in a family repository.

> [!NOTE]
> If the `botfam` command is not found on your PATH, it is located at
> `~/bin/botfam` (e.g., run `~/bin/botfam whoami`), or you can run `./botfam`
> directly from the repository root.

## 1. Identity Resolution

Your actor name is derived dynamically from the **worktree directory basename**
where you are running. The name is parsed by checking the repository name and
stripping common prefixes (such as `wt-` or `botfam-`). You can always resolve
and verify your actor name by running:

```bash
botfam whoami
```

## 2. IRC Connection & Layout

We use a local IRC server for coordination and wake triggers.

- **Connect**: Connect to the IRC server by running the client in the
  background:
  ```bash
  botfam irc-client {{.Actor}}
  ```
  The on-server nick is fam-scoped to `{{.Actor}}-{{.Fam}}` automatically, and
  the pass file resolves on its own (scoped `irc-pass-{{.Actor}}-{{.Fam}}` →
  legacy → anonymous). Override either with `--pass-file <path>` / `--raw-nick`
  if needed.
- **Durability**: The client writes raw traffic to `scratch/irc/{{.Actor}}/log`
  and reads input from the named pipe `scratch/irc/{{.Actor}}/in`.
- **Replay History**: When you boot or reconnect, you MUST read and parse the
  shared history ledger first (e.g., via the `irc_replay` MCP tool). Do not
  assume you saw all traffic live.
- **Wake Loop**: Run `botfam wait` — the unified wake point — to block until
  new IRC **or** forge activity arrives, then re-arm after every wake-up to
  avoid falling asleep. As a botfam member you are expected to start it as soon
  as you boot, and to act autonomously on what it surfaces (work an issue the
  operator assigns you, review a PR another bot requests). It reads your
  per-agent spool (`$FAMROOT/spool/$AGENT`) and prints each message as a
  `===== message N/M · <source> =====` banner followed by the verbatim RFC-822
  message (headers + body). Surfacing a message moves it from `new/` to `cur/`
  (the ack) — there is no cursor to pass back; the next `botfam wait` only shows
  what's new, and `botfam wait --replay` re-reads `cur/` for gap recovery. Forge
  notifications are drained into the spool and **marked read automatically** —
  the spool is the durable record; you do not clear notifications by hand. The
  spool is filled by an ingest goroutine the MCP server starts automatically
  for your agent as soon as your client's workspace roots resolve — so it is
  armed by the time you reach this point. It runs for any resolved agent;
  there is no opt-out flag.
  - **Deprecated fallbacks**: `botfam irc-wait` (IRC only) and
    `botfam forge-wait` (forge only) are the legacy single-source watchers,
    slated for removal in #250 — prefer `botfam wait`.

## 3. Verifying Environment Health

Read the Model Context Protocol (MCP) root resource `botfam:///` first. It
returns an index of all available resources and lists any active environment
health warnings (such as missing API tokens, wrong directories, or offline IRC
client).

## 4. Warm Onboarding / Handover Snapshot

If you are booting to resume a task that was already in progress (e.g.
following a Let It Crash restart):

- **Load the Handover Snapshot**: Do not replay the task history from genesis
  or re-read the entire wiki/corpus. Find the latest Handover Snapshot on the
  forge issue/PR comment (goal, decisions taken so far, branch/PR pointer,
  current blocker, next step).
- **Check out the Branch**: Switch to the branch indicated in the snapshot to
  retrieve the current product state from the data plane, and resume from the
  next step specified.
