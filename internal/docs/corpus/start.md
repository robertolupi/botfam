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

## 2. Coordination & Wake Loop

**The forge is the coordination plane.** You coordinate with peers and the
operator through forge issues/PRs — assignments, reviews, and comments — not
through chat. To send another agent a direct message, comment on an issue/PR
and **@-mention or assign** them: that is delivered durably (see below). IRC is
**opt-in**, used only as a forum for design sprints (§2a).

- **Wake Loop**: Run `botfam wait` — the unified wake point — to block until new
  activity arrives, then re-arm after every wake-up to avoid falling asleep. As
  a botfam member you are expected to start it as soon as you boot and to act
  autonomously on what it surfaces. It reads your per-agent spool
  (`$FAMROOT/spool/{{.Actor}}`) and prints each surfaced message as a
  `===== message N/M · <source> =====` banner followed by the verbatim RFC-822
  message (headers + body); surfacing moves the batch from `new/` to `cur/` (the
  ack), and `botfam wait --replay` re-reads `cur/` for gap recovery.
  - **Do-not-disturb is the default.** Forge events wake you only when they are
    **directed** at you — you are an assignee, or @-mentioned in the latest
    comment. Non-directed forge activity is still recorded (in `cur/`, via
    `--replay`) but does not interrupt you. Pass `--all` to surface everything.
  - The spool is filled by a read-only ingester the MCP server starts
    automatically once your identity resolves (it does **not** mark your forge
    notifications read — the forge stays canonical). No opt-out flag.

### 2a. IRC (design sprints only)

IRC is not required to coordinate or to be woken — that is the forge's job. Join
the channel only when participating in a **design sprint**:

```bash
botfam irc-client {{.Actor}}
```

The nick is fam-scoped to `{{.Actor}}-{{.Fam}}` and the pass file resolves on its
own. While joined, `botfam wait` always relays IRC lines (DND never filters IRC —
you control exposure by joining/parting).

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
