#!/usr/bin/env bash
# move-to-fams-layout.sh — one-time operator script: move the botfam and
# deep-cuts checkout families into the ratified multi-fam layout
# (doc/proposals/fam-generalization.md):
#
#   ~/src/fams/<fam>/main        canonical checkout (merge target)
#   ~/src/fams/<fam>/wt-<actor>  one worktree per actor
#
# Run this ONLY while all agent harness sessions are closed (no IRC clients,
# no wake watchers). The ergo/scribe containers may keep running: they mount
# ~/src/botfam-collab and ~/botfam-irc, which this script does not touch.
#
# The script copies itself to a temp file and re-execs from there, because it
# lives inside a directory it is about to move.

set -euo pipefail

# MOVE_FAMS_SRC / MOVE_FAMS_FORCE exist for sandbox testing only.
SRC="${MOVE_FAMS_SRC:-$HOME/src}"
FAMS="$SRC/fams"

# --- re-exec from a temp copy so the move can't yank the script mid-run ----
if [[ "${MOVE_FAMS_REEXEC:-}" != "1" ]]; then
  tmp="$(mktemp -t move-to-fams-layout)"
  cat "$0" > "$tmp"
  chmod +x "$tmp"
  MOVE_FAMS_REEXEC=1 exec bash "$tmp" "$@"
fi

# --- move table: "old-path new-path" ---------------------------------------
MOVES=(
  "$SRC/botfam            $FAMS/botfam/main"
  "$SRC/wt-claude         $FAMS/botfam/wt-claude"
  "$SRC/wt-agy            $FAMS/botfam/wt-agy"
  "$SRC/wt-codex          $FAMS/botfam/wt-codex"
  "$SRC/wt-rlupi          $FAMS/botfam/wt-rlupi"
  "$SRC/deep-cuts         $FAMS/deep-cuts/main"
  "$SRC/deep-cuts-claude  $FAMS/deep-cuts/wt-claude"
  "$SRC/deep-cuts-agy     $FAMS/deep-cuts/wt-agy"
  "$SRC/deep-cuts-codex   $FAMS/deep-cuts/wt-codex"
  "$SRC/deep-cuts-agy-adapter $FAMS/deep-cuts/wt-agy-adapter"
)

fail() { echo "ABORT: $*" >&2; exit 1; }

# --- preflight --------------------------------------------------------------
echo "== preflight =="

if [[ "${MOVE_FAMS_FORCE:-}" != "1" ]] && pgrep -fl 'botfam (irc-client|irc-wait)' >/dev/null 2>&1; then
  pgrep -fl 'botfam (irc-client|irc-wait)'
  fail "botfam IRC clients/watchers still running — close the agent sessions first"
fi

for entry in "${MOVES[@]}"; do
  read -r old new <<<"$entry"
  [[ -e "$old" ]] || fail "missing source: $old (already moved? edit the move table)"
  [[ -e "$new" ]] && fail "target already exists: $new"
done

for entry in "${MOVES[@]}"; do
  read -r old new <<<"$entry"
  if [[ -d "$old/.git" || -f "$old/.git" ]]; then
    dirty="$(git -C "$old" status --porcelain)" || fail "git status failed in $old"
    if [[ -n "$dirty" ]]; then
      echo "$dirty" | head -5
      fail "dirty working tree: $old — commit or stash first"
    fi
  fi
done
echo "preflight ok: all sources present, targets free, trees clean, no clients running"

# --- move -------------------------------------------------------------------
echo "== moving =="
mkdir -p "$FAMS/botfam" "$FAMS/deep-cuts"
for entry in "${MOVES[@]}"; do
  read -r old new <<<"$entry"
  echo "mv $old -> $new"
  mv "$old" "$new"
done

# --- repair worktree links --------------------------------------------------
echo "== git worktree repair =="
git -C "$FAMS/botfam/main" worktree repair \
  "$FAMS/botfam/wt-claude" "$FAMS/botfam/wt-agy" \
  "$FAMS/botfam/wt-codex" "$FAMS/botfam/wt-rlupi"
git -C "$FAMS/deep-cuts/main" worktree repair \
  "$FAMS/deep-cuts/wt-claude" "$FAMS/deep-cuts/wt-agy" \
  "$FAMS/deep-cuts/wt-codex" "$FAMS/deep-cuts/wt-agy-adapter"

# --- verify -----------------------------------------------------------------
echo "== verify =="
status=0
for entry in "${MOVES[@]}"; do
  read -r old new <<<"$entry"
  if ! git -C "$new" status --porcelain >/dev/null 2>&1; then
    echo "BROKEN: git status fails in $new"
    status=1
    continue
  fi
  branch="$(git -C "$new" rev-parse --abbrev-ref HEAD)"
  email="$(git -C "$new" config user.email || echo '(none)')"
  printf 'ok  %-42s %-28s %s\n' "${new/#$HOME/~}" "[$branch]" "$email"
done

echo
echo "expected identities (botfam fam): wt-claude -> roberto.lupi+claude@gmail.com,"
echo "wt-agy -> +agy, wt-codex -> +codex; main/wt-rlupi -> roberto.lupi@gmail.com."
echo "deep-cuts worktrees will show roberto.lupi@gmail.com until migration step 3"
echo "(per-worktree identity) lands — that is expected, not breakage."

# --- claude harness memory continuity ---------------------------------------
# The Claude Code project key derives from the main checkout path; keep the
# existing memory readable from the new key via a symlink.
OLD_PROJ="$HOME/.claude/projects/-Users-rlupi-src-botfam"
NEW_PROJ="$HOME/.claude/projects/-Users-rlupi-src-fams-botfam-main"
if [[ -d "$OLD_PROJ" && ! -e "$NEW_PROJ" ]]; then
  ln -s "$OLD_PROJ" "$NEW_PROJ"
  echo "linked claude memory: $NEW_PROJ -> $OLD_PROJ"
fi

echo
echo "== done (status $status) =="
cat <<'EOF'
next steps:
  1. Relaunch agent sessions from the NEW worktree paths, e.g.
       ~/src/fams/botfam/wt-claude   (botfam-claude)
       ~/src/fams/deep-cuts/wt-claude (deep-cuts-claude, once that fam is live)
  2. Infra untouched by design: ~/src/botfam-collab, ~/botfam-irc, the ergo and
     scribe containers, ~/bin/botfam. Nothing to restart.
  3. wt-agy-adapter (branch agy/coordination-adapter) was moved along; agy
     should decide whether it is still needed.
  4. Old paths are gone on purpose — update any shell aliases/GUI clients
     (Obsidian!) that pointed at ~/src/botfam or ~/src/wt-*.
EOF
exit "$status"
