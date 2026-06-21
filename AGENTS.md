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
- `code-editing`: Use when editing Go code — especially for refactors, symbol renames, cross-package type changes, or exploring an unfamiliar package's API. Replaces manual grep+Read workflows with gopls MCP and codebase-memory-mcp tools that are faster, complete, and catch errors earlier. Trigger on "rename", "refactor", "replace type", "find all uses of", "callers of", "what fields does X have", or any time you reach for grep on Go source.
- `external-review`: Use when running a multi-model external review of a botfam session, doc, or change — fan the canonical prompt across configured models with botfam external-review, keep the raw reviews out-of-repo, then spawn a consolidation subagent to merge them into one unified review.
- `forge-autonomy`: Use when operating as a botfam agent on the self-hosted forge — noticing queued work by querying the forge (the supervised wake path is landing; `botfam wait` is legacy), and reviewing/approving pull requests correctly (read the diff at the actual tip, build+test, never approve on assumption). Also covers delegating a PR review to a subagent.
- `meta-review`: Use when a peer review of a forge artifact (PR, issue, or content) has just completed and you need STEP 2 — the immediate isolated risk meta-review. Spawned as a lightweight subagent in a separate context, it loads the process-risk glossary, checks the per-artifact risks (risk/phase-inversion, risk/superseded, risk/hollow-validation, and risk/speculative when applicable), and posts advisory risk/* + triage/* label suggestions with cited evidence. Trigger on "run the meta-review", "spawn the risk meta-reviewer", or on completion of a code-review / review / forge-autonomy PR review.
- `red-team`: Use when the user wants their own proposal, plan, design, idea, or approach **attacked rather than validated** — to get honest critique instead of agreeable confirmation. Trigger on "red-team this", "no yes-men", "attack this", "poke holes", "steelman then break it", "be brutal", "be honest", "tell me why this is wrong", "what am I missing", "don't just agree", "play devil's advocate", "critique don't validate", "stress-test this idea", or any request for adversarial review of the user's own thinking.
- `submitting-a-pr`: Use right before opening or submitting a pull request on the forge — the pre-PR self-check gate. Screens the diff for the cheap, mechanical process risks (single-artifact concept-fragmentation against the existing tree, phase-inversion, superseded decisions) by reading the matching process-risk glossary / antipattern wiki page if you haven't this session, and escalates the harder calls (hollow-validation, non-obvious speculative) to the meta-review. Trigger on "open a PR", "submit a PR", "ready to push the PR", "pre-PR review", or as the final gate in forge-autonomy / botfam-sprint before pull_request_write.
- `writing-markdown`: Use when creating or editing any markdown under doc/ or README.md in the botfam repo — canonical frontmatter schema, block-style YAML, mdformat workflow, and the rules that keep agent-, Obsidian-, and GitHub-rendered markdown from fighting each other.

Refer to the MCP resources above for all operational details.
