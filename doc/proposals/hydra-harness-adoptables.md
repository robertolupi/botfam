---
kind: proposal
status: Draft
authors:
  - agy
created: 2026-06-12
---

# Hydra & Harness Adoptables Proposal

This proposal distills the architectural design patterns discovered in the
cloned `hydra` (zero-trust sandbox) and `harness` (multi-agent factory)
repositories, adapting them for the `botfam` ecosystem to enhance security,
efficiency, and context management.

______________________________________________________________________

## 1. Sandbox Security & Host-Guest Separation (from Hydra)

To harden `botfam`'s local tool execution, command runners, and integration
test environments:

### 1.1 Host-Local Secrets Isolation

- **Pattern:** Hydra stores all API credentials, tokens, and keys outside the
  git repository structure (at `~/.config/hydra/secrets.env`) and injects them
  only at container spawn.
- **Adoption:**
  - Formalize `~/.botfam/` as our canonical configuration and credentials
    repository.
  - No file under the repository (including `scratch/`, `.env`, or
    configuration files) may store plain-text keys or sensitive tokens.
  - Credentials (e.g. NickServ passwords) must only be referenced in
    configuration files via path pointers or environment variable names, and
    resolved from `~/.botfam/` at runtime.

### 1.2 Path Resolution & Symlink Auditing

- **Pattern:** Hydra resolves all paths using `fs.realpathSync` prior to
  matching against host-side directory mount allowlists to prevent symlink
  traversal breakouts.
- **Adoption:**
  - Any `botfam` command or internal Go function that validates, mounts, or
    accesses files outside its primary worktree must resolve symlinks first
    (e.g. using `filepath.EvalSymlinks`) before applying prefix checks or path
    authorization filters.

### 1.3 Host-Enforced Bounds

- **Pattern:** The host wrapper monitors container execution, terminating
  containers that run longer than `CONTAINER_TIMEOUT` or produce more than
  `CONTAINER_MAX_OUTPUT_SIZE` bytes.
- **Adoption:**
  - All background tasks spawned by agents or script wrappers (e.g. IRC
    clients, test suites) must run under explicit execution limits (timeout and
    maximum log/output size) to protect host systems from memory/disk
    exhaustion by runaway processes.

### 1.4 Fail-Closed Security

- **Pattern:** If an allowlist config is missing or unparseable, Hydra fails
  closed.
- **Adoption:**
  - Adopt a fail-closed policy across all authorization and path-validation
    shims. If any settings file, path specification, or credential check is
    missing, malformed, or ambiguous, execution must abort immediately with a
    non-zero exit code.

______________________________________________________________________

## 2. Context & Task Lifecycle Management (from Harness)

To manage token overhead and maintain agent focus during long, multi-agent
sessions:

### 2.1 Progressive Skill Disclosure

- **Pattern:** Harness limits core `SKILL.md` files to 500 lines. All detailed
  rules, historical contexts, and extensive templates are pushed to a
  `references/` directory and loaded only when triggered.
- **Adoption:**
  - Limit `skills/*/SKILL.md` to a high-level summary, key workflows, and a
    list of references.
  - Move long instruction subsets, API schemas, and non-essential documentation
    to `skills/*/references/{sub-topic}.md` or `skills/*/scripts/` to protect
    the agents' primary context windows.

### 2.2 Why-First Guidelines

- **Pattern:** Focus guidelines on *why* a constraint was created (citing
  concrete incidents or proposals) instead of prescribing generic `ALWAYS` or
  `NEVER` rules.
- **Adoption:**
  - When updating `doc/collab/PROTOCOL.md` or repository rules, state the
    context and link to the session retrospective or CCREP proposal (e.g.,
    citing the 2026-06-12 misattribution incident). This allows agents to
    understand design intent rather than rigidly adhering to obsolete rules.

### 2.3 Phase-0 Drift Audits

- **Pattern:** Harness executes a "Phase 0" check upon every invocation to
  inspect the state of the workspace and detect drift before starting any
  development activity.
- **Adoption:**
  - Require agents to run a brief workspace verification (e.g. checking recent
    commits, active worktrees, and the latest IRC logs) before formulating
    implementation plans.

### 2.4 Trigger & Boundary Verification

- **Pattern:** Harness validates custom skills using test prompts (10
  should-trigger, 10 should-NOT-trigger) to ensure the agent is not
  over-triggering.
- **Adoption:**
  - Every new skill added to the repo must include a validation section in its
    documentation listing explicit triggers and near-miss non-triggers to test
    execution limits.
