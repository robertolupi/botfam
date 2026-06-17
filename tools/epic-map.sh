#!/bin/sh
# Regenerate the issue/epic map so it never goes stale. Pulls the live forge
# and writes a self-contained interactive d3 page (no build step to view —
# just open it). Run on demand; wire into CI nightly if you want it committed.
#
#   tools/epic-map.sh            # -> docs/epic-map.html
#   tools/epic-map.sh out.html   # custom path
#
# Uses an installed `botfam` if on PATH, else `go run ./cmd/botfam`.
set -eu

out="${1:-docs/epic-map.html}"
mkdir -p "$(dirname "$out")"

if command -v botfam >/dev/null 2>&1; then
  bf="botfam"
else
  bf="go run ./cmd/botfam"
fi

$bf forge graph --all --format html --out "$out"
echo "wrote $out — open it in a browser (toggles: hide closed/isolated, epics+children)"
