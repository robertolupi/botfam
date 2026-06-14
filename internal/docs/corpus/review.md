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
  build tool (e.g. `go build`, `npm run build`, or equivalent).
- **Test**: Run all unit tests using the project's test runner (e.g. `go test`,
  `pytest`, `npm test`, or equivalent). For concurrency changes, enable
  race/thread detection if supported.
- **Vet & Format**:
  - Run the project's static analysis or linting checks (e.g. `go vet`,
    `eslint`, `flake8`, or equivalent).
  - Verify formatting on all modified files (e.g. `gofmt`, `prettier`, or the
    project's markdown formatter script like `tools/mdformat.sh`).
- **Dangling References**: Check that any modified/deleted files do not leave
  dangling references or broken links.

## 3. Submit Review w/ Evidence

Submit your review on Gitea (`APPROVED` or `REQUEST_CHANGES`). Your review
comment MUST contain concrete evidence of what you ran and verified (e.g.
output from `go test` or specific validation steps).
