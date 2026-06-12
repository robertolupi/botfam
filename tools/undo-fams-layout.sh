#!/usr/bin/env bash
# undo-fams-layout.sh — reverse of move-to-fams-layout.sh: restore the botfam
# and deep-cuts checkout families from ~/src/fams/<fam>/{main,wt-<actor>} back
# to their pre-move locations under ~/src/.
#
# Run ONLY while all agent harness sessions are closed. Safe because the move
# is pure directory renames: branches, commits, and per-worktree identity all
# live in git state that the rename never touches.
#
# Last-resort note (covers even a damaged worktree, before or after undo):
# worktrees are disposable; the branch is the durable state. To rebuild one
# from scratch:
#     git -C <main> worktree remove --force <path>
#     git -C <main> worktree add <path> <branch>     # e.g. agent/claude
# Clean trees are enforced before the move, so no uncommitted work is at risk.

set -euo pipefail

# MOVE_FAMS_SRC / MOVE_FAMS_FORCE exist for sandbox testing only.
SRC="${MOVE_FAMS_SRC:-$HOME/src}"
FAMS="$SRC/fams"

# --- re-exec from a temp copy so the move can't yank the script mid-run ----
if [[ "${MOVE_FAMS_REEXEC:-}" != "1" ]]; then
  tmp="$(mktemp -t undo-fams-layout)"
  cat "$0" > "$tmp"
  chmod +x "$tmp"
  MOVE_FAMS_REEXEC=1 exec bash "$tmp" "$@"
fi

# --- move table: "current-path restore-path" (reverse of the move script) ---
MOVES=(
  "$FAMS/botfam/main              $SRC/botfam"
  "$FAMS/botfam/wt-claude         $SRC/wt-claude"
  "$FAMS/botfam/wt-agy            $SRC/wt-agy"
  "$FAMS/botfam/wt-codex          $SRC/wt-codex"
  "$FAMS/botfam/wt-rlupi          $SRC/wt-rlupi"
  "$FAMS/deep-cuts/main           $SRC/deep-cuts"
  "$FAMS/deep-cuts/wt-claude      $SRC/deep-cuts-claude"
  "$FAMS/deep-cuts/wt-agy         $SRC/deep-cuts-agy"
  "$FAMS/deep-cuts/wt-codex       $SRC/deep-cuts-codex"
  "$FAMS/deep-cuts/wt-agy-adapter $SRC/deep-cuts-agy-adapter"
)

fail() { echo "ABORT: $*" >&2; exit 1; }

# --- preflight --------------------------------------------------------------
echo "== preflight =="

if [[ "${MOVE_FAMS_FORCE:-}" != "1" ]] && pgrep -fl 'botfam (irc-client|irc-wait)' >/dev/null 2>&1; then
  pgrep -fl 'botfam (irc-client|irc-wait)'
  fail "botfam IRC clients/watchers still running — close the agent sessions first"
fi

for entry in "${MOVES[@]}"; do
  read -r cur old <<<"$entry"
  [[ -e "$cur" ]] || fail "missing source: $cur (not moved, or already undone? edit the move table)"
  [[ -e "$old" ]] && fail "restore target already exists: $old"
done

for entry in "${MOVES[@]}"; do
  read -r cur old <<<"$entry"
  if [[ -d "$cur/.git" || -f "$cur/.git" ]]; then
    dirty="$(git -C "$cur" status --porcelain)" || fail "git status failed in $cur"
    if [[ -n "$dirty" ]]; then
      echo "$dirty" | head -5
      fail "dirty working tree: $cur — commit or stash first"
    fi
  fi
done
echo "preflight ok: all sources present, targets free, trees clean, no clients running"

# --- move back ---------------------------------------------------------------
echo "== restoring =="
for entry in "${MOVES[@]}"; do
  read -r cur old <<<"$entry"
  echo "mv $cur -> $old"
  mv "$cur" "$old"
done
rmdir "$FAMS/botfam" "$FAMS/deep-cuts" 2>/dev/null || true
rmdir "$FAMS" 2>/dev/null || true

# --- repair worktree links ---------------------------------------------------
echo "== git worktree repair =="
git -C "$SRC/botfam" worktree repair \
  "$SRC/wt-claude" "$SRC/wt-agy" "$SRC/wt-codex" "$SRC/wt-rlupi"
git -C "$SRC/deep-cuts" worktree repair \
  "$SRC/deep-cuts-claude" "$SRC/deep-cuts-agy" \
  "$SRC/deep-cuts-codex" "$SRC/deep-cuts-agy-adapter"

# --- verify -------------------------------------------------------------------
echo "== verify =="
status=0
for entry in "${MOVES[@]}"; do
  read -r cur old <<<"$entry"
  if ! git -C "$old" status --porcelain >/dev/null 2>&1; then
    echo "BROKEN: git status fails in $old (see recreate-from-branch note in header)"
    status=1
    continue
  fi
  branch="$(git -C "$old" rev-parse --abbrev-ref HEAD)"
  email="$(git -C "$old" config user.email || echo '(none)')"
  printf 'ok  %-42s %-28s %s\n' "${old/#$HOME/~}" "[$branch]" "$email"
done

# --- claude harness memory symlink -------------------------------------------
NEW_PROJ="$HOME/.claude/projects/-Users-rlupi-src-fams-botfam-main"
if [[ -L "$NEW_PROJ" ]]; then
  rm "$NEW_PROJ"
  echo "removed claude memory symlink $NEW_PROJ"
fi

echo
echo "== done (status $status) =="
echo "relaunch agent sessions from the restored paths (~/src/wt-claude etc.)."
exit "$status"
