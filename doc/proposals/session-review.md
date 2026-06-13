---
authors:
  - agy
  - claude
kind: proposal
status: Under Review
created: 2026-06-13
proposal-id: session-review-forge-v1
---

# Proposal: Gitea-Native Session Extraction and Review Protocol

> [!NOTE]
> **Status**: Under Review (2026-06-13). Unified proposal merging independent
> drafts from PR #51 and PR #52. Addresses feedback from eight external review
> reports across GPT-5.5, Gemini 3.5 Flash, Gemma 4, and Qwen 3.6.

## Status

Unified proposal responding to Issue #50.

| Field       | Value                                                                                                                        |
| ----------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Proposal id | `session-review-forge-v1`                                                                                                    |
| Executor    | `agy` / `claude`                                                                                                             |
| Governance  | Merge gated by native branch protection (≥2 approvals, dismiss-stale, block-on-rejected); verified via `tools/forge-gate.sh` |
| Deadline    | none                                                                                                                         |

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
3. The retrospective skill still names the IRC scribe ledger as the primary
   record, which is stale post-go-native.

## Proposed CLI Interface

We propose a decoupled two-command approach. The extraction logic is isolated
into a standalone utility command, while the review fan-out tool is updated to
ingest the generated artifacts.

### 1. `botfam session extract`

Extracts a unified chronological timeline and diff summary for a Gitea
milestone.

```bash
botfam session extract --milestone <milestone-title-or-id> [options]
```

**Options:**

- `--out <path>` — Path to save the extracted markdown document (defaults to
  stdout).
- `--since <timestamp>` / `--until <timestamp>` — Filters events to a specific
  time window.
- `--redact` / `--no-redact` — Runs regex sanitization patterns over the
  payload before output (defaults to `--redact` enabled).
- `--interaction-only` — Omits technical diff summaries completely.
- `--with-diffs` — Appends full raw diff contents instead of a summary (opt-in;
  default off to prevent context blowup).
- `--snapshot-timestamp <timestamp>` — Restricts timeline queries to a
  deterministic freeze timestamp, ensuring reproducible outputs even if the
  live milestone membership drifts.

### 2. `botfam external-review`

We extend the existing review utility to natively ingest the extracted session
files.

```bash
botfam external-review --session-file <path> [options]
```

As syntactic sugar, running `--milestone <name>` directly on `external-review`
will execute `session extract` under the hood and pipeline the result into the
model fan-out:

```bash
botfam external-review --milestone <name> [options]
```

## Credential and URL Resolution

To prevent production authentication failures:

1. The tool resolves Gitea API URLs from the active git remotes (using the host
   and organization/repository layout).
2. The Gitea API personal access token is resolved explicitly from the
   `GITEA_TOKEN` environment variable first, falling back to local credentials
   files such as `~/.botfam/token-<fam>-<actor>` or
   `~/.config/botfam/config.json`.
3. An explicit `BOTFAM_ACTOR` environment variable must be supported to avoid
   actor resolution race conditions when multiple agent instances are running
   concurrently in separate worktrees.

## Extracted Timeline Format (Format C)

Based on empirical A/B testing with local `qwen3.5:latest` and `gemma4:31b`
models, the default format is **Format C (Chronological Timeline + Diff
Summary)**. It provides the highest postmortem accuracy and file-level
specificity while remaining context-safe.

The generated markdown structure must follow these invariants:

1. **Thread-Tagging:** Every timeline item must be prefixed with a standard
   reference tag (e.g. `[PR #45]` or `[Issue #50]`) to resolve temporal
   "cross-talk" narrative confusion for smaller models.
2. **Stable Sorting:** Events must be ordered deterministically. The stable
   sorting tie-breaker rule is: `timestamp` (ISO-8601 with explicit timezone
   offsets) $\\rightarrow$ `event_id` / `source_object` $\\rightarrow$ `actor`.
3. **Capture All Review Transition States:** The timeline must explicitly
   record `APPROVED`, `REQUEST_CHANGES`, and automated `DISMISSED` (stale
   review resets) events to preserve the branch-protection governance path.

### Example Markdown Output

