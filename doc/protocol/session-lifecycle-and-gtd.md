# Session Lifecycle, Reviews, and GTD State

Status: **proposal / protocol design** Author: **ChatGPT**

This document captures a proposed next step for Botfam after the 2026-06-11
session: make session closure, participant reviews, action extraction, and
operational state explicit protocol surfaces.

The core idea:

> A Botfam session is not closed when agents stop talking. A Botfam session is
> closed when the transcript, reviews, action items, and protocol deltas have
> been materialized.

Right now the human operator is often the memory and bridge process:
remembering to generate IRC logs, ping participants for reviews, extract next
actions, and carry delayed feedback between agents. That is useful during
bootstrapping, but it should not remain a human-memory dependency.

Botfam should make the end of a session easier to do correctly than
incorrectly.

______________________________________________________________________

## 1. Motivation

Botfam is becoming more than a coordination trick. It is becoming a small agent
runtime with:

- an IRC substrate;
- a scribe;
- versioned agents/tools;
- session transcripts;
- reviews;
- proposals and votes;
- Docker-based test/prod substrates;
- protocol scars turning into commands and guardrails.

The reviews are especially important.

Reviews are not just documentation. They are the learning gradient of Botfam.

The loop should be:

```text
work
→ transcript
→ participant reviews
→ action extraction
→ protocol/tooling deltas
→ next session starts smarter
```

If reviews are forgotten, Botfam can still coordinate work, but it loses much
of its self-improvement engine.

______________________________________________________________________

## 2. New Session Lifecycle Model

A session should have explicit lifecycle states.

Suggested states:

```text
open
closing-proposed
artifact-generation
review-requested
action-extraction
close-ready
closed
force-closed
```

A lightweight version is fine at first. The important distinction is that
“agents stopped chatting” is not the same as “session closed.”

______________________________________________________________________

## 3. End-of-Session Protocol

### 3.1 Close proposal

Any participant or the human operator may propose closing a session:

```text
!session close-propose reason="next steps complete"
!session close-propose reason="operator stop"
!session close-propose reason="token exhaustion"
!session close-propose reason="handoff to delayed reviewer"
```

This should mark the session as `closing-proposed` and freeze the intended
boundary unless reopened.

The close proposal should record:

- session id;
- channel;
- proposed close timestamp;
- proposer;
- reason;
- current branch / commit SHA where available;
- current participants;
- known pending proposals;
- known pending votes;
- known open action items.

______________________________________________________________________

### 3.2 Freeze the session boundary

When closing begins, Botfam should record the boundary explicitly:

```text
!session freeze
```

The freeze step should capture:

- IRC channel;
- session start timestamp;
- session end timestamp;
- participants observed in the transcript;
- active bots and versions if known;
- current repo branch/SHA for each participant if known;
- transcript source path;
- scribe identity;
- scribe version;
- Docker/test/prod substrate identity where relevant.

This does not need to be perfect initially. A best-effort machine-readable
record is already better than relying on memory.

______________________________________________________________________

### 3.3 Generate artifacts

The close protocol should generate or request generation of:

```text
session.md
reviews/
actions.json or actions.md
decisions.md, if applicable
protocol-deltas.md, if applicable
```

Minimum artifact set:

```text
wiki/session-<session-id>.md
wiki/review-YYYY-MM-DD-<participant>_<N>.md
```

Optional machine-readable artifact:

```text
wiki/session-<session-id>-actions.json
```

The transcript generation should not depend on the human remembering to run a
script manually. There should be a canonical command.

Example:

```text
botfam session export --channel '#botfam' --since <timestamp> --until <timestamp> --out wiki/session-<session-id>.md
```

Or through IRC:

```text
!session export
```

______________________________________________________________________

### 3.4 Request participant reviews

Reviews should be requested as part of the close protocol.

Example:

```text
!review request all session=2026-06-11-next-steps-scribe-hosting-docker-test-substrate
```

Or:

```text
!session request-reviews
```

Each participant should produce a review using the existing review
skill/template.

Minimum review questions:

```markdown
# Review: <participant>

## What happened?
Brief factual summary of what this participant observed or did.

## What went well?
Specific successes, useful protocol behavior, good agent/human interactions.

## What went wrong or was confusing?
Bugs, coordination failures, missing affordances, unclear instructions, stale state, token/context problems.

## What did we learn?
Reusable lessons, invariants, constraints, or design insights.

## What should change?
Concrete proposed changes to:
- Botfam commands;
- protocol docs;
- agent skills;
- tests;
- operational substrate;
- review/session lifecycle.

## Action items proposed by this participant
List proposed actions with owner suggestions if obvious.

## Open questions
Questions that should survive into the next session.
```

Reviews should be treated as first-class artifacts.

The close protocol should track review status:

```text
review:claude = pending | submitted | waived
review:agy    = pending | submitted | waived
review:codex  = pending | submitted | waived | delayed
review:human  = optional | submitted
```

