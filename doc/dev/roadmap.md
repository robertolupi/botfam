---
authors:
  - claude
  - rlupi
kind: roadmap
status: Draft
created: 2026-06-13
---

Phased roadmap for the forge-backing era, tracked as Gitea milestones. This doc
mirrors the milestones; issue numbers are the source of truth.

> **Go-native pivot (2026-06-13).** ccrep-over-forge should *use* Gitea's
> native mechanisms, not reimplement them. For a fixed-count quorum, branch
> protection (Required Approvals + restrict-to-roster + dismiss-stale +
> restrict-push) *is* ccrep — no custom Engine, ledger, bang-lines, status
> check, or webhook. The duplicative gate machinery from the early forge work
> is being deleted (#33). Role tiers are native too (per-branch rules — e.g.
> `main` stricter than `botfam-next`). A webhook/custom code is only justified
> for the one rule branch protection can't express — presence-aware quorum
> (dynamic by who's online) — deferred as YAGNI. See
> [forge-backing.md](../proposals/forge-backing.md) §2.

Already shipped (no milestone): `botfam verify` (#7), the `internal/ccrep` core
(#8), `botfam-next` compile-sha (#19), and the fam-ledger / quorum bug fixes
(#20, #21).

# Phase 1 — Forge-backed MVP ✅ complete

`botfam-next` runs the full forge-backed loop for every agent with per-agent
auth.

- #2 Forgejo-in-Docker substrate — shipped (#13)
- #3 Forge port + status poster — `forge.Client` shipped (#13); the custom
  status poster is superseded by go-native (#33)
- #6 merge-gate auto-resolves the fam ledger — shipped (#14)
- #4 `git-credential-botfam` push helper — shipped (#28)
- #5 `forge-setup.sh` onboarding — shipped (#29)

# Phase 2 — Native-forge cleanup

Goal: collapse ccrep-over-forge onto native Gitea branch protection; delete the
duplicative Engine/ledger/bang-line gate machinery. Keep `forge.Client` + the
helper tools.

- #9 external-review skill — shipped (#16)
- #26 `botfam worktree register` (repo_paths drift) — shipped (#31)
- **#33 — lean on native branch protection; delete the duplicative gate path**
  (active; agy owns the deletion). Configure native branch protection *before*
  merging the deletion so the gate never disappears.

# Phase 3 — Autonomy & ergonomics

Goal: agents notice queued work natively, without polling IRC or scheduled
tasks.

## #17 — native botfam-next wait for PR comment / review request

Re-scoped under go-native: the merge gate is now native, so this is purely
*reviewer-wake* (notify an agent when it's requested as a reviewer), not
gating. Lower priority.

# Phase 4 — Validation

Goal: prove the forge-backed loop end-to-end before retiring the IRC substrate.

## #10 — ccrep-integration-test-v1 (two-tier)

Re-scoped under go-native: no custom Engine/ledger/bang-line layer to exercise
— test forge-backed propose → review → (native gate) → merge, plus the helper
tools.

# Phase 5 — Operator front-end

Goal: a slimmer human UX over the forge-backed fam.

## #11 — botfam-server-v1 (auth/UI front-end)

Re-scoped under go-native: the server's merge-gate/webhook role is dropped
(native branch protection handles gating); remaining value is the auth/UI
front-end (`botfam login` + token-file management). Lowest priority.
