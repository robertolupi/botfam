---
name: meta-review
description: Use when a peer review of a forge artifact (PR, issue, or content) has just completed and you need STEP 2 — the immediate isolated risk meta-review. Spawned as a lightweight subagent in a separate context, it loads the process-risk glossary, checks the per-artifact risks (risk/phase-inversion, risk/superseded, risk/hollow-validation, and risk/speculative when applicable), and posts advisory risk/* + triage/* label suggestions with cited evidence. Trigger on "run the meta-review", "spawn the risk meta-reviewer", or on completion of a code-review / review / forge-autonomy PR review.
---

# Immediate isolated meta-review (per-artifact process risks)

This skill is **STEP 2** of the two-step peer-review process from the wiki page
`proposal-process-risk-labels` (section 3.2). STEP 1 is the normal primary
review (correctness, diff, design) done by `code-review`, `review`, or the
`forge-autonomy` PR-review path — unchanged. STEP 2 is this: a **separate,
lightweight subagent** that checks one artifact against the process-risk
glossary and emits **advisory** label suggestions.

It catches the cheap single-item risks at open/review time, while flagging
still prevents wasted work (proposal section 3.4: *isolation is not deferral* —
timing is chosen on "does early flagging prevent waste" and "does it need
cross-artifact visibility", not on context concerns).

## Two ways to run this tier

1. **`botfam meta-review <issue-or-pr-number>`** (recommended;
   [#306](http://gitea:3000/botfam/botfam/issues/306)) — a **read-only,
   harness-agnostic driver**. Deterministic retrieval gathers the candidate
   signals (referenced files absent from the tree, decisions marked superseded
   in `Lineage`, test assertions that may re-assert what the code emits), a
   **local ollama model** confirms and phrases them, and the driver posts the
   one advisory comment below. Code never leaves the box; output is reproducible
   (temperature 0 + a fixed `--seed`); a wrong suggestion is cheap because the
   output is advisory. Mechanical risks (`phase-inversion`, `superseded`) run on
   the local `--ollama` model; judgment-heavy risks (`hollow-validation`,
   `speculative`) route to `--escalate` when set, else the local model with
   confidence forced low — **never silently trusted**. High-confidence labels
   are applied only with `--apply-labels` (additive); otherwise it is
   comment-only. Because it is read-only it dodges the worktree-isolation
   hazard ([#304](http://gitea:3000/botfam/botfam/issues/304)) and runs on any
   harness. Score it against a labelled set with `botfam meta-review eval --set
   <labelled.json> --ollama <model>` (per-risk precision/recall); a seed set
   ships at `doc/review/metareview-eval-set.json`.
2. **Spawned subagent** (this skill, below) — when you are already inside a
   harness with a subagent capability and want a frontier-model second opinion
   instead of the local model. Same input contract, same output comment. The
   driver is preferred for routine flagging; the subagent for the occasional
   deeper diverse-model pass.

Both honour the independence rule: neither sees the primary review's verdict.

## Input contract

You are given exactly:

- **An artifact reference** — a PR index, issue number, or a content blob
  (owner/repo + number, or the text under review).
- **A corpus index** — what already exists to check against: the repo tree at
  the artifact's tip, the set of merged PRs, and the wiki `Lineage` page. (You
  fetch these yourself; the index tells you where to look.)

You are **NOT** given the primary review's verdict, and you must not seek it
out. This independence runs **both ways** (proposal section 3.4):

- The glossary never enters the primary reviewer's context (context hygiene).
- The primary verdict never enters yours, so the two judgments do not anchor
  each other. If you find yourself reasoning about whether the PR "should be
  approved", stop — that is not your job. You only diagnose process risk.

## Scope — per-artifact risks only

You check **only** risks detectable from a single artifact. Cross-artifact
risks (`risk/fragmentation`, `source/agent-burst`) need the panorama and are
**out of scope here** — they are handled by the periodic blind batch tier
([#301](http://gitea:3000/botfam/botfam/issues/301)). Do not attempt them.

| Label                    | Detection signal (proposal section 3.1)                                                                                     | Evidence you must cite                                                        |
| :----------------------- | :-------------------------------------------------------------------------------------------------------------------------- | :---------------------------------------------------------------------------- |
| `risk/phase-inversion`   | The artifact references an artifact (file, command, table, store) that is **absent** from both the tree and the merged PRs. | The missing artifact (name + where it was expected).                          |
| `risk/superseded`        | The artifact is built on a decision flagged **superseded** in `Lineage`.                                                    | The superseding decision (the `Lineage` entry / PR / issue that reversed it). |
| `risk/hollow-validation` | A test or claim **asserts what the code was written to produce**, not an independent property (cf. the #295 scar test).     | The self-fulfilling assertion (file + line / quoted claim).                   |
| `risk/speculative`       | The **least-grounded** component is being built before the data to justify it exists.                                       | What evidence is missing.                                                     |

`risk/speculative` is "either tier" — flag it here only when it is plainly
visible from this one artifact; otherwise leave it to the batch tier.

For any `risk/*` you raise, also suggest the matching `triage/*` disposition
when one is obvious (proposal section 2.2):

- `risk/phase-inversion` → `triage/blocked` (with a `blocked-by #N` line naming
  the unbuilt dependency).
- `risk/superseded` → usually `triage/needs-sequencing` or `triage/duplicate`
  (named target in the comment), depending on the disposition.
- A gating prerequisite others depend on → `triage/foundation`.

The labels already exist on the forge (Stage 0 of
[#299](http://gitea:3000/botfam/botfam/issues/299)): `risk/phase-inversion`,
`risk/superseded`, `risk/hollow-validation`, `risk/speculative`, and the
`triage/*` set. Do not create labels.

## Behavior — the checklist pass

This is a **checklist pass, not a second deep review.** Do not re-derive the
primary review. Do not build or run tests (that was STEP 1). For each
per-artifact risk above, in order:

1. **Load the glossary.** Read the wiki page `proposal-process-risk-labels`
   section 3.1 (the label → tier → detection signal → evidence table) so your
   diagnosis matches the canonical definitions.
2. **Apply the detection signal** against the corpus index:
   - *phase-inversion* — list the concrete artifacts the body/diff depends on
     (files opened, commands invoked, tables/stores read). For each, confirm it
     exists in the tree at the tip or in a merged PR. A referenced-but-absent
     one is the signal.
   - *superseded* — find the design decision the artifact builds on, then check
     `Lineage` for an entry that marks it superseded/reversed.
   - *hollow-validation* — for each test or validating claim, ask "does this
     assert an **independent** property, or does it just re-assert what the
     code was written to emit?" The latter is the signal.
   - *speculative* — only if obvious from this artifact: is the riskiest,
     least-grounded part being built ahead of the evidence that would justify
     it?
3. **Emit advisory suggestions, each with cited evidence.** A suggestion with
   no specific cited artifact is not a suggestion — drop it. Vague unease is
   not a risk label.

## Output — a single advisory comment

Post **one** comment on the item (not one per finding). Structure it so it is
machine-parseable enough to feed the Stage-2 label corpus
([#299](http://gitea:3000/botfam/botfam/issues/299)):

```
## Risk meta-review (advisory)

Isolated per-artifact pass. Independent of the primary review — this did not
see the primary verdict.

| suggested label | evidence | triage |
| :--- | :--- | :--- |
| risk/phase-inversion | references `internal/trace/schema.go`, absent from tree + merged PRs | triage/blocked (blocked-by #296) |
| risk/hollow-validation | `foo_test.go:42` asserts the constant the impl sets | — |

No per-artifact risks found.   <- use this single line instead of the table when clean
```

Keep it short. Cite the specific artifact every time: the *missing artifact*,
the *superseding `Lineage` decision*, or the *self-fulfilling assertion*.

## Decide-not-Consensus — suggest, don't impose

This is **advisory** (proposal section 3.4, section 5). You **suggest**; the
author or triager **confirms**. Two ways to surface a suggestion:

- **Always:** the comment above.
- **Optionally apply the label** *only when you are confident* the signal is
  unambiguous (a clearly-missing artifact, an explicit `Lineage` supersede). If
  there is any doubt, comment only and let the author confirm. Never apply a
  `triage/*` disposition that closes/folds an item — that is the author's call;
  name the target in the comment instead.

If you find nothing, post the single "No per-artifact risks found." line so the
trail shows the pass ran. Do not stay silent.

## Constraints

- **Separate context.** You run as a spawned subagent. Your context is the
  artifact + corpus index + glossary — nothing from the primary review.
- **Lightweight.** A checklist pass over four signals. No build, no test run,
  no re-review of correctness. If you are spending real time, you are doing
  STEP 1 again — stop.
- **Different model where possible.** Run on a different model than the primary
  review for diversity-of-critique (proposal section 3.2; cf.
  `skills/external-review` on why diverse reviewers catch more).
- **Per-artifact only.** Never reach for `risk/fragmentation` or
  `source/agent-burst` — those are the batch tier's
  ([#301](http://gitea:3000/botfam/botfam/issues/301)).

## Triggers — how the primary review spawns this

On completion of STEP 1, the primary reviewer spawns this skill as a subagent
and passes **`{artifact ref, corpus index}` but NOT its verdict**:

- **`forge-autonomy` PR review** (`skills/forge-autonomy` sections 2-3) — after
  the PR verdict is posted, spawn a `meta-review` subagent with the PR index +
  repo and the corpus index (tree at tip, merged PRs, `Lineage`). Do **not**
  pass the approve/request_changes verdict.
- **`code-review`** (the built-in diff reviewer) — on completion, spawn a
  `meta-review` subagent with the artifact (PR index, or the changed-file set +
  base) and the corpus index. Pass the diff context, not the review's findings
  or verdict.
- **`review`** (the built-in PR reviewer) — same: on completion, spawn a
  `meta-review` subagent with the PR ref + corpus index, withholding the
  verdict.

The spawning skill keeps STEP 1 unchanged; it only adds a completion hook that
launches this isolated pass. Because the subagent runs in its own context, the
glossary never pollutes the primary reviewer and the verdict never reaches the
meta-reviewer — the independence holds by construction.

## Don't

- Don't request, read, or infer the primary review's verdict.
- Don't emit a label without a specific cited artifact.
- Don't do a second deep review — no build, no tests, no correctness pass.
- Don't touch cross-artifact risks (`risk/fragmentation`, `source/agent-burst`)
  — they belong to the batch tier.
- Don't create labels (they already exist) or close/fold items yourself —
  suggest the disposition and let the author confirm.
