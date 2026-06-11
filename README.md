# botfam

A tiny, single-binary **CLI tool** that enables a "family" of AI agents to coordinate seamlessly over a shared filesystem. It provides SQLite-backed messaging (with cheap blocking receive) and a lease-based task queue to let agents hand work back and forth safely.

botfam is the lightweight successor to a lineage of multi-agent coordination experiments (`deep-cuts/collab` → `hydra` → `scriba`). It keeps the core idea that proved out—coordination mailboxes and task lists—while stripping away all the operational cruft (no external databases, no background daemons, no SSH keys, and no Python dependencies).

---

## Why botfam?

1. **Lightweight & Portable**: Written in Go and compiled into a single static binary. Nothing to install, no virtual environments to activate.
2. **Cheap, Blocking `recv`**: Instead of burning tokens polling for new work, agents can park on a blocking `recv` call that wakes up cheaply only when a new message arrives.
3. **Lease-Based Task Queue**: Agents claim work from a shared pool. If an agent crashes or hangs, its task lease expires and is automatically reclaimed by the family.
4. **Cooperative & Secure Identity**: Identity is resolved automatically from the git worktree folder or pinned in the environment, with file locking to prevent spoofing.
5. **Built-in Quality Gates**: The `merge-gate` subcommand checks for peer approvals and ensures that no code merges to `main` without a fresh review on the exact commit SHA (preventing AI confabulation).

---

## Architecture at a Glance

```
Agent A (wt-agy)    ──CLI command──> botfam <cmd> ─┐
                                                   ├─> ~/.botfam/fam-<project-hash>/ (SQLite Database & Sockets)
Agent B (wt-claude) ──CLI command──> botfam <cmd> ─┘
```

* **Direct CLI Execution**: Each agent's editor or harness runs `botfam` commands directly from its worktree.
* **State on Disk**: All communication, task leasing, presence, and logs are implemented as SQLite transactions in a local database (`botfam.db`) within a local state directory outside the git repository.
* **Consensus-Driven Merges**: Merges to `main` are validated by checking session logs and proposals for review verdicts.

---

## Feature Status

We have completed the **Wave 1** implementation of the coordination and safety layer:

* **Mailbox Messaging**: Functional `send`, `recv`, `try_recv`, `peek`, `ack`, `seen`, and `inbox` commands.
* **Task Queue**: Functional `post`, `claim`, `complete`, `heartbeat`, `abandon`, and `sweep` commands, with **claim ergonomics** (targeted claim-by-id and type/suggested-owner filters).
* **CCREP Merge Gate**: The `botfam merge-gate --commit <sha> --proposal <id>` subcommand checks and enforces peer review consensus (independent approvals, no blockers, approvals die on new commits).
* **Interactive Session Promotion**: `botfam session close <slug>` renders the session log to markdown, verifies that the git working directory is clean, stages the markdown file, and opens the operator's editor interactively to edit the commit.
* **Integration Testing**: End-to-end integration tests verify messaging, task lifecycle, and topics/cursors.

---

## Developer Quickstart

### 1. Bootstrap a Multi-Agent Workspace
Initialize a repository with a roster of agent worktrees (e.g. `wt-agy`, `wt-claude`, `wt-codex`):

```bash
./bootstrap-botfam.sh /path/to/repo --agents agy,claude,codex
```

This script will:
* Build the `botfam` binary to `~/bin/botfam` and codesign it on macOS.
* Set up the shared `~/.botfam/` project directories.
* Add git worktrees for each agent on their respective branches (`agent/<agent>`).
* Configure the harness to allow direct execution of `botfam` CLI commands.

### 2. Run Tests
Ensure everything is green. If you are running in a restricted sandbox, use the local cache flag:

```bash
# Standard test run
go test ./...

# Sandbox-isolated cache run
env GOCACHE=$PWD/.gocache GOMODCACHE=$PWD/.gomodcache go test ./...
```

### 3. Build the Binary
```bash
go build ./cmd/botfam
```

### 4. Interactive Operator Commands
* **List active sessions**: `botfam session list`
* **Render a session log to markdown**: `botfam session render <slug>`
* **Close and commit a session**: `botfam session close <slug>`
