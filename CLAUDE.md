# botfam agent harness pointer

This worktree belongs to a botfam agent.

1. **Your Name**: Resolved by running `botfam whoami` (or worktree basename).
2. **MCP Onboarding**: Run `resources/read` on `botfam:///docs/start` immediately to orient yourself.
3. **Core Protocol**: The full rules live at `botfam:///docs/protocol` (originally at `doc/collab/PROTOCOL.md`).
4. **Environment Health**: Inspect the health warning blocks at `botfam:///` to ensure your token and client are correctly set up. If the root shows `<unresolved>` (e.g., in system-wide MCP setups), call the `orient` tool with your worktree path (as the `work_dir` argument) to bootstrap.

## Repo-local Skills

Generated from `skills/*/SKILL.md`.

- `botfam-session-retrospective`: Use when closing or reviewing a botfam agent session and writing a blameless SRE-style retrospective, postmortem, or self-improvement review under wiki/review-YYYY-MM-DD-ACTOR_N.md (the Gitea wiki) with concrete evidence, lessons, and trackable improvements.
- `botfam-sprint`: Use when a botfam agent should autonomously work a backlog — looping over the forge's open issues and pull requests to claim an issue, resolve it, open a PR, review a peer's PR, and address comments on its own PRs — repeating until no unassigned issues and no reviewable PRs remain. Trigger on "work the backlog", "run a sprint", "grind through the issues", "loop over issues and PRs", "keep taking issues and reviewing", or any standing instruction to keep resolving issues and reviewing PRs on the forge.
- `design-sprint`: Use when running a collaborative design iteration or sprint on the self-hosted forge wiki and Gitea IRC channel to resolve design questions and arrive at clean modular specs. Trigger on "iterate on proposal", "run a design sprint", "collaborative design on IRC", "grill-me on IRC", or any request to discuss design decisions on the channel.
- `external-review`: Use when running a multi-model external review of a botfam session, doc, or change — fan the canonical prompt across configured models with botfam external-review, keep the raw reviews out-of-repo, then spawn a consolidation subagent to merge them into one unified review.
- `forge-autonomy`: Use when operating as a botfam agent on the self-hosted forge — getting woken on queued work via `botfam forge-wait`, and reviewing/approving pull requests correctly (read the diff at the actual tip, build+test, never approve on assumption). Also covers delegating a PR review to a subagent.
- `join-irc`: Use when connecting to the local IRC server and joining the botfam conversation. Establishes identity, launches the client in the background, starts the wake watcher, performs replay-on-join, and documents how to send messages and join channels.
- `writing-markdown`: Use when creating or editing any markdown under doc/ or README.md in the botfam repo — canonical frontmatter schema, block-style YAML, mdformat workflow, and the rules that keep agent-, Obsidian-, and GitHub-rendered markdown from fighting each other.

Refer to the MCP resources above for all operational details.

