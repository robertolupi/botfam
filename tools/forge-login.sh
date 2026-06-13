#!/usr/bin/env bash
#
# forge-login.sh — mint a forge (Gitea/Forgejo) access token from a
# username/password and store it where botfam-next / the forge MCP server
# expect it: ~/.botfam/token-<fam>-<actor>
#
# Fork-neutral: works against any Gitea-API-compatible forge (Gitea, Forgejo).
# Agent-generic: <actor> and <fam> are derived from the worktree (both
# overridable), so each agent/fam gets its own token file — no hardcoded names.
#
# The password is read hidden, never echoed, and unset after use; only the
# minted token is written to disk (umask 077). Nothing secret is printed.
#
# Usage:
#   tools/forge-login.sh --host <url> [--user <name>] [--actor <name>]
#                        [--fam <name>] [--scopes <csv>] [--token-file <path>] [--yes]
#
set -euo pipefail
set +x  # never trace — keep credentials out of any log

HOST=""; USER_IN=""; ACTOR=""; FAM=""; TOKEN_FILE=""; ASSUME_YES=""
SCOPES="read:repository,read:issue,read:organization,read:user,read:misc"
while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2;;
    --user) USER_IN="$2"; shift 2;;
    --actor) ACTOR="$2"; shift 2;;
    --fam) FAM="$2"; shift 2;;
    --scopes) SCOPES="$2"; shift 2;;
    --token-file) TOKEN_FILE="$2"; shift 2;;
    --yes|-y) ASSUME_YES=1; shift;;
    -h|--help) sed -n '2,20p' "$0"; exit 0;;
    *) echo "error: unknown arg $1" >&2; exit 2;;
  esac
done

[ -n "$HOST" ] || { echo "error: --host <url> is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "error: curl required" >&2; exit 2; }
command -v jq   >/dev/null || { echo "error: jq required"   >&2; exit 2; }

# actor: explicit, else this worktree's basename minus a wt-/botfam- prefix
if [ -z "$ACTOR" ]; then
  ACTOR="$(basename "$PWD")"; ACTOR="${ACTOR#wt-}"; ACTOR="${ACTOR#botfam-}"
fi
# fam: explicit, else $BOTFAM_FAM, else the worktree's parent dir name
[ -n "$FAM" ] || FAM="${BOTFAM_FAM:-$(basename "$(dirname "$PWD")")}"
[ -n "$TOKEN_FILE" ] || TOKEN_FILE="$HOME/.botfam/token-${FAM}-${ACTOR}"

[ -n "$USER_IN" ] || read -rp "Forge username: " USER_IN

# Footgun guard: the token FILENAME is keyed to <actor> (derived from the cwd),
# but you authenticate as <user>. If they disagree (e.g. running inside wt-claude
# with --user agy-bot), you'd overwrite the wrong actor's token. Warn + confirm.
if [ "${USER_IN%-bot}" != "$ACTOR" ]; then
  echo "WARNING: forge user '$USER_IN' does not match actor '$ACTOR'." >&2
  echo "  Token would be written to: $TOKEN_FILE  (actor '$ACTOR')." >&2
  echo "  That OVERWRITES '$ACTOR''s token and is NOT '${USER_IN%-bot}''s file." >&2
  echo "  To set up '${USER_IN%-bot}': run from its worktree, or pass --actor ${USER_IN%-bot}." >&2
  if [ "$ASSUME_YES" != "1" ]; then
    printf "  Continue anyway? [y/N] " >&2; read -r ans
    case "$ans" in y|Y|yes|YES) ;; *) echo "aborted." >&2; exit 1 ;; esac
  fi
fi

read -rsp "Forge password (or existing PAT): " PASS; echo

name="botfam-${FAM}-${ACTOR}-$(date +%Y%m%d%H%M%S)"
payload="$(jq -n --arg n "$name" --arg s "$SCOPES" '{name:$n, scopes:($s|split(","))}')"

resp="$(curl -fsS -u "$USER_IN:$PASS" -X POST "$HOST/api/v1/users/$USER_IN/tokens" \
  -H 'Content-Type: application/json' -d "$payload")" || {
    unset PASS
    echo "error: token request to $HOST failed — wrong creds, 2FA enabled, or an" >&2
    echo "       older forge that rejects 'scopes' (retry with --scopes '')." >&2
    exit 1
  }
unset PASS

tok="$(printf '%s' "$resp" | jq -r '.sha1 // empty')"
[ -n "$tok" ] || {
  echo "error: no token in response: $(printf '%s' "$resp" | jq -r '.message // .' 2>/dev/null)" >&2
  exit 1
}

mkdir -p "$(dirname "$TOKEN_FILE")"
( umask 077; printf '%s' "$tok" > "$TOKEN_FILE" )
echo "wrote token for ${FAM}/${ACTOR} -> ${TOKEN_FILE} ($(wc -c < "$TOKEN_FILE") bytes)"
echo "forge MCP server should use: GITEA_ACCESS_TOKEN_FILE=${TOKEN_FILE}  (host ${HOST})"
