# Lineage v2 — scriba

**Where:** `~/src/scriba-claude-sandbox` (a sandbox clone of `scriba/scriba`).
**Status:** **design only — never implemented.** A docs repo plus five bootstrap
retrospectives. **The high-water mark of over-engineering in this line.**

scriba reacted to hydra's weak worktree isolation by reaching for heavy,
"proper" infrastructure: a local **Gitea** server as the coordination substrate.
It bought real isolation and a nice human-facing UI — at a cost so high the
project never got past standing the infrastructure up.

---

## What it was (proposed)

A "Gitea-native, git-airgapped, MCP" architecture (`doc/DESIGN_V2.md`):

- **Local Gitea server** as the coordination plane. Agents are full Gitea user
  accounts; each gets an isolated **SSH keypair** *and* a **Personal Access
  Token**.
- **Separate clones** (`git clone --shared`) instead of worktrees — each agent
  gets its own `.git/`, closing the hook-injection and shared-object-DB holes.
- **CCREP on Gitea PRs:** propose = open a PR, debate = PR comments, vote = PR
  reviews, consensus gate = branch-protection quorum. Human merge of a *design*
  PR unlocks the *implementation* phase.
- **Gitea Wiki** (`scriba.wiki.git`) as version-controlled shared memory.
- **`gitea-mcp-server`** (HTTP/SSE) bridges Gitea's REST API to agents as MCP
  tools — no custom server to write.
- **Docker test runners** (no network) for evaluating PRs.
- **Trinity memory** (`id.json` + FIFO-pruned `local.json`) per agent.
- **Airgap:** only the operator's workspace holds the public GitHub remote;
  agents push exclusively to local Gitea.

## Pros

- **Strong isolation** — a separate `.git/` per agent is a genuine improvement
  over hydra's shared worktree object DB.
- **First-class human UI** — PRs, threaded review, approvals, and a wiki are
  already built, polished, and human-legible.
- **Version-controlled shared memory** instead of ad-hoc local JSON.
- **Airgap and the design→implement human gate** are genuinely good workflow ideas.
- **No custom coordination server** — leans on Gitea's official MCP bridge.

## Cons

- **Enormous standing infrastructure** for what is fundamentally message-passing:
  a Gitea server **+** the MCP bridge **+** Docker runners, all running before two
  agents can say hello.
- **Credential management swallowed the project.** Accounts, SSH keys, and PATs
  per bot produced exactly the failure you'd predict: secrets were committed in
  plaintext (`FIRST_BOOTSTRAP.md`), had to be redacted and purged from history,
  and **still** require rotation. There are **five** bootstrap retrospectives,
  most of them about credential and first-login friction — not coordination.
- **Plain-HTTP transport** (`http://gitea:3000`) carried passwords and keys in
  cleartext on the LAN.
- **Never implemented.** The design was consensus-approved; the code does not
  exist. All evidence is of *setup* pain, none of operation.
- **Design drifted from reality** — the docs said org `scriba-collab` / repo
  `scriba-core` while every real bootstrap used `scriba` / `scriba`; broken
  `file://` cross-links pointed at a different repo. (Since corrected.)

## Lessons carried into botfam

- **The good ideas did not require a server.** Re-express them cheaply:
  - *design→implement gate* → a CCREP `design_doc` profile with a human merge,
    not a Gitea PR.
  - *per-agent identity* → an env var (botfam) or a bearer token (bottown), not a
    Gitea account + SSH key + PAT.
  - *airgap* → unchanged and free: the human still owns the only GitHub remote.
- **Standing infrastructure + credentials is an operational tax that dwarfs the
  coordination problem.** If onboarding needs five retrospectives, the
  architecture is the bug.
- **Don't build a parliament when you need a mailbox.** botfam is the deliberate
  swing back: one small binary, identity from the environment, state in a
  directory, nothing to provision and nothing to rotate.
- **Strong per-agent isolation is still worth having** — but as a *transport*
  property (separate sandboxes / a shared server with tokens), reintroduced
  carefully in **bottown**, not as a Git-hosting stack.
