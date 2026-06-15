---
name: submitting-a-pr
description: Use right before opening or submitting a pull request on the forge — the pre-PR self-check gate. Screens the diff for the cheap, mechanical process risks (single-artifact concept-fragmentation against the existing tree, phase-inversion, superseded decisions) by reading the matching process-risk glossary / antipattern wiki page if you haven't this session, and escalates the harder calls (hollow-validation, non-obvious speculative) to the meta-review. Trigger on "open a PR", "submit a PR", "ready to push the PR", "pre-PR review", or as the final gate in forge-autonomy / botfam-sprint before pull_request_write.
---

# Pre-PR self-check (catch process risks before they cost a review)

Opening a PR turns ephemeral working-tree work into a durable, outward-facing
artifact others will spend attention on. This skill is the **cheapest place to
catch a process risk** — in your own context, before the PR exists. It is the
author-side, shift-left twin of `meta-review`, which is the **isolated
post-review backstop**.

It is **not** a correctness review (that is `code-review` / the primary
reviewer's STEP 1) and **not** a re-implementation pass. It is a short checklist
against the process-risk glossary, run once, right before submission.

## When to run

Immediately before `pull_request_write` / `gh pr create` — the last gate in the
normal PR flow, including the `forge-autonomy` and `botfam-sprint` paths. Run it
on the **diff you are about to submit**, at your branch tip.

## The checklist — mechanical risks, cheap and inline

For each risk below, detect the smell against your diff **+ the tree at your
tip** (the corpus). If the smell is present, **read the glossary page if you
have not this session** (`proposal-process-risk-labels` §3.1; plus
`antipattern-concept-fragmentation` for the first), apply its Detection, and
either **clear it with a cited fact or fix it before submitting.**

1. **concept-fragmentation (single-artifact form).** Am I adding a new
   implementation / path / helper for a concept that **already has a canonical
   home in the tree**? Smell: "I reimplemented X from scratch" when X exists —
   e.g. a new fam resolver when `famconfig.ResolveFam` already resolves. → fold
   into the existing home, or justify in the PR body why a new path is warranted.
   (The cross-backlog *panorama* form stays with the batch tier; this is the
   "duplicates something already in the tree" form, which is visible from one
   diff.)
2. **phase-inversion.** Does my change **reference an artifact that does not
   exist yet** — a file, command, table, or store absent from both the tree and
   any merged PR? → name the dependency and block on it (`blocked-by #N`) rather
   than shipping a reference to vapor.
3. **superseded.** Is my change **built on a decision `Lineage` marks
   superseded**? → re-sequence onto the superseding decision instead.
4. **speculative (only if obvious).** Am I building the **least-grounded** part
   before the evidence that would justify it exists? → if plainly visible from
   this one diff, hold or descope; otherwise leave it to the batch tier.

A smell you can clear with a specific cited fact is cleared — vague unease is not
a risk. Conversely, do not wave a *real* smell through: if you cannot clear it,
fix it or escalate.

## Escalate the hard calls — don't self-certify them

You are the **worst reviewer of your own work**; the inline pass catches the
obvious and will miss what needs cold eyes. Escalate to the **isolated**
`meta-review` (a separate context, not your motivated one) when:

- **hollow-validation is plausible** — your tests look like they assert **what
  the code was written to emit** rather than an independent property. The cheap
  pass cannot tell this apart from a legitimate literal assertion; it is
  **escalate-only by design**. Always route this to the meta-review, never
  self-certify it.
- A mechanical smell (1–4) is present but you **cannot confidently clear it** —
  hand it to cold eyes rather than rationalizing past it.

Escalation = run `botfam meta-review <pr>` (open the PR as a **draft** first,
then run the read-only driver), or spawn the `meta-review` subagent on your
branch/diff pre-submission. Either path runs **independent of any verdict** — see
`skills/meta-review`.

## Then submit

If the inline checks are clear (or cleared) and any escalation came back clean,
open the PR through the normal path. Record in the PR body any risk you
considered and cleared, with the evidence — it spares the primary reviewer and
the post-review meta-review from re-deriving it.

## Don't

- Don't turn this into a correctness review — that is STEP 1 (`code-review` / the
  primary reviewer). No build, no test-authoring here.
- Don't self-certify hollow-validation — escalate it.
- Don't emit or apply `risk/*` labels yourself from this gate — labeling is the
  meta-review's advisory job; here you **fix or escalate** before submitting.
- Don't reach for cross-artifact risks (`risk/fragmentation` panorama,
  `source/agent-burst`) — those are the batch tier's
  ([#301](http://gitea:3000/botfam/botfam/issues/301)).
- Don't block your own PR on vague unease — a risk needs a cited artifact.
