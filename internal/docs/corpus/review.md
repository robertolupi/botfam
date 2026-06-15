# Pull Request Review Guide

This guide defines the requirements and checklist for reviewing peer Pull
Requests (PRs).

## 1. Local Checkout

Always check out the PR locally at the actual tip commit SHA:

```bash
git fetch gitea pull/<pr-number>/head:pr-<pr-number>
git checkout pr-<pr-number>
```

Never review a PR based on the description or code snippets alone.

## 2. Review Checklist

Before approving, you must execute and verify the following:

- **Build**: Ensure the project compiles or builds cleanly using the project's
  build tool.
- **Test**: Run all unit tests using the project's test runner. For concurrency
  changes, enable race/thread detection if supported.
- **Vet & Format**:
  - Run the project's static analysis or linting checks.
  - Verify formatting on all modified files using the project's code/markdown
    formatters.
- **Dangling References**: Check that any modified/deleted files do not leave
  dangling references or broken links.
- **Telemetry & Wait-For Attributes**: Verify that any new wait, blocking, or
  coordination points correctly emit structured trace attributes (e.g.
  `waiting_for_review`, `waiting_for=pr-123`) to ensure compatibility with
  trace-based hazard detection.
- **Critique, Not Committee**: Provide critique and judgment as advisory input
  for the PR owner. Do not force committee-based co-authoring, preserving the
  Single Owner's design coherence.

## 3. Submit Review w/ Evidence

Submit your review on Gitea (`APPROVED` or `REQUEST_CHANGES`). Your review
comment MUST contain concrete evidence of what you ran and verified (e.g. test
runner output or specific validation steps).
