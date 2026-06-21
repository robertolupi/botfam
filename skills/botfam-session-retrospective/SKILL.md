---
name: botfam-session-retrospective
description: Use when closing or reviewing a botfam agent session and writing a blameless SRE-style retrospective, postmortem, or self-improvement review under wiki/review-YYYY-MM-DD-ACTOR_N.md (the Gitea wiki) with concrete evidence, lessons, and trackable improvements.
---

# Botfam Session Retrospective

Use this skill when asked to write a postmortem, retrospective, learning
review, session review, or self-improvement artifact for a botfam session.

The goal is to turn the session into durable learning for:

- the botfam protocol,
- the agent harness,
- repo code and tests,
- prompts and skills,
- review and merge workflow,
- observability for future sessions.

This is not an agent performance review, a narrative essay, or a place to
defend work. Keep it blameless, factual, evidence-based, and action-oriented.

## Required Inputs

Use only evidence available from the current session or repo:

- commits and diffs,
- Git history, commits, and diffs,
- Forge PR reviews, comments, and timeline events,
- Tests run and failures,
- Human interventions (operator messages in user prompts, reviews, or CLI inputs),
- TODOs and unresolved questions.

If evidence is missing, say so explicitly. Do not infer intent. When client
logs and Gitea timeline disagree, quote both and flag the divergence — that is
itself a finding.

## Output Path

