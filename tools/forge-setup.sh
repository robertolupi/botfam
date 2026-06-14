#!/usr/bin/env bash
#
# forge-setup.sh — one-shot per-agent forge onboarding. Wraps the three manual
# steps it used to take to put an agent on the fam's forge:
#
#   1. mint a token            (tools/forge-login.sh → ~/.botfam/token-<fam>-<actor>)
#   2. install the push helper (git-credential-botfam, scoped to the forge host)
#   3. register the forge MCP  (harness-aware; runs `claude mcp add` for Claude
#                               Code, prints instructions for other harnesses)
#
# …then verifies repo access with the freshly-minted token.
#
# Agent-generic and fork-neutral: <actor>/<fam> are derived from the worktree
# (same rules as forge-login.sh) and overridable. Nothing secret is printed.
#
# Usage:
#   tools/forge-setup.sh --host <url> [--repo <owner/name>] [--user <name>]
#       [--actor <name>] [--fam <name>] [--scopes <csv>]
#       [--harness auto|claude|none] [--reuse-token] [--global|--local]
#       [--skip-token] [--skip-helper] [--skip-mcp] [--yes]
#
set -euo pipefail
set +x  # never trace — keep credentials out of any log

here="$(cd "$(dirname "$0")" && pwd)"

HOST=""; REPO=""; USER_IN=""; ACTOR=""; FAM=""; SCOPES=""
HARNESS="auto"; REUSE=""; SCOPE="--global"; ASSUME_YES=""
SKIP_TOKEN=""; SKIP_HELPER=""; SKIP_MCP=""
while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2;;
    --repo) REPO="$2"; shift 2;;
    --user) USER_IN="$2"; shift 2;;
    --actor) ACTOR="$2"; shift 2;;
    --fam) FAM="$2"; shift 2;;
    --scopes) SCOPES="$2"; shift 2;;
    --harness) HARNESS="$2"; shift 2;;
    --reuse-token) REUSE=1; shift;;
    --global) SCOPE="--global"; shift;;
    --local) SCOPE="--local"; shift;;
    --skip-token) SKIP_TOKEN=1; shift;;
    --skip-helper) SKIP_HELPER=1; shift;;
    --skip-mcp) SKIP_MCP=1; shift;;
    --yes|-y) ASSUME_YES=1; shift;;
    -h|--help) sed -n '2,30p' "$0"; exit 0;;
    *) echo "error: unknown arg $1" >&2; exit 2;;
  esac
done

[ -n "$HOST" ] || { echo "error: --host <url> is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "error: curl required" >&2; exit 2; }

# Identity (sourced from lib-botfam.sh)
source "$(dirname "$0")/lib-botfam.sh"
derive_identity

# Forge host (host[:port]) for git-config scoping, derived from --host.
FORGE_HOSTPORT="${HOST#*://}"; FORGE_HOSTPORT="${FORGE_HOSTPORT%%/*}"
SCHEME="${HOST%%://*}"

# Repo for the access check: explicit, else from remote.gitea.url.
if [ -z "$REPO" ]; then
  ru="$(git config --get remote.gitea.url 2>/dev/null || true)"
  if [ -n "$ru" ]; then
    ru="${ru%.git}"; REPO="$(printf '%s' "$ru" | sed -E 's#.*[:/]([^/]+/[^/]+)$#\1#')"
  fi
fi

echo "forge-setup: fam=${FAM} actor=${ACTOR} user=${USER_IN:-${ACTOR}-bot} host=${HOST}"

# --- 1. token ----------------------------------------------------------------
if [ -n "$SKIP_TOKEN" ]; then
  echo "[1/4] token: skipped (--skip-token)"
elif [ -n "$REUSE" ] && [ -s "$TOKEN_FILE" ]; then
  echo "[1/4] token: reusing existing ${TOKEN_FILE}"
else
  echo "[1/4] token: minting via forge-login.sh"
  set -- --host "$HOST" --actor "$ACTOR" --fam "$FAM"
  [ -n "$USER_IN" ] && set -- "$@" --user "$USER_IN"
  [ -n "$SCOPES" ] && set -- "$@" --scopes "$SCOPES"
  [ -n "$ASSUME_YES" ] && set -- "$@" --yes
  "$here/forge-login.sh" "$@"
fi

# --- 2. credential helper -----------------------------------------------------
if [ -n "$SKIP_HELPER" ]; then
  echo "[2/4] helper: skipped (--skip-helper)"
else
  helper="$here/git-credential-botfam"
  if [ ! -x "$helper" ]; then
    echo "[2/4] helper: WARNING ${helper} not found/executable — skipping (see #4)" >&2
  else
    if [ "$SCOPE" = "--local" ]; then
      git config "$SCOPE" credential.helper ""
    fi
    key="credential.${SCHEME}://${FORGE_HOSTPORT}.helper"
    git config "$SCOPE" "$key" "$helper"
    echo "[2/4] helper: git config ${SCOPE} ${key} -> ${helper}"
  fi
fi

# --- 3. forge MCP registration ------------------------------------------------
detect_harness() {
  [ "$HARNESS" != "auto" ] && { echo "$HARNESS"; return; }
  command -v claude >/dev/null 2>&1 && { echo "claude"; return; }
  echo "unknown"
}
if [ -n "$SKIP_MCP" ]; then
  echo "[3/4] mcp: skipped (--skip-mcp)"
else
  h="$(detect_harness)"
  case "$h" in
    claude)
      echo "[3/4] mcp: registering 'forge' for Claude Code"
      claude mcp add forge -e "GITEA_ACCESS_TOKEN_FILE=${TOKEN_FILE}" \
        -- gitea-mcp-server -t stdio -H "$HOST" \
        && echo "      registered (claude mcp add forge)" \
        || echo "      WARNING: 'claude mcp add' failed — register manually (see below)" >&2
      ;;
    none)
      echo "[3/4] mcp: skipped (--harness none)"
      ;;
    *)
      echo "[3/4] mcp: harness not auto-detected — register manually:"
      echo "      env GITEA_ACCESS_TOKEN_FILE=${TOKEN_FILE}"
      echo "      command: gitea-mcp-server -t stdio -H ${HOST}"
      echo "      name it 'forge'. (Antigravity/Codex: add to that harness's MCP config.)"
      ;;
  esac
fi

# --- 4. access check ----------------------------------------------------------
if [ -s "$TOKEN_FILE" ]; then
  tok="$(tr -d '\r\n' < "$TOKEN_FILE")"
  who="$(curl -fsS -H "Authorization: token $tok" "$HOST/api/v1/user" 2>/dev/null \
         | sed -n 's/.*"login":"\([^"]*\)".*/\1/p' | head -1 || true)"
  if [ -n "$who" ]; then
    echo "[4/4] access: token authenticates as '${who}'"
    if [ -n "$REPO" ]; then
      code="$(curl -fsS -o /dev/null -w '%{http_code}' -H "Authorization: token $tok" \
              "$HOST/api/v1/repos/${REPO}" 2>/dev/null || true)"
      echo "      repo ${REPO}: HTTP ${code} ($([ "$code" = 200 ] && echo accessible || echo 'check access'))"
    fi
  else
    echo "[4/4] access: WARNING token did not authenticate against ${HOST}" >&2
  fi
else
  echo "[4/4] access: no token file at ${TOKEN_FILE} — skipped"
fi

echo "forge-setup: done for ${FAM}/${ACTOR}."
