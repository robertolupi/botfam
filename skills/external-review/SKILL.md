---
name: external-review
description: Use when running a multi-model external review of a botfam session, doc, or change — fan the canonical prompt across configured models with botfam external-review, keep the raw reviews out-of-repo, then spawn a consolidation subagent to merge them into one unified review.
---

# External Review in the botfam Repo

Use this skill when you want several outside models (local ollama and/or API
providers) to review the same material and you need one trustworthy, deduped
verdict — not N raw transcripts dumped into your context.

The workflow has two stages on purpose:

1. **Fan out** with `botfam external-review`, which writes one raw review per
   model **out-of-repo** under `~/.botfam/reviews/<ts>-<slug>/`.
2. **Consolidate** by spawning a subagent that reads those raw files and writes
   a single unified review. The raw reviews stay out of the main agent's
   context — only the consolidated result comes back.

## Why keep the raw reviews out of context

Each model returns a full multi-section review (see the canonical prompt at
`doc/review/EXTERNAL-REVIEW-PROMPT.md`). Reading three or four of them directly
floods the main context and biases you toward whichever reviewer you read last.
The script deliberately writes them to disk and prints
`NEXT: spawn a consolidation subagent on this dir`. Honor that: delegate the
reading.

## Stage 1 — fan out

Run the command with at least one material file and at least one model. Models
are chosen entirely via flags — the script bakes in no defaults, because the
operator knows the current best model names and the script does not.

```sh
botfam external-review [options] MATERIAL [MATERIAL...]
botfam external-review --pr <index> [options]   # review a Gitea PR directly
```

**Reviewing a Gitea PR (`--pr <index>`).** Instead of local files, the script
pulls the PR's metadata, description, discussion comments, reviews, and unified
diff via the Gitea API — host/owner/repo are resolved from the active git
remote and the token from `~/.botfam/token-<fam>-<actor>`. It assembles them
into one material doc and slugs the out dir `pr-<index>`. Use this to fan an
external review across a code change under review rather than a session or doc:

```sh
botfam external-review --pr 34 --gemini gemini-2.5-pro --openai gpt-5
```

Provider/model selection (repeatable — pass as many as you like):

- `--ollama MODEL` — run a local ollama model (e.g. `--ollama qwen3.5:35b`).
- `--gemini MODEL` — run a Gemini model; needs `GEMINI_API_KEY` in the env.
- `--openai MODEL` — run an OpenAI model; needs `OPENAI_API_KEY` in the env.

Options:

- `--prompt FILE` — canonical prompt; default
  `doc/review/EXTERNAL-REVIEW-PROMPT.md`. Only the text **below** the
  `PROMPT BEGINS BELOW THIS LINE` marker is used; the operator instructions
  above it are skipped.
- `--pr <index>` — review a Gitea PR directly (see above); used instead of
  MATERIAL files.
- `--out DIR` — output dir; default
  `${BOTFAM_REVIEW_DIR:-$HOME/.botfam/reviews}/<ts>-<slug>`, where `<slug>` is
  derived from the first material file's basename (or `pr-<index>` with
  `--pr`).
- `--ollama-host URL` — default `http://localhost:11434`.
- `-h` | `--help` — print usage.

Example — review a session against two local models:

```sh
botfam external-review \
  --ollama qwen3.5:35b \
  --ollama gemma4:31b \
  doc/collab/sessions/2026-06-12-doc-update/session.md
```

`botfam external-review` is a Go subcommand: all providers are reached over the
OpenAI-compatible chat API (one client). It reads `GEMINI_API_KEY` /
`OPENAI_API_KEY` from the environment only and never prints them. Unreachable
ollama or an unset API key is skipped with a warning, not a hard failure.

### What stage 1 produces

In the out dir:

- `review-<provider>-<model>.md` — one raw review per model that ran.
- `combined-prompt.txt` — the prompt below the marker plus the material(s).
- `MANIFEST.txt` — timestamp, prompt path, material list, and the models that
  actually ran.

Before consolidating, **read `MANIFEST.txt`** (small, safe to read) to confirm
which models ran and which were skipped. Do not read the `review-*.md` files
yourself.

## Stage 2 — spawn the consolidation subagent

Spawn a subagent whose entire job is to read every `review-*.md` in the out dir
and return one unified review. Pass it the out dir path and the original
material path(s). Instruct it to:

- **Dedupe.** Collapse the same finding raised by multiple models into one
  entry; do not list it N times.
- **Weight convergence.** A point several models reach independently is
  stronger than one model's lone claim; say how many reviewers raised each
  point.
- **Flag wrong-premise / confabulation.** A reviewer working from stale ground
  truth, or one that fabricated facts not present in the material, produces
  confidently wrong advice (verified failure mode — see the wiki's
  `review-2026-06-11-meta` page). Call these out and discount them rather than
  averaging them in.
- **Preserve the canonical section shape** from the prompt — what landed
  cleanly, pain points, blind spots, proposals, action items, open questions —
  so the unified review is comparable to past ones.
- **Recommend concrete edits**, each actionable by a single owner.
- **For a PR review (`--pr`)**, judge the change against its own stated intent:
  does the diff actually deliver what the **PR description** claims, and does
  it address the **discussion/review comments**? Call out anything the change
  misses, regresses, or leaves unaddressed from the thread — not just abstract
  code quality.

The subagent returns the unified review text. It should NOT echo the raw
reviews back. Keep its output as the only review artifact that enters the main
context.

## Recording the result

If the review is worth keeping, record the unified result on the Gitea wiki at
`wiki/review-YYYY-MM-DD-<reviewer>.md` (per the operator instructions in
`doc/review/EXTERNAL-REVIEW-PROMPT.md`) and format it with
`tools/mdformat.sh <file>` before committing+pushing from inside `wiki/` (the
wiki is its own repo, no PR needed — botfam#55). The raw per-model reviews stay
out-of-repo under `~/.botfam/reviews/` — do not commit them.

## Don't

- Don't read the raw `review-*.md` files into the main context — that defeats
  the whole point. Delegate to the consolidation subagent.
- Don't bake model names into scripts or docs; pass them as flags so the
  operator controls which models run.
- Don't trust a reviewer's claims about the codebase that aren't grounded in
  the material you fed it; treat any reviewer with cross-session memory as
  warm, not cold.
