---
authors:
  - claude
  - rlupi
kind: proposal
status: Implemented
created: 2026-06-13
proposal-id: forge-backing-v1
executor: TBD
quorum: majority
deadline: none
---

# Proposal: Forge-as-backing (self-hosted Forgejo) + green/blue migration

> [!NOTE]
> **Status**: Implemented (2026-06-13). This proposal for the Gitea/Forgejo
> self-hosted forge integration has been fully implemented and serves as the
> current consensus backing layer.

- **Participants:**
  - Roberto Lupi (Operator) — direction, hosting, identity
  - Claude (Agent, `wt-claude`) — this draft
- **Scope:** back botfam coordination on a self-hosted **forge** (Forgejo in
  Docker) instead of the bespoke IRC-scribe substrate, and migrate via a
  green/blue split behind the `internal/ccrep` interfaces.

______________________________________________________________________

## 1. The unlock

The scribe + bang-format + `merge-gate` substrate exists for one **primary**
reason: **we could not provision a forge programmatically.** We had IRC and
bots, so we built consensus on top of them. (Not the *only* reason —
local-first operation and IRC's real-time channel matter too — but the forge
gap is what forced the bespoke consensus layer.)

Self-hosting **Forgejo in Docker** removes that constraint. `app.ini`
config-as-code + the admin API let a script create the org/repo, bot and human
users, scoped API tokens, branch protection, and webhooks. The reason the
bespoke substrate existed is gone — so the question becomes whether the forge
should *be* the substrate.

## 2. What a forge backs — and the line it does not cross

A forge provides, with a real implementation + UI + audit, most of the
coordination *artifacts* we have been hand-building:

| botfam concept                               | Forge primitive                                       |
| -------------------------------------------- | ----------------------------------------------------- |
| `propose`                                    | open a pull request                                   |
| `revise`                                     | push a new commit to the PR branch                    |
| `vote` (approve / request_changes / comment) | PR **review** (the same verbs)                        |
| `tally` / quorum                             | review state + branch-protection "required approvals" |
| `gate`                                       | the PR mergeable check                                |
| `merge` + `executed`                         | merge the PR (API/UI)                                 |
| tasks (bucket D), `#cross` issues            | issues (cross-org references)                         |
| proposals                                    | PRs and/or the wiki                                   |
| human/bot identity + auth                    | forge **users + scoped API tokens** (out-of-process)  |
| notifications                                | webhooks                                              |

**But a forge does not subsume the *decision rule*.** Branch-protection "N
required approvals" cannot express botfam's quorum — presence-aware,
role-tiered, with the human-guard ladder — nor executor, deadline, abstention,
or approvals-die-on-new-commit; and the PR-review model is lossy (a comment is
not a vote; `request_changes` is not a clean reject). So the **Engine keeps the
decision rule**: the `Ledger` port reads PR/review/label state and folds it
with *our* tally logic, with vote semantics encoded in PR labels/metadata. The
forge is a **backing store + review surface + identity + host** — not the
consensus authority. The lossy rows above (`vote`→review,
`tally`→branch-protection) are *recorded* on the forge but *decided* by the
Engine. For that to hold, branch protection must restrict merge to **a
consensus-verifying merge-bot (or the operator)** — otherwise anyone clicking
"merge" in the forge UI would bypass quorum (§9 Phase B tests this).

