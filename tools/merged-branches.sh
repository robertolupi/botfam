#!/usr/bin/env bash
# merged-branches.sh — list remote branches already merged into the integration
# branch (botfam-next) or the release branch (main), and therefore safe to delete.
#
# Detects EVERY merge style Gitea allows:
#   • merge / fast-forward / rebase-FF  → branch tip is an ancestor of the target
#   • squash / rebase                   → merging the branch would add nothing
#                                          (3-way merge tree == target's tree)
# Skips protected branches, the targets themselves, and any branch checked out
# in a worktree.
#
# Usage:
#   ./merged-branches.sh                       # dry-run: report + delete commands
#   DELETE=1 ./merged-branches.sh              # actually delete the remote branches
#   REMOTE=origin TARGETS="botfam-next main" ./merged-branches.sh
#
# Needs git >= 2.38 (for `git merge-tree --write-tree`).
set -uo pipefail

REMOTE="${REMOTE:-origin}"
read -ra TARGETS <<<"${TARGETS:-botfam-next main}"
# Branches (name without the remote prefix) never proposed for deletion:
PROTECTED='^(main|master|botfam-next|agent/.*|human/.*)$'

git fetch -q --prune "$REMOTE" || true

# Branches checked out in a worktree must never be deleted.
_active=()
while IFS= read -r b; do [ -n "$b" ] && _active+=("$b"); done \
  < <(git worktree list --porcelain | sed -n 's#^branch refs/heads/##p')
is_active() { local x; for x in "${_active[@]:-}"; do [ "$x" = "$1" ] && return 0; done; return 1; }

# True if $2 (target) already contains everything in $1 (ref) via a squash/rebase
# merge: the 3-way merge of ref into target produces target's own tree.
tree_contained() {
  local mt
  mt=$(git merge-tree --write-tree "$2" "$1" 2>/dev/null | head -1) || return 1
  [ -n "$mt" ] && [ "$mt" = "$(git rev-parse "$2^{tree}")" ]
}

HITS=()
while IFS= read -r ref; do
  [ "$ref" = "$REMOTE" ] && continue           # skip the origin/HEAD symbolic ref
  br="${ref#"$REMOTE"/}"
  [ "$br" = "HEAD" ] && continue
  [[ "$br" =~ $PROTECTED ]] && continue
  is_active "$br" && continue
  for t in "${TARGETS[@]}"; do
    tgt="$REMOTE/$t"
    git rev-parse -q --verify "$tgt^{commit}" >/dev/null 2>&1 || continue
    if git merge-base --is-ancestor "$ref" "$tgt" 2>/dev/null; then
      HITS+=("$br|$t|merged"); break
    elif tree_contained "$ref" "$tgt"; then
      HITS+=("$br|$t|squash/contained"); break
    fi
  done
done < <(git for-each-ref --format='%(refname:short)' "refs/remotes/$REMOTE")

if [ "${#HITS[@]}" -eq 0 ]; then
  echo "No merged remote branches to delete on '$REMOTE'."
  exit 0
fi

printf '%-46s %-12s %s\n' "BRANCH" "MERGED-INTO" "HOW"
printf '%-46s %-12s %s\n' "----------------------------------------------" "------------" "------------------"
for h in "${HITS[@]}"; do IFS='|' read -r b t how <<<"$h"; printf '%-46s %-12s %s\n' "$b" "$t" "$how"; done

echo
echo "# safe to delete — run these (or re-run with DELETE=1):"
for h in "${HITS[@]}"; do IFS='|' read -r b _ _ <<<"$h"; printf 'git push %s --delete %q\n' "$REMOTE" "$b"; done

if [ "${DELETE:-0}" = "1" ]; then
  echo
  echo ">> DELETE=1 — deleting ${#HITS[@]} branch(es) on $REMOTE ..."
  for h in "${HITS[@]}"; do IFS='|' read -r b _ _ <<<"$h"; git push "$REMOTE" --delete "$b"; done
fi