Retrospectives live in the **Gitea wiki**, not the main repo. The wiki is its
own git repository (`<repo>.wiki.git`) with no branch protection, so reviews
ship without a double-approval PR (botfam#55). Each worktree has a local clone
at `wiki/` (cloned by `botfam newfam`, gitignored by the main repo).

Write the review to:

```text
wiki/review-YYYY-MM-DD-ACTOR_N.md
```

Where:

- `YYYY-MM-DD` is the current local date.
- `ACTOR` is the botfam actor name: resolved by running `botfam whoami` (or the
  worktree directory basename with prefixes stripped per PROTOCOL §1).
- `N` is the next progressive integer for that date and actor.

Before writing:

1. Read `doc/collab/PROTOCOL.md`.
2. Determine the actor by running `botfam whoami` (or using the protocol rule).
3. If `wiki/` is missing, clone it:
   `git clone "$(git remote get-url gitea | sed 's/\.git$//').wiki.git" wiki`.
4. List existing matching files in `wiki/review-*`.
5. Pick the lowest positive integer `N` that does not already exist.

If this repo already uses an older unnumbered review file for the same actor,
do not overwrite it. Start numbered files at `1`.

After writing, commit and push from inside `wiki/` (no PR needed):

```sh
cd wiki && git add review-*.md && git commit -m "docs: <actor> session retrospective <date>" && git push origin main
```

Older retrospectives migrated out of `doc/review/` are indexed on the wiki's
**Reviews** page.

## Workflow

1. Gather evidence from git status, recent commits, relevant diffs, tests, your
   conversation transcript/logs, and Forge reviews/issues.
2. Reconstruct the timeline from evidence. Use the conversation transcript/logs
   as the authoritative event order. When there are gaps, mark them explicitly.
3. Identify outcomes and verification gaps.
4. Extract durable lessons as reusable principles.
5. Write bounded self-improvement items only when they convert into concrete
   changes.
6. Create action items with owner, priority, target artifact, and verification.
7. Save the file and report the path plus any verification that was or was not
   possible.

## Review Template

````markdown
# Botfam Session Retrospective - YYYY-MM-DD - ACTOR - N

## 1. Session Metadata

```yaml
session_id:
branch_or_worktree:
started_at:
ended_at:
agents:
human_operator:
session_goal:
final_status: success | partial | failed | abandoned | unknown
postmortem_author:

# Cost and Value Metrics Vector (Value in Human-Touches & Instrument the Harness)
# Crucial: Retrieve exact usage figures from the harness / observability store out-of-band.
# Do NOT ask the agent to self-report or estimate tokens (Self-reported Telemetry is forbidden).
metrics_vector:
  token_usage: <total input + output + cache tokens spent>
  financial_cost_usd: <estimated cost in USD>
  wall_clock_minutes: <elapsed wall-clock time>
  human_touches: <count of operator interventions: unblocks, corrections, re-prompts>
  escaped_defects: <defects introduced or missed by this session>
```

## 2. Executive Summary

- What was the session trying to accomplish?
- What actually changed?
- Did the session achieve its goal?
- What was the biggest improvement?
- What was the biggest failure, friction, or near miss?
- What should change before the next similar session?

Avoid vague statements such as "coordination could be better." Name the
specific protocol, code, test, skill, prompt, or workflow weakness.

## 3. Timeline

```text
T+00:00 - Session started with goal: ...
T+00:05 - Actor inspected ...
T+00:12 - First implementation or review action ...
T+00:30 - Test failure, review disagreement, or protocol issue ...
T+00:45 - Human intervention ...
T+01:10 - Final state ...
```

For each important event, include actor, action, evidence, and consequence.

## 4. Outcome

```yaml
completed:
  -
partially_completed:
  -
not_completed:
  -
merged_or_shipped:
  -
left_unmerged:
  -
tests_run:
  -
tests_not_run:
  -
known_broken:
  -
```

Also answer:

- Is the repo better than before the session?
- Is the protocol better than before the session?
- Is the next session easier, safer, or more observable?

## 5. Lessons Learned

Each lesson must be a reusable principle.

```markdown
### Lesson 1: <principle>

Evidence from this session:
-

Protocol implication:
-

Code/tooling implication:
-
```

Good lesson shape:

- Review approval must be represented as protocol state, not inferred from
  comments.
- Any mergeable proposal must include test evidence generated after the final
  diff.
- If two agents duplicate work, the protocol lacks ownership discovery.

Bad lesson shape:

- Communicate better.
- Be more careful.
- Improve the docs.

## 6. Things That Went Well

```markdown
### Went well: <specific thing>

Evidence:
-

Why it mattered:
-

Should we preserve or reinforce this?
- yes/no
```

## 7. Things That Went Poorly

Focus on systems, not blame.

```markdown
### Went poorly: <specific failure or friction>

Evidence:
-

Impact:
-

Likely system cause:
-

Could automation (e.g. linters, git hooks, protocols, skills) prevent this?
- yes/no

Suggested fix:
-
```

## 8. Agent Self-Improvement / Capability Reflection

Only include items that can become one of:

- a skill patch,
- a protocol rule,
- a harness or tooling capability request,
- a test,
- an invariant,
- an observability signal,
- a tracked bug.

Do not write generic self-criticism, personality feedback, or advice such as
"be more careful," "communicate better," or "pay more attention."

```markdown
### Self-improvement item: <specific behavior or capability>

Observed failure or friction:
-

Evidence:
-

Current limitation:
- Missing skill | Ambiguous instruction | Missing protocol state |
  Missing tool capability | Missing validation check | Missing test pattern |
  Missing observability | Conflicting incentives | Context window limitation |
  Harness limitation | Human-only decision point

Better future behavior:
-

Required support:
- Update skill | Update system prompt | Update session protocol |
  Add MCP/harness API | Add CLI command | Add test | Add invariant |
  Add log/event | Add review checklist | Add bug

Proposed patch:
-

Verification:
-

Priority:
- P0 | P1 | P2 | P3

Convert to action item?
- yes/no
```

## 9. Action Items

Every action item must be trackable. Prefer changes to systems over reminders
for humans to behave differently.

```yaml
action_items:
  - id:
    priority: P0 | P1 | P2 | P3
    owner:
    target_artifact: skill | protocol | code | test | harness | docs | issue | tools
    summary:
    proposed_change:
    verification:
    source_section:
```

## 10. Limits, Gaps, and Handoff

```yaml
agent_limitations:
  unverifiable_claims:
    -
  assumptions_made:
    -
  context_missing:
    -
  should_not_trust:
    -

next_agent_handoff:
  # Handover Snapshot (for let-it-crash recovery / warm restarts)
  # Distill the current reasoning state so the next agent doesn't pay the onboarding tax.
  handover_snapshot:
    goal: <what needs to be achieved next>
    decisions_so_far:
      - <key choices and their rationales>
    branch_or_pr_pointer: <e.g., origin/improve-skills-botfam-session-retrospective>
    current_blocker: <what is holding the work back, if anything>
    next_step: <the next safest action to take>
  first_checks:
    -
  avoid_repeating:
    -
  safest_next_action:
    -
```
````

## Quality Bar

A good retrospective:

- names exact artifacts, commits, files, tests, messages, or logs as evidence;
- distinguishes facts from assumptions;
- measures and reports the cost-and-value efficiency vector (tokens, $,
  wall-clock, human-touches, escaped-defects) using harness telemetry instead
  of self-reported guessing;
- ensures any uncompleted or partial task is left with a valid, durable
  Handover Snapshot;
- produces action items with verification;
- avoids blame and vague self-talk;
- makes the next comparable session easier to run or review.

A poor retrospective:

- relies on memory without evidence;
- uses self-reported token counts or guesses metrics in prose;
- leaves incomplete tasks without a Handover Snapshot;
- says "communicate better" or "be more careful";
- creates action items with no owner, target artifact, or verification;
- hides missing evidence;
- turns the session into a narrative instead of a learning artifact.
