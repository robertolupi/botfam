---
name: botfam-session-retrospective
description: Use when closing or reviewing a botfam agent session and writing a blameless SRE-style retrospective, postmortem, or self-improvement review under doc/review/YYYY-MM-DD-ACTOR-N.md with concrete evidence, lessons, and trackable improvements.
---

# Botfam Session Retrospective

Use this skill when asked to write a postmortem, retrospective, learning review,
session review, or self-improvement artifact for a botfam session.

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
- IRC channel logs — the primary coordination record:
  - your own client log (`scratch/irc/<actor>/log`, timestamped),
  - the scribe's machine-readable ledger (JSONL at `COLLAB_HISTORY`,
    one event per line with sender/type/target/body),
  - server-side history when available (ergo `CHATHISTORY`),
- ccrep state from bang-verb lines in the ledger
  (`!propose`/`!evaluate`/`!vote` and scribe `!tally` replies),
- review comments and verdicts (sent as `!evaluate` lines),
- tests run and failures,
- human interventions (operator messages on the channel),
- TODOs and unresolved questions.

If evidence is missing, say so explicitly. Do not infer intent. When client
logs and the scribe ledger disagree, quote both and flag the divergence — that
is itself a finding.

## Output Path

Write the review to:

```text
doc/review/YYYY-MM-DD-ACTOR-N.md
```

Where:

- `YYYY-MM-DD` is the current local date.
- `ACTOR` is the botfam actor name: the worktree basename with leading `wt-`
  or `botfam-` stripped.
- `N` is the next progressive integer for that date and actor.

Before writing:

1. Read `doc/collab/PROTOCOL.md`.
2. Determine the actor from `basename "$PWD"` using the protocol rule.
3. List existing matching files in `doc/review/`.
4. Pick the lowest positive integer `N` that does not already exist.

If this repo already uses an older unnumbered review file for the same actor,
do not overwrite it. Start numbered files at `1`.

## Workflow

1. Gather evidence from git status, recent commits, relevant diffs, tests,
   your IRC client log, and the scribe ledger when available.
2. Reconstruct the timeline from evidence. IRC logs carry per-line timestamps —
   use them verbatim; the channel log is the authoritative event order. When
   your client was disconnected, mark the gap explicitly and fill it from the
   scribe ledger or server history, noting the source.
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
- produces action items with verification;
- avoids blame and vague self-talk;
- makes the next comparable session easier to run or review.

A poor retrospective:

- relies on memory without evidence;
- says "communicate better" or "be more careful";
- creates action items with no owner, target artifact, or verification;
- hides missing evidence;
- turns the session into a narrative instead of a learning artifact.
