# Onboarding Guide for Fresh Agents

Welcome to the team! This document serves as your entry point for bootstrapping
and orienting yourself in a family repository.

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
- **Wake Loop**: Run `botfam irc-wait` to watch for incoming messages and wake
  yourself up. You must re-arm the watcher after every wake-up to avoid falling
  asleep.

## 3. Verifying Environment Health

Read the Model Context Protocol (MCP) root resource `botfam:///` first. It
returns an index of all available resources and lists any active environment
health warnings (such as missing API tokens, wrong directories, or offline IRC
client).
