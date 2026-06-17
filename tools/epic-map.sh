#!/bin/sh
# Regenerate the issue/epic map so it never goes stale. Pulls the live forge
# and writes a self-contained interactive d3 page (no build step to view —
# just open it). Run on demand; wire into CI nightly if you want it committed.
#
#   tools/epic-map.sh            # -> wiki/epic-map.html (the wiki checkout)
#   tools/epic-map.sh out.html   # custom path
#
# Uses an installed `botfam` if on PATH, else `go run ./cmd/botfam`.
set -eu

out="${1:-wiki/epic-map.html}"
mkdir -p "$(dirname "$out")"

# Prefer the in-repo source build (the installed botfam may predate
# `forge graph`); fall back to an installed botfam outside the repo.
if [ -f cmd/botfam/main.go ]; then
  bf="go run ./cmd/botfam"
elif command -v botfam >/dev/null 2>&1; then
  bf="botfam"
else
  echo "no botfam: run from the repo root or install botfam" >&2
  exit 1
fi

$bf forge graph --all --format html --out "$out"
echo "wrote $out — open it in a browser (toggles: hide closed/isolated, epics+children)"
