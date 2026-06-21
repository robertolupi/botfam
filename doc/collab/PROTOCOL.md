# botfam Coordination Protocol (Forge-First)

Canonical, single source of truth for how fam members coordinate. The harness
entry files (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`) are deliberately
lightweight pointers here — put substantive rules in this file, never there.

> **Coordination is forge-first.** Members coordinate through forge issues/PRs
> (assignments, reviews, comments); `botfam wait` is the wake loop and runs
> do-not-disturb by default (forge events wake you only when you're an assignee
> or @-mentioned).

______________________________________________________________________

## 1. Identity & Workspace Layout

Every agent works in its own git worktree of the repository. Your actor name is
derived from the **worktree directory basename** by dynamically checking the
repository name R (from the git common directory parent basename) and stripping
the first matching prefix in this order: `wt-R-`, `R-`, `wt-`, or `botfam-`. If
no prefix matches, name resolution fails closed (yielding no actor).

For example, in the `botfam` repository:

- `wt-claude` → `claude`
- `botfam-codex` → `codex`
- `wt-agy` → `agy`

In the `deep-cuts` repository:

- `deep-cuts-agy` → `agy`
- `wt-deep-cuts-claude` → `claude`

Day-to-day coordination runs on the **forge** (issues/PRs — assignments,
reviews, comments; see the wake loop below).

- **Wake loop:** `botfam wait` is the wake loop every member runs. It blocks on
  the per-agent spool, which a read-only ingester fills with forge activity; the MCP server starts the ingester automatically
  once identity resolves (no opt-out flag, and it does not mark forge
  notifications read — forge stays canonical). **Do-not-disturb is the
  default:** forge events wake you only when directed at you (assignee or
  @-mention in the latest comment); `--all` surfaces everything.

______________________________________________________________________

## 2. Durability

The **forge is the durable coordination record** — issues/PRs persist across
restarts and `botfam wait` replays missed forge activity from the spool, so no
coordination is lost to a restart.

- **Markdown Formatting:** Format `doc/` markdown with `tools/mdformat.sh`
  before committing. It pins the canonical mdformat + plugin versions so all
  agents produce byte-identical output and review diffs stay free of reflow
  noise; never format docs with anything else.

______________________________________________________________________

## 3. Gitea Pull Request Consensus Layer

All changes to shared state (such as landing commits on `botfam-next` or
`main`) are governed by Gitea's native branch protection rules instead of
custom IRC bot scripts.

### The Pull Request Workflow

1. **Feature/Refactor Branching:** An agent creates a dedicated branch (e.g.,
   `agent/agy` or `claude/feature-x`) from `botfam-next`.
2. **Opening a Pull Request:** The agent opens a Pull Request on Gitea
   targeting the integration branch (`botfam-next`).
3. **Cross-Review & Approvals:**
   - Independent peer agents review the PR description, discussions, and diffs.
   - Evaluators submit reviews using Gitea's PR review system.
   - A correct consensus requires meeting the branch protection's approval
     counts (typically **2 approvals** for `botfam-next` and **3 approvals**
     for `main`).
4. **Merge Execution:**
   - The repository owner (`rlupi`) acts as the single whitelist executor who
     merges the PR once Gitea's requirements are satisfied.
   - Direct merge bypasses by admins are blocked.

### Consensus Rules

- **Approvals Die on New Commits:** Gitea's branch protection is configured to
  dismiss stale approvals automatically when a new commit is pushed. Peers must
  re-evaluate new revisions.
- **Block on Rejected Reviews:** A request for changes (`REQUEST_CHANGES`
  review) blocks the merge gate until the reviewer explicitly approves or
  dismisses their block.
- **Spoof Resistance:** Gitea authentication (using secure tokens or SSH keys)
  prevents any spoofing of reviewer identities or pushes.

### Plane Separation & Ownership Rules

We enforce strict separation of roles and planes to optimize reasoning and
avoid deadlocks:

- **Plane Separation (Control vs. Data)**: Keep the control plane (the Gitea
  forge: issues, PRs, reviews, assignments) distinct from the data plane (the
  git repo: committed code). Process state (such as who is assigned, review
  approval, or merge-readiness) lives exclusively on the forge. The repository
  tip is mutated only by merges on the control plane.
- **Decompose by Coupling & Single Owner**: Group issues by coupling (shared
  design/contracts/data models). A coupled cluster must be assigned to a
  **single owner** who claims all related issues end-to-end to avoid Concept
  Fragmentation. The owner agent can fan out to subagents (hands) under its own
  context for execution. Peer agents must not work concurrently on different
  parts of a coupled cluster.
- **Bounded WIP**: Default to **WIP=1** for coupled clusters. Juggling multiple
  coupled tasks in one overfull context degrades reasoning and increases cost.

______________________________________________________________________

## 4. Worktree Ownership

Other actors' worktrees are **read-only**. To update one, coordinate with the owner on the Gitea forge. Only act yourself when the owner is known-offline, the tree is clean, the operation is a pure fast-forward, and you record the action on Gitea immediately.

### Repository Family Boundaries

A multi-family orchestration setup (e.g., `botfam` and `deep-cuts` running on
the same host) has strict isolation boundaries:

- **Read-only access is permitted**: An agent is allowed to read files, status,
  or logs in another repository family's directory for reference and
  cross-checking.
- **No cross-family writing, execution, or process management**: An agent must
  never write to files, run modifying shell commands, or spawn, manage, or
  terminate background processes/daemons (such as wait watchers or MCP servers) in worktrees or environments belonging to a different
  repository family.
- **No identity impersonation**: An agent must never impersonate or act on
  behalf of another agent or bot from a different repository family, nor use
  their credentials or local workspace configurations.
- **Coordination must occur over the forge**: Any request requiring action (writing
  or execution) in another family's checkout must be requested and discussed on
  the target family's Gitea repository. The corresponding agent
  belonging to that family must execute the actions themselves.

### Offline Cross-Family Issue Tracking (Contract & Bindings)

To allow asynchronous, offline issue tracking between repository families
(where agents in different families may not be online at the same time), we
establish a decoupled model consisting of a transport-agnostic contract (Layer
1\) and interchangeable transport bindings (Layer 2).

#### Layer 1: The Issue Tracking Contract (Transport-Agnostic)

Every cross-family issue report must conform to a strict schema and state
machine:

- **JSON Payload Schema**:
  ```json
  {
    "version": "1.0",
    "timestamp": "2026-06-13T06:30:00Z",
    "id": "dc-stale-venv-v1",
    "source": {
      "family": "deep-cuts",
      "nick": "claude-dc",
      "worktree": "wt-claude"
    },
    "target": {
      "family": "botfam",
      "nick": "agy"
    },
    "title": "Short descriptive title of the issue",
    "description": "Detailed description of the issue or feature request",
    "status": "reported",
    "evidence": "Log trace, error snippet, or command output if applicable"
  }
  ```
- **State Machine**: Issues follow the states
  `reported -> acknowledged -> resolved`. All updates to an issue are keyed by
  the unique issue `id` to ensure idempotency and prevent duplicate processing
  across families.

#### Layer 2: Transport Bindings

The Layer 1 contract is transport-agnostic and can be satisfied by Gitea issues/PRs or, as a fallback when offline, a shared file-system queue:

- **Binding A (Gitea/Forgejo) [Preferred]**:
  - **Transport**: Issues are opened directly on the target family's repository.
  - **Durability**: Gitea stores all issue data durably.
- **Binding B (Shared File-System Queue) [Fallback]**:
  - **Transport**: Used when the forge or network is offline. JSON payloads are dropped into the host-local shared directory `~/.botfam/cross-fam/issues/`.
  - **Filename Format**: Filenames must match the pattern: `~/.botfam/cross-fam/issues/<yyyy-mm-dd>-<source-family>-<source-nick>-<slug>.json`.
  - **Lifecycle**: The target family's processor agent scans the directory, ingests pending `.json` files, and renames them to `.processed` (or moves them to `processed/`) to prevent duplicate processing.

### Main checkout discipline

The main checkout (`~/src/botfam`) is the shared merge target. Rules, each paid
for by a 2026-06-12 incident:

- **Single writer per operation.** Any ref-changing operation there (merge,
  reset, cherry-pick, push) is claimed on the forge first (via issue/PR comments); everyone else
  waits until the actor reports done. Two agents executing the same recovery
  concurrently produce orphaned commits at best and a half-applied state at
  worst.
- **main is merge-only.** Never rebase it, never force-push it. A
  `pull --rebase` in the main checkout flattened three ratified merge commits
  and rewrote every ledger SHA (restored same morning). GUI git clients
  (Obsidian, IDEs) must not run sync against this checkout — point them at your
  own worktree.
- **Executor merges carry executor identity.** The main checkout matches no
  one's `includeIf`, so merge with explicit identity:
  `git -c user.name=<actor> -c user.email=dev+<actor>@example.com merge --no-ff <sha>`.
- **Worktree identity is set per-worktree, not via includeIf alone.** A
  `user.*` entry in the shared `.git/config` silently overrides `includeIf` for
  every linked worktree. With `extensions.worktreeConfig` enabled (repo-wide
  since 2026-06-12), each actor sets `git config --worktree user.name <actor>`
  and the plus-addressed email in their own tree. Reviewers: check `%an` on
  every proposed commit.

### Let It Crash & Warm Handover Protocol

Because agents are transient and fragile actors that degrade as their context
windows fill, we design for failure recovery rather than trying to recover
in-place:

- **Let It Crash**: Do not write complex, defensive error-recovery code inside
  an agent. Similarly, for agent actions: if the botfam harness or tooling
  itself fails to work, and you are not acting as a designated debugger (e.g.,
  developing a feature or explicitly instructed by the operator), do not search
  for or implement ad-hoc workarounds. Instead, let the execution fail, make
  the problem visible by filing a Gitea issue, and wait for human instruction.
  If context-fullness (computed out-of-band by the harness) approaches the
  crash threshold, or if the agent stalls or loops, let it crash/exit
  immediately.
- **Handover Snapshot**: Before crashing (or at regular progress milestones),
  the agent must write a compact **Handover Snapshot** to the control plane
  (the Gitea forge issue or PR comment). The snapshot's distilled reasoning
  state must contain:
  1. The task **goal**.
  2. The **decisions taken so far** and why.
  3. A pointer to the **git branch/PR** (so the product state is referenced,
     not copied).
  4. The **current blocker**, if any.
  5. The **next step** to be taken.
- **Supervision & Warm Restart**: The harness acts as a stateless one-for-one
  supervisor that detects the exit, spins up a fresh agent, and injects the
  Handover Snapshot. The new agent resumes warm from the snapshot and branch,
  avoiding the high onboarding tax of replaying history from genesis.

______________________________________________________________________

## 5. Platform Gotchas

- **macOS Gatekeeper:** Rebuilt binaries must be codesigned:
  `codesign --force --sign - ~/bin/botfam`.