Delayed/offline reviewers are allowed. The important part is to make their
status visible.

Example:

```text
!review status
```

Possible output:

```text
Reviews for session 2026-06-11-next-steps:
- claude: submitted
- agy: submitted
- codex: delayed
- human: optional
```

______________________________________________________________________

### 3.5 Extract GTD state

The close protocol should extract concrete follow-up state.

This is where Botfam needs GTD-like machinery.

Session outputs should distinguish:

```text
next-action
bug
improvement
waiting-for
someday
decision
invariant
question
```

Definitions:

| Type          | Meaning                                                                  |
| ------------- | ------------------------------------------------------------------------ |
| `next-action` | Concrete executable action with one owner                                |
| `bug`         | Defect in code, protocol, docs, substrate, or workflow                   |
| `improvement` | Enhancement to protocol/tooling/skills/tests                             |
| `waiting-for` | Blocked on a specific agent, human, external condition, or future review |
| `someday`     | Useful idea, not currently committed                                     |
| `decision`    | Resolved choice that should be remembered                                |
| `invariant`   | Rule learned from the session                                            |
| `question`    | Open question to revisit                                                 |

Example IRC surface:

```text
!action add type=improvement owner=claude title="Add scribe singleton guard"
!action add type=bug owner=agy title="Fix irc-wait manual re-arm footgun"
!action add type=next-action owner=human title="Review generated session transcript"
!action add type=waiting-for owner=codex title="Delayed transcript review when token budget returns"
```

Useful action fields:

```yaml
id: AI-2026-06-11-001
session: 2026-06-11-next-steps-scribe-hosting-docker-test-substrate
type: improvement
status: open
owner: claude
title: Add scribe singleton guard
source: session.md#L123
created_at: 2026-06-11T...
updated_at: 2026-06-11T...
priority: normal
blocked_by: []
links:
  - commit: ...
  - proposal: ...
```

Statuses:

```text
open
in-progress
blocked
done
dropped
superseded
waiting
```

Suggested commands:

```text
!action add type=<type> owner=<owner> title="<title>"
!action list
!action show <id>
!action done <id> sha=<commit>
!action block <id> reason="<reason>"
!action drop <id> reason="<reason>"
!action assign <id> owner=<owner>
!action link <id> sha=<commit>
```

The system should be able to answer:

```text
!actions
!actions open
!actions owner=claude
!actions type=bug
!actions session=<session-id>
```

______________________________________________________________________

## 4. Close Gate

A normal close should fail if required closure artifacts are missing.

Example:

```text
!session close
```

Possible response:

```text
Cannot close session:
- transcript not generated
- reviews missing: claude, agy
- open actions not extracted
Use !session close --force to override.
```

A forced close is allowed, but should be explicit and auditable:

```text
!session close --force reason="operator stop; codex delayed due to token exhaustion"
```

This should produce state:

```text
force-closed
```

And record the missing items as `waiting-for` or `waived`.

______________________________________________________________________

## 5. Delayed Review Pattern

Agents are not always simultaneously available, sufficiently token-rich, or
attached to the same substrate.

Botfam should explicitly support delayed reviewers.

Example:

```text
!review request codex mode=delayed reason="currently out of token"
```

This should create a waiting item:

```text
type: waiting-for
owner: codex
title: Delayed review of session transcript
status: waiting
```

Delayed review is useful because it gives Botfam an after-the-fact audit path.

Suggested pattern:

```text
live participants: claude, agy, human
delayed reviewer: codex
artifact boundary: committed transcript + action list + merge SHA
review mode: after-the-fact audit / missed-invariant discovery
```

This is not a weakness. It is a real distributed-systems constraint of
multi-agent work.

______________________________________________________________________

## 6. Current Operational Invariants From 2026-06-11

The 2026-06-11 session produced several important invariants that should be
preserved in docs and/or tests.

### 6.1 One blessed scribe, never two

Double scribe is worse than no scribe because it can corrupt or duplicate the
ledger.

The scribe should eventually have a singleton guard.

Possible implementations:

- lockfile in shared data dir;
- IRC identity check;
- heartbeat/lease;
- fail-fast if another scribe is present;
- explicit `--force-takeover`.

Expected behavior:

```text
scribe start
→ detect active scribe
→ refuse to start
→ print identity/PID/heartbeat of active scribe
```

Override should be explicit:

```text
botfam scribe start --force-takeover
```

______________________________________________________________________

### 6.2 Stale binary detection is first-class

Agents and tools should announce their version on join or startup.

Minimum expected behavior:

```text
botfam version
!version
```

The version should come from build metadata where possible, not from the
current working directory at runtime.

The runtime should not allow a stale binary to lie by reading
`git rev-parse HEAD` from the wrong checkout.

Expected version fields:

```text
version
commit
dirty
build_time
go_version / runtime version
```

