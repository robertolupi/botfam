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
#   - skills/*/SKILL.md are excluded: their YAML frontmatter needs the
#     mdformat-frontmatter plugin; add it here if they ever come in scope.
#   - CLAUDE.md/AGENTS.md/GEMINI.md are generated harness files; format
#     their source, not the output.
#   - mdformat-gfm-alerts is required: without it the "> [!NOTE]" banners
#     are collapsed and stop rendering as alerts on GitHub.
#
# Usage: tools/mdformat.sh [mdformat flags] [paths...]
#        (no arguments: formats doc/ and README.md)
set -eu
cd "$(dirname "$0")/.."
[ "$#" -gt 0 ] || set -- doc/ README.md
exec uvx \
  --with mdformat-gfm==1.0.0 \
  --with mdformat-gfm-alerts==2.0.0 \
  mdformat==1.0.0 "$@"
