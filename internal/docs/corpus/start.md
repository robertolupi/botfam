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

## 2. Coordination & Wake

**The forge is the coordination plane.** You coordinate with peers and the
operator through forge issues/PRs — assignments, reviews, and comments — not
through chat. To send another agent a direct message, comment on an issue/PR
and **@-mention or assign** them: that is delivered durably.

- **Wake is supervised (in transition).** The wake substrate is moving from the
  legacy `botfam wait` loop to a **supervisor** (`botfam sprint run`,
  EventDeliveryV2). The supervisor decides when your session ends. It has not
  landed yet, so **a human operator is the manual supervisor**: there is no
  automatic wake loop to start. The expected pattern is **finish the task →
  yield → tell the operator in plain text that you are done → wait to be run
  again by hand.** Re-derive your worklist by **querying the forge** on each
  boot, not from a wake stream.
- **`botfam wait` is legacy, not a wake loop.** It still blocks on your
  per-agent spool (`$FAMROOT/spool/{{.Actor}}`) and prints surfaced messages,
  but the ingester that filled that spool is **disabled by default**
  (EventDeliveryV2 M0c), so on a current binary it has nothing to drain. Do not
  start it as your boot loop. (Fams still on the old binary keep today's
  semantics via the `legacy_ingest` opt-in.)

## 3. Target Branch

Open PRs against **`{{.IntegrationBranch}}`** (the integration branch).
**`{{.ReleaseBranch}}`** is the public release target — do not target it unless
explicitly instructed.

## 4. Verifying Environment Health

Read the Model Context Protocol (MCP) root resource `botfam:///` first. It
returns an index of all available resources and lists any active environment
health warnings (such as missing API tokens or wrong directories).

## 5. Warm Onboarding / Handover Snapshot

If you are booting to resume a task that was already in progress (e.g.
following a Let It Crash restart):

- **Load the Handover Snapshot**: Do not replay the task history from genesis
  or re-read the entire wiki/corpus. Find the latest Handover Snapshot on the
  forge issue/PR comment (goal, decisions taken so far, branch/PR pointer,
  current blocker, next step).
- **Check out the Branch**: Switch to the branch indicated in the snapshot to
  retrieve the current product state from the data plane, and resume from the
  next step specified.
