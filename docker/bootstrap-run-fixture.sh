#!/usr/bin/env bash
# bootstrap-run-fixture.sh — seed and snapshot a deterministic issue-run fixture.
#
# Usage:
#   bash docker/bootstrap-run-fixture.sh seed
#   bash docker/bootstrap-run-fixture.sh inspect
#
# The script is intentionally conservative and idempotent so local runs can repeat
# without mutating fixture intent across repeated executions.
set -eu
set -o pipefail

FORGE="http://localhost:13000"
OWNER="botfam"
REPO="run-issue-fixture"
BRANCH="run-issue-smoke"
ISSUE_TITLE="Run one issue through fixture repo for botfam run smoke"
ISSUE_BODY='Seeded fixture issue for `botfam run --issue` discovery spikes.'
SNAP_DIR="${BOTFAM_RUN_FIXTURE_STATE_DIR:-/tmp/botfam-run-fixture-state}"
COMPOSE="compose.test.yaml"

api_call() {
  local method=$1 path=$2 token=$3 body=${4-}
  local tmp code
  tmp="$(mktemp)"
  if [ -n "$body" ]; then
    code="$(curl -sS -o "$tmp" -w "%{http_code}" \
      -X "$method" \
      -H "Authorization: token ${token}" \
      -H "Content-Type: application/json" \
      -d "$body" \
      "$FORGE/api/v1$path")"
  else
    code="$(curl -sS -o "$tmp" -w "%{http_code}" \
      -X "$method" \
      -H "Authorization: token ${token}" \
      "$FORGE/api/v1$path")"
  fi
  LAST_HTTP_CODE="$code"
  cat "$tmp"
  rm -f "$tmp"
}

api_capture() {
  local __var=$1
  shift
  local tmp
  tmp="$(mktemp)"
  if ! api_call "$@" > "$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  printf -v "$__var" '%s' "$(cat "$tmp")"
  rm -f "$tmp"
}

ensure_admin_token() {
  if [ ! -f "$HOME/.botfam/token-botfam-admin-test" ]; then
    bash "$(dirname "$0")/bootstrap-test-forgejo.sh"
  fi

  token="$(tr -d '\r\n' < "$HOME/.botfam/token-botfam-admin-test")"
  if [ -z "${token:-}" ]; then
    return 1
  fi
  echo "$token"
}

read_repo_default_branch() {
  local json=$1
  printf '%s' "$json" | python3 -c 'import sys, json
raw = sys.stdin.read().strip()
if not raw:
    print("main")
    raise SystemExit
print(json.loads(raw).get("default_branch", "main"))
'
}

ensure_repo() {
  local token=$1
  local resp
  if ! api_capture resp GET "/repos/$OWNER/$REPO" "$token"; then
    return 1
  fi

  if [ "$LAST_HTTP_CODE" != "200" ]; then
    payload='{"name":"'"$REPO"'","private":false,"auto_init":true,"default_branch":"main"}'
    api_capture resp POST "/orgs/$OWNER/repos" "$token" "$payload"
    if [ "$LAST_HTTP_CODE" != "201" ]; then
      echo "bootstrap-run-fixture: failed to create repo $OWNER/$REPO (HTTP ${LAST_HTTP_CODE})" >&2
      return 1
    fi
  fi
  printf "%s" "$resp"
}

ensure_branch() {
  local token=$1 default_branch=$2
  if api_call GET "/repos/$OWNER/$REPO/branches/$BRANCH" "$token" >/dev/null 2>&1; then
    if [ "$LAST_HTTP_CODE" = "200" ]; then
      return 0
    fi
  fi

  api_capture ref_resp GET "/repos/$OWNER/$REPO/branches/$default_branch" "$token"
  if [ "$LAST_HTTP_CODE" != "200" ]; then
    echo "bootstrap-run-fixture: unable to read base branch $default_branch for $OWNER/$REPO (HTTP ${LAST_HTTP_CODE})" >&2
    return 1
  fi
  base_sha="$(python3 -c 'import json,sys; commit=json.load(sys.stdin).get("commit",{}); print(commit.get("sha") or commit.get("id") or "")' <<<"$ref_resp")"
  if [ -z "$base_sha" ]; then
    echo "bootstrap-run-fixture: missing commit SHA for base branch $default_branch" >&2
    return 1
  fi

  body='{"new_branch_name":"'"$BRANCH"'","old_branch_name":"'"$default_branch"'"}'
  api_call POST "/repos/$OWNER/$REPO/branches" "$token" "$body" >/dev/null
  if [ "$LAST_HTTP_CODE" != "201" ]; then
    echo "bootstrap-run-fixture: failed create branch $BRANCH (HTTP ${LAST_HTTP_CODE})" >&2
    return 1
  fi
}