This lets the forge back the scribe, bang-format, and `merge-gate` *roles*
while the consensus logic stays ours. *(All four red-team models converged on
this distinction — it is the load-bearing correction to an earlier "forge
subsumes ccrep" framing.)*

> [!IMPORTANT]
> **Update (2026-06-13) — go-native supersedes this for a fixed-count quorum.**
> In practice most of the "decision rule" maps onto branch protection after
> all: *approvals-die-on-new-commit* = "dismiss stale approvals"; *executor /
> no UI-merge bypass* = "restrict pushes" + required approvals (only a
> consensus-satisfying PR can merge, so no merge-bot is needed); *roster
> restriction* = "restrict approvals to a team"; author-exclusion is automatic.
> So for a **fixed-count quorum, native branch protection _is_ ccrep**, and the
> custom Engine / Ledger / bang-line gate was duplication — deleted in #34 (per
> #33). **Role tiers are native too** — different per-branch protection rules
> (e.g. `main` stricter than `botfam-next`) give tiered approval without a line
> of code. See the Gitea milestones (Phase 2) and issue #33.

## 3. Forgejo vs Gitea

**Recommendation: Forgejo**, behind a fork-neutral `Forge` abstraction.

- **Federation** (ForgeFed/ActivityPub) is on Forgejo's roadmap and maps onto
  the cross-fam (`#cross`) future — federated forges = cross-fam coordination
  across hosts without a central server. (Verify current maturity during the
  spike.)
- **Governance**: Forgejo is stewarded by a non-profit (Codeberg e.V.); the
  fork happened because Gitea moved under a for-profit holding the trademark.
  Better footing for a tool we self-host long-term.
- **Compatibility**: Forgejo still speaks the Gitea `/api/v1` and works with
  the Gitea Go SDK (`code.gitea.io/sdk/gitea`), so the port impl is the same
  and the choice is reversible.

Caveats: Forgejo is now a hard fork, so long-term Gitea-API parity isn't
guaranteed (pin to Forgejo's API docs); Gitea has more mature Actions/CI (we
don't need CI for v1). We build the `Forge` port against the **shared API
subset** so "Forgejo vs Gitea" stays a config + image choice.

## 4. What survives — the ports pay off

The `internal/ccrep` core (Engine + `Transport`/`Ledger`/`VersionControl`
ports) is **backing-agnostic**. A forge backing is a new set of port
implementations, not a rewrite:

- `VersionControl` → git (unchanged).
- `Ledger` → reads PR/review/issue state via the forge API (replaces the
  scribe-ledger fold).
- `Transport` → "open PR / push / post review / merge" via the forge API
  (replaces IRC bang lines).
- The Engine and the CLI / MCP / thin-UI adapters are unchanged.
- The forge ports can be backed by a **`gitea-mcp-server`** (native MCP tools
  for Forgejo PRs/issues/reviews) rather than hand-rolled API clients — less
  custom code, and directly inspectable when state desyncs.

The ports&adapters design — and the ccrep verb set we just ratified — were the
right call precisely because they make this swap cheap.

## 5. Stable + dev channels: `botfam` and `botfam-next`

Green/blue here is a **release-channel** split of **one codebase**
(`cmd/botfam`) — not two packages, and not two permanent substrates:

- **`botfam`** (`~/bin/botfam`) — the **stable** channel: built from `main` and
  **frozen** between merges. The known-good fallback.
- **`botfam-next`** (`~/bin/botfam-next`) — the **dev** channel: built from the
  long-lived **`botfam-next` git branch** where we iterate. Same source, newer
  code. `fam.toml` (read by both executables) points the fam's working branch
  at `botfam-next`.
- **Agents prefer `botfam-next`, and can run *both* to assert semantic
  equivalence** — a built-in differential check: the dev build must match
  stable except for the change under development — **falling back to stable
  `botfam` if the dev build breaks.**
- **`botfam` is never retired.** When `botfam-next` merges to `main`, the
  stable executable is **rebuilt** to the new `main` and dev continues on the
  branch. `botfam-next` is the perpetual dev branch; the stable channel just
  advances at each merge.
- **One authoritative substrate per item.** While a cycle's change spans both
  channels, a given proposal/issue lives in exactly one backing, never both —
  no cross-substrate references — which removes the split-brain risk all four
  reviewers flagged.

This channel split is the **general iteration vehicle** (it generalizes beyond
this doc). The forge backing here is simply the **current payload** on the dev
channel: `botfam-next` wires the forge ports and is cross-checked against the
IRC/scribe-backed stable `botfam` until it merges.

## 6. What stays on IRC

IRC remains the **real-time** layer: chat, presence, and the wake watcher that
drives agents. The forge is the **durable** layer: review, consensus, tracking.
A forge→IRC **webhook bridge** posts events (PR opened, review submitted,
merged) into the channel for awareness. Complementary, not redundant.

**Bridge contract:** webhook delivery is at-least-once, so each event carries a
stable id and the bridge dedups on it (post once). The bridge is
**awareness-only — never a wake/act trigger** (that would risk a forge↔IRC
feedback loop), and on a detected gap (dropped or reordered event) it
reconciles by *polling forge state*, not by trusting the stream. The forge —
not the bridge — is the source of truth. The bridge runs as a lightweight
service inside the compose project (alongside Forgejo), so the `botfam-next`
runtime stays cleanly packaged.

**Audit:** forge comments, reviews, and labels are *mutable*; the IRC scribe
log is append-only. Because the bridge posts every ccrep action into the
channel where the scribe logs it, we get a durable, append-only **audit
mirror** of the forge's mutable state for free — no new component (the existing
scribe suffices).

## 7. Identity & auth (resolves the bearer-token thread)

Forge **users + scoped API tokens** are the real, out-of-process human/bot
boundary we sketched earlier: a token authenticates to the forge and the forge
enforces — an agent cannot forge another user's identity. "Fold the operator's
approve+merge for quorum=majority" becomes: a forge review by the human user
satisfies branch protection *and* carries merge authority, in one act. This
also gives the operator the registered identity they currently lack on IRC
([IRC-OPS.md](../collab/IRC-OPS.md): `rlupi` has no NickServ account).

**Token lifecycle (must be specified before this is load-bearing):** bot tokens
live **outside the repo and the worktree** at `~/.botfam/token-<fam>-<actor>`
(mirroring the NickServ pass-file convention), least-privilege scoped per fam,
with rotation, revocation, and an admin recovery path. Use **long-lived
tokens** (12h+, matching human-developer cadence) and **bubble any runtime API
auth error to the channel** — a startup-only check is too weak, since a token
expiring mid-execution would otherwise *silently freeze* `botfam-next`.

**Auth is a `botfam login` flow** (specified separately as the sibling
`botfam-server-v1`): a per-repo server issues an OAuth-style or simple bearer
token, and `botfam login` persists it to the `~/.botfam/token-<fam>-<actor>`
file (above) that the CLI and Engine read at runtime. That gives identity
without re-typing and keeps the secret **out of the repo** and out of
*prompt/context* (the binary reads the file; it is never pasted into an agent's
prompt). It is **not** a boundary against a *malicious* agent — anything with
the agent's shell access can `cat` it. The real boundary is server-side: the
forge enforces the token's scope and can revoke it, and identity binds to
whoever authenticated. So the file is a convenience + provenance +
accidental-leak guard, not a vault. This doc depends only on *a* token as the
identity; the login/server/UI front-end is its own proposal.

## 8. Slimmer UI

If the forge provides the issues / PR / review / wiki UI, the planned bubbletea
TUI / web UI collapses to the **botfam-specific glue the forge lacks**: fam and
agent presence, the IRC wake/chat layer, fam orchestration. The forge *is* the
operator UI for everything it covers. That remaining glue — including a
debugging view — lives in the `botfam-server` front-end (sibling proposal), not
in this backing.

## 9. The spike (prerequisite — this doc rests on it)

Before committing, prove the premise in two phases:

**Phase A — provisioning** (proves §1's premise): `docker compose up` Forgejo
with `app.ini` config-as-code; **bootstrap the first admin credential during
compose-up** (e.g. a CLI-created admin + token) so `bootstrap-botfam.sh` can
then run the admin API hands-free — create an org + repo, a bot user and a
human user, scoped API tokens, branch protection with required approvals, and
the webhook → IRC bridge (as a compose service); and **back up + restore** the
instance.

**Phase B — semantic fit + failure recovery** (proves the forge can actually
*host* ccrep, not merely exist): drive a full proposal lifecycle through the
Gitea Go SDK (open → review approve/request_changes → merge); confirm botfam's
quorum/guard semantics survive the mapping (§2); then inject failures — a
dropped webhook, an expired token, a Forgejo restart mid-proposal — and confirm
clean recovery; and confirm the forge **cannot bypass the Engine** — a direct
UI merge before quorum must be blocked by branch protection.

Also confirm Forgejo's current federation status for the `#cross` story.

If **either** phase fails, we stay on the IRC substrate and shelve this.

## 10. Scope, phasing & relation to the roadmap

This **would supersede** (if ratified) much of
[post-pivot-cleanup.md](post-pivot-cleanup.md): the scribe-as-store, the async
task ledger (bucket D), the cross-fam issue mechanism, the read-side resource
projections, and the human-guard auth — a forge *backs* all of them (§2).
`ccrep-mcp-tools-v1`'s **core + adapters survive**; its IRC-scribe port impls
become the "blue" side of the green/blue split.

**MVP scope:** the first deliverable is the §9 spike + a `botfam-next` that
runs one ccrep cycle through forge PRs. The MVP **keeps IRC's current
load-bearing role unchanged** — reducing IRC to a chat/presence substrate is a
*later* phase, not part of the MVP. One step at a time.

**Phasing (post-spike):** (0) decide the fate of the existing scribe ledger —
mirror or archive it so the audit trail is not lost; (1) provisioning-as-code +
the `Forge` port; (2) the **dev-channel build** (`~/bin/botfam-next` from the
`botfam-next` branch) wiring the forge ports + the webhook→IRC bridge; (3)
migrate proposals to PRs; (4) port issues/tasks; (5) *optionally* retire the
`botfam` (IRC-scribe) binary once `botfam-next` is proven — not required. The
auth/UI front-end (`botfam login` + the per-repo server) is tracked separately
as **`botfam-server-v1`**.

## 11. Non-goals

- Not abandoning IRC — it stays the real-time/chat/presence layer.
- Not adopting forge CI/Actions in v1.
- No big-bang cutover — two binaries, parity-gated, reversible; `botfam` need
  not be retired (it stays the fallback).
- `git push` / repo hosting moving to the forge is **in scope** here (origin
  becomes load-bearing again, with a real reason), reversing the interim
  "origin not load-bearing" stance — **gated on a Forgejo ops/outage runbook**
  (backup, restore, upgrade) existing first.
