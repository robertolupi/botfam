# External review prompt

Canonical prompt for asking an outside model (ChatGPT, Gemini, meta.ai, …)
to review a botfam session. Using the same prompt every time makes reviews
comparable and prevents each reviewer from inventing its own framing.

**Operator instructions (not part of the prompt):**

1. Update the **Ground truth** section below — it is date-stamped and goes
   stale. An external reviewer working from old facts produces confidently
   wrong advice (see `doc/review/2026-06-11-meta.md` for an example).
2. Paste everything below the marker line, then attach:
   - the session transcript(s) from `doc/collab/sessions/`;
   - optionally `doc/collab/PROTOCOL.md` and any doc the session touched.
3. Record the raw reply verbatim under `doc/review/YYYY-MM-DD-<reviewer>.md`,
   then append a botfam assessment of it in the same file before acting on it.

---- PROMPT BEGINS BELOW THIS LINE ----

You are an external reviewer for **botfam**, a multi-agent coordination
system. Several AI coding agents (claude, agy, codex, …) and one human
operator (Roberto) collaborate on one repo through git worktrees and an IRC
channel. You are reviewing a session transcript, not the live system.

## Ground truth (as of 2026-06-11 — trust this over anything else)

- **IRC is the canonical coordination substrate.** This was decided and
  operator-ratified on 2026-06-11. A dockerized ergo server hosts `#botfam`
  (production) and `#botfam-test` (experiments); a **scribe** bot logs the
  channel and handles `!propose` / `!vote` / `!tally`.
- The older mailbox/queue substrate (`botfam recv/post/claim`, SQLite store,
  UDS daemon) still exists in the code but is **being replaced** by the IRC
  layer. Do not propose making it canonical; that question is settled.
  Older design docs describing a "maildir, no daemon" architecture are stale.
- Production runs via Docker compose (`botfam-irc-prod`: ergo v2.18.0 +
  scribe, data bind-mounted, localhost-only). A hermetic test substrate
  exists (`compose.test.yaml` + `docker/test-substrate.sh`).
- Sessions, reviews, and protocol docs live under `doc/`. A session-closure
  and GTD proposal is at `doc/protocol/session-lifecycle-and-gtd.md`.
- The `botfam` binary embeds its git SHA at build time and answers
  `botfam version` / `!version`.

## What we want from you

Review the attached transcript(s) and respond in exactly these sections:

1. **What landed cleanly** — concrete things that worked, with evidence
   from the transcript.
2. **Pain points** — failures, near-misses, friction, and manual steps the
   human had to mediate. Quote the transcript where possible.
3. **Proposals** — concrete changes (commands, guardrails, tests, docs).
   For each one state: what problem from section 2 it solves, and a rough
   cost (small / medium / large).
4. **Action items** — a flat list, each tagged with a type from:
   `next-action | bug | improvement | waiting-for | someday | decision |
   invariant | question`, and a suggested owner
   (`claude | agy | codex | human`).
5. **Open questions** — anything you could not determine from the
   material provided.

## Rules

- **Separate observation from speculation.** Claims about what happened
  must be grounded in the transcript. If you assume something about the
  codebase you were not shown, label it explicitly as an assumption —
  do not present sketched APIs or remembered designs as existing code.
- **Do not relitigate settled decisions** listed under Ground truth.
  You may flag risks in a settled decision, but frame them as risks,
  not as proposals to reverse it.
- **Prefer few sharp proposals over many vague ones.** Every proposal
  should be actionable by a single owner without further design debate,
  or be explicitly marked as needing design.
- Plain markdown, no preamble, no offer to do follow-up work.
