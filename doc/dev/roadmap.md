---
authors:
  - claude
  - rlupi
kind: roadmap
status: Draft
created: 2026-06-13
---

Phased roadmap for the forge-backing era. Milestones are `#` headings (paste
the title + the *Goal* line as the Gitea milestone name + description); issues
are `##` headings under their milestone (assign each open issue to that
milestone). All items below now have filed issues.

Already shipped (no milestone needed): `botfam verify` (#7), the
`internal/ccrep` core + ports (#8, PR #15 merged), `botfam-next` compile-sha
(#19), and the fam-ledger / quorum bug reports (#20, #21).

# Phase 1 — Forge-backed MVP

Goal: `botfam-next` runs the full ccrep loop (propose → review → gate → merge)
over the self-hosted forge for every agent, with per-agent auth that "just
works". This is the load-bearing milestone — everything else builds on it.

## #2 — [forge] Forgejo-in-Docker target + scripted provisioning (integration-test substrate)

Delivered by PR #13 (`compose.test.yaml` forgejo service +
`docker/bootstrap-test-forgejo.sh`). Close when #13 merges.

## #3 — [forge] Forge port + ccrep-merge-gate status auto-poster (botfam-next)

Delivered by PR #13 (`internal/forge.Client` + `PostCommitStatus` auto-poster).
Close when #13 merges. Two design calls to settle first: presence fallback
(agreed fail-secure; liveness note only) and the convergence question deferred
to Phase 2.

## #6 — [tooling] merge-gate auto-resolves the fam ledger (drop manual COLLAB_HISTORY)

PR #14, verified by agy in production via the host symlink. Ready to merge.

## #4 — [tooling] forge git-push credential helper (token-file)

Open (claude). Install a git credential helper that reads
`~/.botfam/token-<fam>-<actor>` so branch pushes to the forge work per-agent
without the inline helper dance.

## #5 — [tooling] botfam forge setup — per-agent onboarding (token + MCP + access)

Open. One harness-aware command: mint token + register the forge MCP + confirm
repo access, so adding an agent to the forge is a single step.

# Phase 2 — ccrep core convergence

Goal: collapse the two parallel ccrep-over-forge implementations onto one
authoritative substrate — the `internal/ccrep` Engine + ports — and finish the
review tooling. Removes the "which codepath is real?" ambiguity from Phase 1.

## #25 — Migrate legacy internal/fam forge wiring onto the internal/ccrep Engine + ports

The green/blue design call from PR #13: #13 wires forge into the legacy
`internal/fam` commands behind `UseForge()`, while #15 landed the clean
Engine/ports. Decide the target and migrate the forge adapter under the Engine
so there is one ccrep path, not two.

## #9 — [ccrep] external-review skill (wrap external-review.sh + consolidation subagent)

PR #16, agy-approved, `mdformat --check` clean. Ready to merge.

## #26 — Fix stale fam.toml repo_paths so the roster sees all worktrees

Root cause of #20: `fam.toml` `repo_paths` lists only `main` + `wt-agy` but six
worktrees exist on disk. Regenerate / wire `botfam worktree sync` so presence
and quorum derivation see every member.

# Phase 3 — Autonomy & ergonomics

Goal: agents notice queued work (review requests, new comments) natively,
without polling IRC or relying on scheduled tasks.

## #17 — native botfam-next wait for PR comment, review request, etc.

Open (rlupi). Coordinate with the daemon / forge-setup design (#5) before
implementing, per agy.

# Phase 4 — Validation

Goal: prove the whole forge-backed coordination loop end-to-end before we
retire the IRC substrate.

## #10 — [test] ccrep-integration-test-v1 — two-tier (BOOTSTRAP fam → botfam-next)

Open. Tier 1 cheap models role-play the protocol on a filesystem; Tier 2 audits
Tier 1 via botfam-next. Gates the IRC → forge cutover.

# Phase 5 — Operator front-end

Goal: a slimmer human UX over the forge-backed fam.

## #11 — [design] botfam-server-v1 — auth/UI front-end (botfam login + token file)

Open. Auth/UI front-end (`botfam login` + token file). Lowest priority until
Phases 1–4 land.
