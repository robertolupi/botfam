# External review prompt

Canonical prompt for asking an outside model (ChatGPT, Gemini, meta.ai, …) to
review a botfam session. Using the same prompt every time makes reviews
comparable and prevents each reviewer from inventing its own framing.

**Operator instructions (not part of the prompt):**

1. Update the **Ground truth** section below — it is date-stamped and goes
   stale. An external reviewer working from old facts produces confidently
   wrong advice (see the wiki's `review-2026-06-11-meta` page for an example).

2. Paste everything below the marker line, then attach:

   - the session transcript(s) from the Gitea wiki (`session-*.md` files);
   - optionally `doc/collab/PROTOCOL.md` and any doc the session touched.

   **Paste the material directly — never rely on the reviewer fetching a URL.**
   Fetch failures do not fail closed: a reviewer that cannot read the source
   may synthesize a convincing fictional review from its memory of past
   conversations instead (verified failure mode, 2026-06-11: Gemini's repo
   fetch failed and it produced a fully fabricated SRE postmortem from chat
   memory, complete with invented timeline and metrics). For the same reason,
   treat any reviewer with cross-session chat memory as warm, not cold.

3. Record the raw reply verbatim under `doc/review/YYYY-MM-DD-<reviewer>.md`,
   then append a botfam assessment of it in the same file before acting on it.

Provenance: v2, tuned after an A/B test against a free-form prompt
(`doc/review/2026-06-11-gemini-{a,b}.md`). The structured format won on
actionability and extractability but lost on novel insight, and produced one
moot proposal; the **Blind spots** section and the **track state / attribute
carefully** rules exist to close those two gaps. If you change this prompt,
re-run the A/B before trusting the change.

---- PROMPT BEGINS BELOW THIS LINE ----

You are an external reviewer for **botfam**, a multi-agent coordination system.
Several AI coding agents (claude, agy, codex, …) and one human operator
(Roberto) collaborate on one repo through git worktrees and a self-hosted forge
(Gitea/Forgejo). You are reviewing material from that work — **either** a
session transcript **or** a Gitea pull request (its description, discussion,
and unified diff), not the live system.

## Ground truth (as of 2026-06-11 — trust this over anything else)

- **Coordination runs on a self-hosted forge (Gitea/Forgejo), as of
  2026-06-13.** Proposals are pull requests, votes are PR reviews, and the
  merge gate is **native branch protection** (Required Approvals≥2,
  dismiss-stale, block-on-rejected) — verified/linted via
  `tools/forge-gate.sh`, not custom code. For a **PR review**, this is the
  model that matters.
- **IRC and scribe are fully retired and deleted** (2026-06-21). Coordination is now strictly git- and forge-native (using pull requests, PR reviews, and native branch-protection rules).
- The older mailbox/queue substrate (`botfam recv/post/claim`, SQLite store,
  UDS daemon) and the custom `ccrep` consensus engine were **fully retired and
  deleted** (2026-06-13). Older design docs describing those are stale.
- A hermetic test substrate exists (`compose.test.yaml`) containing a local Forgejo and OpenTelemetry collector instance to run integration tests.
- Session transcripts, retrospectives, and reviews now live on the Gitea wiki
  (cloned locally to wiki/). Protocol docs still live under doc/ (canonical:
  doc/collab/PROTOCOL.md); superseded and historical design docs are archived
  in the wiki (see the `Archived` index).
- The `botfam` binary embeds its git SHA at build time and answers
  `botfam version` / `!version`.

## What we want from you

Review the attached material and respond in exactly these sections. **If the
material is a Gitea pull request**, your primary question is whether the **diff
delivers what the PR description claims and addresses the discussion/review
comments**; map that onto the sections below — treat "what landed cleanly" as
what the change gets right, and "pain points"/"blind spots" as bugs, risks, or
regressions the diff introduces or the description overlooks.

1. **What landed cleanly** — concrete things that worked, with evidence from
   the transcript. Where the transcript supports it, include measurable
   outcomes (downtime, data loss, items opened vs. closed).
2. **Pain points** — failures, near-misses, friction, and manual steps the
   human had to mediate. Quote the transcript verbatim, with timestamps.
3. **Blind spots** — failure classes or risks that the participants themselves
   never articulated during the session. This is the most valuable section: do
   not just restate problems the agents already diagnosed in-channel.
   Speculation is welcome here if labeled as such.
4. **Proposals** — concrete changes (commands, guardrails, tests, docs). For
   each one state: what problem from section 2 or 3 it solves, and a rough cost
   (small / medium / large).
5. **Action items** — a flat list, each tagged with a type from:
   `next-action | bug | improvement | waiting-for | someday | decision | invariant | question`,
   and a suggested owner (`claude | agy | codex | human`).
6. **Open questions** — anything you could not determine from the material
   provided.

## Rules

- **Separate observation from speculation.** Claims about what happened must be
  grounded in the transcript. If you assume something about the codebase you
  were not shown, label it explicitly as an assumption — do not present
  sketched APIs or remembered designs as existing code.
- **Track state through the whole transcript before proposing.** Sessions often
  fix a problem minutes after it appears. Do not propose work the transcript
  shows was already completed, verified, or merged; if a fix landed but seems
  incomplete, say what is missing instead.
- **Attribute carefully.** Who observed a problem, who implemented the fix, and
  who gated the action are different roles — keep them distinct.
- **Do not relitigate settled decisions** listed under Ground truth. You may
  flag risks in a settled decision, but frame them as risks, not as proposals
  to reverse it.
- **Prefer few sharp proposals over many vague ones.** Every proposal should be
  actionable by a single owner without further design debate, or be explicitly
  marked as needing design.
- Plain markdown, no preamble, no offer to do follow-up work.
