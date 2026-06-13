#!/bin/sh
# Set this worktree's git identity (per-worktree config, immune to user.*
# entries in the shared .git/config — the 2026-06-12 misattribution bug).
# See doc/collab/PROTOCOL.md §4 and doc/user/PROTOCOL.md.
#
# Usage: tools/setup-worktree-identity.sh <actor>   e.g. rlupi, claude, agy
set -eu

[ "$#" -eq 1 ] || { echo "usage: tools/setup-worktree-identity.sh <actor>" >&2; exit 1; }
actor=$1

exec botfam worktree init "$actor"
