# botfam — Phase 2: CCREP (quality ratchet)

> [!NOTE]
> **Status**: Superseded/Evolved into the IRC-first consensus layer (2026-06-11).
> The standalone `ledger.jsonl` file under CCREP has been replaced by the total-ordered `#ccrep` IRC channel, which is durably logged by the `scribe` bot to `history.jsonl`. Consensus tallies are computed dynamically from the bang-verbs (`!propose`, `!evaluate`, `!vote`, `!revision`, `!executed`) sent over IRC.

Status: **Future / Phase 2** · depends on Phase 1 ([DESIGN.md](DESIGN.md)) shipping first

> **Implementation Note**: A slice of CCREP (the `merge-gate` consensus gate check) has been pulled forward and implemented in Phase 1 to enforce review and merge integrity before the full event ledger is built.
> See `botfam merge-gate --help`.

CCREP (Collaborative Code Review & Evaluation Protocol) is botfam's **second
layer**: the quality ratchet. Phase 1 (`collab`) lets agents *coordinate* — talk,
hand off, queue work. CCREP lets them make one specific artifact *provably
better*: submit it, evaluate it in isolation, gather admissible peer critiques,
and merge only when a consensus gate passes. The two compose — coordinate over
`collab`, ratchet with CCREP — and are deliberately separate so the heavy
machinery never sits in the messaging hot path.

This is the proven deep-cuts `tools/ccrep` design (see lineage
[v0](lineage/v0-deep-cuts.md)), re-expressed for botfam: Go, a JSONL ledger
quarantined to this layer, no SQLite, no daemon.

---

## 1. Why it is a separate layer (the lesson from v1)

hydra's mistake ([v1](lineage/v1-hydra.md)) was folding coordination *into* the
consensus ledger, so even "your turn" paid hash-chain + CAS costs. botfam keeps
them apart:

- **`collab` (Phase 1):** pure maildir renames. No ledger. The hot path.
- **CCREP (Phase 2):** an append-only event ledger + a pure reducer that derives
  consensus. Touched only when an artifact must be *proven*, not merely agreed on.

CCREP runs as a **second stdio MCP server from the same binary** (`botfam ccrep`),
registered alongside `collab` in `.mcp.json`. You can ship and run Phase 1 with no
CCREP at all.

## 2. Self-hosting plan (the point of building it second)

1. **Build & bootstrap Phase 1** by hand — normal development.
2. **Dogfood `collab`:** the agents building CCREP coordinate *over botfam's own
   collab layer* — `post`/`claim` the build tasks, hand off, block on `recv`.
3. **Build CCREP under that coordination.**
4. **Self-hosting milestone:** CCREP ratchets *itself* — its design docs land via
   the `design_doc` profile, then its code via `code_change`. The first real
   proposal CCREP gates is one of its own. From then on botfam improves through
   its own gate.

## 3. State model

Under the fam root (so the whole fam shares it):

```
~/.botfam/<fam>/ccrep/ledger.jsonl     append-only event log (flock on append)
~/.botfam/<fam>/ccrep/eval-cache/      content-addressed evaluation results
```

- **Ledger:** one JSON event per line — `proposal_submitted`,
  `evaluation_completed`, `critique_submitted`, `revision_submitted`,
  `merge_recorded`, `proposal_abandoned`. Append under `flock`; JSONL keeps botfam
  dependency-free (no SQLite/cgo). Hash-chaining the events (as hydra did) is
  optional and can be added without changing the tool surface.
- **Consensus is *derived*, never written.** A pure reducer folds the log into
  `ConsensusState` for a proposal. Agents produce evidence; the reducer computes
  the verdict. Never assert consensus — let the fold decide.

## 4. Artifact profiles

`submit_proposal` carries an `artifact_profile` that selects the eval suite/gate:

| Profile | Use for | Automated gate |
|---|---|---|
| `code_change` | independent development | build + test + lint + fmt; no golden-metric regression |
| `code_review` | reviewing an existing/external diff | build + test on head; deliverable is the critique set + verdict |
| `design_doc` | proposals & docs | link-check + structural lint + provenance **warnings**; no metric/AST gates |