```markdown
# Session/Milestone: Adopt Forgejo

## Timeline
- **2026-06-13T19:00:26+02:00** - [PR #45] `agy` opened: `feat(bootstrap): replace bootstrap-botfam.sh with native newfam command`
  > Replaces the legacy shell script bootstrap-botfam.sh...
- **2026-06-13T19:03:19+02:00** - [PR #45] `claude-bot` submitted review (APPROVED):
  > **Approve.** Verified at 09843957...
- **2026-06-13T19:08:57+02:00** - [PR #45] `rlupi` submitted review (REQUEST_CHANGES):
  > Let's not remove files at all...
- **2026-06-13T19:12:03+02:00** - [PR #45] `agy` pushed commit `cb2797c5`:
  > refactor(bootstrap): remove legacy MCP config...
- **2026-06-13T19:13:08+02:00** - [PR #45] `rlupi` submitted review (APPROVED)
- **2026-06-13T19:22:30+02:00** - [PR #45] `rlupi` merged PR #45 (commit `d10190bf`)

---

## Technical Diff Summary

### Files Changed in PR #45:
- **internal/fam/newfam.go**:
  - Deleted legacy MCP files cleaning logic entirely (`cleanLegacyMCP`).
  - Modified `writeClaudeSettings` to parse `settings.json` into `map[string]json.RawMessage` and selectively merge keys.
- **internal/fam/newfam_test.go**:
  - Added `TestWriteClaudeSettingsPreservesFields`.
- **cmd/botfam/bootstrap_test.go**:
  - Cleaned up legacy MCP configuration assertions.
```

## Technical Diff Summary Constraints

To eliminate confabulation loops and recursive LLM hallucinations:

1. The **Technical Diff Summary** must be generated deterministically by
   default (extracting file names, modified symbols, additions/deletions
   counts, and commit message subject lines).
2. If an optional local LLM is used to generate semantic prose describing code
   changes, all such sections must be clearly and explicitly tagged with
   `[GENERATED/NON-AUTHORITATIVE]` so the reviewer model does not treat
   hallucinations as code facts.
3. If a force-push or rebase causes a commit SHA to 404, the tool must
   gracefully fall back to diffing against the overall PR state.

## Safety and Robustness Invariants

1. **Exhaustive Pagination:** The Go implementation
   (`internal/fam/session_extract.go`) must exhaustively query Gitea timeline,
   comment, and review endpoints to prevent silent truncation.
2. **Redaction Guardrails:** The `--redact` filter must scrub standard pattern
   matches for basic auth credentials, API keys, Authorization headers, and
   internal system mount paths before writing the extraction to file.
3. **Milestone Hygiene Warnings:** The tool will query all merged/closed PRs
   and issues within the milestone's active date range. If any items lack the
   milestone tag but contain references or commits matching the worktree, the
   tool will output a completeness warning at the top of the timeline.
4. **Context Budget Checks:** If the extracted timeline exceeds a predefined
   token limit (e.g. 50,000 tokens), the tool will exit with a warning,
   advising the operator to use temporal bounds (`--since`/`--until`) or enable
   `--interaction-only`.

## Rollout Phasing

### Phase 1: Go Implementation

- Implement the Gitea timeline, PR comment, and commit extraction engine under
  \[internal/fam/session_extract.go\](file:///Users/rlupi/src/fams/botfam/wt-agy/internal/fam/session_extract.go).
- Wire the `botfam session extract` CLI command under
  \[cmd/botfam/main.go\](file:///Users/rlupi/src/fams/botfam/wt-agy/cmd/botfam/main.go).
- Implement regex-based `--redact` filters and stable sorting routines.

### Phase 2: Ingestion and Tool Integration

- Update `botfam external-review` to ingest the milestone session files and
  wire the direct `--milestone` command-line sugar.
- Update `skills/botfam-session-retrospective` to point to the new Gitea-native
  milestone extractor rather than the retired IRC scribe ledger.

### Phase 3: Verification

- Write fixture-based unit and integration tests confirming pagination,
  timezone offset normalization, deterministic sorting, and redaction bounds.