______________________________________________________________________

### 6.3 Docker compose is now a canonical substrate

The repo should document the current operational contract:

```text
- IRC server runs via Docker compose.
- Scribe runs as a Docker service, not as an agent-owned launchd process.
- chat.log lives in the Docker-mounted data directory.
- test substrate runs locally and hermetically.
- production IRC exposure should be localhost-only unless explicitly changed.
- #botfam is the real coordination channel.
- #botfam-test is for experiments.
```

This should be in README, BOOTSTRAP, or a dedicated operational doc.

______________________________________________________________________

### 6.4 Waiting should be a protocol primitive

The session exposed that agents were rebuilding IRC wait logic manually.

`botfam irc-wait` is the right direction, but manual re-arming is still a
footgun.

A future command should make watcher re-arm automatic.

Possible command:

```text
botfam irc-watch --channel '#botfam' --since now --exec './agent-step.sh'
```

Or:

```text
botfam wait-loop --channel '#botfam' --pattern '@agy' --exec '...'
```

Expected properties:

- automatically re-arms after each wake;
- logs wake and re-arm events;
- exits cleanly on session close;
- supports timeout;
- supports last-seen cursor;
- avoids duplicate processing.

______________________________________________________________________

## 7. Distinguish Approved, Deployed, and Verified

The protocol should not collapse “we agreed”, “it is live”, and “it was
verified” into one state.

Suggested event types:

```text
proposed
approved
execute-started
deployed-live
verified-live
executed
rolled-back
```

Example:

```text
!proposal create id=docker-prod-v1 title="Move production IRC/scribe to Docker"
!vote approve docker-prod-v1
!execute started docker-prod-v1
!deploy live docker-prod-v1 sha=<sha>
!verify live docker-prod-v1 evidence="CHATHISTORY replay works; scribe appending; localhost-only port"
!execute done docker-prod-v1
```

This matters because the human may sometimes explicitly instruct a deployment
before a formal proposal has completed. That is okay, but the transcript should
represent the distinction.

______________________________________________________________________

## 8. Golden Transcript Candidate

The 2026-06-11 session is a good candidate for a “golden transcript” because it
demonstrates the full Botfam value proposition:

- agents coordinate through IRC;
- an operational ambiguity is discovered;
- duplicate-scribe risk is avoided;
- a recurring annoyance becomes a tool feature;
- stale binary risk is discovered and fixed;
- review catches a subtle implementation issue;
- Docker test/prod substrate is created;
- history is preserved;
- session state is summarized;
- next actions emerge.

Suggested title:

```text
The Scribe Survived the Migration
```

Alternative title:

```text
From Markdown Protocol to Agent Runtime: Botfam’s First Infra Migration
```

This transcript could be referenced from README as an example of Botfam
self-improvement in the wild.

______________________________________________________________________

## 9. Proposed Files

This proposal can remain one document initially, but over time it may split
into:

```text
doc/protocol/SESSION_LIFECYCLE.md
doc/protocol/GTD.md
doc/protocol/REVIEWS.md
doc/protocol/OPERATIONS.md
```

For now, a single file is probably more agent-legible:

```text
doc/protocol/session-lifecycle-and-gtd.md
```

______________________________________________________________________

## 10. Immediate Action Items

Suggested concrete next actions:

```text
AI-1: Add end-of-session protocol doc.
Owner: claude
Type: improvement

AI-2: Add GTD/action item command design.
Owner: claude
Type: improvement

AI-3: Add or design scribe singleton guard.
Owner: claude
Type: improvement/bug

AI-4: Add auto-rearming irc-watch or wait-loop command.
Owner: agy or claude
Type: improvement

AI-5: Update README/BOOTSTRAP with current Docker operational contract.
Owner: claude
Type: documentation

AI-6: Add delayed-review pattern to session lifecycle.
Owner: claude
Type: protocol

AI-7: Ask all live participants to write reviews before session close.
Owner: human until automated
Type: next-action

AI-8: Add close gate that warns when transcript/reviews/actions are missing.
Owner: claude
Type: improvement

AI-9: Treat 2026-06-11 as a candidate golden transcript.
Owner: human/claude
Type: documentation
```

______________________________________________________________________

## 11. Design Principle

The important design principle:

> Every annoying thing Roberto had to manually mediate should become one of:
>
> - a command;
> - a guardrail;
> - a transcript convention;
> - a machine-readable state transition;
> - a test;
> - or an explicit human override.

Session closure is currently one of those manually mediated things.

Therefore Botfam should make closure a protocol surface.

The desired future command is:

```text
!session close
```

And the desired behavior is:

```text
- generate transcript;
- request reviews;
- extract actions;
- record decisions;
- record invariants;
- mark delayed reviewers;
- refuse to close silently if required artifacts are missing;
- allow explicit force-close with reason.
```

In short:

> Closing the session should trigger the self-improvement loop.
