---
name: design-sprint
description: Use when running a collaborative design iteration or sprint on the self-hosted forge wiki and Gitea IRC channel to resolve design questions and arrive at clean modular specs. Trigger on "iterate on proposal", "run a design sprint", "collaborative design on IRC", "grill-me on IRC", or any request to discuss design decisions on the channel.
---

# Running a design sprint

This skill guides a botfam agent to collaboratively design a new feature or architectural pivot in partnership with the operator and peer agents. It relies on the Gitea wiki for hosting proposal specs and the Gitea IRC channel for running structured Q&A interviews.

## 0. Set up

1. **Pull Wiki**: Checkout the `wiki` repository (`git pull origin main` in the `wiki/` directory) to fetch the latest proposals and indexes.
2. **Find the Target**: Locate the main design proposal page on the wiki (e.g. `proposal-mcp-self-discoverability.md`).
3. **Connect to IRC**: Ensure your local IRC client is connected and that you have replayed channel logs to see previous design details.

## 1. Structured Q&A (IRC Grilling)

Instead of using standard private user-grilling tools (which isolate details to a single conversation), run the design interview on Gitea IRC (`#botfam`) so that all agents and the operator share context.

- **Ask Questions One at a Time**: Break down the design decisions into a logical sequence.
- **Provide Recommendations**: For every question you ask, provide your own recommended solution and briefly state the rationale.
- **Wait for Consensus**: Allow the operator and peer agents to respond, debate, and refine the answer before proceeding to the next question.

## 2. Document Splitting (Modular Specs)

To prevent edit conflicts in Gitea's wiki repository (which does not use PR branch protection and is merge-on-push):
- Keep the main proposal page as a thin **Umbrella Index** outlining the problem, goal, principles, resolved design decisions, and links to detailed specs.
- Split the technical details into **modular sub-pages** with a single designated owner (e.g. `proposal-mcp-embedded-corpus.md` for agy, `proposal-mcp-wiki-provider.md` for claude).
- Ensure sibling pages do not touch overlapping files or write to the same pages concurrently.

## 3. Wiki Renames and Link Integrity

When creating or renaming wiki files:
- **Clean Filenames**: Avoid Gitea's default spaces/punctuation formatting (which appends stray `.-` to filenames). Use clean, lowercase, dash-separated filenames (e.g. `proposal-mcp-embedded-corpus.md`).
- **Grep for References**: If a wiki page is renamed or created, run `grep` to find and update all inbound references across:
  - `Proposals.md` (the main wiki index page)
  - Sibling proposal documents
  - Active session retrospectives
- **Commit Directly**: Commit and push the verified wiki pages directly to the wiki remote. No pull requests are used for Gitea wikis.

## 4. Completion

- Post a final success summary on Gitea IRC listing the resolved decisions, the updated specs, and their wiki URLs.
