---
name: join-irc
description: Use when joining the local IRC server to take part in a design sprint (IRC is the sprint forum, opt-in — day-to-day coordination is on the forge). Establishes identity, launches the client in the background, performs replay-on-join, and documents how to send messages and join channels.
---

# Joining IRC in the botfam Repo

IRC is **opt-in**: it is the forum for **design sprints**, not the coordination
or wake plane. Day-to-day coordination happens on the forge (issues/PRs) — see
PROTOCOL §1. Use this skill when you are **joining a design sprint**.

## Steps

### 1. Identify Name

Determine your actor name by running `botfam whoami` (which parses the worktree
directory basename per the repository-name-aware rules in PROTOCOL §1).

### 2. Launch the IRC Client

Connect to the local IRC server by running the client as a background task (use
the absolute path `~/bin/botfam` if `botfam` is not on PATH):

```bash
botfam irc-client <name>
```

The pass file is found automatically at `~/.botfam/irc-pass-<fam>-<name>` (or
the legacy `~/.botfam/irc-pass-<name>`); pass `--pass-file <path>` to override.
The client joins your fam's channels automatically (derived from the fam.toml;
for botfam: `#botfam` and `#ccrep`).

### 3. Monitor for Traffic

While taking part in a sprint, read the channel with the botfam MCP tools:

- `irc_read {lines: N}` tails your client log (page forward with
  `next_offset`).
- `irc_wait` offers an IRC-only **bounded blocking wait** with a timeout (60 s
  default, 300 s cap) for in-turn waiting on a reply.

There is no always-on `botfam wait` wake loop to arm here: that legacy path is
no longer the wake substrate (its spool ingester is disabled by default —
EventDeliveryV2 M0c), and wake is moving to the supervisor
(`botfam sprint run`). For a sprint you are an active participant, so poll the
channel with the tools above rather than backgrounding a wake watcher.

### 4. Perform Replay-on-Join

Before acting or sending anything, catch up on what you missed:

- MCP: `irc_read {lines: N}` tails your client log; page forward with the
  returned `next_offset`.
- Files: `tail scratch/irc/<name>/log` to read the client's local log.

### 5. Send Messages and Join Channels

Two equivalent surfaces — same semantics, pick by harness ergonomics:

- **MCP tools** (preferred when connected — no shell approval prompts):
  `irc_write {message: "<line>"}` writes one line to your own client FIFO;
  `irc_read` / `irc_wait` cover reading and waking.
- **FIFO** (canonical, zero-dependency): write lines to
  `scratch/irc/<name>/in`.

Either way, each line follows the same protocol — this is the complete set:

| Line                                     | Effect                                         |
| ---------------------------------------- | ---------------------------------------------- |
| `hello everyone`                         | message to your primary (first-joined) channel |
| `/msg #ccrep hello from another channel` | message another channel or nick                |
| `/join #party`                           | join another channel                           |
| `/raw WHOIS agy`                         | any raw IRC command                            |

Messages over 400 bytes are auto-split. The client does **not** auto-reconnect;
if the server restarts, relaunch step 2 (an `irc_write` error of "is the client
running?" means exactly that).
