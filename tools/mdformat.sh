#!/bin/sh
# Canonical markdown formatter invocation for this repo. Run it before
# committing doc changes; pass --check to verify without rewriting.
#
# Versions are pinned HERE because mdformat plugins activate by being
# installed — .mdformat.toml can only pin options (wrap, number). Two agents
# on different plugin sets would reflow each other's files forever, so any
# version bump must change this file for everyone at once.
#
# Scope notes:
#   - CLAUDE.md/AGENTS.md/GEMINI.md are generated harness files; format
#     their source, not the output. This script skips them (and the Go
#     templates under doc/template/*.tmpl) even when named explicitly, so
#     no invocation — globbed or hand-listed — can churn generated output or
#     mangle a template's `{{ ... }}` blocks. Regenerate the harness files
#     with `botfam agent-docs generate` instead.
#   - mdformat-gfm-alerts is required: without it the "> [!NOTE]" banners
#     are collapsed and stop rendering as alerts on GitHub.
#   - mdformat-frontmatter is required: doc/ files carry YAML frontmatter
#     (doc/proposals/doc-metadata.md); without the plugin mdformat mangles
#     the --- block. It also unblocks formatting skills/*/SKILL.md.
#
# Usage: tools/mdformat.sh [mdformat flags] [paths...]
#        (no arguments: formats doc/ and README.md)
set -eu
cd "$(dirname "$0")/.."
has_paths=false
for arg in "$@"; do
  case "$arg" in
    -*) ;;
    *) has_paths=true ;;
  esac
done
if [ "$has_paths" = false ]; then
  set -- "$@" doc/ README.md
fi

# Drop files mdformat must never touch (generated harness docs, Go
# templates), keeping flags and everything else. A flag-only invocation is
# left untouched so mdformat reports its own "nothing to do".
args=""
for arg in "$@"; do
  case "$arg" in
    -*) ;;
    *.tmpl)
      printf 'mdformat.sh: skipping Go template %s (format its rendered .md source)\n' "$arg" >&2
      continue
      ;;
    AGENTS.md | CLAUDE.md | GEMINI.md | ./AGENTS.md | ./CLAUDE.md | ./GEMINI.md)
      printf 'mdformat.sh: skipping generated harness file %s (run: botfam agent-docs generate)\n' "$arg" >&2
      continue
      ;;
  esac
  args="$args $arg"
done
# shellcheck disable=SC2086
set -- $args

exec uvx \
  --with mdformat-gfm==1.0.0 \
  --with mdformat-gfm-alerts==2.0.0 \
  --with mdformat-frontmatter==2.1.2 \
  mdformat==1.0.0 "$@"