(botfam's own `doc/` lands via `design_doc` — step 4 of the self-hosting plan.)

## 5. Tools

| Need | Tool |
|---|---|
| Take a review/build task | `claim_task` *(or use `collab`'s task queue)* |
| Propose a change on a branch, resolved to an immutable `commit_sha` | `submit_proposal(task_id, ref, artifact_profile)` |
| Run the profile's suite in an isolated worktree → `EvaluationReport` | `run_evaluation(proposal)` |
| File a structured finding against the exact commit | `submit_critique(proposal, severity, finding, evidence)` |
| Address blocking findings on a new commit (expires prior approvals) | `submit_revision(proposal, ref)` |
| Read the derived gate state | `compute_consensus(proposal)` |
| Merge (human-gated for sensitive categories) | `merge_proposal(proposal)` |

## 6. The gate (Phase 2 scope)

A proposal merges only when **all** hold:

1. **Automated checks green** — the profile's suite passes; for `code_change`, no
   golden-metric regression.
2. **One independent approval** — from a reviewer who is **not** the author,
   **preferably a different model family**. The author may explain or amend but
   can never satisfy the quorum.
3. **No open blocking critiques.**

**Any new commit expires prior approvals** (re-review the thing that actually
exists). This is intentionally the *whole* gate for Phase 2.

## 7. Admissible critique (the judgment the server can't supply)

The schema forces structure; only a reviewer supplies substance. A finding
**blocks merge only if it is specific + actionable + evidence-linked +
severity-classified**:

- **Evidence-linked** — cite a `file:line` (or eval metric); the server verifies
  the link *resolves at the proposed `commit_sha`*. A dead link is rejected as
  malformed before review.
- **Specific + actionable** — name what is wrong and what resolves it. "Feels too
  complex" is inadmissible; "`store.go:88` double-counts a self-approve — skip
  `actor == author`" is admissible.
- **Severity-classified** — only `blocking` findings gate; lower severities inform.

Inadmissible critiques don't block — so vibes can't stop a merge, and noise is cheap to ignore.

## 8. Human-gated merges

`merge_proposal` refuses, pending explicit human confirmation, for: **public API
change**, **destructive migration**, **model-or-dataset change**, **large
architecture change**. Surface these to the operator; agents never self-merge them.

**Detection is rule-based, not self-declared.** These categories are triggered by
inspectable rules — primarily path/pattern matches on the diff (e.g. changes under
`api/`, `migrations/`, `models/`, or to exported symbols) — **not** by the proposing
agent's own classification, which a buggy or adversarial agent could understate to
slip the gate. Self-declaration may only *add* a gate, never *remove* one. Richer
enforcement (e.g. diffing the change against the approved design's AST) is a
later-phase concern (§10); Phase 2 ships the path-rule gate plus the human backstop.

## 9. Executor & eval cache

`run_evaluation` shells out: `git worktree add` a detached checkout of the exact
commit, run the profile's suite, capture the `EvaluationReport`, then
`git worktree remove --force`. Results are cached by a content hash of
`(commit_sha, suite, dataset, env)` so an unchanged input is never re-run.

## 10. Explicitly deferred (later phases)

Not in Phase 2 — do not expect: AST/line revision-budget gates; plateau /
edit-war auto-escalation; any voting math (Kendall's W, Schulze/Condorcet,
log-odds weighting); weighted quorum; reviewer routing. The gate is exactly the
three conditions in §6. If a loop churns without converging, escalate to the
operator by hand.

## 11. Composition with Phase 1

CCREP does not replace the collab task queue — it sits above it. Typical flow:
an agent `collab/post`s "review proposal X"; a peer `collab/claim`s it,
`run_evaluation` + `submit_critique` over CCREP, then `collab/send`s the handoff
back. Coordination stays in maildir; only the artifact verdict touches the ledger.
