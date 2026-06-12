---
authors:
  - agy
  - claude
kind: proposal
status: Draft
created: 2026-06-12
proposal-id: hydra-harness-adoptables-v1
executor: claude
quorum: majority
deadline: none
---

# Proposal: Adoptables from the Hydra and Harness codebases

> [!NOTE]
> **Status**: Draft (2026-06-12). Distills the 2026-06-12 exploration of two
> external multi-agent projects into a ranked list of practices for botfam to
> adopt, design into upcoming work, or explicitly decline.

## Context

On 2026-06-12 the operator cloned two external projects and asked the fam to
study them (`#botfam`, 17:16 CEST). Two explore subagents (claude) and a
parallel pair (agy) reported back on the channel; claude and agy each drafted a
distillation and merged them into this proposal (channel consensus, 17:28
CEST). Evidence paths reference the local clones.

- **Hydra** (`~/src/gh/hydra`) — a security-first Node.js/TypeScript CLI
  harness that runs coding agents (Claude Code) inside ephemeral, non-root
  Docker containers. Notable for its mount-approval model, filesystem IPC, and
  fail-closed posture. Unrelated to botfam despite the prior-art confusion
  fixed in
  [2026-06-12-gemini-competitive-analysis-self-improvement.md](../review/2026-06-12-gemini-competitive-analysis-self-improvement.md).
- **Harness** (`~/src/gh/harness`) — not a runtime: a Claude Code plugin
  (meta-skill) that generates multi-agent teams (agent definitions, skills,
  orchestrator) from a domain prompt, built around six team architecture
  patterns and a seven-phase generation workflow.

## Tier 1 — adopt now (low effort, high yield)

1. **Phase-0 drift audit before acting.** Harness re-audits its generated
   artifacts on every invocation and routes to new/extend/maintenance modes
   (`skills/harness/SKILL.md`, Phase 0). botfam equivalent: a `botfam doctor`
   check (or session-boot habit) that verifies the generated harness files
   (`AGENTS.md`/`CLAUDE.md`/`GEMINI.md`) match `internal/fam/agent_docs.go`,
   skills match their `SKILL.md` sources, and `fam.toml` matches the scribe's
   loaded roster — plus the lighter per-session habit of checking recent
   commits, active worktrees, and the latest IRC log before formulating a plan.
   This would have caught the three-way harness-file conflict flagged during
   today's sync.
2. **Progressive disclosure for `skills/`.** Harness caps skill bodies at ~500
   lines and pushes depth into `references/` loaded on demand
   (`skills/harness/references/skill-writing-guide.md`). Adopt as a convention
   for botfam skills before they grow: SKILL.md carries the high-level summary,
   key workflows, and pointers; long instruction subsets, schemas, and
   templates move to `skills/*/references/` (or `scripts/`). agy flagged this
   as the top context-bloat defense for long sessions.
3. **Fail-closed defaults everywhere.** Hydra blocks all extra mounts when its
   global allowlist file is missing and never injects secrets that are not
   explicitly declared (`src/security/mount-security.ts`). botfam adoption: if
   any settings file, path specification, or credential check is missing,
   malformed, or ambiguous, abort with a non-zero exit code — never
   default-allow.
4. **Audit-log every denied operation.** Hydra logs each unauthorized IPC send,
   cross-group schedule, and rejected mount with its reason. The botfam
   scribe/merge-gate already validates sender identity; extend it to record
   *rejected* bang commands and votes (with reason) in `history.jsonl` so spoof
   attempts and malformed proposals are visible after the fact.

## Tier 2 — design into upcoming work

05. **Intent-vs-grant dual configuration.** Hydra splits "what the project
    requests" (`hydra.yml`, agent-writable) from "what the host grants"
    (`~/.config/hydra/mount-allowlist.json`, never mounted into agents). botfam
    already half-follows this (`~/.botfam/` for credentials); formalize the
    principle in PROTOCOL.md: anything an agent can write may only express
    intent, grants live outside the repo. Corollary for secrets: no file under
    the repo (including `scratch/`) may hold plain-text keys or tokens —
    configuration references them by path pointer or env-var name, resolved
    from `~/.botfam/` at runtime.
06. **Symlink resolution before path authorization.** Hydra resolves paths with
    `fs.realpathSync` before matching them against allowlists, defeating
    symlink-traversal breakouts (`src/security/mount-security.ts`). botfam
    adoption: any Go code that validates or authorizes file access outside the
    primary worktree resolves symlinks first (`filepath.EvalSymlinks`) before
    applying prefix checks.
07. **Host-enforced execution bounds.** Hydra terminates containers that exceed
    a timeout or output-size cap. botfam adoption: background tasks spawned by
    agents or wrappers (IRC clients, test suites, subagent runs) carry explicit
    timeout and max log/output size limits so a runaway process cannot exhaust
    the host.
08. **Identity from path/namespace, never from env or argv.** Hydra derives IPC
    identity from the per-group directory an agent can reach, not from
    environment variables. botfam's worktree-basename identity and the scribe's
    `*-cli` nick normalization are the same idea — ratify it as a stated
    invariant rather than folklore.
09. **Trigger test suites for skills.** Harness ships 8–10 should-trigger and
    8–10 should-NOT-trigger prompts per generated skill plus with/without-skill
    A/B comparisons (`skills/harness/references/skill-testing-guide.md`).
    Adoption: every new repo skill includes a validation section listing
    explicit triggers and near-miss non-triggers.
10. **Why-first skill prose.** Harness teaches reasoning ("X works because…")
    instead of ALWAYS/NEVER rules, on the argument that models generalize
    intent better than rule lists. Our `writing-markdown` skill already cites
    the incident behind each rule; make that the house style for all skills and
    for PROTOCOL.md updates (cite the retrospective or CCREP proposal behind
    each constraint).

## Tier 3 — noted, not adopted now

11. **Full container sandboxing.** Hydra's ephemeral containers,
    docker-socket-proxy, and internal-only agent networks are excellent but
    conflict with the ratified architecture formula ("IRC + bots + local
    sandbox-only shims") and our host-worktree model. Revisit only if untrusted
    third-party agents ever join the fam.
12. **Credential isolation.** Hydra mounts Anthropic credentials into agent
    containers and documents the gap (agents can read them). We share the same
    unsolved problem; record it as a known limitation rather than pretending
    either system solves it.
13. **Dependency SLA documentation.** Harness publishes explicit response SLAs
    for upstream breaking changes (`docs/experimental-dependency.md`).
    Interesting for a public botfam release; premature today.

## Asks

1. Ratify the tier list (approve = agreement on priorities, not on
   implementation details).
2. Tier 1 items become tracked improvements; executor volunteers per item on
   `#botfam` (claude volunteers for item 1, the drift audit).
3. Tier 2 items get folded into the next PROTOCOL.md revision and skill
   authoring as they come up; no dedicated workstream.
4. agy's standalone draft (`wt-agy` commit 6e9f703,
   `doc/proposals/hydra-harness-adoptables.md`) is superseded by this unified
   doc and is not separately proposed.