ensure_seed_file() {
  local token=$1
  local body_payload
  body_payload='{"message":"Add baseline fixture file","branch":"'"$BRANCH"'","content":"QmVhcmVyLXNlZWRlciBmaXhldHVyZSBmaWxl"}'
  api_call PUT "/repos/$OWNER/$REPO/contents/fixture/BASeline.txt" "$token" "$body_payload" >/dev/null || true
}

ensure_issue() {
  local token=$1
  local issues
  if ! api_capture issues GET "/repos/$OWNER/$REPO/issues?state=open" "$token"; then
    return 1
  fi
  issue_number="$(printf '%s' "$issues" | python3 -c 'import json, sys; title=sys.argv[1]; issues=json.loads(sys.stdin.read() or "[]"); print(next((str(issue.get("number", "")) for issue in issues if issue.get("title")==title), ""))' "$ISSUE_TITLE")"
  if [ -n "${issue_number:-}" ]; then
    echo "$issue_number"
    return 0
  fi

  payload="$(python3 -c 'import json,sys; payload = {"title": sys.argv[1], "body": sys.argv[2], "labels": []}; print(json.dumps(payload))' "$ISSUE_TITLE" "$ISSUE_BODY")"
  api_capture issue POST "/repos/$OWNER/$REPO/issues" "$token" "$payload"
  if [ "$LAST_HTTP_CODE" != "201" ]; then
    echo "bootstrap-run-fixture: unable to create issue (HTTP ${LAST_HTTP_CODE})" >&2
    return 1
  fi
  echo "$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("number",""))' <<<"$issue")"
}

snapshot_state() {
  local token=$1
  local outdir=$2
  mkdir -p "$outdir"

  for endpoint in \
    "/repos/$OWNER/$REPO" \
    "/repos/$OWNER/$REPO/branches" \
    "/repos/$OWNER/$REPO/issues?state=all" \
    "/repos/$OWNER/$REPO/pulls?state=all&limit=100" ; do
    local safe
    safe="$(echo "$endpoint" | tr -c 'A-Za-z0-9' '_')"
    api_call GET "$endpoint" "$token" > "$outdir${safe}.json"
    echo "captured $endpoint -> $outdir${safe}.json"
  done

  local issues
  issues="$(api_call GET "/repos/$OWNER/$REPO/issues?state=all" "$token")"
  printf '%s\n' "$issues" | python3 -c 'import json,sys
issues = json.load(sys.stdin)
if not isinstance(issues, list):
    issues = []
for issue in issues:
    print(issue.get("number", ""))
' > "$outdir/issue_comments_index.txt"

  if docker compose -f "$COMPOSE" logs --no-log-prefix forgejo >/dev/null 2>&1; then
    docker compose -f "$COMPOSE" logs --no-log-prefix forgejo > "$outdir/forgejo.log"
  fi
}

seed_fixture() {
  local token default_branch issue_number

  token="$(ensure_admin_token)"
  if [ -z "$token" ]; then
    echo "bootstrap-run-fixture: admin token unavailable" >&2
    exit 1
  fi

  repo_json="$(ensure_repo "$token")"
  default_branch="$(read_repo_default_branch "$repo_json")"
  ensure_branch "$token" "$default_branch"
  ensure_seed_file "$token"
  issue_number="$(ensure_issue "$token")"

  outdir="${SNAP_DIR}/seed-$(date +%s)"
  mkdir -p "$outdir"
  printf '%s\n' "$repo_json" > "$outdir/repo.json"
  printf '%s\n' "$default_branch" > "$outdir/default_branch.txt"
  printf '%s\n' "$issue_number" > "$outdir/seed_issue_number.txt"
  echo "seeded fixture issue: $issue_number"
  echo "seed snapshot: $outdir"
}

inspect_fixture() {
  local token
  token="$(ensure_admin_token)"
  if [ -z "$token" ]; then
    echo "bootstrap-run-fixture: admin token unavailable" >&2
    exit 1
  fi
  outdir="${SNAP_DIR}/inspect-$(date +%s)"
  snapshot_state "$token" "$outdir/"
  echo "snapshot saved: $outdir"
}

case "${1:-seed}" in
  seed)
    seed_fixture
    ;;
  inspect)
    inspect_fixture
    ;;
  *)
    echo "usage: $0 [seed|inspect]" >&2
    exit 1
    ;;
esac
