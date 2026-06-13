---
authors:
  - agy
kind: proposal
status: Draft
created: 2026-06-13
proposal-id: gitea-session-extraction-agy-v1
executor: agy
quorum: majority
deadline: none
---

# Proposal: Gitea-Native Session Extraction for LLM Reviews

> [!NOTE]
> **Status**: Draft (2026-06-13). Proposes a native command
> `botfam session extract` to compile chronological milestone or project
> timelines from Gitea to feed into external LLM reviews.

## Status

**Draft** (2026-06-13, by agy). Opened for review and unification with peer
proposals.

| Field       | Value                           |
| ----------- | ------------------------------- |
| Proposal id | gitea-session-extraction-agy-v1 |
| Executor    | agy                             |
| Quorum      | majority                        |
| Deadline    | none                            |

## Problem

Historically, botfam session retrospectives and external LLM reviews relied on
chronological timeline extraction from local/scribe IRC logs (e.g.
`botfam irclog2sessions`). However, following our pivot to Gitea-native branch
protection and reviews (Issue #33), the bulk of our coordination context (PR
descriptions, reviews, inline comments, issue discussions, commits, and merges)
now resides on the forge rather than IRC.

The current `external-review` tool operates on a single PR diff and discussion.
For larger features and workday sessions, coordination spans multiple
interconnected issues and PRs (such as the recent "Adopt Forgejo" milestone).

If we do nothing:

1. LLM reviews cannot analyze the holistic narrative of how multiple PRs and
   issues interacted to achieve a milestone.
2. Developing retrospects and postmortems for milestones will require tedious,
   manual copy-pasting of Gitea timelines.

## Proposed Behavior

We introduce the `botfam session extract` command to automatically generate a
unified chronological narrative of a Gitea milestone or project.

### 1. CLI Invocation

An agent or operator can run:

```bash
botfam session extract --milestone <milestone-title-or-id> [--out <path>]
```

By default, it will:

1. Resolve Gitea credentials (token) and repository URL from the active git
   remotes.
2. Fetch the milestone details and list all issues and pull requests associated
   with it.
3. Query the Gitea API to extract the full timeline for each issue and PR:
   - Creation metadata & description.
   - Issue comments & PR review comments (with timestamps, authors, and body).
   - Commits pushed (SHAs, authors, messages).
   - State changes (open, close, merge, review approvals, requests for
     changes).
4. Merge all events into a single, unified chronological timeline across all
   issues and PRs.
5. Format the unified timeline into a clean Markdown document at the specified
   `<path>` (or stdout).

### 2. Extracted Timeline Format

The generated markdown will present events chronologically:

```markdown
# Session/Milestone: Adopt Forgejo

## Timeline

- **19:00:26** - `agy` opened PR #45: `feat(bootstrap): replace bootstrap-botfam.sh with native newfam command`
  > Replaces the legacy shell script bootstrap-botfam.sh with a native Go implementation...
- **19:03:19** - `claude-bot` submitted review on PR #45 (APPROVED):
  > **Approve.** Verified at 09843957... One minor nit...
- **19:08:57** - `rlupi` submitted review on PR #45 (REQUEST_CHANGES):
  > Let's not remove files at all. I think the whole cleanLegacyMCP can go.
- **19:12:03** - `agy` pushed commit `cb2797c5` to PR #45:
  > refactor(bootstrap): remove legacy MCP config cleaning logic entirely
- **19:13:08** - `rlupi` submitted review on PR #45 (APPROVED)
- **19:22:30** - `rlupi` merged PR #45 (commit `d10190bf`)
```

### 3. Integration with external-review

We will update `botfam external-review` to accept a generated milestone session
file as an input target (similar to how it currently accepts PR numbers),
feeding the complete milestone timeline and aggregated diffs into the LLM
prompt.

## Rollout

### Phase 0: Manual A/B testing (Zero Code)

We manually construct a milestone timeline markdown file for the "Adopt
Forgejo" milestone and run it through `botfam external-review` (using Qwen or
Gemma in Ollama) to validate that the LLM produces a high-quality postmortem
from Gitea-native events.

### Phase 1: Go Implementation

Implement the Gitea API extraction logic under
`internal/fam/session_extract.go` and wire the new subcommand into
`cmd/botfam/main.go`.

### Phase 2: Tool Integration

Update `botfam external-review` to support direct ingestion of milestone
extractions.

## Costs and Risks

- **API Rate Limits / Latency**: Querying Gitea for multiple issues, PRs,
  comments, reviews, and commits will make dozens of API calls. We must
  implement bulk endpoints where possible or run them concurrently to avoid
  sluggish execution.
- **Milestone Discipline Dependency**: If issues or PRs are not correctly
  linked to the Gitea milestone, they will be silently omitted from the session
  logs. The protocol must enforce that all worktree changes are associated with
  a Gitea milestone or project.

## First Expected Payoff

The extraction of a complete milestone timeline for the next feature sprint
(e.g. Issue #50 itself) to run an automated external LLM postmortem.
