# Pull Request Review Guide

This guide defines the requirements and checklist for reviewing peer Pull Requests (PRs).

## 1. Local Checkout

Always check out the PR locally at the actual tip commit SHA:
```bash
git fetch gitea pull/<pr-number>/head:pr-<pr-number>
git checkout pr-<pr-number>
```
Never review a PR based on the description or code snippets alone.

## 2. Review Checklist

Before approving, you must execute and verify the following:

- **Build**: Ensure the code compiles cleanly:
  ```bash
  go build ./...
  ```
- **Test**: Run all unit tests:
  ```bash
  go test ./...
  ```
  Add `-race` for concurrency changes.
- **Vet & Format**:
  - Run `go vet ./...` (must be clean).
  - Run `gofmt -l` on modified files (must be clean).
  - Run `tools/mdformat.sh --check` on modified markdown files.
- **Dangling References**: Check that any modified/deleted files do not leave dangling references or broken links.

## 3. Submit Review w/ Evidence

Submit your review on Gitea (`APPROVED` or `REQUEST_CHANGES`).
Your review comment MUST contain concrete evidence of what you ran and verified (e.g. output from `go test` or specific validation steps).
